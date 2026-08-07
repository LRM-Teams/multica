package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) isActiveOnboardingAgent(ctx context.Context, workspaceID, agentID pgtype.UUID) bool {
	if !workspaceID.Valid || !agentID.Valid {
		return false
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil {
		return false
	}
	return h.isActiveOnboardingAgentRecord(ctx, agent)
}

func (h *Handler) isActiveOnboardingAgentRecord(ctx context.Context, agent db.Agent) bool {
	if agent.ArchivedAt.Valid {
		return false
	}
	return h.isWorkspaceOnboardingAgent(ctx, agent.WorkspaceID, agent.ID)
}

func (h *Handler) isWorkspaceOnboardingAgent(ctx context.Context, workspaceID, agentID pgtype.UUID) bool {
	boundID, err := h.Queries.GetWorkspaceOnboardingAgentID(ctx, workspaceID)
	return err == nil && boundID.Valid && boundID == agentID
}

func (h *Handler) requireOnboardingAgentLifecycleOwner(w http.ResponseWriter, r *http.Request, agent db.Agent) bool {
	if !h.isWorkspaceOnboardingAgent(r.Context(), agent.WorkspaceID, agent.ID) {
		return true
	}
	member, ok := h.workspaceMember(w, r, uuidToString(agent.WorkspaceID))
	if !ok {
		return false
	}
	if member.Role != "owner" {
		writeError(w, http.StatusForbidden, "only the workspace owner may archive or restore the onboarding agent")
		return false
	}
	return true
}

func (h *Handler) builtinSkillsForAgent(ctx context.Context, agent db.Agent) []service.AgentSkillData {
	return h.TaskService.BuiltinSkillsForAgent(
		h.isActiveOnboardingAgentRecord(ctx, agent),
	)
}
