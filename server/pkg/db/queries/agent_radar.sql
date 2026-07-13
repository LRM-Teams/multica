-- name: CreateAgentRadarRun :one
INSERT INTO agent_radar_run (
    workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
    status, cooldown_key, context_summary, scheduled_for
) VALUES (
    $1, $2, sqlc.narg('runtime_id'), $3, $4,
    COALESCE(sqlc.narg('status'), 'planned'), $5, $6, COALESCE(sqlc.narg('scheduled_for'), now())
)
-- Handle both the per-agent active guard and the workspace-supervisor active
-- guard installed by migration 169.
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetAgentRadarRun :one
SELECT * FROM agent_radar_run
WHERE id = $1;

-- name: ListAgentRadarRunsByAgent :many
SELECT * FROM agent_radar_run
WHERE workspace_id = $1 AND agent_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListPlannedAgentRadarRuns :many
SELECT * FROM agent_radar_run
WHERE status = 'planned' AND scheduled_for <= now()
ORDER BY scheduled_for ASC, created_at ASC
LIMIT $1;

-- name: IsWorkspaceRadarSupervisorAuthorized :one
-- A Wendy binding remains valid only while its owner still owns the workspace.
-- Scheduler and executor both use this check so an ownership transfer cannot
-- leave the former owner's private agent supervising workspace data.
SELECT EXISTS (
    SELECT 1
    FROM workspace_radar_state state
    JOIN agent supervisor
      ON supervisor.workspace_id = state.workspace_id
     AND supervisor.id = state.supervisor_agent_id
    JOIN member owner_member
      ON owner_member.workspace_id = state.workspace_id
     AND owner_member.user_id = supervisor.owner_id
     AND owner_member.role = 'owner'
    WHERE state.workspace_id = sqlc.arg('workspace_id')
      AND state.supervisor_agent_id = sqlc.arg('agent_id')
      AND state.enabled
      AND supervisor.archived_at IS NULL
);

-- name: UpdateAgentRadarRunStatus :one
UPDATE agent_radar_run
SET
    status = $2,
    task_id = COALESCE(sqlc.narg('task_id'), task_id),
    action_plan = COALESCE(sqlc.narg('action_plan'), action_plan),
    error = COALESCE(sqlc.narg('error'), error),
    started_at = CASE WHEN $2 = 'running' AND started_at IS NULL THEN now() ELSE started_at END,
    finished_at = CASE WHEN $2 IN ('succeeded', 'no_action', 'failed', 'cancelled') THEN now() ELSE finished_at END,
    updated_at = now()
WHERE id = $1
  AND (
    $2 NOT IN ('succeeded', 'no_action', 'failed', 'cancelled')
    OR status = 'executing'
  )
RETURNING *;

-- name: CountRecentWorkspaceScheduledRadarRuns :one
SELECT count(*)::bigint
FROM agent_radar_run rr
LEFT JOIN agent_task_queue atq ON atq.id = rr.task_id
WHERE rr.workspace_id = $1
  AND rr.trigger_kind = 'scheduled'
  AND rr.cooldown_key = 'workspace_supervisor_radar'
  AND rr.created_at >= $2
  -- Migration 167 closed invalid pre-existing runs so the active-run unique
  -- guard could be installed. Those rows never represented a new Radar
  -- attempt and must not consume the retry-storm budget.
  AND COALESCE(atq.failure_reason, '') NOT IN (
      'radar_active_run_repair',
      'radar_stale_dispatch_repair'
  )
  AND NOT (
      rr.status = 'failed'
      AND (
          COALESCE(rr.error, '') LIKE 'migration:%'
          OR COALESCE(rr.error, '') = 'radar_stale_dispatch_repair'
      )
  );

-- name: FailStaleDispatchedAgentRadarTasks :many
-- ReclaimAgentTask refreshes dispatched_at when it re-delivers a claim whose
-- start acknowledgement never arrived. Use the immutable creation age here
-- so repeated re-delivery cannot keep a Radar task active forever. The one-hour
-- threshold is deliberately much larger than the normal 90-second claim
-- recovery and five-minute dispatch timeout windows.
WITH victims AS MATERIALIZED (
    SELECT rr.id AS radar_run_id, atq.id AS task_id
    FROM agent_radar_run rr
    JOIN agent_task_queue atq ON atq.id = rr.task_id
    WHERE rr.status = 'queued'
      AND rr.started_at IS NULL
      AND rr.created_at < now() - make_interval(secs => @stale_age_secs::double precision)
      AND atq.status = 'dispatched'
      AND atq.started_at IS NULL
      AND atq.created_at < now() - make_interval(secs => @stale_age_secs::double precision)
      AND atq.agent_id = rr.agent_id
      -- Runtime consolidation can reassign the task before deleting the old
      -- runtime, which leaves rr.runtime_id stale or NULL. The task FK plus
      -- the context backpointer are the durable pair identity.
      AND atq.context->>'type' = 'agent_radar'
      AND atq.context->>'radar_run_id' = rr.id::text
    ORDER BY atq.created_at ASC, atq.id ASC
    LIMIT @max_per_tick::int
    -- Completion locks task then run. This repair only mutates the task, so lock
    -- only that row and let HandleFailedTasks terminalize the run afterwards.
    FOR UPDATE OF atq SKIP LOCKED
)
UPDATE agent_task_queue atq
SET
    status = 'failed',
    completed_at = now(),
    error = 'Radar task remained dispatched without starting',
    failure_reason = 'radar_stale_dispatch_repair'
FROM victims v
WHERE atq.id = v.task_id
  AND atq.status = 'dispatched'
  AND atq.started_at IS NULL
  AND atq.created_at < now() - make_interval(secs => @stale_age_secs::double precision)
RETURNING atq.*;

-- name: MarkAgentRadarRunRunningByTaskID :execrows
UPDATE agent_radar_run
SET
    status = 'running',
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE task_id = $1
  AND status IN ('planned', 'queued');

-- name: ClaimAgentRadarRunForExecution :one
-- Completion requests are retryable at the HTTP layer. Only one request may
-- cross this state boundary and execute the action plan.
UPDATE agent_radar_run
SET
    status = 'executing',
    started_at = COALESCE(started_at, now()),
    updated_at = now()
WHERE id = $1
  AND status IN ('queued', 'running')
RETURNING *;

-- name: ClaimAgentRadarRunForCompletedTask :one
-- CompleteAgentTask and this claim run in the same transaction. Other
-- transactions therefore observe either running+queued/running or
-- completed+executing, never the crash window completed+unclaimed.
UPDATE agent_radar_run run
SET
    status = 'executing',
    started_at = COALESCE(run.started_at, now()),
    updated_at = now()
FROM agent_task_queue task
WHERE run.id = sqlc.arg('run_id')
  AND run.task_id = sqlc.arg('task_id')
  AND task.id = sqlc.arg('task_id')
  AND task.agent_id = run.agent_id
  AND task.status = 'completed'
  AND task.context->>'type' = 'agent_radar'
  AND task.context->>'radar_run_id' = run.id::text
  AND run.status IN ('queued', 'running')
RETURNING run.*;

-- name: FailAgentRadarRunByTaskID :execrows
UPDATE agent_radar_run
SET
    status = 'failed',
    error = COALESCE(sqlc.narg('error'), error),
    finished_at = COALESCE(finished_at, now()),
    updated_at = now()
WHERE task_id = $1
  AND status IN ('planned', 'queued', 'running', 'executing');

-- name: CancelAgentRadarRunByTaskID :execrows
UPDATE agent_radar_run
SET
    status = 'cancelled',
    error = COALESCE(sqlc.narg('error'), error),
    finished_at = COALESCE(finished_at, now()),
    updated_at = now()
WHERE task_id = $1
  AND status IN ('planned', 'queued', 'running', 'executing');

-- name: CreateAgentRadarAction :one
INSERT INTO agent_radar_action (
    radar_run_id, workspace_id, agent_id, action_type, status, risk_level,
    confidence, dedupe_key, target_kind, target_id, reason, evidence, payload
) VALUES (
    $1, $2, $3, $4, COALESCE(sqlc.narg('status'), 'proposed'), $5,
    $6, $7, $8, sqlc.narg('target_id'), $9, $10, $11
)
-- Handle both the historical per-agent dedupe index and the workspace-wide
-- scheduled-Wendy index installed by migration 169.
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ListAgentRadarActionsByRun :many
SELECT * FROM agent_radar_action
WHERE radar_run_id = $1
ORDER BY created_at ASC, id ASC;

-- name: UpdateAgentRadarActionStatus :one
UPDATE agent_radar_action
SET
    status = $2,
    result = COALESCE(sqlc.narg('result'), result),
    error = COALESCE(sqlc.narg('error'), error),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkWorkspaceRadarSucceeded :execrows
-- Use the run's scheduled_for timestamp as the observation watermark. Changes
-- committed while the model was running therefore remain visible to the next
-- sweep instead of being skipped by a completion-time watermark.
--
-- The receipt CTE makes this state transition idempotent. last_full_review_at
-- advances only when this run was due for the six-hour full review; ordinary
-- change-driven runs no longer postpone that review indefinitely.
WITH eligible AS MATERIALIZED (
    SELECT run.*, scan.change_cursor_through_version, scan.changes_has_more,
           COALESCE((scan.static_wrapped_sections->>'all')::boolean, false) AS static_cycle_complete
    FROM agent_radar_run run
    JOIN workspace_radar_run_scan scan ON scan.radar_run_id = run.id
    WHERE run.id = $1
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status IN ('succeeded', 'no_action')
), claimed AS (
    INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
    SELECT eligible.id, 'succeeded'
    FROM eligible
    ON CONFLICT (radar_run_id) DO NOTHING
    RETURNING radar_run_id
)
UPDATE workspace_radar_state state
SET
    last_success_at = GREATEST(COALESCE(state.last_success_at, run.scheduled_for), run.scheduled_for),
    last_full_review_at = CASE
      WHEN run.static_cycle_complete
      THEN GREATEST(COALESCE(state.last_full_review_at, run.scheduled_for), run.scheduled_for)
      ELSE state.last_full_review_at
    END,
    change_cursor_version = GREATEST(state.change_cursor_version, run.change_cursor_through_version),
    last_applied_scheduled_for = GREATEST(COALESCE(state.last_applied_scheduled_for, run.scheduled_for), run.scheduled_for),
    consecutive_failures = 0,
    next_due_at = CASE
      WHEN run.changes_has_more
        OR state.change_version > run.change_cursor_through_version
        OR NOT run.static_cycle_complete
      THEN now()
      ELSE now() + interval '30 minutes'
    END,
    updated_at = now()
FROM eligible run
JOIN claimed ON claimed.radar_run_id = run.id
WHERE run.workspace_id = state.workspace_id
  AND run.agent_id = state.supervisor_agent_id
  AND (
    state.last_applied_scheduled_for IS NULL
    OR run.scheduled_for >= state.last_applied_scheduled_for
  );

-- name: MarkWorkspaceRadarFailedByRunID :execrows
WITH eligible AS MATERIALIZED (
    SELECT run.*
    FROM agent_radar_run run
    WHERE run.id = $1
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'failed'
), claimed AS (
    INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
    SELECT eligible.id, 'failed'
    FROM eligible
    ON CONFLICT (radar_run_id) DO NOTHING
    RETURNING radar_run_id
)
UPDATE workspace_radar_state state
SET
    consecutive_failures = state.consecutive_failures + 1,
    next_due_at = now() + CASE
      WHEN state.consecutive_failures = 0 THEN interval '15 minutes'
      WHEN state.consecutive_failures = 1 THEN interval '30 minutes'
      WHEN state.consecutive_failures = 2 THEN interval '1 hour'
      ELSE interval '2 hours'
    END,
    last_applied_scheduled_for = GREATEST(COALESCE(state.last_applied_scheduled_for, run.scheduled_for), run.scheduled_for),
    updated_at = now()
FROM eligible run
JOIN claimed ON claimed.radar_run_id = run.id
WHERE run.workspace_id = state.workspace_id
  AND run.agent_id = state.supervisor_agent_id
  AND (
    state.last_applied_scheduled_for IS NULL
    OR run.scheduled_for >= state.last_applied_scheduled_for
  );

-- name: MarkWorkspaceRadarFailedByTaskID :execrows
WITH eligible AS MATERIALIZED (
    SELECT run.*
    FROM agent_radar_run run
    WHERE run.task_id = $1
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'failed'
), claimed AS (
    INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
    SELECT eligible.id, 'failed'
    FROM eligible
    ON CONFLICT (radar_run_id) DO NOTHING
    RETURNING radar_run_id
)
UPDATE workspace_radar_state state
SET
    consecutive_failures = state.consecutive_failures + 1,
    next_due_at = now() + CASE
      WHEN state.consecutive_failures = 0 THEN interval '15 minutes'
      WHEN state.consecutive_failures = 1 THEN interval '30 minutes'
      WHEN state.consecutive_failures = 2 THEN interval '1 hour'
      ELSE interval '2 hours'
    END,
    last_applied_scheduled_for = GREATEST(COALESCE(state.last_applied_scheduled_for, run.scheduled_for), run.scheduled_for),
    updated_at = now()
FROM eligible run
JOIN claimed ON claimed.radar_run_id = run.id
WHERE run.workspace_id = state.workspace_id
  AND run.agent_id = state.supervisor_agent_id
  AND (
    state.last_applied_scheduled_for IS NULL
    OR run.scheduled_for >= state.last_applied_scheduled_for
  );

-- name: MarkWorkspaceRadarCancelledByRunID :execrows
WITH eligible AS MATERIALIZED (
    SELECT run.*
    FROM agent_radar_run run
    WHERE run.id = $1
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'cancelled'
), claimed AS (
    INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
    SELECT eligible.id, 'cancelled'
    FROM eligible
    ON CONFLICT (radar_run_id) DO NOTHING
    RETURNING radar_run_id
)
UPDATE workspace_radar_state state
SET
    next_due_at = now() + interval '30 minutes',
    last_applied_scheduled_for = GREATEST(COALESCE(state.last_applied_scheduled_for, run.scheduled_for), run.scheduled_for),
    updated_at = now()
FROM eligible run
JOIN claimed ON claimed.radar_run_id = run.id
WHERE run.workspace_id = state.workspace_id
  AND run.agent_id = state.supervisor_agent_id
  AND (
    state.last_applied_scheduled_for IS NULL
    OR run.scheduled_for >= state.last_applied_scheduled_for
  );

-- name: MarkWorkspaceRadarCancelledByTaskID :execrows
WITH eligible AS MATERIALIZED (
    SELECT run.*
    FROM agent_radar_run run
    WHERE run.task_id = $1
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'cancelled'
), claimed AS (
    INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
    SELECT eligible.id, 'cancelled'
    FROM eligible
    ON CONFLICT (radar_run_id) DO NOTHING
    RETURNING radar_run_id
)
UPDATE workspace_radar_state state
SET
    next_due_at = now() + interval '30 minutes',
    last_applied_scheduled_for = GREATEST(COALESCE(state.last_applied_scheduled_for, run.scheduled_for), run.scheduled_for),
    updated_at = now()
FROM eligible run
JOIN claimed ON claimed.radar_run_id = run.id
WHERE run.workspace_id = state.workspace_id
  AND run.agent_id = state.supervisor_agent_id
  AND (
    state.last_applied_scheduled_for IS NULL
    OR run.scheduled_for >= state.last_applied_scheduled_for
  );

-- name: ReconcileTerminalWorkspaceRadarRuns :one
-- Atomic crash-window repair. Cancelled tasks defer the next check without
-- increasing the failure counter; completed/failed tasks count as one failure.
-- A normally executing completion owns a terminal task, so reclaim it only
-- after its execution claim has been stale for five minutes.
WITH terminalized AS MATERIALIZED (
    UPDATE agent_radar_run run
    SET status = CASE WHEN task.status = 'cancelled' THEN 'cancelled' ELSE 'failed' END,
        error = CASE
          WHEN run.error <> '' THEN run.error
          WHEN task.status = 'completed' THEN 'Radar completion processing was interrupted'
          ELSE COALESCE(task.error, task.failure_reason, 'Radar task terminated')
        END,
        finished_at = COALESCE(run.finished_at, now()),
        updated_at = now()
    FROM agent_task_queue task
    WHERE run.task_id = task.id
      AND (
        (
          task.status IN ('failed', 'cancelled')
          AND run.status IN ('planned', 'queued', 'running', 'executing')
        )
        OR (
          task.status = 'completed'
          AND (
            (
              run.status IN ('planned', 'queued', 'running')
              AND COALESCE(task.completed_at, task.created_at) < now() - interval '5 minutes'
            )
            OR (
              run.status = 'executing'
              AND run.updated_at < now() - interval '5 minutes'
            )
          )
        )
      )
    RETURNING run.id, run.workspace_id, run.agent_id, run.trigger_kind,
              run.cooldown_key, run.status, run.scheduled_for
), outcomes AS MATERIALIZED (
    SELECT terminalized.id, terminalized.workspace_id, terminalized.agent_id,
           terminalized.status, terminalized.scheduled_for,
           CASE
             WHEN terminalized.status IN ('succeeded', 'no_action') THEN 'succeeded'
             ELSE terminalized.status
           END AS outcome
    FROM terminalized
    WHERE terminalized.trigger_kind = 'scheduled'
      AND terminalized.cooldown_key = 'workspace_supervisor_radar'

    UNION ALL

    SELECT pending.id, pending.workspace_id, pending.agent_id,
           pending.status, pending.scheduled_for,
           CASE
             WHEN pending.status IN ('succeeded', 'no_action') THEN 'succeeded'
             ELSE pending.status
           END AS outcome
    FROM (
      SELECT run.id, run.workspace_id, run.agent_id, run.status, run.scheduled_for
      FROM agent_radar_run run
      WHERE run.trigger_kind = 'scheduled'
        AND run.cooldown_key = 'workspace_supervisor_radar'
        AND run.status IN ('succeeded', 'no_action', 'failed', 'cancelled')
        AND NOT EXISTS (
          SELECT 1
          FROM workspace_radar_run_state_ack ack
          WHERE ack.radar_run_id = run.id
        )
        AND NOT EXISTS (
          SELECT 1 FROM terminalized WHERE terminalized.id = run.id
        )
      ORDER BY run.finished_at ASC NULLS FIRST, run.created_at ASC, run.id ASC
      LIMIT 500
    ) pending
), latest_current_outcomes AS MATERIALIZED (
    SELECT DISTINCT ON (outcomes.workspace_id)
           outcomes.id, outcomes.workspace_id, outcomes.agent_id,
           outcomes.status, outcomes.scheduled_for, outcomes.outcome
    FROM outcomes
    JOIN workspace_radar_state state
      ON state.workspace_id = outcomes.workspace_id
     AND state.supervisor_agent_id = outcomes.agent_id
    WHERE state.last_applied_scheduled_for IS NULL
       OR outcomes.scheduled_for > state.last_applied_scheduled_for
    ORDER BY outcomes.workspace_id, outcomes.scheduled_for DESC, outcomes.id DESC
), state_updates AS (
    UPDATE workspace_radar_state state
    SET last_success_at = CASE
          WHEN latest_current_outcomes.outcome = 'succeeded'
          THEN GREATEST(COALESCE(state.last_success_at, latest_current_outcomes.scheduled_for), latest_current_outcomes.scheduled_for)
          ELSE state.last_success_at
        END,
        last_full_review_at = CASE
          WHEN latest_current_outcomes.outcome = 'succeeded'
            AND (
              state.last_full_review_at IS NULL
              OR latest_current_outcomes.scheduled_for >= state.last_full_review_at + interval '6 hours'
            )
          THEN GREATEST(COALESCE(state.last_full_review_at, latest_current_outcomes.scheduled_for), latest_current_outcomes.scheduled_for)
          ELSE state.last_full_review_at
        END,
        consecutive_failures = CASE
          WHEN latest_current_outcomes.outcome = 'succeeded' THEN 0
          WHEN latest_current_outcomes.outcome = 'failed' THEN state.consecutive_failures + 1
          ELSE state.consecutive_failures
        END,
        next_due_at = CASE
          WHEN latest_current_outcomes.outcome IN ('succeeded', 'cancelled') THEN now() + interval '30 minutes'
          ELSE now() + CASE
            WHEN state.consecutive_failures = 0 THEN interval '15 minutes'
            WHEN state.consecutive_failures = 1 THEN interval '30 minutes'
            WHEN state.consecutive_failures = 2 THEN interval '1 hour'
            ELSE interval '2 hours'
          END
        END,
        last_applied_scheduled_for = GREATEST(
          COALESCE(state.last_applied_scheduled_for, latest_current_outcomes.scheduled_for),
          latest_current_outcomes.scheduled_for
        ),
        updated_at = now()
    FROM latest_current_outcomes
    WHERE latest_current_outcomes.workspace_id = state.workspace_id
      AND latest_current_outcomes.agent_id = state.supervisor_agent_id
      AND (
        state.last_applied_scheduled_for IS NULL
        OR latest_current_outcomes.scheduled_for > state.last_applied_scheduled_for
      )
    RETURNING state.workspace_id,
              latest_current_outcomes.id AS radar_run_id,
              latest_current_outcomes.outcome
), claimable AS MATERIALIZED (
    -- Outcomes that are obsolete for the current binding can be acknowledged
    -- immediately. The selected current-Wendy outcome is acknowledged only if
    -- its state update actually happened, so a concurrent rebind cannot make
    -- reconciliation permanently discard unapplied work.
    SELECT outcomes.id AS radar_run_id, outcomes.outcome
    FROM outcomes
    WHERE NOT EXISTS (
      SELECT 1
      FROM latest_current_outcomes current_outcome
      WHERE current_outcome.id = outcomes.id
    )

    UNION ALL

    SELECT state_updates.radar_run_id, state_updates.outcome
    FROM state_updates
), claimed AS (
    INSERT INTO workspace_radar_run_state_ack (radar_run_id, outcome)
    SELECT claimable.radar_run_id, claimable.outcome
    FROM claimable
    ON CONFLICT (radar_run_id) DO NOTHING
    RETURNING radar_run_id
)
SELECT
  (SELECT count(*)::bigint FROM terminalized) AS terminalized_count,
  (SELECT count(*)::bigint FROM state_updates) AS state_updated_count;
