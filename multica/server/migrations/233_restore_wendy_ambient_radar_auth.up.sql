-- Migration 174 authorized event-driven ambient Radar runs
-- (trigger_kind=event, cooldown_key wendy_ambient:<channel>, agent still a
-- channel member). Migration 223 rewrote workspace_radar_task_is_authorized
-- onto agent_inbox_event for the wake clean cutover but only kept the
-- manual/scheduled branches, silently dropping the ambient event branch.
--
-- Result: legitimate ambient Radar rows stay pending forever under the
-- BEFORE-dispatch guard (RETURN NULL), while leaseAgentInboxEventForRuntime
-- still inserts deliveries because its UPDATE ignored affected-row count.
-- Per-agent FIFO then livelocks behind the poison head until the 2h queue
-- sweeper marks it queued_expired.
--
-- Restore the 174 ambient branch on the post-223 agent_inbox_event shape.
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
        OR (
          run.trigger_kind = 'event'
          AND run.cooldown_key LIKE 'wendy_ambient:%'
          AND EXISTS (
            SELECT 1
            FROM agent a
            JOIN channel_member cm
              ON cm.workspace_id = run.workspace_id
             AND cm.channel_id = CAST(substring(run.cooldown_key FROM 15) AS uuid)
             AND cm.member_type = 'agent'
             AND cm.member_id = a.id
            WHERE a.id = run.agent_id
              AND a.workspace_id = run.workspace_id
              AND a.archived_at IS NULL
          )
        )
      )
  );
$$;
