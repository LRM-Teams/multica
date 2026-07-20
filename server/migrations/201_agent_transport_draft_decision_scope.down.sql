DROP INDEX IF EXISTS idx_agent_transport_draft_inbox_target;
DROP INDEX IF EXISTS idx_agent_transport_draft_source_target;

-- The prior schema allowed one pending draft per agent/target. A deliberate
-- rollback therefore retains the most recently updated decision for each old
-- key before restoring that uniqueness contract.
DELETE FROM agent_transport_draft AS draft
USING (
  SELECT id
  FROM (
    SELECT id, row_number() OVER (
      PARTITION BY workspace_id, agent_id, target
      ORDER BY updated_at DESC, created_at DESC, id DESC
    ) AS row_number
    FROM agent_transport_draft
  ) AS ranked
  WHERE ranked.row_number > 1
) AS duplicate
WHERE draft.id = duplicate.id;

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_source_check;

ALTER TABLE agent_transport_draft
  DROP CONSTRAINT IF EXISTS agent_transport_draft_decision_fact_nonempty_check;

ALTER TABLE agent_transport_draft
  ADD CONSTRAINT agent_transport_draft_workspace_id_agent_id_target_key
  UNIQUE (workspace_id, agent_id, target);

ALTER TABLE agent_transport_draft
  DROP COLUMN IF EXISTS decision_fact_id,
  DROP COLUMN IF EXISTS inbox_event_id,
  DROP COLUMN IF EXISTS task_id;
