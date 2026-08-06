BEGIN;

-- A group manager's server-created adaptive patrol is a real Reminder
-- definition, but it is not a user-authored reminder. Keep the provenance
-- explicit so reads, permissions, dedupe, and fire behavior never infer it
-- from mutable names or display text.
ALTER TABLE agent_reminder
  ADD COLUMN origin_kind TEXT NOT NULL DEFAULT 'agent',
  ADD COLUMN managed_kind TEXT,
  ADD COLUMN origin_key TEXT;

ALTER TABLE agent_reminder
  ADD CONSTRAINT agent_reminder_origin_kind_check
    CHECK (origin_kind IN ('agent', 'group_manager_auto')),
  ADD CONSTRAINT agent_reminder_managed_kind_check
    CHECK (managed_kind IS NULL OR managed_kind = 'patrol'),
  ADD CONSTRAINT agent_reminder_managed_origin_check
    CHECK (
      (origin_kind = 'agent' AND managed_kind IS NULL AND origin_key IS NULL)
      OR
      (
        origin_kind = 'group_manager_auto'
        AND managed_kind IS NOT NULL
        AND origin_key IS NOT NULL
        AND btrim(origin_key) <> ''
      )
    );

ALTER TABLE agent_reminder_lifecycle_event
  DROP CONSTRAINT IF EXISTS agent_reminder_lifecycle_event_actor_type_check;
ALTER TABLE agent_reminder_lifecycle_event
  ADD CONSTRAINT agent_reminder_lifecycle_event_actor_type_check
  CHECK (actor_type IN ('agent', 'system', 'user'));

CREATE UNIQUE INDEX agent_reminder_active_managed_patrol_uidx
  ON agent_reminder (workspace_id, agent_id, anchor_channel_id)
  WHERE origin_kind = 'group_manager_auto'
    AND managed_kind = 'patrol'
    AND status IN ('scheduled', 'firing');

-- A claimed legacy row may already have emitted a member-target message before
-- crashing. Refuse to guess whether it is safe to discard; deployment must
-- drain old schedulers and resolve the row before retrying this migration.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pending_handoff WHERE status = 'claimed') THEN
    RAISE EXCEPTION USING
      ERRCODE = 'P0001',
      MESSAGE = 'group_manager_handoff_cutover_claimed_rows_require_audit';
  END IF;
END;
$$;

-- Pending rows were automatic workgraph nudges. Product cutover deliberately
-- does not replay them: issue assignment/source-thread system events already
-- deliver real work, while exceptional coordination belongs to patrol.

-- The first patrol is deterministic for every currently live group manager.
-- A cancelled patrol is deliberately not covered by ordinary Ensure/reuse;
-- only this cutover backfill, a newly bound manager, or explicit re-enable may
-- create one. The first wake is a short bootstrap; the group manager replans the same
-- one-shot definition adaptively after inspecting the live group.
WITH candidates AS (
  SELECT
    ch.workspace_id,
    ch.id AS channel_id,
    ch.group_manager_agent_id AS manager_id,
    ch.created_by AS initiator_user_id,
    (
      SELECT message.id
      FROM channel_message message
      WHERE message.channel_id = ch.id
        AND message.workspace_id = ch.workspace_id
        AND message.deleted_at IS NULL
      ORDER BY message.created_at ASC, message.id ASC
      LIMIT 1
    ) AS anchor_message_id
  FROM channel ch
  JOIN agent manager
    ON manager.id = ch.group_manager_agent_id
   AND manager.workspace_id = ch.workspace_id
   AND manager.archived_at IS NULL
   AND manager.managed_role = 'group_manager'
  WHERE ch.kind = 'group'
    AND ch.archived_at IS NULL
),
inserted AS (
  INSERT INTO agent_reminder (
    workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
    anchor_message_id, fire_at, origin_kind, managed_kind, origin_key
  )
  SELECT
    workspace_id, manager_id, initiator_user_id, '群巡检', channel_id,
    anchor_message_id, now() + interval '15 minutes',
    'group_manager_auto', 'patrol',
    'patrol:' || channel_id::text
  FROM candidates
  ON CONFLICT DO NOTHING
  RETURNING *
)
INSERT INTO agent_reminder_lifecycle_event (
  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
  next_fire_at, title_snapshot, cadence_snapshot, resulting_state,
  reason_code, details
)
SELECT
  id, workspace_id, agent_id, 'scheduled', 'system', agent_id,
  fire_at, title, cadence, 'scheduled', 'group_manager_patrol_backfilled',
  jsonb_build_object('origin_kind', origin_kind, 'managed_kind', managed_kind)
FROM inserted;

-- Group lifecycle owns automatic schedules immediately. The Reminder timer
-- projection trigger emits the corresponding daemon cancellation.
CREATE OR REPLACE FUNCTION cancel_group_manager_reminders_for_channel()
RETURNS TRIGGER AS $$
DECLARE
  target_reason TEXT;
  reminder_row RECORD;
BEGIN
  -- Workspace deletion cascades through both channels and reminders. Once the
  -- parent row is invisible, inserting a lifecycle child would violate its
  -- workspace FK; the reminder cascade already owns that teardown.
  IF NOT EXISTS (SELECT 1 FROM workspace WHERE id = OLD.workspace_id) THEN
    RETURN OLD;
  END IF;
  IF TG_OP = 'DELETE' THEN
    target_reason := 'channel_deleted';
  ELSIF NEW.archived_at IS NOT NULL AND OLD.archived_at IS NULL THEN
    target_reason := 'channel_archived';
  ELSIF NEW.group_manager_agent_id IS DISTINCT FROM OLD.group_manager_agent_id THEN
    target_reason := 'group_manager_unbound';
  ELSE
    RETURN NEW;
  END IF;

  FOR reminder_row IN
    UPDATE agent_reminder
    SET status = 'cancelled', terminal_reason = target_reason,
        current_occurrence_id = NULL, version = version + 1, updated_at = now()
    WHERE workspace_id = OLD.workspace_id
      AND anchor_channel_id = OLD.id
      AND origin_kind = 'group_manager_auto'
      AND (OLD.group_manager_agent_id IS NULL OR agent_id = OLD.group_manager_agent_id)
      AND status IN ('scheduled', 'firing')
    RETURNING *
  LOOP
    INSERT INTO agent_reminder_lifecycle_event (
      reminder_id, workspace_id, agent_id, event_type, actor_type,
      previous_fire_at, title_snapshot, cadence_snapshot, timezone_snapshot,
      resulting_state, reason_code
    ) VALUES (
      reminder_row.id, reminder_row.workspace_id, reminder_row.agent_id,
      'cancelled', 'system', reminder_row.fire_at, reminder_row.title,
      reminder_row.cadence, reminder_row.schedule_timezone, 'cancelled',
      target_reason
    );
  END LOOP;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cancel_group_manager_reminders_for_channel_trigger
BEFORE DELETE OR UPDATE OF archived_at, group_manager_agent_id ON channel
FOR EACH ROW EXECUTE FUNCTION cancel_group_manager_reminders_for_channel();

CREATE OR REPLACE FUNCTION cancel_group_manager_reminders_for_membership()
RETURNS TRIGGER AS $$
DECLARE
  reminder_row RECORD;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM workspace WHERE id = OLD.workspace_id) THEN
    RETURN OLD;
  END IF;
  IF OLD.member_type <> 'agent' THEN
    RETURN OLD;
  END IF;
  IF NOT EXISTS (
    SELECT 1
    FROM channel
    WHERE id = OLD.channel_id
      AND workspace_id = OLD.workspace_id
      AND group_manager_agent_id = OLD.member_id
  ) THEN
    RETURN OLD;
  END IF;
  FOR reminder_row IN
    UPDATE agent_reminder
    SET status = 'cancelled', terminal_reason = 'group_manager_removed',
        current_occurrence_id = NULL, version = version + 1, updated_at = now()
    WHERE workspace_id = OLD.workspace_id
      AND anchor_channel_id = OLD.channel_id
      AND agent_id = OLD.member_id
      AND origin_kind = 'group_manager_auto'
      AND status IN ('scheduled', 'firing')
    RETURNING *
  LOOP
    INSERT INTO agent_reminder_lifecycle_event (
      reminder_id, workspace_id, agent_id, event_type, actor_type,
      previous_fire_at, title_snapshot, cadence_snapshot, timezone_snapshot,
      resulting_state, reason_code
    ) VALUES (
      reminder_row.id, reminder_row.workspace_id, reminder_row.agent_id,
      'cancelled', 'system', reminder_row.fire_at, reminder_row.title,
      reminder_row.cadence, reminder_row.schedule_timezone, 'cancelled',
      'group_manager_removed'
    );
  END LOOP;
  RETURN OLD;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER cancel_group_manager_reminders_for_membership_trigger
BEFORE DELETE ON channel_member
FOR EACH ROW EXECUTE FUNCTION cancel_group_manager_reminders_for_membership();

DROP TABLE pending_handoff;

COMMIT;
