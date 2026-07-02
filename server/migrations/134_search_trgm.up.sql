CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_channel_message_content_trgm
    ON channel_message USING GIN (content gin_trgm_ops);
