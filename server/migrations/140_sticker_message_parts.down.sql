DROP TABLE IF EXISTS sticker_asset;
DROP TABLE IF EXISTS sticker_pack;

ALTER TABLE channel_message
  DROP COLUMN IF EXISTS parts;

ALTER TABLE chat_message
  DROP COLUMN IF EXISTS parts;
