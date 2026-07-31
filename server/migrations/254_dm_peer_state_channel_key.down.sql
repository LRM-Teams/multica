DELETE FROM dm_peer_state WHERE peer_type = 'channel';

ALTER TABLE dm_peer_state
  DROP CONSTRAINT IF EXISTS dm_peer_state_peer_type_check;

ALTER TABLE dm_peer_state
  ADD CONSTRAINT dm_peer_state_peer_type_check
  CHECK (peer_type IN ('user', 'agent'));
