-- LRM-1082 / LRM-1079 P3 rollback (safe, reversible).
--
-- This migration only added COMMENT ON TABLE/COLUMN markers. Rolling back
-- clears those comments; no schema, data, or FK changes are undone because
-- none were made in the up migration.
--
-- Rollback procedure:
--   1. Deploy a build that still expects the deprecate comments OR is
--      indifferent to them (comments are documentation-only).
--   2. Run this down migration (or re-apply migrate down for version 274).
--   3. No data restore needed.
--
-- If a later DROP migration is added after leftover readers (env-dispatch /
-- onboarding / standalone Agent Chat / voice) migrate away, that DROP must
-- ship its own backup/restore down story — this file does not cover DROP.

COMMENT ON TABLE chat_session IS NULL;
COMMENT ON TABLE channel_agent_session IS NULL;
COMMENT ON COLUMN agent_inbox_event.chat_session_id IS NULL;
