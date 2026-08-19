package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	periodBriefAgentName        = "weekly-report"
	periodBriefAgentDisplayName = "周报"
	periodBriefAgentTemplate    = "weekly-report"
	// Detects personas that predate collector Work-groups → Brief expansion.
	// Ensure refreshes template instructions when missing so stale DB copy
	// cannot fight the wake contract.
	periodBriefInstructionsCapabilityMarker = "Start from collector ## Work groups"
)

// EnsurePeriodBriefAgentResponse is returned by POST /api/agents/period-brief.
type EnsurePeriodBriefAgentResponse struct {
	Agent   AgentResponse `json:"agent"`
	Created bool          `json:"created"`
}

type ensurePeriodBriefAgentRequest struct {
	RuntimeID string `json:"runtime_id"`
	Model     string `json:"model"`
}

// EnsurePeriodBriefAgent idempotently provisions the Workspace Period Brief
// Agent (「周报」 / name weekly-report) from the curated weekly-report template.
// Existing workspaces resolve the same permanent name; humans may still pick
// any other Agent as synthesizer in the Notes dialog.
func (h *Handler) EnsurePeriodBriefAgent(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "EnsurePeriodBriefAgent") {
		return
	}
	ownerID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireManageAgents(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	if agent, found, err := h.findPeriodBriefAgent(r.Context(), wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	} else if found {
		agent = h.refreshPeriodBriefInstructionsIfStale(r.Context(), agent)
		resp := agentToResponse(agent)
		redactAgentResponseForActor(&resp, "member")
		writeJSON(w, http.StatusOK, EnsurePeriodBriefAgentResponse{Agent: resp, Created: false})
		return
	}

	var req ensurePeriodBriefAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	runtimeID := strings.TrimSpace(req.RuntimeID)
	model := strings.TrimSpace(req.Model)
	if runtimeID == "" {
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	if err := service.RequireAgentModel(model); err != nil {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}
	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          runtimeUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !canUseRuntimeForAgent(member, runtime) {
		writeError(w, http.StatusForbidden, "this runtime is private; only its owner or a workspace admin can create agents on it")
		return
	}

	tmpl, found := agentTemplates.Get(periodBriefAgentTemplate)
	if !found {
		writeError(w, http.StatusInternalServerError, "weekly-report template missing")
		return
	}

	createParams := db.CreateAgentParams{
		WorkspaceID:        wsUUID,
		Name:               periodBriefAgentName,
		DisplayName:        periodBriefAgentDisplayName,
		Description:        tmpl.Description,
		Instructions:       tmpl.Instructions,
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          runtime.ID,
		MaxConcurrentTasks: 6,
		OwnerID:            parseUUID(ownerID),
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
		Model:              pgtype.Text{String: model, Valid: true},
	}
	applyCreateAgentAvatar(&createParams, resolvedAgentAvatar{})

	created, err := h.createAgentManagedCommit(r.Context(), wsUUID, createParams, periodBriefAgentDisplayName)
	if err != nil {
		// Concurrent ensure: another caller won the UNIQUE(workspace_id, name) race.
		if identityUniqueViolation(err, "agent_workspace_name_unique") {
			if agent, found, findErr := h.findPeriodBriefAgent(r.Context(), wsUUID); findErr == nil && found {
				agent = h.refreshPeriodBriefInstructionsIfStale(r.Context(), agent)
				resp := agentToResponse(agent)
				redactAgentResponseForActor(&resp, "member")
				writeJSON(w, http.StatusOK, EnsurePeriodBriefAgentResponse{Agent: resp, Created: false})
				return
			}
			writeError(w, http.StatusConflict, "name is already in use")
			return
		}
		if errors.Is(err, errIdentityHandleInvalid) {
			writeError(w, http.StatusBadRequest, "name must be 1-32 lowercase letters, digits, or hyphens")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create period brief agent: "+err.Error())
		return
	}

	resp := agentToResponse(created)
	redactAgentResponseForActor(&resp, "member")
	writeJSON(w, http.StatusCreated, EnsurePeriodBriefAgentResponse{Agent: resp, Created: true})
}

// findPeriodBriefAgent looks up the non-archived Period Brief Agent by
// permanent name. Display-name-only matches are ignored so a renamed custom
// Agent cannot steal the default synthesizer slot.
func (h *Handler) findPeriodBriefAgent(ctx context.Context, workspaceID pgtype.UUID) (db.Agent, bool, error) {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	for _, agent := range agents {
		if agent.Name == periodBriefAgentName {
			return agent, true, nil
		}
	}
	return db.Agent{}, false, nil
}

// refreshPeriodBriefInstructionsIfStale rewrites the platform-provisioned
// weekly-report persona from the curated template when it still teaches the
// pre-reporting-shape contract (flat ≤3 bullets, optional Mermaid). Custom
// synthesizers (any other Agent name) are never touched.
func (h *Handler) refreshPeriodBriefInstructionsIfStale(ctx context.Context, agent db.Agent) db.Agent {
	if agent.Name != periodBriefAgentName {
		return agent
	}
	if strings.Contains(agent.Instructions, periodBriefInstructionsCapabilityMarker) {
		return agent
	}
	tmpl, found := agentTemplates.Get(periodBriefAgentTemplate)
	if !found || strings.TrimSpace(tmpl.Instructions) == "" {
		return agent
	}
	if !strings.Contains(tmpl.Instructions, periodBriefInstructionsCapabilityMarker) {
		return agent
	}
	updated, err := h.Queries.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:           agent.ID,
		Description:  pgtype.Text{String: tmpl.Description, Valid: true},
		Instructions: pgtype.Text{String: tmpl.Instructions, Valid: true},
	})
	if err != nil {
		return agent
	}
	return updated
}
