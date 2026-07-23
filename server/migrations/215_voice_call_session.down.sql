BEGIN;

DROP TABLE IF EXISTS voice_call_turn;
DROP TRIGGER IF EXISTS voice_call_session_status_transition ON voice_call_session;
DROP FUNCTION IF EXISTS enforce_voice_call_session_status_transition();
DROP TABLE IF EXISTS voice_call_session;

COMMIT;
