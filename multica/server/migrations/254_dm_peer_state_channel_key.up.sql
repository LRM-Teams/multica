-- Supervised agent_pair DM viewer prefs (pin/mute/unread/close) are keyed by
-- channel, not by a single participant. Using participants[0] would collide
-- with the owner's 1:1 DM against that same agent. peer_type='channel' +
-- peer_id=channel.id keeps viewer state per conversation.

ALTER TABLE dm_peer_state
  DROP CONSTRAINT IF EXISTS dm_peer_state_peer_type_check;

ALTER TABLE dm_peer_state
  ADD CONSTRAINT dm_peer_state_peer_type_check
  CHECK (peer_type IN ('user', 'agent', 'channel'));
