ALTER TABLE agent_transport_draft
  ADD COLUMN IF NOT EXISTS task_id UUID REFERENCES agent_task_queue(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS decision_fact_id TEXT;

-- Drafts have always been paired with a held transport audit. Backfill their
-- source from that durable audit so a pending decision cannot leak to another
-- task for the same agent and target.
UPDATE agent_transport_draft AS draft
SET task_id = source.task_id,
    inbox_event_id = source.inbox_event_id,
    decision_fact_id = source.context_pack->>'producer_fact_id'
FROM LATERAL (
  SELECT audit.task_id, audit.inbox_event_id, audit.context_pack
  FROM agent_task_transport_audit AS audit
  WHERE audit.workspace_id = draft.workspace_id
    AND audit.agent_id = draft.agent_id
    AND audit.target = draft.target
    AND audit.action = 'message_send'
    AND audit.context_pack->>'held' = 'true'
  ORDER BY audit.created_at DESC
  LIMIT 1
) AS source
WHERE draft.task_id IS NULL
  AND draft.inbox_event_id IS NULL;

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_source_check;

ALTER TABLE agent_transport_draft
  ADD CONSTRAINT agent_transport_draft_source_check
  CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL)) NOT VALID;

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_workspace_id_agent_id_target_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_source_target
  ON agent_transport_draft(workspace_id, agent_id, task_id, target)
  WHERE task_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_inbox_target
  ON agent_transport_draft(workspace_id, agent_id, inbox_event_id, target)
  WHERE inbox_event_id IS NOT NULL;

ALTER TABLE agent_task_transport_audit
  DROP CONSTRAINT IF EXISTS agent_task_transport_audit_action_check;

ALTER TABLE agent_task_transport_audit
  ADD CONSTRAINT agent_task_transport_audit_action_check
  CHECK (action IN ('message_send', 'message_react', 'message_read', 'message_search', 'thread_unfollow', 'message_discard_draft'));
