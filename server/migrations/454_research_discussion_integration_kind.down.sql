-- 'integration' discussions have no equivalent under the narrower kind list
-- this migration reverts to, so there is no safe remap target. Fail loud when
-- real rows exist instead of letting the ADD CONSTRAINT below reject them
-- (matching migration 107/143/186's guard pattern).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM research_discussion WHERE kind = 'integration';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 454 down cannot proceed: % row(s) in research_discussion have kind=''integration''. There is no safe value to remap them to under the narrower kind list this migration is reverting to. If you accept permanently losing these discussions, run: DELETE FROM research_discussion WHERE kind = ''integration''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE research_discussion
  DROP CONSTRAINT research_discussion_kind_check;

ALTER TABLE research_discussion
  ADD CONSTRAINT research_discussion_kind_check
  CHECK (kind IN ('match', 'promotion', 'assimilation', 'dispute'));
