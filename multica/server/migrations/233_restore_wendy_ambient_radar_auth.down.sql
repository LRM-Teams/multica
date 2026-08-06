-- Revert to the migration-223 shape (manual/scheduled only; no ambient event
-- branch). Only used when rolling back this forward fix.
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
