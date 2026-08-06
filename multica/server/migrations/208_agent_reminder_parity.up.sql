BEGIN;

-- Migration 208: Raft Reminder parity.

-- Reminder ownership and anchor identity are durable historical facts.  The
-- V1 foreign keys cascaded the definition away when an agent or channel was
-- deleted, which made it impossible to record the explicit terminal outcome
-- required by the Reminder contract.  Workspace deletion still owns cleanup.
ALTER TABLE agent_reminder
  DROP CONSTRAINT IF EXISTS agent_reminder_agent_id_fkey,
  DROP CONSTRAINT IF EXISTS agent_reminder_anchor_channel_id_fkey;

ALTER TABLE agent_reminder
  ADD COLUMN initiator_user_id UUID,
  ADD COLUMN cadence TEXT,
  ADD COLUMN schedule_timezone TEXT,
  ADD COLUMN cadence_next_at TIMESTAMPTZ,
  ADD COLUMN current_occurrence_id UUID,
  ADD COLUMN terminal_reason TEXT;

ALTER TABLE agent_reminder
  ADD CONSTRAINT agent_reminder_cadence_shape_check CHECK (
    cadence IS NULL
    OR cadence ~ '^every:[1-9][0-9]*[mhd]$'
    OR cadence ~ '^daily@([01][0-9]|2[0-3]):[0-5][0-9]$'
    OR cadence ~ '^weekly:(mon|tue|wed|thu|fri|sat|sun)(,(mon|tue|wed|thu|fri|sat|sun))*@([01][0-9]|2[0-3]):[0-5][0-9]$'
  ),
  ADD CONSTRAINT agent_reminder_cadence_timezone_check CHECK (
    (cadence IS NULL AND cadence_next_at IS NULL)
    OR (cadence LIKE 'every:%' AND cadence_next_at IS NOT NULL)
    OR ((cadence LIKE 'daily@%' OR cadence LIKE 'weekly:%') AND schedule_timezone IS NOT NULL AND cadence_next_at IS NOT NULL)
  );

CREATE TABLE agent_reminder_occurrence (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reminder_id UUID NOT NULL REFERENCES agent_reminder(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL,
  cadence_scheduled_for TIMESTAMPTZ NOT NULL,
  due_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'claimed', 'fired', 'cancelled')),
  title_snapshot TEXT NOT NULL CHECK (char_length(title_snapshot) BETWEEN 1 AND 500),
  cadence_snapshot TEXT,
  timezone_snapshot TEXT,
  receipt_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  fired_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  anchor_available BOOLEAN,
  terminal_reason TEXT,
  claimed_at TIMESTAMPTZ,
  fired_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (reminder_id, cadence_scheduled_for),
  CHECK (status <> 'claimed' OR claimed_at IS NOT NULL),
  CHECK (status <> 'fired' OR fired_at IS NOT NULL),
  CHECK (terminal_reason IS NULL OR status = 'cancelled')
);

CREATE INDEX idx_agent_reminder_occurrence_reminder_history
  ON agent_reminder_occurrence(reminder_id, COALESCE(fired_at, due_at) DESC, id DESC);

CREATE TABLE agent_reminder_lifecycle_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reminder_id UUID NOT NULL REFERENCES agent_reminder(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL,
  occurrence_id UUID REFERENCES agent_reminder_occurrence(id) ON DELETE SET NULL,
  event_type TEXT NOT NULL
    CHECK (event_type IN ('scheduled', 'fired', 'snoozed', 'updated', 'cancelled')),
  actor_type TEXT NOT NULL CHECK (actor_type IN ('agent', 'system')),
  actor_id UUID,
  previous_fire_at TIMESTAMPTZ,
  next_fire_at TIMESTAMPTZ,
  title_snapshot TEXT NOT NULL CHECK (char_length(title_snapshot) BETWEEN 1 AND 500),
  cadence_snapshot TEXT,
  timezone_snapshot TEXT,
  resulting_state TEXT NOT NULL
    CHECK (resulting_state IN ('scheduled', 'firing', 'fired', 'cancelled')),
  reason_code TEXT,
  details JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_reminder_lifecycle_event_log
  ON agent_reminder_lifecycle_event(reminder_id, created_at ASC, id ASC);

CREATE UNIQUE INDEX idx_agent_reminder_lifecycle_event_occurrence_once
  ON agent_reminder_lifecycle_event(occurrence_id, event_type)
  WHERE occurrence_id IS NOT NULL AND event_type IN ('fired', 'cancelled');

-- Every pre-V2 definition gets a schedule event without changing its identity,
-- anchor, next fire, or state.  Fired legacy rows also get one immutable
-- occurrence so Agent Card history and reminder log start from a complete
-- ledger instead of silently beginning at migration time.
INSERT INTO agent_reminder_lifecycle_event (
  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
  next_fire_at, title_snapshot, resulting_state, created_at
)
SELECT id, workspace_id, agent_id, 'scheduled', 'agent', agent_id,
       fire_at, title, status, created_at
FROM agent_reminder;

WITH inserted AS (
  INSERT INTO agent_reminder_occurrence (
    reminder_id, workspace_id, agent_id, cadence_scheduled_for, due_at,
    status, title_snapshot, receipt_message_id, fired_task_id,
    anchor_available, claimed_at, fired_at, created_at, updated_at
  )
  SELECT
    id, workspace_id, agent_id, fire_at, fire_at,
    CASE WHEN status = 'fired' OR fired_task_id IS NOT NULL THEN 'fired' ELSE 'claimed' END,
    title, NULL, fired_task_id, NULL,
    CASE WHEN status = 'firing' AND fired_task_id IS NULL THEN updated_at ELSE NULL END,
    CASE WHEN status = 'fired' OR fired_task_id IS NOT NULL THEN COALESCE(fired_at, updated_at) ELSE NULL END,
    created_at, updated_at
  FROM agent_reminder
  WHERE status IN ('fired', 'firing')
  RETURNING *
)
INSERT INTO agent_reminder_lifecycle_event (
  reminder_id, workspace_id, agent_id, occurrence_id, event_type,
  actor_type, previous_fire_at, title_snapshot, resulting_state, created_at
)
SELECT reminder_id, workspace_id, agent_id, id, 'fired',
       'system', due_at, title_snapshot, 'fired', fired_at
FROM inserted
WHERE status = 'fired';

UPDATE agent_reminder reminder
SET current_occurrence_id = occurrence.id
FROM agent_reminder_occurrence occurrence
WHERE occurrence.reminder_id = reminder.id
  AND reminder.status = 'firing'
  AND occurrence.status = 'claimed';

ALTER TABLE agent_reminder
  ADD CONSTRAINT agent_reminder_current_occurrence_fkey
  FOREIGN KEY (current_occurrence_id)
  REFERENCES agent_reminder_occurrence(id)
  ON DELETE SET NULL;

CREATE INDEX idx_agent_reminder_occurrence_claimed
  ON agent_reminder_occurrence(claimed_at)
  WHERE status = 'claimed';

COMMIT;
