package researchrun

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type lostV6InboxTaskStore interface {
	ListLostV6InboxTaskIDs(context.Context, int) ([]string, error)
}

type v6InboxCanceller interface {
	Cancel(context.Context, []string, string) error
}

func cancelLostV6InboxTasks(ctx context.Context, store lostV6InboxTaskStore, canceller v6InboxCanceller, limit int) (int, error) {
	if store == nil || limit <= 0 {
		return 0, nil
	}
	inboxTaskIDs, err := store.ListLostV6InboxTaskIDs(ctx, limit)
	if err != nil || len(inboxTaskIDs) == 0 {
		return 0, err
	}
	if canceller == nil {
		return 0, fmt.Errorf("cancel lost V6 Inbox tasks: dispatcher is unavailable")
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err = canceller.Cancel(cancelCtx, inboxTaskIDs, "research_v6_attempt_lease_expired"); err != nil {
		return 0, fmt.Errorf("cancel lost V6 Inbox tasks: %w", err)
	}
	return len(inboxTaskIDs), nil
}

// ListLostV6InboxTaskIDs keeps cancellation durable without another outbox:
// rows remain eligible until the shared TaskService makes the Inbox task
// terminal, so a transient cancellation failure is retried next reconcile.
func (s *PostgresStore) ListLostV6InboxTaskIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.inbox_task_id::text
		FROM research_work_item_attempt a
		JOIN research_session run ON run.id=a.session_id
		JOIN agent_inbox_event inbox ON inbox.id=a.inbox_task_id
		WHERE run.orchestrator_version='research-run-v6'
		  AND a.status='lost'
		  AND inbox.status IN ('pending','draining','failed')
		  AND inbox.terminal_outcome IS NULL
		ORDER BY a.completed_at,a.id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) RecoverExpiredV6WorkItems(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	tx, err := s.beginResearchTx(ctx, txOpV6WorkItemRecover, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		WITH due AS (
		  SELECT w.id
		  FROM research_work_item w
		  JOIN research_session s ON s.id=w.session_id
		  WHERE s.orchestrator_version='research-run-v6'
		    AND w.status IN ('dispatching','running') AND w.lease_expires_at <= now()
		    AND NOT (w.status='dispatching' AND EXISTS (
		      SELECT 1 FROM research_work_item_attempt active_attempt
		      JOIN research_v6_outbox active_outbox
		        ON active_outbox.kind='dispatch_work_item'
		       AND COALESCE(active_outbox.payload->'access'->>'attempt_id',active_outbox.payload->'access'->>'AttemptID')=active_attempt.id::text
		       AND active_outbox.status IN ('pending','delivering')
		      WHERE active_attempt.work_item_id=w.id AND active_attempt.status='dispatching'
		    ))
		    AND NOT EXISTS (
		      SELECT 1
		      FROM research_work_item_attempt active_attempt
		      JOIN agent_inbox_event inbox ON inbox.id=active_attempt.inbox_task_id
		      WHERE active_attempt.work_item_id=w.id
		        AND active_attempt.status IN ('dispatching','running')
		        AND inbox.terminal_outcome IS NULL
		        AND (
		          inbox.status='pending'
		          OR (inbox.status='failed' AND inbox.retryable)
		          OR (
		            inbox.status='draining'
		            AND inbox.started_at IS NOT NULL
		            AND inbox.started_at + make_interval(
		              secs => GREATEST(COALESCE((s.run_config->>'task_timeout_seconds')::double precision,1800),1)
		            ) > now()
		          )
		        )
		    )
		  ORDER BY s.id,w.lease_expires_at,w.id
		  FOR UPDATE OF s,w SKIP LOCKED LIMIT $1
		), received AS (
		  SELECT DISTINCT a.work_item_id
		  FROM research_work_item_attempt a
		  JOIN research_v6_work_submission sub ON sub.attempt_id=a.id AND sub.status IN ('received','processing','accepted')
		  WHERE a.work_item_id IN (SELECT id FROM due)
		), lost AS (
		  UPDATE research_work_item_attempt a SET status='lost',completed_at=now(),updated_at=now(),failure_class='lease_expired'
		  WHERE a.work_item_id IN (SELECT id FROM due) AND a.status IN ('dispatching','running')
		    AND a.work_item_id NOT IN (SELECT work_item_id FROM received)
		  RETURNING a.id,a.work_item_id
		), stale_outbox AS (
		  UPDATE research_v6_outbox o
		  SET status='failed',lease_token=NULL,lease_expires_at=NULL,
		      last_error=CASE WHEN last_error='' THEN 'stale dispatch attempt' ELSE last_error END,
		      updated_at=now()
		  WHERE o.kind='dispatch_work_item' AND o.status IN ('pending','delivering')
		    AND (o.lease_expires_at IS NULL OR o.lease_expires_at <= now())
		    AND COALESCE(o.payload->'access'->>'attempt_id',o.payload->'access'->>'AttemptID') IN (SELECT id::text FROM lost)
		  RETURNING o.id
		)
		UPDATE research_work_item w
		SET status=CASE
		      WHEN w.id IN (SELECT work_item_id FROM received) THEN 'awaiting_input'
		      WHEN w.attempt_count >= w.max_attempts THEN 'failed'
		      ELSE 'ready' END,
		    lease_token=NULL,lease_expires_at=NULL,state_version=state_version+1,
		    terminal_reason_code=CASE WHEN w.attempt_count >= w.max_attempts THEN 'attempt_budget_exhausted' ELSE '' END,
		    ready_at=CASE WHEN w.attempt_count < w.max_attempts AND w.id NOT IN (SELECT work_item_id FROM received) THEN now() ELSE w.ready_at END,
		    updated_at=now()
		WHERE w.id IN (SELECT id FROM due)
		RETURNING w.workspace_id::text,w.session_id::text,w.id::text,w.status,w.state_version
	`, limit)
	if err != nil {
		return 0, err
	}
	type recovered struct {
		workspaceID, runID, workItemID, status string
		version                                int64
	}
	items := []recovered{}
	for rows.Next() {
		var item recovered
		if err = rows.Scan(&item.workspaceID, &item.runID, &item.workItemID, &item.status, &item.version); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range items {
		if _, err = appendEvent(ctx, tx, item.workspaceID, item.runID, "v6_work_item_recovered",
			fmt.Sprintf("v6-work-item-recovered:%s:%d", item.workItemID, item.version), "system", "",
			map[string]any{"work_item_id": item.workItemID, "status": item.status, "state_version": item.version}); err != nil {
			return 0, err
		}
	}
	if err = s.commitResearchTx(ctx, txOpV6WorkItemRecover, tx); err != nil {
		return 0, err
	}
	return len(items), nil
}
