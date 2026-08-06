-- A dispatched task can be re-delivered every 90 seconds when its start
-- acknowledgement never arrives. Re-delivery refreshes dispatched_at, so the
-- generic five-minute dispatch timeout cannot detect this failure mode. Repair
-- only old, never-started Radar pairs and leave fresh, running, and local-path
-- waiting tasks untouched.
WITH victims AS MATERIALIZED (
    SELECT rr.id AS radar_run_id, atq.id AS task_id
    FROM agent_radar_run rr
    JOIN agent_task_queue atq ON atq.id = rr.task_id
    WHERE rr.status = 'queued'
      AND rr.started_at IS NULL
      AND rr.created_at < now() - interval '1 hour'
      AND atq.status = 'dispatched'
      AND atq.started_at IS NULL
      AND atq.created_at < now() - interval '1 hour'
      AND atq.agent_id = rr.agent_id
      -- Runtime consolidation can reassign the task before deleting the old
      -- runtime, which leaves rr.runtime_id stale or NULL. The task FK plus
      -- the context backpointer are the durable pair identity.
      AND atq.context->>'type' = 'agent_radar'
      AND atq.context->>'radar_run_id' = rr.id::text
    FOR UPDATE OF rr, atq SKIP LOCKED
), failed_tasks AS (
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
      AND atq.created_at < now() - interval '1 hour'
    RETURNING atq.id
)
UPDATE agent_radar_run rr
SET
    status = 'failed',
    error = 'radar_stale_dispatch_repair',
    finished_at = COALESCE(rr.finished_at, now()),
    updated_at = now()
FROM victims v
JOIN failed_tasks ft ON ft.id = v.task_id
WHERE rr.id = v.radar_run_id
  AND rr.status = 'queued'
  AND rr.started_at IS NULL
  AND rr.created_at < now() - interval '1 hour';
