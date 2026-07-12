ALTER TABLE task_message
    DROP COLUMN IF EXISTS tone,
    DROP COLUMN IF EXISTS summary,
    DROP COLUMN IF EXISTS action_label;

UPDATE task_message
SET visibility = 'diagnostic_only'
WHERE type = 'tool_result';

ALTER TABLE agent_activity_event
    DROP COLUMN IF EXISTS tone,
    DROP COLUMN IF EXISTS reason_label,
    DROP COLUMN IF EXISTS summary,
    DROP COLUMN IF EXISTS action_label;

UPDATE agent_activity_event
SET visibility = 'diagnostic_only'
WHERE event_kind IN (
    'tool_output',
    'telemetry',
    'compaction_started',
    'compaction_finished',
    'transport',
    'custom'
)
   OR reason_code LIKE '%freshness%';
