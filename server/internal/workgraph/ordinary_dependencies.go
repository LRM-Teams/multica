package workgraph

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// UnlockIssueDependents promotes ordinary backlog Issues only after every
// declared prerequisite has produced a reviewable or done result. It excludes
// Goal-managed Issues: their frontier is owned by effective_completion.
func (s *Store) UnlockIssueDependents(ctx context.Context, workspaceID, completedIssueID string) ([]string, error) {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	completed, err := uuid.Parse(completedIssueID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		WITH candidate AS (
			SELECT DISTINCT CASE dependency.type
				WHEN 'blocked_by' THEN dependency.issue_id
				WHEN 'blocks' THEN dependency.depends_on_issue_id
			END AS issue_id
			FROM issue_dependency dependency
			WHERE (dependency.type='blocked_by' AND dependency.depends_on_issue_id=$2)
			   OR (dependency.type='blocks' AND dependency.issue_id=$2)
		), ready AS (
			SELECT item.id
				FROM candidate
				JOIN issue item ON item.id=candidate.issue_id AND item.workspace_id=$1
				JOIN issue_decompose_child managed_child ON managed_child.workspace_id=$1 AND managed_child.issue_id=item.id
			WHERE item.status='backlog'
			  AND NOT EXISTS (SELECT 1 FROM work_graph_node managed WHERE managed.issue_id=item.id)
			  AND NOT EXISTS (
				SELECT 1
				FROM issue_dependency dependency
				JOIN issue prerequisite ON prerequisite.id = CASE dependency.type
					WHEN 'blocked_by' THEN dependency.depends_on_issue_id
					WHEN 'blocks' THEN dependency.issue_id
				END
				WHERE ((dependency.type='blocked_by' AND dependency.issue_id=item.id)
				   OR (dependency.type='blocks' AND dependency.depends_on_issue_id=item.id))
				  AND prerequisite.status NOT IN ('in_review','done')
			  )
		)
		UPDATE issue item SET status='todo',updated_at=now()
		FROM ready WHERE item.id=ready.id
		RETURNING item.id::text
	`, w, completed)
	if err != nil {
		return nil, err
	}
	ready := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ready = append(ready, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}

	for _, id := range ready {
		issueID, _ := uuid.Parse(id)
		var handoff string
		if err = tx.QueryRow(ctx, `
			SELECT COALESCE(string_agg(
				format('- %s: %s', prerequisite.title,
					COALESCE((SELECT content FROM comment WHERE issue_id=prerequisite.id AND type='comment' ORDER BY created_at DESC LIMIT 1),
					         'completed without a result comment')),
				E'\n' ORDER BY prerequisite.created_at), '')
			FROM issue_dependency dependency
			JOIN issue prerequisite ON prerequisite.id = CASE dependency.type
				WHEN 'blocked_by' THEN dependency.depends_on_issue_id
				WHEN 'blocks' THEN dependency.issue_id
			END
			WHERE (dependency.type='blocked_by' AND dependency.issue_id=$1)
			   OR (dependency.type='blocks' AND dependency.depends_on_issue_id=$1)
		`, issueID).Scan(&handoff); err != nil {
			return nil, err
		}
		content := "Dependencies satisfied. Read the upstream Issues and use this bounded handoff before starting:\n\n" + handoff
		if _, err = tx.Exec(ctx, `INSERT INTO comment(issue_id,workspace_id,author_type,author_id,content,type) VALUES($1,$2,'system',$3,$4,'system')`, issueID, w, uuid.Nil, content); err != nil {
			return nil, fmt.Errorf("write dependency handoff: %w", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ready, nil
}

// IsDecomposedIssue reports whether Issue-DAG runtime semantics own this Issue.
func (s *Store) IsDecomposedIssue(ctx context.Context, workspaceID, issueID string) (bool, error) {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return false, ErrInvalidGraph
	}
	i, err := uuid.Parse(issueID)
	if err != nil {
		return false, ErrInvalidGraph
	}
	var managed bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_decompose_child WHERE workspace_id=$1 AND issue_id=$2)`, w, i).Scan(&managed)
	return managed, err
}

func (s *Store) DispatchReadyIssues(ctx context.Context, workspaceID string, issueIDs []string) {
	if s.OnNodesReady != nil && len(issueIDs) > 0 {
		s.OnNodesReady(ctx, workspaceID, issueIDs)
	}
}
