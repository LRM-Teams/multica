package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// resolveUserResearchMessageTarget picks the Agent that should receive a user
// chat. V6 Runs use the current Director assignment; V5 keeps the workspace
// Fleet Lead. An explicit target_agent_id always wins.
func resolveUserResearchMessageTarget(orchestratorVersion string, requested, directorAgentID, fleetLead pgtype.UUID) pgtype.UUID {
	if requested.Valid {
		return requested
	}
	if orchestratorVersion == researchrun.OrchestratorVersionV6 && directorAgentID.Valid {
		return directorAgentID
	}
	return fleetLead
}

func (h *Handler) loadActiveV6DirectorAgentID(ctx context.Context, session db.ResearchSession) pgtype.UUID {
	var director pgtype.UUID
	if session.OrchestratorVersion != researchrun.OrchestratorVersionV6 || !session.CurrentDirectorAssignmentID.Valid || h.DB == nil {
		return director
	}
	_ = h.DB.QueryRow(ctx, `
		SELECT director_agent_id
		FROM research_director_assignment
		WHERE id = $1 AND workspace_id = $2 AND session_id = $3 AND status = 'active'
	`, session.CurrentDirectorAssignmentID, session.WorkspaceID, session.ID).Scan(&director)
	return director
}
