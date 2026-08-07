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

const (
	WorkerModeReuseAgent   = "reuse_agent"
	WorkerModeDerivedAgent = "derived_agent"
)

func issueDerivedAgentName(idempotencyKey, tempID string) string {
	sum := sha256.Sum256([]byte(tempID))
	key := strings.ReplaceAll(idempotencyKey, "-", "")
	if len(key) > 10 {
		key = key[:10]
	}
	return "issue-worker-" + key + "-" + hex.EncodeToString(sum[:4])
}

// cloneIssueWorker creates an isolated execution identity. It copies only
// approved executable configuration, skills, and a point-in-time memory
// snapshot. Credentials, sessions, messages, and memory write history are
// intentionally not inherited.
func cloneIssueWorker(ctx context.Context, tx pgx.Tx, workspaceID, sourceAgentID uuid.UUID, idempotencyKey, tempID string) (uuid.UUID, error) {
	name := issueDerivedAgentName(idempotencyKey, tempID)
	var derived uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id,name,display_name,description,avatar_url,runtime_mode,
			runtime_config,runtime_id,max_concurrent_tasks,owner_id,instructions,
			custom_env,custom_args,mcp_config,model,thinking_level,source_agent_id
		)
		SELECT workspace_id,$3,display_name,description,avatar_url,runtime_mode,
		       runtime_config,runtime_id,max_concurrent_tasks,owner_id,instructions,
		       custom_env,custom_args,mcp_config,model,thinking_level,id
		FROM agent
		WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL AND runtime_id IS NOT NULL
		RETURNING id
	`, workspaceID, sourceAgentID, name).Scan(&derived)
	if err != nil {
		return uuid.Nil, fmt.Errorf("clone issue worker: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO agent_skill(agent_id,skill_id) SELECT $1,skill_id FROM agent_skill WHERE agent_id=$2 ON CONFLICT DO NOTHING`, derived, sourceAgentID); err != nil {
		return uuid.Nil, fmt.Errorf("copy issue worker skills: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO agent_memory(workspace_id,agent_id,name,content,config,sync_key,content_hash,created_by)
		SELECT workspace_id,$1,name,content,config,sync_key,content_hash,created_by
		FROM agent_memory WHERE agent_id=$2
		ON CONFLICT DO NOTHING
	`, derived, sourceAgentID); err != nil {
		return uuid.Nil, fmt.Errorf("snapshot issue worker memory: %w", err)
	}
	return derived, nil
}

// ArchiveDerivedAgentForIssue closes the temporary identity after its only
// child Issue reaches done or cancelled. Keeping it active through in_review
// preserves the same isolated identity for requested rework.
func (s *Store) ArchiveDerivedAgentForIssue(ctx context.Context, workspaceID, issueID string) error {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return ErrInvalidGraph
	}
	i, err := uuid.Parse(issueID)
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
		UPDATE issue_derived_agent_assignment assignment
		SET status='archived',archived_at=now()
		WHERE workspace_id=$1 AND issue_id=$2 AND status='active'
		  AND EXISTS(SELECT 1 FROM issue item WHERE item.id=assignment.issue_id AND item.status IN('done','cancelled'))
		RETURNING derived_agent_id
	`, w, i).Scan(&derived)
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
