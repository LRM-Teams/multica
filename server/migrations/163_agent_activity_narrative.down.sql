ALTER TABLE agent_activity_event
    DROP COLUMN IF EXISTS tone,
    DROP COLUMN IF EXISTS reason_label,
    DROP COLUMN IF EXISTS summary,
    DROP COLUMN IF EXISTS action_label,
    DROP COLUMN IF EXISTS visibility;

ALTER TABLE agent_activity_event
    DROP CONSTRAINT IF EXISTS agent_activity_event_event_kind_check;

UPDATE agent_activity_event
SET event_kind = CASE
    WHEN event_kind = 'error' THEN 'lifecycle'
    WHEN event_kind = 'transport' THEN 'lifecycle'
    ELSE 'platform_decision'
END
WHERE event_kind NOT IN ('lifecycle', 'platform_decision');

ALTER TABLE agent_activity_event
    ADD CONSTRAINT agent_activity_event_event_kind_check
    CHECK (event_kind IN ('lifecycle', 'platform_decision'));

ALTER TABLE task_message
    DROP COLUMN IF EXISTS tone,
    DROP COLUMN IF EXISTS summary,
    DROP COLUMN IF EXISTS action_label,
    DROP COLUMN IF EXISTS visibility;
