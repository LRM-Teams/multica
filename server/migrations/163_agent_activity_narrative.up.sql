ALTER TABLE agent_activity_event
    DROP CONSTRAINT IF EXISTS agent_activity_event_event_kind_check;

UPDATE agent_activity_event
SET event_kind = CASE
    WHEN severity = 'error' THEN 'error'
    WHEN event_type IN ('server_ping_received', 'daemon_liveness_probe_sent', 'probe_timeout_reconnect', 'transport_reconnected') THEN 'transport'
    ELSE 'custom'
END
WHERE event_kind NOT IN (
    'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
    'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
    'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
);

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom'
    ));

ALTER TABLE task_message
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'user_facing'
        CHECK (visibility IN ('user_facing', 'diagnostic_only')),
    ADD COLUMN IF NOT EXISTS action_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tone TEXT NOT NULL DEFAULT 'action'
        CHECK (tone IN ('wake', 'action', 'progress', 'success', 'failure', 'muted'));

ALTER TABLE agent_activity_event
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'user_facing'
        CHECK (visibility IN ('user_facing', 'diagnostic_only')),
    ADD COLUMN IF NOT EXISTS action_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reason_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tone TEXT NOT NULL DEFAULT 'action'
        CHECK (tone IN ('wake', 'action', 'progress', 'success', 'failure', 'muted'));
