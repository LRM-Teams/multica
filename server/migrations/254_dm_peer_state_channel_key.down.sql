-- Task #101 (2026-08-02): peer_type='channel' rows are real, active
-- production data (dm.go) with no safe remap target — this migration's own
-- up.sql comment explains why 'channel' exists at all: remapping to 'user'/
-- 'agent' with participants[0] would recreate the exact collision (against
-- the owner's 1:1 DM with the same agent) this migration was written to fix.
-- The original DELETE silently destroyed viewer prefs (pin/mute/unread/close)
-- on rollback with no warning. Fail loud instead, only when there's real
-- data at stake — matching migration 107/143/181/182/186/207/223's fix
-- (task #99/#101).
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count FROM dm_peer_state WHERE peer_type = 'channel';
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 254 down cannot proceed: % row(s) in dm_peer_state have peer_type=''channel''. Remapping them to user/agent would recreate the exact 1:1-DM collision this migration exists to avoid — there is no safe remap target. If you accept permanently losing these viewer preferences (pin/mute/unread/close state), run: DELETE FROM dm_peer_state WHERE peer_type = ''channel''; -- then re-run this down migration.', affected_count;
    END IF;
END $$;

ALTER TABLE dm_peer_state
  DROP CONSTRAINT IF EXISTS dm_peer_state_peer_type_check;

ALTER TABLE dm_peer_state
  ADD CONSTRAINT dm_peer_state_peer_type_check
  CHECK (peer_type IN ('user', 'agent'));
