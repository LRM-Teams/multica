-- Stopgap for task #395: Radar jobs were enqueued into the generic task queue
-- before runtimes advertised explicit support for that task kind. Terminalize
-- active-ish rows so they stop polluting Profile execution logs.
UPDATE agent_task_queue
SET
    status = 'cancelled',
    completed_at = COALESCE(completed_at, now()),
    error = COALESCE(NULLIF(error, ''), 'agent_radar disabled pending explicit runtime capability support'),
    failure_reason = COALESCE(NULLIF(failure_reason, ''), 'agent_radar_disabled')
WHERE context->>'type' = 'agent_radar'
  AND status IN ('queued', 'dispatched');

UPDATE agent_radar_run
SET
    status = 'cancelled',
    error = COALESCE(NULLIF(error, ''), 'agent_radar disabled pending explicit runtime capability support'),
    finished_at = COALESCE(finished_at, now()),
    updated_at = now()
WHERE status IN ('planned', 'queued', 'running')
  AND (
    task_id IS NULL
    OR EXISTS (
      SELECT 1
      FROM agent_task_queue t
      WHERE t.id = agent_radar_run.task_id
        AND t.context->>'type' = 'agent_radar'
    )
  );
