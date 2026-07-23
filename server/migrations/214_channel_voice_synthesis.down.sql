BEGIN;

DROP TRIGGER IF EXISTS channel_message_enqueue_voice_synthesis ON channel_message;
DROP FUNCTION IF EXISTS enqueue_channel_voice_synthesis();
DROP TABLE IF EXISTS channel_voice_synthesis;

COMMIT;
