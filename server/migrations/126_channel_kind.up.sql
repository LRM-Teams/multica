-- Add channel.kind to distinguish 1-on-1 direct messages ('dm') from regular
-- group channels ('group', the default for every existing channel). A DM is a
-- 2-member channel created via the DM create-or-find path; uniqueness of a
-- member pair is enforced by the existing UNIQUE (workspace_id, name) using a
-- canonical dm name (e.g. "dm:agent:<id>|user:<id>"), so no extra index is
-- needed here. Group channels keep their human-given names.
ALTER TABLE channel
  ADD COLUMN kind TEXT NOT NULL DEFAULT 'group'
  CHECK (kind IN ('group', 'dm'));
