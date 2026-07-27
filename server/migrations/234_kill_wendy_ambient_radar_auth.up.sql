-- Product kill follow-up after #1257 merged the pre-kill head that restored
-- event+wendy_ambient authorization (migration 233). Frank/Barry decision:
-- wendy_ambient event runs must not execute. This migration:
--   1) strips only the ambient event branch from workspace_radar_task_is_authorized
--      (manual/scheduled unchanged)
--   2) terminalizes residual wendy_ambient queue/run rows so they cannot sit
--      as FIFO heads or resume after deploy
-- Application code keeps the #1257 lease hard gate + durable unauthorized
-- terminalize. Producer stop is in the companion Go change; full module
-- deletion remains task #780.

CREATE OR REPLACE FUNCTION workspace_radar_task_is_authorized(candidate_task_id UUID)
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
  SELECT EXISTS (
    SELECT 1
    FROM agent_inbox_event event
    JOIN agent_radar_run run
      ON run.task_id = event.id
     AND run.id::text = event.context->>'radar_run_id'
     AND run.agent_id = event.agent_id
    WHERE event.id = candidate_task_id
      AND event.context->>'type' = 'agent_radar'
      AND run.status IN ('planned', 'queued', 'running')
      AND (
        run.trigger_kind = 'manual'
        OR (
          run.trigger_kind = 'scheduled'
          AND run.cooldown_key = 'workspace_supervisor_radar'
          AND EXISTS (
            SELECT 1
            FROM workspace_radar_state state
            JOIN agent supervisor
              ON supervisor.workspace_id = state.workspace_id
             AND supervisor.id = state.supervisor_agent_id
            JOIN member owner_member
              ON owner_member.workspace_id = state.workspace_id
             AND owner_member.user_id = supervisor.owner_id
             AND owner_member.role = 'owner'
            WHERE state.workspace_id = run.workspace_id
              AND state.supervisor_agent_id = run.agent_id
              AND state.enabled
              AND supervisor.archived_at IS NULL
          )
        )
      )
  );
$$;

-- Clear residual ambient event work from the inbox so directed wakes cannot
-- sit behind revived poison/authorized ambient heads after 233 applied.
UPDATE agent_inbox_event e
SET status = 'acked',
    completed_at = COALESCE(e.completed_at, now()),
    terminal_at = COALESCE(e.terminal_at, now()),
    acked_at = COALESCE(e.acked_at, now()),
    terminal_outcome = 'failed',
    error = COALESCE(NULLIF(e.error, ''), 'wendy_ambient product kill'),
    failure_reason = 'radar_unauthorized',
    updated_at = now()
FROM agent_radar_run run
WHERE run.task_id = e.id
  AND run.cooldown_key LIKE 'wendy_ambient:%'
  AND run.trigger_kind = 'event'
  AND e.context->>'type' = 'agent_radar'
  AND e.status IN ('pending', 'failed', 'draining');

UPDATE agent_radar_run run
SET status = 'failed',
    error = CASE
      WHEN COALESCE(run.error, '') = '' THEN 'wendy_ambient product kill'
      ELSE run.error
    END,
    finished_at = COALESCE(run.finished_at, now()),
    updated_at = now()
WHERE run.cooldown_key LIKE 'wendy_ambient:%'
  AND run.trigger_kind = 'event'
  AND run.status IN ('planned', 'queued', 'running', 'executing');
