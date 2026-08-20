package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// Retired Workspace Agent name. Ensure archives leftover rows; 写汇报
	// synthesizes with 笔记助手 instead.
	retiredPeriodBriefAgentName = "weekly-report"
)

// EnsurePeriodBriefAgentResponse is returned by POST /api/agents/period-brief.
type EnsurePeriodBriefAgentResponse struct {
	Agent   AgentResponse `json:"agent"`
	Created bool          `json:"created"`
}

// EnsurePeriodBriefAgent no longer provisions 「周报」. It archives leftover
// weekly-report agents and returns the Workspace Notes Assistant, which is
// the 写汇报 synthesizer. Missing 笔记助手 → 409 (create it via the Notes bubble).
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

	h.archiveRetiredWeeklyReportAgents(r.Context(), wsUUID, parseUUID(ownerID))

	agent, found, err := h.findNotesAssistantAgent(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	if !found {
		writeError(w, http.StatusConflict, "notes assistant is not provisioned")
		return
	}
	agent = h.refreshNotesAssistantInstructionsIfStale(r.Context(), agent)
	resp := agentToResponse(agent)
	redactAgentResponseForActor(&resp, "member")
	writeJSON(w, http.StatusOK, EnsurePeriodBriefAgentResponse{Agent: resp, Created: false})
}

func (h *Handler) archiveRetiredWeeklyReportAgents(ctx context.Context, workspaceID, archivedBy pgtype.UUID) {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return
	}
	for _, agent := range agents {
		if agent.Name != retiredPeriodBriefAgentName {
			continue
		}
		_, _ = h.Queries.ArchiveAgent(ctx, db.ArchiveAgentParams{
			ID:         agent.ID,
			ArchivedBy: archivedBy,
		})
	}
}
