BEGIN;

DROP TRIGGER IF EXISTS cancel_group_manager_reminders_for_membership_trigger ON channel_member;
DROP FUNCTION IF EXISTS cancel_group_manager_reminders_for_membership();
DROP TRIGGER IF EXISTS cancel_group_manager_reminders_for_channel_trigger ON channel;
DROP FUNCTION IF EXISTS cancel_group_manager_reminders_for_channel();

CREATE TABLE pending_handoff (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  urgency TEXT NOT NULL CHECK (urgency IN ('fast', 'slow')),
  reason_code TEXT NOT NULL CHECK (reason_code IN (
    'unlock', 'block_route', 'interrupt_stop', 'stalled_ask_why',
    'progress_nudge', 'start_work'
  )),
  target_actor_type TEXT NOT NULL CHECK (target_actor_type IN ('member', 'agent')),
  target_actor_id UUID NOT NULL,
  related_node_ids UUID[] NOT NULL DEFAULT '{}',
  channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
  dedupe_key TEXT NOT NULL,
  not_before TIMESTAMPTZ NOT NULL DEFAULT now(),
  status TEXT NOT NULL CHECK (status IN ('pending', 'claimed', 'done', 'cancelled')),
  claim_token UUID,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX pending_handoff_active_dedupe_uidx
  ON pending_handoff (workspace_id, dedupe_key)
  WHERE status IN ('pending', 'claimed');

CREATE INDEX pending_handoff_due_idx
  ON pending_handoff (urgency, reason_code, not_before, created_at)
  WHERE status = 'pending';

DELETE FROM agent_reminder
WHERE origin_kind = 'group_manager_auto';

ALTER TABLE agent_reminder_lifecycle_event
  DROP CONSTRAINT IF EXISTS agent_reminder_lifecycle_event_actor_type_check;
ALTER TABLE agent_reminder_lifecycle_event
  ADD CONSTRAINT agent_reminder_lifecycle_event_actor_type_check
  CHECK (actor_type IN ('agent', 'system'));

DROP INDEX IF EXISTS agent_reminder_active_managed_patrol_uidx;

ALTER TABLE agent_reminder
  DROP CONSTRAINT IF EXISTS agent_reminder_managed_origin_check,
  DROP CONSTRAINT IF EXISTS agent_reminder_managed_kind_check,
  DROP CONSTRAINT IF EXISTS agent_reminder_origin_kind_check,
  DROP COLUMN IF EXISTS origin_key,
  DROP COLUMN IF EXISTS managed_kind,
  DROP COLUMN IF EXISTS origin_kind;

COMMIT;
