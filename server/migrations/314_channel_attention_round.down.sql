-- Down migration for 313: remove the Channel Attention Round persistence layer.
-- Attention decisions participants are ephemeral audit data; dropping the
-- tables removes them.
DROP TABLE IF EXISTS channel_attention_response_grant;
DROP TABLE IF EXISTS channel_attention_participant;
DROP TABLE IF EXISTS channel_attention_round;
