DROP TABLE IF EXISTS team_knowledge_edge;

-- Wiki kinds must be removed before restoring the pre-268 CHECK; otherwise
-- ADD CONSTRAINT fails when any context/decision rows remain.
DELETE FROM team_knowledge_item WHERE kind IN ('context', 'decision');

ALTER TABLE team_knowledge_item
  DROP CONSTRAINT IF EXISTS team_knowledge_item_kind_check;

ALTER TABLE team_knowledge_item
  ADD CONSTRAINT team_knowledge_item_kind_check
  CHECK (kind IN ('memory', 'pattern', 'skill', 'policy', 'troubleshooting'));
