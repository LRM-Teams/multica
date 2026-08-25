package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const maxBulkAgentTargets = 100

type bulkUpdateAgentRuntimeConfigRequest struct {
	AgentIDs      []string `json:"agent_ids"`
	RuntimeID     string   `json:"runtime_id"`
	Model         string   `json:"model"`
	ThinkingLevel string   `json:"thinking_level"`
}

type bulkUpdateAgentRuntimeConfigResponse struct {
	UpdatedAgentIDs []string `json:"updated_agent_ids"`
}

// BulkUpdateAgentRuntimeConfig atomically assigns one runtime/model/reasoning
// tuple to multiple Agents. It is the single request boundary used by the
// Computer page; clients must not fan out PUT /agents/{id} requests.
func (h *Handler) BulkUpdateAgentRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.AgentPrincipalFromContext(r.Context()); ok {
		writeError(w, http.StatusForbidden, "agent principals cannot bulk update agents")
		return
	}
	var req bulkUpdateAgentRuntimeConfigRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.AgentIDs) == 0 || len(req.AgentIDs) > maxBulkAgentTargets {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("agent_ids must contain between 1 and %d agents", maxBulkAgentTargets))
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	targetRuntimeID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	targetRuntime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          targetRuntimeID,
		WorkspaceID: workspaceUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}
	targetRuntimeOwnerID, _ := h.resolveRuntimeOwnerQuery(r.Context(), targetRuntime)
	if !canUseRuntimeForAgent(member, targetRuntime, targetRuntimeOwnerID) {
		writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can move agents onto it")
		return
	}
	if req.ThinkingLevel != "" && !agent.IsKnownThinkingValue(targetRuntime.Provider, req.ThinkingLevel) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("thinking_level %q is not a recognised value for runtime %q", req.ThinkingLevel, targetRuntime.Provider))
		return
	}

	agentIDs := make([]pgtype.UUID, 0, len(req.AgentIDs))
	seen := make(map[string]struct{}, len(req.AgentIDs))
	for _, rawID := range req.AgentIDs {
		agentID, valid := parseUUIDOrBadRequest(w, rawID, "agent_id")
		if !valid {
			return
		}
		canonicalID := uuidToString(agentID)
		if _, duplicate := seen[canonicalID]; duplicate {
			writeError(w, http.StatusBadRequest, "agent_ids must not contain duplicates")
			return
		}
		seen[canonicalID] = struct{}{}
		agentIDs = append(agentIDs, agentID)
	}
	workspaceAgents, err := h.Queries.ListAgents(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agents")
		return
	}
	existingAgents := make([]db.Agent, 0, len(agentIDs))
	for _, existing := range workspaceAgents {
		if _, selected := seen[uuidToString(existing.ID)]; selected {
			existingAgents = append(existingAgents, existing)
		}
	}
	if len(existingAgents) != len(agentIDs) {
		writeError(w, http.StatusNotFound, "one or more agents were not found")
		return
	}
	isAdmin := roleAllowed(member.Role, "owner", "admin")
	for _, existing := range existingAgents {
		if !isAdmin && existing.OwnerID != member.UserID {
			writeError(w, http.StatusForbidden, "only the agent owner can manage this agent")
			return
		}
	}

	runtimes, err := h.Queries.ListAgentRuntimes(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runtimes")
		return
	}
	runtimeByID := make(map[pgtype.UUID]db.AgentRuntime, len(runtimes))
	for _, runtime := range runtimes {
		runtimeByID[runtime.ID] = runtime
	}
	movingAgentIDs := make([]pgtype.UUID, 0, len(existingAgents))
	for _, existing := range existingAgents {
		if existing.RuntimeID == targetRuntime.ID {
			continue
		}
		movingAgentIDs = append(movingAgentIDs, existing.ID)
		currentRuntime, found := runtimeByID[existing.RuntimeID]
		if !found {
			writeError(w, http.StatusInternalServerError, "failed to load current runtime")
			return
		}
		if !runtimesShareMachine(currentRuntime, targetRuntime) && !agentRuntimeHasCapability(targetRuntime, protocol.DaemonCapabilityMemoryCrossDeviceSync) {
			writeCodedError(w, http.StatusConflict, "daemon_memory_sync_required", "target daemon must upgrade before moving an agent between computers")
			return
		}
	}
	if len(movingAgentIDs) > 0 && !agentRuntimeHasCapability(targetRuntime, protocol.DaemonCapabilityReminderVersionedCache) {
		var hasActiveReminders bool
		if err := h.DB.QueryRow(r.Context(), `
			SELECT EXISTS (
			  SELECT 1 FROM agent_reminder
			  WHERE agent_id = ANY($1::uuid[])
			    AND status IN ('scheduled', 'firing')
			)
		`, movingAgentIDs).Scan(&hasActiveReminders); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate reminder runtime capability")
			return
		}
		if hasActiveReminders {
			writeCodedError(w, http.StatusConflict, "daemon_outdated", "target runtime must upgrade before moving an agent with active reminders")
			return
		}
	}

	oldByID := make(map[pgtype.UUID]db.Agent, len(existingAgents))
	for _, existing := range existingAgents {
		oldByID[existing.ID] = existing
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agents")
		return
	}
	defer tx.Rollback(r.Context())
	lockedRows, err := tx.Query(r.Context(), `
		SELECT id, owner_id, runtime_id
		FROM agent
		WHERE workspace_id = $1
		  AND id = ANY($2::uuid[])
		  AND archived_at IS NULL
		FOR UPDATE
	`, workspaceUUID, agentIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock agents")
		return
	}
	lockedCount := 0
	for lockedRows.Next() {
		var id, ownerID, runtimeID pgtype.UUID
		if err := lockedRows.Scan(&id, &ownerID, &runtimeID); err != nil {
			lockedRows.Close()
			writeError(w, http.StatusInternalServerError, "failed to lock agents")
			return
		}
		before, found := oldByID[id]
		if !found || before.OwnerID != ownerID || before.RuntimeID != runtimeID {
			lockedRows.Close()
			writeError(w, http.StatusConflict, "agents changed while the bulk update was being applied")
			return
		}
		lockedCount++
	}
	if err := lockedRows.Err(); err != nil {
		lockedRows.Close()
		writeError(w, http.StatusInternalServerError, "failed to lock agents")
		return
	}
	lockedRows.Close()
	if lockedCount != len(existingAgents) {
		writeError(w, http.StatusConflict, "agents changed while the bulk update was being applied")
		return
	}
	rows, err := tx.Query(r.Context(), `
		UPDATE agent SET
		  runtime_id = $1,
		  runtime_mode = $2,
		  model = $3,
		  thinking_level = NULLIF($4::text, ''),
		  runtime_reassigned_at = CASE
		    WHEN runtime_id IS DISTINCT FROM $1 THEN now()
		    ELSE runtime_reassigned_at
		  END,
		  updated_at = now()
		WHERE workspace_id = $5
		  AND id = ANY($6::uuid[])
		  AND archived_at IS NULL
		RETURNING id
	`, targetRuntime.ID, targetRuntime.RuntimeMode, model, req.ThinkingLevel, workspaceUUID, agentIDs)
	if err != nil {
		if isReminderDaemonOutdatedError(err) {
			writeCodedError(w, http.StatusConflict, "daemon_outdated", "target runtime must upgrade before moving an agent with active reminders")
			return
		}
		slog.Warn("bulk update agents failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update agents")
		return
	}
	updatedIDs := make([]string, 0, len(existingAgents))
	for rows.Next() {
		var updatedID pgtype.UUID
		if err := rows.Scan(&updatedID); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "failed to update agents")
			return
		}
		updatedIDs = append(updatedIDs, uuidToString(updatedID))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		writeError(w, http.StatusInternalServerError, "failed to update agents")
		return
	}
	rows.Close()
	if len(updatedIDs) != len(existingAgents) {
		writeError(w, http.StatusConflict, "agents changed while the bulk update was being applied")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agents")
		return
	}

	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	for _, updatedID := range updatedIDs {
		updated, err := h.Queries.GetAgent(r.Context(), parseUUID(updatedID))
		if err != nil {
			slog.Warn("load Agent after bulk update failed", append(logger.RequestAttrs(r), "error", err, "agent_id", updatedID)...)
			continue
		}
		resp := agentToResponse(updated)
		if err := h.attachAgentSkills(r.Context(), &resp, updated.ID); err != nil {
			slog.Warn("load agent skills after bulk update failed", append(logger.RequestAttrs(r), "error", err, "agent_id", updatedID)...)
		}
		h.attachAgentRuntimeName(r.Context(), &resp)
		h.refreshAgentSkillSuggestions(r.Context(), updated)
		h.publishAgentVisibilityEvent(protocol.EventAgentStatus, workspaceID, actorType, actorID, updated, map[string]any{"agent": broadcastAgentResponse(resp)})

		existing := oldByID[updated.ID]
		if existing.RuntimeID != updated.RuntimeID {
			if err := h.reconcileAgentReminderRuntime(r.Context(), updated.ID, existing.RuntimeID, updated.RuntimeID); err != nil {
				slog.Warn("reconcile reminders after bulk Agent Runtime move", append(logger.RequestAttrs(r), "error", err, "agent_id", updatedID)...)
			}
			h.reassignClaimableInboxEventsAfterAgentRuntimeMove(r.Context(), updated.ID, existing.RuntimeID, updated.RuntimeID)
			h.reconcileConnectedRuntimes(r.Context(), workspaceID, existing.RuntimeID, updated.RuntimeID)
		}
	}

	writeJSON(w, http.StatusOK, bulkUpdateAgentRuntimeConfigResponse{UpdatedAgentIDs: updatedIDs})
}
