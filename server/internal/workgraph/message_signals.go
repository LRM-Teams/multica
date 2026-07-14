package workgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// HandleHumanRework marks explicitly targeted active work as needing rework,
// stops active downstream work, and asks the owner to fix it.
func (s *Store) HandleHumanRework(ctx context.Context, workspaceID, channelID pgtype.UUID, agentIDs []pgtype.UUID) error {
	for _, agentID := range agentIDs {
		rows, err := s.pool.Query(ctx, `
			SELECT id, owner_id, linked_issue_id
			FROM work_node
			WHERE workspace_id = $1 AND primary_channel_id = $2
			  AND owner_type = 'agent' AND owner_id = $3
			  AND status IN ('active', 'waiting', 'blocked')
		`, workspaceID, channelID, agentID)
		if err != nil {
			return fmt.Errorf("list rework targets: %w", err)
		}
		for rows.Next() {
			var nodeID, ownerID, issueID pgtype.UUID
			if err := rows.Scan(&nodeID, &ownerID, &issueID); err != nil {
				rows.Close()
				return err
			}
			if _, err := s.pool.Exec(ctx, `UPDATE work_node SET status = 'needs_rework', updated_at = now() WHERE id = $1`, nodeID); err != nil {
				rows.Close()
				return fmt.Errorf("mark node needs rework: %w", err)
			}
			if err := s.insertHandoff(ctx, workspaceID, "fast", "progress_nudge", ownerTypeAgent, ownerID, []pgtype.UUID{nodeID}, channelID, issueID, "rework_nudge:"+nodeID.String()+":"+ownerID.String()); err != nil {
				rows.Close()
				return err
			}
			if err := s.enqueueDownstreamInterrupts(ctx, workspaceID, channelID, nodeID); err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func (s *Store) enqueueDownstreamInterrupts(ctx context.Context, workspaceID, channelID, reworkNodeID pgtype.UUID) error {
	rows, err := s.pool.Query(ctx, `
		SELECT downstream.id, downstream.owner_type, downstream.owner_id, downstream.linked_issue_id
		FROM work_edge edge
		JOIN work_node downstream ON downstream.id = edge.from_node_id
		WHERE edge.workspace_id = $1 AND edge.to_node_id = $2
		  AND edge.kind = 'waits_on' AND edge.status = 'open'
		  AND downstream.status = 'active'
		  AND downstream.owner_id IS NOT NULL
		  AND downstream.owner_type IN ('agent', 'member')
	`, workspaceID, reworkNodeID)
	if err != nil {
		return fmt.Errorf("list active downstream work: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID, ownerID, issueID pgtype.UUID
		var ownerType string
		if err := rows.Scan(&nodeID, &ownerType, &ownerID, &issueID); err != nil {
			return err
		}
		if err := s.insertHandoff(ctx, workspaceID, "fast", "interrupt_stop", ownerType, ownerID, []pgtype.UUID{nodeID, reworkNodeID}, channelID, issueID, "interrupt_stop:"+nodeID.String()+":"+ownerID.String()); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) insertHandoff(ctx context.Context, workspaceID pgtype.UUID, urgency, reason, ownerType string, ownerID pgtype.UUID, related []pgtype.UUID, channelID, issueID pgtype.UUID, dedupe string) error {
	_, err := s.queries.InsertPendingHandoff(ctx, db.InsertPendingHandoffParams{
		WorkspaceID: workspaceID, Urgency: urgency, ReasonCode: reason,
		TargetActorType: ownerType, TargetActorID: ownerID, RelatedNodeIds: related,
		ChannelID: channelID, IssueID: issueID, DedupeKey: dedupe,
		NotBefore: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("insert %s handoff: %w", reason, err)
	}
	return nil
}

// UpsertChatCommitment records high-confidence direct assignments with a
// deterministic dedupe key so repeated chat messages do not create work spam.
func (s *Store) UpsertChatCommitment(ctx context.Context, workspaceID, channelID, agentID pgtype.UUID, title string) error {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(title))))
	key := hex.EncodeToString(sum[:8])
	_, err := s.pool.Exec(ctx, `
		INSERT INTO work_node (workspace_id, kind, title, owner_type, owner_id, status, primary_channel_id, description)
		SELECT $1, 'chat_commitment', $2, 'agent', $3, 'active', $4, $5
		WHERE NOT EXISTS (
			SELECT 1 FROM work_node
			WHERE workspace_id = $1 AND kind = 'chat_commitment' AND owner_id = $3
			  AND primary_channel_id = $4 AND description = $5 AND status NOT IN ('done', 'cancelled')
		)
	`, workspaceID, title, agentID, channelID, "chat_commitment:"+key)
	return err
}
