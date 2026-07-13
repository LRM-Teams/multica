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

	switch assigneeType.String {
	case "agent":
		if _, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          assigneeID,
			WorkspaceID: workspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("assignee_id does not refer to an agent in the radar run workspace")
			}
			return err
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
	case "squad":
		if _, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID:          assigneeID,
			WorkspaceID: workspaceID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("assignee_id does not refer to a squad in the radar run workspace")
			}
			return err
		}
	default:
		return errors.New("assignee_type must be 'member', 'agent', or 'squad'")
	}
	return nil
}
