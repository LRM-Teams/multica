-- Thread replies belong only to their thread. Clear the historical projection
-- before dropping the write/read surface so prior rows remain available in the
-- original thread instead of appearing in a channel timeline.
UPDATE channel_message
SET main_timeline_visible = false
WHERE main_timeline_visible;

DROP INDEX IF EXISTS idx_channel_message_main_projection_seq;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS main_timeline_visible;

-- The transport draft used this field only to persist the now-retired
-- show_in_channel behavior. Preserve every pending draft's message content,
-- parts, and target while retiring that dead compatibility surface.
ALTER TABLE agent_transport_draft
  DROP COLUMN IF EXISTS options;
