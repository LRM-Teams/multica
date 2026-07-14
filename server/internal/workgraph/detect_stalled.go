package workgraph

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const stalledNudgeDelay = 10 * time.Minute

// DetectStalledNodes schedules a slow nudge only for agent-owned work that
// has previously become eligible to run through an unlock or resolved wait.
func (s *Store) DetectStalledNodes(ctx context.Context, workspaceID pgtype.UUID) error {
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.workspace_id, n.owner_id, n.primary_channel_id, n.linked_issue_id,
		       n.last_progress_at, n.last_progress_summary, n.last_wendy_nudge_at
		FROM work_node n
		WHERE n.workspace_id = $1
		  AND n.status = 'active'
		  AND n.owner_type = 'agent'
		  AND n.owner_id IS NOT NULL
		  AND n.primary_channel_id IS NOT NULL
		  AND (
		    n.last_wendy_nudge_kind = 'unlock'
		    OR EXISTS (
		      SELECT 1 FROM work_edge edge
		      WHERE edge.workspace_id = n.workspace_id
		        AND edge.from_node_id = n.id
		        AND edge.kind = 'waits_on'
		        AND edge.status = 'resolved'
		    )
		  )
	`, workspaceID)
	if err != nil {
		return fmt.Errorf("list stalled work nodes: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	for rows.Next() {
		var nodeID, ownerID, channelID pgtype.UUID
		var issueID pgtype.UUID
		var lastProgress, lastNudge pgtype.Timestamptz
		var summary string
		if err := rows.Scan(&nodeID, &workspaceID, &ownerID, &channelID, &issueID, &lastProgress, &summary, &lastNudge); err != nil {
			return fmt.Errorf("scan stalled work node: %w", err)
		}
		base := now
		if lastProgress.Valid {
			base = lastProgress.Time
		}
		if lastNudge.Valid && lastNudge.Time.After(base) {
			base = lastNudge.Time
		}
		notBefore := base.Add(stalledNudgeDelay)
		if !notBefore.After(now) {
			notBefore = now
		}
		reason := "stalled_ask_why"
		if summary != "" {
			reason = "progress_nudge"
		}
		_, err := s.queries.InsertPendingHandoff(ctx, db.InsertPendingHandoffParams{
			WorkspaceID:     workspaceID,
			Urgency:         "slow",
			ReasonCode:      reason,
			TargetActorType: ownerTypeAgent,
			TargetActorID:   ownerID,
			RelatedNodeIds:  []pgtype.UUID{nodeID},
			ChannelID:       channelID,
			IssueID:         issueID,
			DedupeKey:       "slow:" + nodeID.String() + ":" + reason,
			NotBefore:       pgtype.Timestamptz{Time: notBefore, Valid: true},
		})
		if err != nil && err != pgx.ErrNoRows {
			return fmt.Errorf("insert stalled handoff: %w", err)
		}
	}
	return rows.Err()
}
