ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_event_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN (
        'thinking', 'tool_call', 'tool_output', 'turn_end', 'session_init',
        'compaction_started', 'compaction_finished', 'wake_attempt', 'error',
        'text', 'system', 'transport', 'telemetry', 'blocked', 'custom',
        'workspace_file'
    ));

ALTER TABLE agent_activity_event
    DROP CONSTRAINT agent_activity_event_target_kind_check;

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_target_kind_check
    CHECK (target_kind IN ('issue', 'dm', 'channel', 'thread', 'agent', 'file', 'none'));
