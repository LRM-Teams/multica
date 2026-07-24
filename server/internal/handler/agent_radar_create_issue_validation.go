package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) validateRadarIssueCreateWorkspaceTargets(
	ctx context.Context,
	run db.AgentRadarRun,
	agent db.Agent,
	assigneeType pgtype.Text,
	assigneeID pgtype.UUID,
	projectID pgtype.UUID,
) error {
	if !radarUUIDsMatch(run.WorkspaceID, agent.WorkspaceID) {
		return errors.New("radar agent does not belong to the run workspace")
	}
	if run.AgentID.Valid && !radarUUIDsMatch(run.AgentID, agent.ID) {
		return errors.New("radar agent does not match the run")
	}
	if _, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          agent.ID,
		WorkspaceID: run.WorkspaceID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("radar agent does not belong to the run workspace")
		}
		return err
	}

	workspaceID := run.WorkspaceID
	if projectID.Valid {
		if _, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          projectID,
			WorkspaceID: workspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("project_id does not refer to a project in the radar run workspace")
			}
			return err
		}
	}
	if !assigneeType.Valid && !assigneeID.Valid {
		return nil
	}
	if !assigneeType.Valid || !assigneeID.Valid {
		return errors.New("assignee_type and assignee_id must be provided together")
	}

	switch assigneeType.String {
	case "agent":
		target, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          assigneeID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("assignee_id does not refer to an agent in the radar run workspace")
			}
			return err
		}
		if target.ArchivedAt.Valid || !target.RuntimeID.Valid {
			return errors.New("assignee agent is not available for delivery work")
		}
	case "member":
		if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      assigneeID,
			WorkspaceID: workspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("assignee_id does not refer to a member in the radar run workspace")
			}
			return err
		}
	default:
		return errors.New("assignee_type must be 'member' or 'agent'")
	}
	return nil
}

func (h *Handler) validateRadarIssueCreateAssigneeForChannel(
	ctx context.Context,
	run db.AgentRadarRun,
	supervisor db.Agent,
	channelID pgtype.UUID,
	assigneeType pgtype.Text,
	assigneeID pgtype.UUID,
) error {
	if !assigneeType.Valid || assigneeType.String != "agent" || !assigneeID.Valid {
		return nil
	}
	var managedRole pgtype.Text
	var archivedAt pgtype.Timestamptz
	var runtimeID pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT managed_role, archived_at, runtime_id
		FROM agent
		WHERE id = $1 AND workspace_id = $2
	`, assigneeID, run.WorkspaceID).Scan(&managedRole, &archivedAt, &runtimeID); err != nil {
		return errors.New("assignee agent does not belong to the radar workspace")
	}
	if managedRole.Valid {
		if radarUUIDsMatch(assigneeID, supervisor.ID) {
			return errors.New("group manager cannot assign delivery work to itself")
		}
		return errors.New("managed group managers cannot be assigned delivery work")
	}
	if archivedAt.Valid || !runtimeID.Valid {
		return errors.New("assignee agent is not available for delivery work")
	}
	if channelID.Valid && !h.channelHasAgentMember(ctx, run.WorkspaceID, channelID, assigneeID) {
		return errors.New("assignee agent is not a member of the radar channel")
	}
	return nil
}
