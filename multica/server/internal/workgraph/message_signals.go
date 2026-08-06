package workgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// HandleHumanRework marks explicitly targeted active work as needing rework.
// The human's directed message already owns delivery; the workgraph must not
// synthesize a second manager nudge or alter the assignee's priority queue.
func (s *Store) HandleHumanRework(ctx context.Context, workspaceID, channelID pgtype.UUID, agentIDs []pgtype.UUID) error {
	for _, agentID := range agentIDs {
		rows, err := s.pool.Query(ctx, `
			SELECT id
			FROM work_node
			WHERE workspace_id = $1 AND primary_channel_id = $2
			  AND owner_type = 'agent' AND owner_id = $3
			  AND status IN ('active', 'waiting', 'blocked')
		`, workspaceID, channelID, agentID)
		if err != nil {
			return fmt.Errorf("list rework targets: %w", err)
		}
		// Drain into memory and close the cursor BEFORE calling Exec: doing
		// a second pool acquire while this cursor is still open can
		// deadlock a bounded pool under concurrent requests (same shape as
		// the #1803 attachAgentRuntimeNames bug / task #90).
		var nodeIDs []pgtype.UUID
		for rows.Next() {
			var nodeID pgtype.UUID
			if err := rows.Scan(&nodeID); err != nil {
				rows.Close()
				return err
			}
			nodeIDs = append(nodeIDs, nodeID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, nodeID := range nodeIDs {
			if _, err := s.pool.Exec(ctx, `UPDATE work_node SET status = 'needs_rework', updated_at = now() WHERE id = $1`, nodeID); err != nil {
				return fmt.Errorf("mark node needs rework: %w", err)
			}
		}
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
