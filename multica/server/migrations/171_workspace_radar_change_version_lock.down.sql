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
  UPDATE workspace_radar_state
  SET change_version = change_version + 1,
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
