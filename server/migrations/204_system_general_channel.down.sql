BEGIN;

LOCK TABLE channel IN ACCESS EXCLUSIVE MODE;

ALTER TABLE channel DISABLE TRIGGER trg_journal_workspace_radar_channel;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM channel system_channel
    JOIN channel_message message ON message.channel_id = system_channel.id
    WHERE system_channel.system_key = 'general'
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_down_blocked_by_messages';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM channel system_channel
    JOIN channel_member member ON member.channel_id = system_channel.id
    WHERE system_channel.system_key = 'general'
      AND (
        member.pinned_at IS NOT NULL
        OR member.manual_unread_at IS NOT NULL
        OR member.muted_at IS NOT NULL
      )
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_down_blocked_by_member_state';
  END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_sync_system_general_agent ON agent;
DROP FUNCTION IF EXISTS sync_system_general_agent();
DROP TRIGGER IF EXISTS trg_sync_system_general_human ON member;
DROP FUNCTION IF EXISTS sync_system_general_human();
DROP TRIGGER IF EXISTS trg_guard_system_general_roster ON channel_member;
DROP FUNCTION IF EXISTS guard_system_general_roster();
DROP TRIGGER IF EXISTS trg_guard_system_general_channel ON channel;
DROP FUNCTION IF EXISTS guard_system_general_channel();

DELETE FROM channel
WHERE system_key = 'general';

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM system_channel_collision_audit audit
    LEFT JOIN channel collision ON collision.id = audit.channel_id
    WHERE audit.migration_key = '204_system_general_channel'
      AND (
        collision.id IS NULL
        OR collision.workspace_id IS DISTINCT FROM audit.workspace_id
        OR collision.name IS DISTINCT FROM audit.renamed_name
        OR collision.kind IS DISTINCT FROM 'group'
        OR collision.archived_at IS NULL
      )
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_down_collision_changed';
  END IF;

  IF EXISTS (
    SELECT 1
    FROM system_channel_collision_audit audit
    JOIN channel occupied
      ON occupied.workspace_id = audit.workspace_id
     AND occupied.name = audit.original_name
     AND occupied.id <> audit.channel_id
    WHERE audit.migration_key = '204_system_general_channel'
  ) THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'system_general_down_original_name_occupied';
  END IF;
END;
$$;

UPDATE channel collision
SET name = audit.original_name,
    updated_at = collision.updated_at
FROM system_channel_collision_audit audit
WHERE collision.id = audit.channel_id
  AND collision.workspace_id = audit.workspace_id
  AND collision.name = audit.renamed_name
  AND collision.kind = 'group'
  AND collision.archived_at IS NOT NULL;

CREATE OR REPLACE FUNCTION journal_workspace_radar_channel()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  row_value channel%ROWTYPE;
BEGIN
  row_value := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  IF row_value.kind <> 'group' THEN
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  END IF;
  IF TG_OP = 'UPDATE' AND EXISTS (
    SELECT 1
    FROM agent_radar_action action
    JOIN agent_radar_run run ON run.id = action.radar_run_id
    WHERE action.workspace_id = row_value.workspace_id
      AND action.target_id = row_value.id
      AND action.action_type = 'mention_agent'
      AND action.status = 'executing'
      AND run.trigger_kind = 'scheduled'
      AND run.cooldown_key = 'workspace_supervisor_radar'
      AND run.status = 'executing'
  ) THEN
    RETURN NEW;
  END IF;
  PERFORM record_workspace_radar_change(
    row_value.workspace_id, 'group_channel', row_value.id, clock_timestamp(),
    'channel', row_value.id,
    jsonb_build_object(
      'channel_id', row_value.id,
      'name', left(row_value.name, 160),
      'description', left(COALESCE(row_value.description, ''), 300),
      'archived_at', row_value.archived_at,
      'updated_at', row_value.updated_at,
      'operation', lower(TG_OP)
    )
  );
  RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;

ALTER TABLE channel ENABLE TRIGGER trg_journal_workspace_radar_channel;

DROP FUNCTION IF EXISTS ensure_system_general_channel(UUID, UUID);
DROP TABLE IF EXISTS system_channel_collision_audit;
DROP INDEX IF EXISTS channel_workspace_system_key_unique;
ALTER TABLE channel DROP CONSTRAINT IF EXISTS channel_system_key_check;
ALTER TABLE channel DROP COLUMN IF EXISTS system_key;

COMMIT;
