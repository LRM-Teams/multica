package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type updateAgentWorkspaceRoleRequest struct {
	Role string `json:"role"`
}

// UpdateAgentWorkspaceRole changes the workspace-level authority of an agent.
// This human route is restricted to workspace owner/admin actors and has no
// /api/agent alias (task #32: admins, not just the owner, can edit).
func (h *Handler) UpdateAgentWorkspaceRole(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "UpdateAgentWorkspaceRole") {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	agentID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "agentId"), "agent id")
	if !ok {
		return
	}

	var req updateAgentWorkspaceRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	newRole := strings.TrimSpace(req.Role)
	if newRole != "member" && newRole != "admin" {
		writeError(w, http.StatusBadRequest, "role must be member or admin")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent workspace role")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	var actorRole string
	err = tx.QueryRow(r.Context(), `
		SELECT role
		FROM member
		WHERE workspace_id = $1
		  AND user_id = $2
		FOR UPDATE
	`, workspaceID, parseUUID(userID)).Scan(&actorRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to authorize workspace owner")
		return
	}
	if actorRole != "owner" && actorRole != "admin" {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	var previousRole string
	err = tx.QueryRow(r.Context(), `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
		  AND workspace_id = $2
		  AND archived_at IS NULL
		FOR UPDATE
	`, agentID, workspaceID).Scan(&previousRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}

	var updatedAgent db.Agent
	var eventPayload map[string]any
	if previousRole != newRole {
		if _, err := tx.Exec(r.Context(), `
			UPDATE agent
			SET workspace_role = $3,
			    updated_at = now()
			WHERE id = $1
			  AND workspace_id = $2
		`, agentID, workspaceID, newRole); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update agent workspace role")
			return
		}

		details, err := json.Marshal(map[string]any{
			"actor_workspace_role": actorRole,
			"agent_id":             uuidToString(agentID),
			"previous_role":        previousRole,
			"role":                 newRole,
			"request_id":           chimw.GetReqID(r.Context()),
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record agent workspace role")
			return
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO activity_log (
				workspace_id, actor_type, actor_id, action, details
			)
			VALUES ($1, 'member', $2, 'agent_workspace_role_changed', $3)
		`, workspaceID, parseUUID(userID), details); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to record agent workspace role")
			return
		}

		updatedAgent, err = qtx.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          agentID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load updated agent")
			return
		}
		resp := agentToResponse(updatedAgent)
		if err := h.attachAgentSkills(r.Context(), &resp, updatedAgent.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load agent skills")
			return
		}
		h.attachAgentRuntimeName(r.Context(), &resp)
		eventPayload = map[string]any{"agent": broadcastAgentResponse(resp)}
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update agent workspace role")
		return
	}
	if previousRole != newRole {
		h.publishAgentVisibilityEvent(
			protocol.EventAgentStatus,
			uuidToString(workspaceID),
			"member",
			userID,
			updatedAgent,
			eventPayload,
		)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"agent_id":       uuidToString(agentID),
		"workspace_role": newRole,
	})
}
