ALTER TABLE agent_transport_draft
  ADD COLUMN IF NOT EXISTS task_id UUID REFERENCES agent_task_queue(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS inbox_event_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS decision_fact_id TEXT;

-- A legacy draft can only be claimed by held audits that carry its own
-- client_message_id. Older servers could emit more than one held audit while
-- replacing the same draft as newer context arrived. Those rows are safe only
-- when every matching audit names the same valid tagged source and the audit
-- facts for the draft's persisted seen/latest range agree. created_at/id only
-- break ties between equivalent current-range winner audits. Never use a
-- "latest audit for agent/target" heuristic, which could cross task sources.
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
    HAVING COUNT(audit.id) = 0
       OR COUNT(audit.id) FILTER (
            WHERE (audit.task_id IS NULL) = (audit.inbox_event_id IS NULL)
          ) <> 0
       OR COUNT(audit.id) FILTER (
            WHERE COALESCE(btrim(audit.context_pack->>'producer_fact_id'), '') = ''
          ) <> 0
       OR COUNT(DISTINCT CASE
            WHEN audit.task_id IS NOT NULL AND audit.inbox_event_id IS NULL
              THEN 'task:' || audit.task_id::text
            WHEN audit.task_id IS NULL AND audit.inbox_event_id IS NOT NULL
              THEN 'inbox:' || audit.inbox_event_id::text
          END) <> 1
       OR COUNT(audit.id) FILTER (
            WHERE audit.context_pack->>'seen_up_to_seq' = draft.seen_up_to_seq::text
              AND audit.context_pack->>'latest_seq' = draft.held_to_seq::text
          ) = 0
       OR COUNT(DISTINCT audit.context_pack->>'producer_fact_id') FILTER (
            WHERE audit.context_pack->>'seen_up_to_seq' = draft.seen_up_to_seq::text
              AND audit.context_pack->>'latest_seq' = draft.held_to_seq::text
          ) <> 1
  ) THEN
    RAISE EXCEPTION
      'cannot safely backfill agent_transport_draft source: each legacy draft needs one unambiguous source and one current-range fact across held audits with the same client_message_id';
  END IF;
END $$;

WITH ranked_audits AS (
  SELECT draft.id AS draft_id,
         audit.task_id,
         audit.inbox_event_id,
         audit.context_pack->>'producer_fact_id' AS decision_fact_id,
         ROW_NUMBER() OVER (
           PARTITION BY draft.id
           ORDER BY audit.created_at DESC, audit.id DESC
         ) AS winner_rank
  FROM agent_transport_draft AS draft
  JOIN agent_task_transport_audit AS audit
    ON audit.workspace_id = draft.workspace_id
   AND audit.agent_id = draft.agent_id
   AND audit.target = draft.target
   AND audit.client_message_id = draft.client_message_id
   AND audit.action = 'message_send'
   AND audit.context_pack->>'held' = 'true'
   AND audit.context_pack->>'seen_up_to_seq' = draft.seen_up_to_seq::text
   AND audit.context_pack->>'latest_seq' = draft.held_to_seq::text
   AND COALESCE(btrim(audit.context_pack->>'producer_fact_id'), '') <> ''
  WHERE draft.task_id IS NULL
    AND draft.inbox_event_id IS NULL
)
UPDATE agent_transport_draft AS draft
SET task_id = winner.task_id,
    inbox_event_id = winner.inbox_event_id,
    decision_fact_id = winner.decision_fact_id
FROM ranked_audits AS winner
WHERE winner.draft_id = draft.id
  AND winner.winner_rank = 1;

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_source_check;

ALTER TABLE agent_transport_draft
  ADD CONSTRAINT agent_transport_draft_source_check
  CHECK ((task_id IS NOT NULL) <> (inbox_event_id IS NOT NULL));

ALTER TABLE agent_transport_draft
  ALTER COLUMN decision_fact_id SET NOT NULL;

ALTER TABLE agent_transport_draft
  ADD CONSTRAINT agent_transport_draft_decision_fact_nonempty_check
  CHECK (btrim(decision_fact_id) <> '');

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_workspace_id_agent_id_target_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_source_target
  ON agent_transport_draft(workspace_id, agent_id, task_id, target)
  WHERE task_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_transport_draft_inbox_target
  ON agent_transport_draft(workspace_id, agent_id, inbox_event_id, target)
  WHERE inbox_event_id IS NOT NULL;
