-- LRM-1000 rollback: 'context'/'decision' rows are user-authored knowledge
-- items with no safe remap target — the pre-268 kinds ('memory', 'pattern',
-- 'skill', 'policy', 'troubleshooting') mean different things, so relabeling
-- would silently misclassify the user's knowledge base. A plain DELETE makes
-- rollback succeed while destroying that data. Fail loud instead, and only
-- when there is real data at stake.
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count
      FROM team_knowledge_item WHERE kind IN ('context', 'decision');
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 268 down cannot proceed: % row(s) in team_knowledge_item have kind in (''context'', ''decision''). These are user-authored knowledge items with no safe remap target — the pre-268 kinds carry different meanings, so relabeling would misclassify them. If you accept permanently losing these items, run: DELETE FROM team_knowledge_item WHERE kind IN (''context'', ''decision''); -- then re-run this down migration.', affected_count;
    END IF;
END $$;

DROP TABLE IF EXISTS team_knowledge_edge;

ALTER TABLE team_knowledge_item
  DROP CONSTRAINT IF EXISTS team_knowledge_item_kind_check;

ALTER TABLE team_knowledge_item
  ADD CONSTRAINT team_knowledge_item_kind_check
  CHECK (kind IN ('memory', 'pattern', 'skill', 'policy', 'troubleshooting'));
