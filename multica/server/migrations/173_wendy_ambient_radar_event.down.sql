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
  );
