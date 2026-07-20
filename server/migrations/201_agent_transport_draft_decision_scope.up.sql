ALTER TABLE agent_transport_draft
  ADD COLUMN IF NOT EXISTS task_id UUID REFERENCES agent_task_queue(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS decision_fact_id TEXT;

-- A legacy draft can only be claimed by the held audit that carries its own
-- client_message_id. Do not use a "latest audit for agent/target" heuristic:
-- that can bind a pending draft to a different task. Any missing or ambiguous
-- source stops the migration so an operator can reconcile it explicitly.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM agent_transport_draft AS draft
    LEFT JOIN agent_task_transport_audit AS audit
      ON audit.workspace_id = draft.workspace_id
     AND audit.agent_id = draft.agent_id
     AND audit.target = draft.target
     AND audit.client_message_id = draft.client_message_id
     AND audit.action = 'message_send'
     AND audit.context_pack->>'held' = 'true'
    WHERE draft.task_id IS NULL
      AND draft.inbox_event_id IS NULL
    GROUP BY draft.id
    HAVING COUNT(audit.id) <> 1
       OR COUNT(audit.id) FILTER (
            WHERE (audit.task_id IS NULL) = (audit.inbox_event_id IS NULL)
          ) <> 0
  ) THEN
    RAISE EXCEPTION
      'cannot safely backfill agent_transport_draft source: each legacy draft needs exactly one source-bearing held audit with the same client_message_id';
  END IF;
END $$;

UPDATE agent_transport_draft AS draft
SET task_id = audit.task_id,
    inbox_event_id = audit.inbox_event_id,
    decision_fact_id = audit.context_pack->>'producer_fact_id'
FROM agent_task_transport_audit AS audit
WHERE draft.task_id IS NULL
  AND draft.inbox_event_id IS NULL
  AND audit.workspace_id = draft.workspace_id
  AND audit.agent_id = draft.agent_id
  AND audit.target = draft.target
  AND audit.client_message_id = draft.client_message_id
  AND audit.action = 'message_send'
  AND audit.context_pack->>'held' = 'true';

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_source_check;

ALTER TABLE agent_transport_draft
  ADD CONSTRAINT agent_transport_draft_source_check
  CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL));

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_workspace_id_agent_id_target_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_source_target
  ON agent_transport_draft(workspace_id, agent_id, task_id, target)
  WHERE task_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_inbox_target
  ON agent_transport_draft(workspace_id, agent_id, inbox_event_id, target)
  WHERE inbox_event_id IS NOT NULL;
