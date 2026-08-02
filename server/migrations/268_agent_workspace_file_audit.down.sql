-- Remap any rows using this migration's new values BEFORE narrowing the
-- CHECK constraints back down, mirroring migration 163's down.sql pattern.
-- Without this, ADD CONSTRAINT fails outright the moment any real
-- workspace_file/file row exists (which happens on the first agent file
-- read/write after this migration ships) — the down migration would be
-- unable to run at all instead of degrading gracefully.
UPDATE agent_activity_event
SET target_kind = 'none', target_slug = ''
WHERE target_kind = 'file';

UPDATE agent_activity_event
SET event_kind = 'custom'
WHERE event_kind = 'workspace_file';

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_target_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_target_kind_check
    CHECK (target_kind IN ('issue', 'dm', 'channel', 'thread', 'agent', 'none'));

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_event_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
    ));
