-- Chat work runs through agent_inbox_event rather than agent_task_queue. Keep
-- its runtime and execution settings immutable at birth so an agent Profile
-- edit changes only later work, never a queued or running chat turn.
ALTER TABLE agent_inbox_event
    ADD COLUMN runtime_id UUID REFERENCES agent_runtime(id),
    ADD COLUMN execution_config JSONB;

-- Historical inbox rows had no immutable snapshot. Preserve their existing
-- routing as a compatibility fallback; new writes always set both fields.
UPDATE agent_inbox_event e
SET runtime_id = s.runtime_id
FROM agent_session s
WHERE e.runtime_id IS NULL
  AND e.agent_session_id = s.id;

CREATE INDEX idx_agent_inbox_event_runtime_pending
    ON agent_inbox_event (runtime_id, priority DESC, created_at, id)
    WHERE status IN ('pending', 'failed');
