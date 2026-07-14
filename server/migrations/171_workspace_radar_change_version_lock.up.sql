-- Serialize Radar change-version allocation per workspace. The advisory lock
-- also protects the no-state-row case, while supervisor binding owns creation
-- of workspace_radar_state.
CREATE OR REPLACE FUNCTION record_workspace_radar_change(
  changed_workspace_id UUID,
  changed_entity_kind TEXT,
  changed_entity_id UUID,
  changed_occurred_at TIMESTAMPTZ,
  changed_target_kind TEXT,
  changed_target_id UUID,
  changed_payload JSONB
)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
  assigned_version BIGINT;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(changed_workspace_id::text, 0));

  -- No state row means no supervisor is bound yet, so preserve the previous
  -- no-op behavior rather than assigning a supervisor from a journal trigger.
  UPDATE workspace_radar_state
  SET change_version = GREATEST(
        change_version,
        COALESCE((
          SELECT max(change.change_version)
          FROM workspace_radar_change change
          WHERE change.workspace_id = changed_workspace_id
        ), 0)
      ) + 1,
      next_due_at = LEAST(next_due_at, now()),
      updated_at = now()
  WHERE workspace_id = changed_workspace_id
    AND enabled
  RETURNING change_version INTO assigned_version;

  IF assigned_version IS NULL THEN
    RETURN;
  END IF;

  INSERT INTO workspace_radar_change (
    workspace_id, entity_kind, entity_id, change_version, occurred_at,
    target_kind, target_id, payload
  ) VALUES (
    changed_workspace_id, changed_entity_kind, changed_entity_id,
    assigned_version, COALESCE(changed_occurred_at, clock_timestamp()),
    changed_target_kind, changed_target_id, COALESCE(changed_payload, '{}'::jsonb)
  )
  ON CONFLICT (workspace_id, entity_kind, entity_id) DO UPDATE
  SET change_version = EXCLUDED.change_version,
      occurred_at = EXCLUDED.occurred_at,
      target_kind = EXCLUDED.target_kind,
      target_id = EXCLUDED.target_id,
      payload = EXCLUDED.payload;
END;
$$;
