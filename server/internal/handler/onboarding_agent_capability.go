package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
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
		h.activeResearchFleetRole(ctx, agent),
	)
}

func (h *Handler) builtinSkillsForInboxEvent(ctx context.Context, event db.AgentInboxEvent, agent db.Agent) []service.AgentSkillData {
	researchRole := h.activeResearchFleetRole(ctx, agent)
	if researchRole == "" && h.isV6ReportPackageInboxEvent(ctx, event) {
		researchRole = "reporter"
	}
	return h.TaskService.BuiltinSkillsForAgent(
		h.isActiveOnboardingAgentRecord(ctx, agent),
		researchRole,
	)
}

func (h *Handler) isV6ReportPackageInboxEvent(ctx context.Context, event db.AgentInboxEvent) bool {
	if h.DB == nil || !event.ID.Valid || !event.WorkspaceID.Valid || !event.AgentID.Valid {
		return false
	}
	var expectedResult string
	err := h.DB.QueryRow(ctx, `
		SELECT work.expected_result_schema_id
		FROM research_work_item_attempt attempt
		JOIN research_work_item work
		  ON work.workspace_id = attempt.workspace_id
		 AND work.session_id = attempt.session_id
		 AND work.id = attempt.work_item_id
		WHERE attempt.inbox_task_id = $1
		  AND attempt.workspace_id = $2
		  AND attempt.assigned_agent_id = $3
		LIMIT 1`, event.ID, event.WorkspaceID, event.AgentID).Scan(&expectedResult)
	return err == nil && expectedResult == string(researchrun.V6ContractReportPackageSubmission)
}

func (h *Handler) activeResearchFleetRole(ctx context.Context, agent db.Agent) string {
	if !agent.WorkspaceID.Valid || !agent.ID.Valid || agent.ArchivedAt.Valid {
		return ""
	}
	member, err := h.Queries.GetResearchFleetMemberByAgent(ctx, db.GetResearchFleetMemberByAgentParams{
		WorkspaceID: agent.WorkspaceID,
		AgentID:     agent.ID,
	})
	if err != nil || member.Status == "archived" {
		return ""
	}
	return member.Role
}
