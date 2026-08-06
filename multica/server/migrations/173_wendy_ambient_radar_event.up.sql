-- Ambient group reviews enqueue event-driven Radar runs with cooldown_key
-- wendy_ambient:<channel_id>. Migration 169 banned all active event runs so
-- old replicas could not schedule per-agent event Radar during rollout.
-- Allow only the ambient cooldown prefix so workspace-supervisor exclusivity
-- stays intact for scheduled runs.
ALTER TABLE agent_radar_run
  DROP CONSTRAINT IF EXISTS agent_radar_run_active_scheduled_workspace_check;

ALTER TABLE agent_radar_run
  ADD CONSTRAINT agent_radar_run_active_scheduled_workspace_check
  CHECK (
    status NOT IN ('planned', 'queued', 'running', 'executing')
    OR trigger_kind = 'manual'
    OR (
      trigger_kind = 'scheduled'
      AND cooldown_key = 'workspace_supervisor_radar'
    )
    OR (
      trigger_kind = 'event'
      AND cooldown_key LIKE 'wendy_ambient:%'
    )
  );
