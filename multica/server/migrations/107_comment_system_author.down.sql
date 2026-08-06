-- Task #99 (2026-08-02): this migration cannot roll back safely if any
-- system-authored comments exist. Those rows (the MUL-2538 platform-owned
-- child-issue-done parent notifications) use a zero-UUID sentinel author_id
-- with no real member/agent behind it — there is no semantically safe value
-- to remap them to within the narrower CHECK (member, agent). Remapping
-- would misattribute a fake author; author_id has no FK, so that would
-- succeed at the DB level while being wrong. The original version of this
-- down migration silently DELETEd them instead, which "worked" but
-- destroyed notification history with no warning. Fail loud instead: a
-- rollback that succeeds by quietly deleting data is more dangerous than
-- one that refuses outright.
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM comment WHERE author_type = 'system';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 107 down cannot proceed: % row(s) in comment have author_type=''system'' (platform-owned child-issue-done parent-notification comments, MUL-2538). They use a zero-UUID sentinel author_id with no real member/agent behind it, so there is no safe value to remap them to under the narrower CHECK (member, agent) this migration is reverting to. If you accept permanently losing this notification history, run: DELETE FROM comment WHERE author_type = ''system''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE comment DROP CONSTRAINT IF EXISTS comment_author_type_check;
ALTER TABLE comment ADD CONSTRAINT comment_author_type_check
    CHECK (author_type IN ('member', 'agent'));
