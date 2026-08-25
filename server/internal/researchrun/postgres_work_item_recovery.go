package researchrun

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type lostV6InboxTaskStore interface {
	ListLostV6InboxTaskIDs(context.Context, int) ([]string, error)
}

type settledV6InboxTaskStore interface {
	ListSettledV6InboxTaskIDs(context.Context, int) ([]string, error)
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

func cancelSettledV6InboxTasks(ctx context.Context, store settledV6InboxTaskStore, canceller v6InboxCanceller, limit int) (int, error) {
	if store == nil || limit <= 0 {
		return 0, nil
	}
	inboxTaskIDs, err := store.ListSettledV6InboxTaskIDs(ctx, limit)
	if err != nil || len(inboxTaskIDs) == 0 {
		return 0, err
	}
	if canceller == nil {
		return 0, fmt.Errorf("cancel settled V6 Inbox tasks: dispatcher is unavailable")
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err = canceller.Cancel(cancelCtx, inboxTaskIDs, "research_v6_attempt_settled"); err != nil {
		return 0, fmt.Errorf("cancel settled V6 Inbox tasks: %w", err)
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

// ListSettledV6InboxTaskIDs closes the execution half of an Inbox round after
// the durable Research result has already settled its Attempt. The rows stay
// eligible until TaskService makes the Inbox task terminal, so cancellation is
// retried after transient runtime or websocket failures.
func (s *PostgresStore) ListSettledV6InboxTaskIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.inbox_task_id::text
		FROM research_work_item_attempt a
		JOIN research_session run ON run.id=a.session_id
		JOIN agent_inbox_event inbox ON inbox.id=a.inbox_task_id
		WHERE run.orchestrator_version='research-run-v6'
		  AND a.status IN ('succeeded','failed','cancelled')
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
		WITH invalid_manifest AS (
		  SELECT DISTINCT w.id
		  FROM research_work_item w
		  JOIN research_work_item_attempt active_attempt ON active_attempt.work_item_id=w.id
		    AND active_attempt.status IN ('dispatching','running')
		  JOIN research_v6_work_item_branch branch_scope ON branch_scope.work_item_id=w.id
		  WHERE w.expected_result_schema_id='atomic_result_submission'
		    AND w.status IN ('dispatching','running')
		    AND COALESCE(active_attempt.manifest->'branch_refs','[]'::jsonb)='[]'::jsonb
		    AND NOT EXISTS (
		      SELECT 1 FROM research_v6_work_submission sub
		      WHERE sub.attempt_id=active_attempt.id AND sub.status IN ('received','processing','accepted')
		    )
		), terminal_inbox AS (
		  SELECT DISTINCT active_attempt.work_item_id
		  FROM research_work_item_attempt active_attempt
		  JOIN agent_inbox_event inbox ON inbox.id=active_attempt.inbox_task_id
		  WHERE active_attempt.status IN ('dispatching','running')
		    AND inbox.terminal_outcome IS NOT NULL
		), due AS (
		  SELECT w.id,(invalid.id IS NOT NULL) AS platform_invalid_manifest,
		    (terminal.work_item_id IS NOT NULL) AS inbox_terminal
		  FROM research_work_item w
		  JOIN research_session s ON s.workspace_id=w.workspace_id AND s.id=w.session_id
		  LEFT JOIN invalid_manifest invalid ON invalid.id=w.id
		  LEFT JOIN terminal_inbox terminal ON terminal.work_item_id=w.id
		  WHERE s.orchestrator_version='research-run-v6'
		    AND w.status IN ('dispatching','running')
		    AND (invalid.id IS NOT NULL OR (
		      (w.lease_expires_at <= now() OR terminal.work_item_id IS NOT NULL)
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
		    ))
		  ORDER BY s.id,w.lease_expires_at,w.id
		  FOR UPDATE OF s,w SKIP LOCKED LIMIT $1
		), received AS (
		  SELECT DISTINCT a.work_item_id
		  FROM research_work_item_attempt a
		  JOIN research_v6_work_submission sub ON sub.attempt_id=a.id AND sub.status IN ('received','processing','accepted')
		  WHERE a.work_item_id IN (SELECT id FROM due)
		), lost AS (
		  UPDATE research_work_item_attempt a SET status='lost',completed_at=now(),updated_at=now(),
		    failure_class=CASE
		      WHEN a.work_item_id IN (SELECT id FROM due WHERE platform_invalid_manifest) THEN 'platform_invalid_manifest'
		      WHEN a.work_item_id IN (SELECT id FROM due WHERE inbox_terminal) THEN 'inbox_terminal'
		      ELSE 'lease_expired' END,
		    diagnostics=CASE WHEN a.work_item_id IN (SELECT id FROM due WHERE platform_invalid_manifest)
		      THEN 'Work Manifest omitted persisted Branch scope'
		      WHEN a.work_item_id IN (SELECT id FROM due WHERE inbox_terminal)
		      THEN 'Inbox delivery reached a terminal outcome before Work completion'
		      ELSE diagnostics END
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
		      WHEN w.id IN (SELECT id FROM due WHERE platform_invalid_manifest) THEN 'ready'
		      WHEN w.attempt_count >= w.max_attempts THEN 'failed'
		      ELSE 'ready' END,
		    attempt_count=CASE
		      WHEN w.id IN (SELECT id FROM due WHERE platform_invalid_manifest)
		        AND w.id NOT IN (SELECT work_item_id FROM received)
		      THEN GREATEST(w.attempt_count-1,0)
		      ELSE w.attempt_count END,
		    lease_token=NULL,lease_expires_at=NULL,state_version=state_version+1,
		    terminal_reason_code=CASE
		      WHEN w.id NOT IN (SELECT id FROM due WHERE platform_invalid_manifest) AND w.attempt_count >= w.max_attempts
		      THEN 'attempt_budget_exhausted' ELSE '' END,
		    ready_at=CASE
		      WHEN w.id NOT IN (SELECT work_item_id FROM received)
		        AND (w.id IN (SELECT id FROM due WHERE platform_invalid_manifest) OR w.attempt_count < w.max_attempts)
		      THEN now() ELSE w.ready_at END,
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
		recoveryKind                           string
	}
	items := []recovered{}
	for rows.Next() {
		var item recovered
		if err = rows.Scan(&item.workspaceID, &item.runID, &item.workItemID, &item.status, &item.version); err != nil {
			rows.Close()
			return 0, err
		}
		item.recoveryKind = "lease_or_budget"
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	// A 'ready' item whose attempt budget is already exhausted is invisible to
	// dispatch preparation (attempt_count>=max_attempts) and to lease recovery
	// (status not in dispatching/running), so nothing would ever settle it or
	// wake the Director. Fail it explicitly so the budget_exhausted outcome
	// becomes a Run Event the Director trigger processor reacts to.
	zombieRows, err := tx.Query(ctx, `
		WITH zombie AS (
		  SELECT w.id
		  FROM research_work_item w
		  JOIN research_session s ON s.id=w.session_id
		  WHERE s.orchestrator_version='research-run-v6'
		    AND w.status='ready' AND w.attempt_count>=w.max_attempts
		  ORDER BY s.id,w.id
		  FOR UPDATE OF s,w SKIP LOCKED LIMIT $1
		)
		UPDATE research_work_item w
		SET status='failed',terminal_reason_code='attempt_budget_exhausted',
		    lease_token=NULL,lease_expires_at=NULL,state_version=w.state_version+1,updated_at=now()
		WHERE w.id IN (SELECT id FROM zombie)
		RETURNING w.workspace_id::text,w.session_id::text,w.id::text,w.status,w.state_version
	`, limit)
	if err != nil {
		return 0, err
	}
	for zombieRows.Next() {
		var item recovered
		if err = zombieRows.Scan(&item.workspaceID, &item.runID, &item.workItemID, &item.status, &item.version); err != nil {
			zombieRows.Close()
			return 0, err
		}
		item.recoveryKind = "lease_or_budget"
		items = append(items, item)
	}
	if err = zombieRows.Err(); err != nil {
		zombieRows.Close()
		return 0, err
	}
	zombieRows.Close()
	// A ready queue behind one Agent is not parallel execution. Keep the highest
	// priority candidate for each idle member and reject the surplus so the
	// Director can create distinct run-scoped Agents and reassign the independent
	// dimensions. If the member already has active execution, every additional
	// ready item is surplus.
	oversubscribedRows, err := tx.Query(ctx, `
		WITH ranked AS (
		  SELECT w.id,w.workspace_id,w.session_id,
		    row_number() OVER (PARTITION BY w.workspace_id,w.session_id,w.assigned_agent_id
		      ORDER BY w.priority DESC,w.ready_at,w.id) AS queue_position,
		    EXISTS (
		      SELECT 1 FROM research_work_item active
		      WHERE active.workspace_id=w.workspace_id AND active.session_id=w.session_id
		        AND active.assigned_agent_id=w.assigned_agent_id
		        AND active.id<>w.id AND active.status IN ('dispatching','running','awaiting_input')
		    ) AS agent_busy
		  FROM research_work_item w
		  JOIN research_session s ON s.id=w.session_id
		  WHERE s.orchestrator_version='research-run-v6' AND s.status='running'
		    AND w.kind<>'director' AND w.status='ready'
		), surplus AS (
		  SELECT w.id
		  FROM research_work_item w JOIN ranked candidate ON candidate.id=w.id
		  WHERE candidate.agent_busy OR candidate.queue_position>1
		  ORDER BY w.session_id,w.assigned_agent_id,w.priority DESC,w.ready_at,w.id
		  FOR UPDATE OF w SKIP LOCKED LIMIT $1
		)
		UPDATE research_work_item w
		SET status='failed',terminal_reason_code='contract_rejected',
		    terminal_reason_detail='同一个智能体被分配了多个活动 Work；独立调研方向必须分配给不同的 run-scoped Agent。',
		    lease_token=NULL,lease_expires_at=NULL,state_version=w.state_version+1,updated_at=now()
		WHERE w.id IN (SELECT id FROM surplus)
		RETURNING w.workspace_id::text,w.session_id::text,w.id::text,w.status,w.state_version
	`, limit)
	if err != nil {
		return 0, err
	}
	for oversubscribedRows.Next() {
		var item recovered
		if err = oversubscribedRows.Scan(&item.workspaceID, &item.runID, &item.workItemID, &item.status, &item.version); err != nil {
			oversubscribedRows.Close()
			return 0, err
		}
		item.recoveryKind = "agent_active_work_conflict"
		items = append(items, item)
	}
	if err = oversubscribedRows.Err(); err != nil {
		oversubscribedRows.Close()
		return 0, err
	}
	oversubscribedRows.Close()
	// A retryable runtime-capacity failure is not evidence that the research
	// contract or Agent is bad. Reopen each exhausted Work Item once with a
	// bounded delay. The one-time Run Event fence prevents an unavailable model
	// from creating an infinite retry loop while still recovering a transient
	// Codex capacity incident without user intervention.
	runtimeRows, err := tx.Query(ctx, `
		WITH retryable_runtime AS (
		  SELECT w.id
		  FROM research_work_item w
		  JOIN research_session s ON s.id=w.session_id
		  WHERE s.orchestrator_version='research-run-v6'
		    AND s.status='running'
		    AND w.kind<>'director'
		    AND w.status='failed'
		    AND w.terminal_reason_code='attempt_budget_exhausted'
		    AND EXISTS (
		      SELECT 1
		      FROM research_work_item_attempt a
		      JOIN agent_inbox_event inbox ON inbox.id=a.inbox_task_id
		      WHERE a.work_item_id=w.id
		        AND inbox.terminal_outcome='failed'
		        AND inbox.retryable
		        AND inbox.failure_reason='agent_error.model_not_found_or_unavailable'
		    )
		    AND NOT EXISTS (
		      SELECT 1
		      FROM research_work_item_attempt a
		      LEFT JOIN agent_inbox_event inbox ON inbox.id=a.inbox_task_id
		      WHERE a.work_item_id=w.id
		        AND (inbox.id IS NULL
		          OR inbox.terminal_outcome IS DISTINCT FROM 'failed'
		          OR NOT COALESCE(inbox.retryable,false)
		          OR inbox.failure_reason IS DISTINCT FROM 'agent_error.model_not_found_or_unavailable')
		    )
		    AND NOT EXISTS (
		      SELECT 1 FROM research_run_event e
		      WHERE e.session_id=w.session_id
		        AND e.idempotency_key='v6-work-item-runtime-reopened:'||w.id::text
		    )
		  ORDER BY s.id,w.updated_at,w.id
		  FOR UPDATE OF s,w SKIP LOCKED LIMIT $1
		)
		UPDATE research_work_item w
		SET status='ready',attempt_count=0,terminal_reason_code='',terminal_reason_detail='',
		    lease_token=NULL,lease_expires_at=NULL,ready_at=now()+interval '2 minutes',
		    state_version=state_version+1,updated_at=now()
		WHERE w.id IN (SELECT id FROM retryable_runtime)
		RETURNING w.workspace_id::text,w.session_id::text,w.id::text,w.status,w.state_version
	`, limit)
	if err != nil {
		return 0, err
	}
	for runtimeRows.Next() {
		var item recovered
		if err = runtimeRows.Scan(&item.workspaceID, &item.runID, &item.workItemID, &item.status, &item.version); err != nil {
			runtimeRows.Close()
			return 0, err
		}
		item.recoveryKind = "retryable_runtime"
		items = append(items, item)
	}
	if err = runtimeRows.Err(); err != nil {
		runtimeRows.Close()
		return 0, err
	}
	runtimeRows.Close()
	// Membership state tracks active V6 Work, not the lifetime of the Agent.
	// Terminal Attempts must release the member or the graph permanently shows
	// "working" after execution has already ended.
	workItemIDs := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		workItemID, parseErr := uuid.Parse(item.workItemID)
		if parseErr != nil {
			return 0, parseErr
		}
		workItemIDs = append(workItemIDs, workItemID)
	}
	if len(workItemIDs) > 0 {
		_, err = tx.Exec(ctx, `
		UPDATE research_team_membership m
		SET state='idle'
		WHERE m.state='working'
		  AND EXISTS (
		    SELECT 1 FROM research_work_item_attempt settled
		    WHERE settled.membership_id=m.id
		      AND settled.work_item_id=ANY($1::uuid[])
		      AND settled.status IN ('succeeded','failed','cancelled','lost')
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM research_work_item_attempt active
		    WHERE active.membership_id=m.id
		      AND active.status IN ('dispatching','running')
		  )
	`, workItemIDs)
		if err != nil {
			return 0, err
		}
	}
	for _, item := range items {
		eventKey := fmt.Sprintf("v6-work-item-recovered:%s:%d", item.workItemID, item.version)
		if item.recoveryKind == "retryable_runtime" {
			eventKey = "v6-work-item-runtime-reopened:" + item.workItemID
		}
		if _, err = appendEvent(ctx, tx, item.workspaceID, item.runID, "v6_work_item_recovered",
			eventKey, "system", "", map[string]any{
				"work_item_id": item.workItemID, "status": item.status,
				"state_version": item.version, "recovery_kind": item.recoveryKind,
			}); err != nil {
			return 0, err
		}
	}
	if err = s.commitResearchTx(ctx, txOpV6WorkItemRecover, tx); err != nil {
		return 0, err
	}
	return len(items), nil
}
