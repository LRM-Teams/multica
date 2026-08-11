package workgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func reviewAgentName(idempotencyKey, tempID string) string {
	sum := sha256.Sum256([]byte(tempID))
	key := strings.ReplaceAll(idempotencyKey, "-", "")
	if len(key) > 10 {
		key = key[:10]
	}
	return "goal-reviewer-" + key + "-" + hex.EncodeToString(sum[:4])
}

// cloneReviewAgent creates an ephemeral reviewer identity from an approved
// reviewer profile. It intentionally copies no memory, credentials, provider
// session, custom environment, custom arguments, or MCP configuration.
func cloneReviewAgent(ctx context.Context, tx pgx.Tx, workspaceID, sourceAgentID uuid.UUID, idempotencyKey, tempID string) (uuid.UUID, error) {
	name := reviewAgentName(idempotencyKey, tempID)
	var derived uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id,name,display_name,description,avatar_url,runtime_mode,
			runtime_config,runtime_id,max_concurrent_tasks,owner_id,instructions,
			model,thinking_level,source_agent_id
		)
		SELECT workspace_id,$3,display_name,description,avatar_url,runtime_mode,
		       runtime_config,runtime_id,1,owner_id,instructions,
		       model,thinking_level,COALESCE(source_agent_id,id)
		FROM agent
		WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL AND runtime_id IS NOT NULL
		RETURNING id
	`, workspaceID, sourceAgentID, name).Scan(&derived)
	if err != nil {
		return uuid.Nil, fmt.Errorf("clone goal reviewer: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO agent_skill(agent_id,skill_id) SELECT $1,skill_id FROM agent_skill WHERE agent_id=$2 ON CONFLICT DO NOTHING`, derived, sourceAgentID); err != nil {
		return uuid.Nil, fmt.Errorf("copy goal reviewer skills: %w", err)
	}
	return derived, nil
}

// ArchiveReviewAgentForNode releases an ephemeral reviewer identity once its
// immutable verdict has been recorded. Review attempts and evidence remain.
func (s *Store) ArchiveReviewAgentForNode(ctx context.Context, workspaceID, verifierNodeID string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	n, err := uuid.Parse(verifierNodeID)
	if err != nil {
		return ErrInvalidGraph
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var derived uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE work_review_agent_assignment
		SET status='archived',archived_at=now()
		WHERE workspace_id=$1 AND verifier_node_id=$2 AND status='active'
		RETURNING derived_agent_id
	`, w, n).Scan(&derived)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE agent SET archived_at=now(),status='offline',updated_at=now() WHERE workspace_id=$1 AND id=$2 AND source_agent_id IS NOT NULL`, w, derived); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) archiveReviewAgentsForGoal(ctx context.Context, workspaceID, goalID uuid.UUID) error {
	rows, err := s.pool.Query(ctx, `
		SELECT assignment.verifier_node_id::text
		FROM work_review_agent_assignment assignment
		JOIN work_graph graph ON graph.id=assignment.graph_id
		WHERE assignment.workspace_id=$1 AND graph.anchor_kind='channel_goal'
		  AND graph.anchor_id=$2 AND assignment.status='active'
	`, workspaceID, goalID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.ArchiveReviewAgentForNode(ctx, workspaceID.String(), id); err != nil {
			return err
		}
	}
	return nil
}
