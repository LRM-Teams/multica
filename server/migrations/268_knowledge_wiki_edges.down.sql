DROP TABLE IF EXISTS team_knowledge_edge;

ALTER TABLE team_knowledge_item
  DROP CONSTRAINT IF EXISTS team_knowledge_item_kind_check;

ALTER TABLE team_knowledge_item
  ADD CONSTRAINT team_knowledge_item_kind_check
  CHECK (kind IN ('memory', 'pattern', 'skill', 'policy', 'troubleshooting'));
