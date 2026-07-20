-- Retire only the legacy avatar URLs that the first-party draft dialog
-- demonstrably generated locally. A draft stored with avatar_url NULL was
-- assigned a random human image only when the user confirmed creation; the
-- resulting agent therefore has no user-supplied avatar intent to preserve.
--
-- Deliberately excluded:
--   * pool URLs on any other agent: historic API/Windy-link data cannot tell
--     whether a pool URL was an automatic fallback or an explicit selection;
--   * human-11: Wendy's fixed system preset, not the random pool;
--   * every non-pool URL: uploaded and external avatar URLs are user intent.
--   * agents with conflicting used drafts: the schema does not make
--     used_agent_id unique, so one explicit used draft makes the historical
--     provenance ambiguous and must win over a separate NULL draft.
--
-- This is intentionally a one-way data repair. Once the URL is cleared the
-- renderer derives the stable default from the immutable agent ID instead of
-- storing a default choice in the database.
UPDATE agent AS a
SET avatar_url = NULL,
    updated_at = now()
WHERE a.avatar_url IN (
    '/agent-avatars/human-01.jpg',
    '/agent-avatars/human-02.jpg',
    '/agent-avatars/human-03.jpg',
    '/agent-avatars/human-04.jpg',
    '/agent-avatars/human-05.jpg',
    '/agent-avatars/human-06.jpg',
    '/agent-avatars/human-07.jpg',
    '/agent-avatars/human-08.jpg',
    '/agent-avatars/human-09.jpg',
    '/agent-avatars/human-10.jpg',
    '/agent-avatars/human-12.jpg',
    '/agent-avatars/human-13.jpg',
    '/agent-avatars/human-14.jpg',
    '/agent-avatars/human-15.jpg',
    '/agent-avatars/human-16.jpg',
    '/agent-avatars/human-17.jpg',
    '/agent-avatars/human-18.jpg',
    '/agent-avatars/human-19.jpg',
    '/agent-avatars/human-20.jpg',
    '/agent-avatars/human-21.jpg',
    '/agent-avatars/human-22.jpg',
    '/agent-avatars/human-23.jpg',
    '/agent-avatars/human-24.jpg'
)
AND EXISTS (
    SELECT 1
    FROM agent_creation_draft AS d
    WHERE d.used_agent_id = a.id
      AND d.status = 'used'
      AND d.avatar_url IS NULL
)
AND NOT EXISTS (
    SELECT 1
    FROM agent_creation_draft AS d
    WHERE d.used_agent_id = a.id
      AND d.status = 'used'
      AND d.avatar_url IS NOT NULL
);
