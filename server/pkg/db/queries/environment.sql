-- name: CreateEnvironment :one
INSERT INTO environment (workspace_id, sandbox_ids, parent_env_id, mode, domain)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetEnvironment :one
SELECT * FROM environment
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteEnvironment :exec
DELETE FROM environment WHERE id = $1 AND workspace_id = $2;

-- name: GetEnvDispatchRequest :one
SELECT * FROM env_dispatch_request
WHERE workspace_id = $1 AND idempotency_key = $2;

-- name: CreateEnvDispatchRequest :exec
INSERT INTO env_dispatch_request (workspace_id, idempotency_key, response)
VALUES ($1, $2, $3);

-- name: ListOwnedEnvDispatchResources :one
-- Returns the derived resources owned by an env-dispatch binding (keyed by
-- env_id + source_agent_id) for idempotent cleanup: sandbox_instance_id,
-- runtime_id, derived_agent_id, daemon_id, and training session identity.
SELECT id, env_id, agent_id, source_agent_id, derived_agent_id,
       sandbox_instance_id, runtime_id, daemon_id, training_session_id,
       training_session_ref, credential_kind, model_config_owner_agent_id,
       status
FROM environment_agent_sandbox
WHERE env_id = $1 AND source_agent_id = $2;

-- name: CreateEnvDispatchRun :exec
-- Persists the durable dispatch root row for a project (one row per project,
-- keyed by project_id). Created after the project exists, carrying workspace_id
-- and training_mode. root_task_id starts NULL and is bound later via
-- BindEnvDispatchRootTask. Upsert on conflict so a re-dispatch of the same
-- project refreshes workspace_id/training_mode without orphaning the row.
INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode)
VALUES ($1, $2, $3)
ON CONFLICT (project_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  training_mode = EXCLUDED.training_mode;

-- name: BindEnvDispatchRootTask :exec
-- Binds the enqueued leader task as the dispatch root
-- (env_dispatch_run.root_task_id = rootTaskID). Called immediately after the
-- leader task is enqueued. No-op (0 rows affected) when no run row exists yet;
-- callers treat an unbound root as in_progress.
UPDATE env_dispatch_run
SET root_task_id = $2
WHERE project_id = $1;

-- name: GetEnvDispatchRootTaskStatus :one
-- Resolves the status of the dispatch's bound root task for the GET /dag
-- readiness decision. Readiness is derived EXCLUSIVELY from this row, not from
-- training_dispatch. The INNER JOIN yields no rows (pgx.ErrNoRows) when no
-- env_dispatch_run exists for the (project_id, workspace_id) pair or when
-- root_task_id is NULL (rollout not started / root not enqueued); the caller
-- treats both as "in_progress" (keep polling). A non-terminal status is also
-- in_progress; a terminal status (completed/failed/cancelled) triggers DAG
-- assembly.
SELECT atq.status
FROM env_dispatch_run r
JOIN agent_inbox_event atq ON atq.id = r.root_task_id
WHERE r.project_id = $1 AND r.workspace_id = $2;


-- name: CreateEnvDispatchRunWithSource :one
INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode, source_task_id, sample_index)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SetEnvDispatchRunLocalTargets :exec
UPDATE env_dispatch_run
SET local_issue_id = $3,
    local_channel_id = $4
WHERE project_id = $1 AND workspace_id = $2;

-- name: GetEnvDispatchRunSourceTask :one
SELECT st.id, st.workspace_id, st.type, st.payload, st.content_hash, st.created_at
FROM env_dispatch_run edr
JOIN source_task st ON st.id = edr.source_task_id
WHERE edr.project_id = $1 AND edr.workspace_id = $2;


-- name: CreateMixedRLRun :one
INSERT INTO env_dispatch_run (
  project_id, workspace_id, training_mode, run_id, source_task_id, sample_index,
  quiet_window_ms, total_timeout_seconds, status
) VALUES (
  sqlc.arg(project_id), sqlc.arg(workspace_id), false, sqlc.arg(run_id),
  sqlc.narg(source_task_id), sqlc.arg(sample_index), sqlc.arg(quiet_window_ms),
  sqlc.arg(total_timeout_seconds), 'provisioning'
)
RETURNING *;

-- name: GetMixedRLRun :one
SELECT * FROM env_dispatch_run
WHERE run_id = sqlc.arg(run_id);

-- name: ListMixedRLQuiescenceCandidates :many
-- The evaluator makes the final transition decision after this bounded scan.
SELECT * FROM env_dispatch_run
WHERE status IN ('running', 'quiet_candidate')
  AND (
    timeout_deadline_at <= sqlc.arg(now_at)
    OR (status = 'running'
        AND active_turn_count = 0
        AND pending_delivery_count = 0
        AND queued_message_count = 0
        AND inflight_tool_count = 0
        AND unfinished_capture_batch_count = 0)
    OR status = 'quiet_candidate'
  )
ORDER BY timeout_deadline_at NULLS LAST, run_id;

-- name: LockMixedRLRun :one
SELECT * FROM env_dispatch_run
WHERE run_id = sqlc.arg(run_id)
FOR UPDATE;

-- name: TransitionMixedRLRunStatus :one
WITH transition AS (
  SELECT sqlc.arg(next_status)::text AS next_status,
         sqlc.arg(run_id)::uuid AS run_id,
         sqlc.arg(expected_status)::text AS expected_status
)
UPDATE env_dispatch_run AS run
SET status = transition.next_status,
    quiet_candidate_since = CASE
      WHEN transition.next_status = 'quiet_candidate' THEN now()
      WHEN transition.next_status = 'running' THEN NULL
      ELSE run.quiet_candidate_since
    END,
    updated_at = now()
FROM transition
WHERE run.run_id = transition.run_id
  AND run.status = transition.expected_status
  AND (
    (transition.expected_status = 'provisioning' AND transition.next_status = 'preflight')
    OR (transition.expected_status = 'preflight' AND transition.next_status = 'failed_preflight')
    OR (transition.expected_status = 'running' AND transition.next_status IN ('quiet_candidate', 'freezing'))
    OR (transition.expected_status = 'quiet_candidate' AND transition.next_status IN ('running', 'freezing'))
  )
RETURNING run.*;

-- name: StartMixedRLRunTimeout :one
WITH timeout_start AS (
  SELECT sqlc.arg(submitted_at)::timestamptz AS submitted_at,
         sqlc.arg(run_id)::uuid AS run_id
)
UPDATE env_dispatch_run AS run
SET status = 'running',
    initial_message_submitted_at = timeout_start.submitted_at,
    timeout_deadline_at = timeout_start.submitted_at + make_interval(secs => run.total_timeout_seconds),
    quiet_candidate_since = NULL,
    updated_at = now()
FROM timeout_start
WHERE run.run_id = timeout_start.run_id
  AND run.status = 'preflight'
  AND run.initial_message_submitted_at IS NULL
  AND NOT EXISTS (
    SELECT 1
    FROM env_dispatch_run_agent agent
    WHERE agent.run_id = run.run_id
      AND agent.training_mode = 'online_rl'
      AND agent.areal_session_id IS NULL
  )
RETURNING run.*;

-- name: CompleteMixedRLRunWithSnapshot :one
UPDATE env_dispatch_run AS run
SET status = sqlc.arg(terminal_status),
    frozen_snapshot_id = snapshot.snapshot_id,
    snapshot_hash = snapshot.snapshot_hash,
    frozen_at = snapshot.created_at,
    quiet_candidate_since = NULL,
    updated_at = now()
FROM interaction_dag_frozen_snapshot AS snapshot
WHERE run.run_id = sqlc.arg(run_id)
  AND run.status = 'freezing'
  AND snapshot.run_id = run.run_id
  AND snapshot.snapshot_id = sqlc.arg(snapshot_id)
  AND snapshot.snapshot_hash = sqlc.arg(snapshot_hash)
  AND snapshot.run_status = sqlc.arg(terminal_status)
  AND sqlc.arg(terminal_status)::text IN ('completed', 'failed_timeout')
RETURNING run.*;

-- name: AdjustMixedRLRunActivity :one
UPDATE env_dispatch_run
SET active_turn_count = active_turn_count + sqlc.arg(active_turn_delta),
    pending_delivery_count = pending_delivery_count + sqlc.arg(pending_delivery_delta),
    queued_message_count = queued_message_count + sqlc.arg(queued_message_delta),
    inflight_tool_count = inflight_tool_count + sqlc.arg(inflight_tool_delta),
    unfinished_capture_batch_count = unfinished_capture_batch_count + sqlc.arg(unfinished_capture_delta),
    quiet_candidate_since = NULL,
    status = CASE WHEN status = 'quiet_candidate' THEN 'running' ELSE status END,
    updated_at = now()
WHERE run_id = sqlc.arg(run_id)
  AND active_turn_count + sqlc.arg(active_turn_delta) >= 0
  AND pending_delivery_count + sqlc.arg(pending_delivery_delta) >= 0
  AND queued_message_count + sqlc.arg(queued_message_delta) >= 0
  AND inflight_tool_count + sqlc.arg(inflight_tool_delta) >= 0
  AND unfinished_capture_batch_count + sqlc.arg(unfinished_capture_delta) >= 0
RETURNING *;

-- name: CreateMixedRLRunAgent :one
INSERT INTO env_dispatch_run_agent (
  run_agent_id, run_id, source_agent_id, execution_agent_id, runtime_id, pi_session_id,
  training_mode, areal_session_id, capture_boundary
) VALUES (
  sqlc.arg(run_agent_id), sqlc.arg(run_id), sqlc.arg(source_agent_id), sqlc.arg(execution_agent_id),
  sqlc.arg(runtime_id), sqlc.arg(pi_session_id), sqlc.arg(training_mode),
  NULLIF(sqlc.arg(areal_session_id), ''), sqlc.arg(capture_boundary)
)
RETURNING *;

-- name: GetMixedRLRunAgent :one
SELECT * FROM env_dispatch_run_agent
WHERE run_id = sqlc.arg(run_id) AND run_agent_id = sqlc.arg(run_agent_id);

-- name: ListMixedRLRunAgents :many
SELECT * FROM env_dispatch_run_agent
WHERE run_id = sqlc.arg(run_id)
ORDER BY source_agent_id, run_agent_id;

-- name: CreateMixedRLResidentTurn :one
WITH allocated AS (
  UPDATE env_dispatch_run_agent
  SET next_turn_ordinal = next_turn_ordinal + 1
  WHERE run_id = sqlc.arg(run_id) AND run_agent_id = sqlc.arg(run_agent_id)
  RETURNING next_turn_ordinal - 1 AS turn_ordinal
)
INSERT INTO env_dispatch_resident_turn (
  turn_id, run_id, run_agent_id, turn_ordinal, status,
  capture_started_at, accepted_message_ids
)
SELECT sqlc.arg(turn_id), sqlc.arg(run_id), sqlc.arg(run_agent_id),
       allocated.turn_ordinal, sqlc.arg(status), now(),
       sqlc.arg(accepted_message_ids)::uuid[]

FROM allocated
RETURNING *;

-- name: GetMixedRLResidentTurn :one
SELECT turn_id, run_id, run_agent_id, turn_ordinal, status,
       capture_started_at, capture_completed_at, accepted_message_ids,
       started_at, completed_at
FROM env_dispatch_resident_turn
WHERE turn_id = sqlc.arg(turn_id);

-- name: GetMixedRLTurnCaptureBatch :one
SELECT capture_batch_id, turn_id, capture_boundary, call_count,
       action_count, consumption_count, payload_hash, accepted_at
FROM env_dispatch_turn_capture_batch
WHERE turn_id = sqlc.arg(turn_id);

-- name: InsertMixedRLTurnCaptureBatch :one
WITH matched_turn AS MATERIALIZED (
  SELECT turn.turn_id
  FROM env_dispatch_resident_turn turn
  JOIN env_dispatch_run_agent agent
    ON agent.run_id = turn.run_id
   AND agent.run_agent_id = turn.run_agent_id
  WHERE turn.turn_id = sqlc.arg(turn_id)
    AND agent.capture_boundary = sqlc.arg(capture_boundary)
  FOR KEY SHARE OF turn, agent
)
INSERT INTO env_dispatch_turn_capture_batch (
  capture_batch_id, turn_id, capture_boundary, call_count,
  action_count, consumption_count, payload_hash
)
SELECT sqlc.arg(capture_batch_id), matched_turn.turn_id,
       sqlc.arg(capture_boundary), sqlc.arg(call_count),
       sqlc.arg(action_count), sqlc.arg(consumption_count),
       sqlc.arg(payload_hash)
FROM matched_turn
RETURNING *;

-- name: ListMixedRLActiveResidentTurns :many
-- The timeout freezer uses this stable order to convert every in-flight
-- resident turn into an explicit capture gap before publishing a partial DAG.
SELECT turn_id, run_id, run_agent_id, turn_ordinal, status,
       capture_started_at, capture_completed_at, accepted_message_ids,
       started_at, completed_at
FROM env_dispatch_resident_turn
WHERE run_id = sqlc.arg(run_id)
  AND status = 'active'
ORDER BY run_agent_id, turn_ordinal, turn_id;

-- name: CompleteMixedRLResidentTurn :one
UPDATE env_dispatch_resident_turn
SET status = sqlc.arg(status),
    capture_completed_at = sqlc.arg(completed_at),
    completed_at = sqlc.arg(completed_at)
WHERE turn_id = sqlc.arg(turn_id)
  AND status = 'active'
RETURNING *;

-- name: CreateMixedRLDeliveryObligation :one
WITH upserted AS (
  INSERT INTO env_dispatch_delivery_obligation AS obligation (
    delivery_id, run_id, channel_message_id, source_recipient_agent_id,
    run_agent_id, state, queued_at
  ) VALUES (
    sqlc.arg(delivery_id), sqlc.arg(run_id), sqlc.arg(channel_message_id),
    sqlc.arg(source_recipient_agent_id), sqlc.arg(run_agent_id),
    sqlc.arg(state), sqlc.narg(queued_at)
  )
  ON CONFLICT (channel_message_id, run_agent_id) DO UPDATE
  SET source_recipient_agent_id = obligation.source_recipient_agent_id
  RETURNING obligation.*, (xmax = 0) AS inserted
), counted AS (
  UPDATE env_dispatch_run AS run
  SET pending_delivery_count = run.pending_delivery_count + 1,
      quiet_candidate_since = NULL,
      status = CASE WHEN run.status = 'quiet_candidate' THEN 'running' ELSE run.status END,
      updated_at = now()
  FROM upserted
  WHERE upserted.inserted
    AND upserted.state IN ('pending', 'queued', 'accepted')
    AND run.run_id = upserted.run_id
  RETURNING run.run_id
)
SELECT delivery_id, run_id, channel_message_id, source_recipient_agent_id,
       run_agent_id, state, queued_at, settled_at, created_at
FROM upserted
WHERE NOT inserted
   OR state NOT IN ('pending', 'queued', 'accepted')
   OR EXISTS (SELECT 1 FROM counted);

-- name: SettleMixedRLDeliveryObligation :one
WITH input AS (
  SELECT sqlc.arg(state)::text AS state,
         sqlc.arg(settled_at)::timestamptz AS settled_at,
         sqlc.arg(delivery_id)::uuid AS delivery_id
), target AS MATERIALIZED (
  SELECT obligation.*
  FROM env_dispatch_delivery_obligation AS obligation, input
  WHERE obligation.delivery_id = input.delivery_id
  FOR UPDATE OF obligation
), settled AS (
  UPDATE env_dispatch_delivery_obligation AS obligation
  SET state = input.state, settled_at = input.settled_at
  FROM target, input
  WHERE obligation.delivery_id = target.delivery_id
    AND target.state IN ('pending', 'queued', 'accepted')
    AND input.state IN ('completed', 'failed', 'cancelled')
  RETURNING obligation.*
), counted AS (
  UPDATE env_dispatch_run AS run
  SET pending_delivery_count = GREATEST(run.pending_delivery_count - 1, 0),
      updated_at = now()
  FROM settled
  WHERE run.run_id = settled.run_id
  RETURNING run.run_id
), result AS (
  SELECT settled.*, true AS transitioned
  FROM settled
  UNION ALL
  SELECT target.*, false AS transitioned
  FROM target, input
  WHERE NOT EXISTS (SELECT 1 FROM settled)
    AND target.state IN ('completed', 'failed', 'cancelled')
    AND input.state IN ('completed', 'failed', 'cancelled')
)
SELECT delivery_id, run_id, channel_message_id, source_recipient_agent_id,
       run_agent_id, state, queued_at, settled_at, created_at
FROM result
WHERE NOT transitioned OR EXISTS (SELECT 1 FROM counted);

-- name: RecordMixedRLCaptureGap :one
WITH locked_run AS MATERIALIZED (
  SELECT env_dispatch_run.run_id, env_dispatch_run.status,
         env_dispatch_run.frozen_snapshot_id
  FROM env_dispatch_run
  WHERE env_dispatch_run.run_id = sqlc.arg(run_id)
  FOR UPDATE
), inserted AS (
  INSERT INTO env_dispatch_run_audit_event (
    event_id, run_id, run_agent_id, turn_id, kind, reason, summary, snapshot_id
  )
  SELECT sqlc.arg(event_id), locked_run.run_id, sqlc.narg(run_agent_id),
         sqlc.narg(turn_id),
         CASE
           WHEN locked_run.status IN ('completed', 'failed_timeout') THEN 'late_event'
           ELSE 'capture_gap'
         END,
         sqlc.arg(reason), sqlc.arg(summary),
         CASE
           WHEN locked_run.status IN ('completed', 'failed_timeout')
             THEN locked_run.frozen_snapshot_id
           ELSE NULL
         END
  FROM locked_run
  WHERE locked_run.status NOT IN ('freezing', 'failed_preflight')
  RETURNING run_id, kind
)
UPDATE env_dispatch_run AS run
SET capture_gap_count = run.capture_gap_count + CASE
      WHEN inserted.kind = 'capture_gap' THEN 1 ELSE 0
    END,
    updated_at = CASE
      WHEN inserted.kind = 'capture_gap' THEN now() ELSE run.updated_at
    END
FROM inserted
WHERE run.run_id = inserted.run_id
RETURNING run.*;

-- name: ListMixedRLRunAuditEvents :many
SELECT * FROM env_dispatch_run_audit_event
WHERE run_id = sqlc.arg(run_id)
ORDER BY received_at, event_id;

-- name: DeleteMixedRLRunsByAgentIDs :exec
DELETE FROM env_dispatch_run AS run
WHERE EXISTS (
  SELECT 1
  FROM env_dispatch_run_agent AS run_agent
  WHERE run_agent.run_id = run.run_id
    AND (
      run_agent.source_agent_id = ANY(sqlc.arg(agent_ids)::uuid[])
      OR run_agent.execution_agent_id = ANY(sqlc.arg(agent_ids)::uuid[])
    )
);

-- name: DeleteMixedRLDeliveryObligationsBySourceAgentIDs :exec
WITH deleted AS MATERIALIZED (
  DELETE FROM env_dispatch_delivery_obligation
  WHERE source_recipient_agent_id = ANY(sqlc.arg(source_agent_ids)::uuid[])
  RETURNING run_id, state
), removed_pending AS (
  SELECT run_id, count(*)::bigint AS pending_count
  FROM deleted
  WHERE state IN ('pending', 'queued', 'accepted')
  GROUP BY run_id
)
UPDATE env_dispatch_run AS run
SET pending_delivery_count = GREATEST(run.pending_delivery_count - removed_pending.pending_count, 0),
    updated_at = now()
FROM removed_pending
WHERE run.run_id = removed_pending.run_id;

-- name: DeleteMixedRLRun :execrows
DELETE FROM env_dispatch_run
WHERE run_id = sqlc.arg(run_id) AND workspace_id = sqlc.arg(workspace_id);
