package researchrun

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) LoadV6WorkManifest(ctx context.Context, access V6AttemptAccess) (V6WorkManifest, error) {
	var manifest V6WorkManifest
	err := s.pool.QueryRow(ctx, `
		SELECT a.manifest, a.manifest_hash
		FROM research_work_item_attempt a
		JOIN research_work_item w ON (w.workspace_id,w.session_id,w.id)=(a.workspace_id,a.session_id,a.work_item_id)
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid AND m.state NOT IN ('archived','failed')
		  AND a.status IN ('dispatching','running') AND w.status IN ('dispatching','running','awaiting_input')
		  AND ($6='' OR a.inbox_task_id=$6::uuid)
	`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID).Scan(&manifest.Bytes, &manifest.ETag)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6WorkManifest{}, ErrAttemptNotAssigned
	}
	return manifest, err
}
