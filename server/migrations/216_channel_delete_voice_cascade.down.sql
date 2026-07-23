ALTER TABLE voice_call_session
  DROP CONSTRAINT IF EXISTS voice_call_session_channel_id_fkey;

ALTER TABLE voice_call_session
  ADD CONSTRAINT voice_call_session_channel_id_fkey
  FOREIGN KEY (channel_id) REFERENCES channel(id);
