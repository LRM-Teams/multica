-- Repair Radar runs that cannot make progress before enforcing the one-active-run
-- invariant. Prefer the most advanced, oldest valid run when duplicate active runs
-- exist for the same agent.
WITH active_runs AS MATERIALIZED (
    SELECT
        rr.id,
        rr.workspace_id,
        rr.agent_id,
        rr.task_id,
        rr.created_at,
        rr.status AS run_status,
        atq.status AS task_status,
        CASE
            WHEN rr.task_id IS NULL THEN 'migration: active Radar run had no linked task'
            WHEN atq.id IS NULL THEN 'migration: active Radar run referenced a missing task'
            WHEN atq.status IN ('completed', 'failed', 'cancelled') THEN
                'migration: active Radar run referenced a terminal task'
            WHEN run_runtime.status = 'offline' OR task_runtime.status = 'offline' THEN
                'migration: active Radar run targeted an offline runtime'
            ELSE NULL
        END AS invalid_reason
    FROM agent_radar_run rr
    LEFT JOIN agent_task_queue atq ON atq.id = rr.task_id
    LEFT JOIN agent_runtime run_runtime ON run_runtime.id = rr.runtime_id
    LEFT JOIN agent_runtime task_runtime ON task_runtime.id = atq.runtime_id
    WHERE rr.status IN ('planned', 'queued', 'running')
), ranked_valid_runs AS MATERIALIZED (
    SELECT
        ar.*,
        row_number() OVER (
            PARTITION BY ar.workspace_id, ar.agent_id
            ORDER BY
                CASE ar.task_status
                    WHEN 'running' THEN 1
                    WHEN 'waiting_local_directory' THEN 2
                    WHEN 'dispatched' THEN 3
                    WHEN 'queued' THEN 4
                    ELSE 5
                END,
                CASE ar.run_status
                    WHEN 'running' THEN 1
                    WHEN 'queued' THEN 2
                    WHEN 'planned' THEN 3
                    ELSE 4
                END,
                ar.created_at ASC,
                ar.id ASC
        ) AS active_rank
    FROM active_runs ar
    WHERE ar.invalid_reason IS NULL
), runs_to_fail AS MATERIALIZED (
    SELECT ar.id, ar.task_id, ar.invalid_reason AS reason
    FROM active_runs ar
    WHERE ar.invalid_reason IS NOT NULL

    UNION ALL

    SELECT rvr.id, rvr.task_id, 'migration: duplicate active Radar run' AS reason
    FROM ranked_valid_runs rvr
    WHERE rvr.active_rank > 1
), cancelled_tasks AS (
    UPDATE agent_task_queue atq
    SET
        status = 'cancelled',
        completed_at = COALESCE(atq.completed_at, now()),
        error = COALESCE(NULLIF(atq.error, ''), 'Radar run invalidated during active-run repair'),
        failure_reason = COALESCE(NULLIF(atq.failure_reason, ''), 'radar_active_run_repair')
    FROM (
        SELECT DISTINCT rtf.task_id
        FROM runs_to_fail rtf
        WHERE rtf.task_id IS NOT NULL
    ) failed_task
    WHERE atq.id = failed_task.task_id
      AND atq.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
      -- Do not cancel a task that is also referenced by the one active run kept
      -- for an agent. This only matters for pre-existing corrupt shared links.
      AND NOT EXISTS (
          SELECT 1
          FROM active_runs ar
          WHERE ar.task_id = atq.id
            AND NOT EXISTS (
                SELECT 1
                FROM runs_to_fail rtf
                WHERE rtf.id = ar.id
            )
      )
    RETURNING atq.id
)
UPDATE agent_radar_run rr
SET
    status = 'failed',
    error = CASE WHEN rr.error = '' THEN rtf.reason ELSE rr.error END,
    finished_at = COALESCE(rr.finished_at, now()),
    updated_at = now()
FROM runs_to_fail rtf
WHERE rr.id = rtf.id
  AND rr.status IN ('planned', 'queued', 'running');

CREATE UNIQUE INDEX idx_agent_radar_run_active_agent
    ON agent_radar_run (workspace_id, agent_id)
    WHERE status IN ('planned', 'queued', 'running');
