BEGIN;

ALTER TABLE attachment
  ADD COLUMN channel_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE;

-- The old schema can represent only one message per file resource. On a
-- rollback, retain the earliest reference deterministically and discard the
-- additional references that the old model cannot encode.
UPDATE attachment
SET channel_message_id = (
  SELECT reference.channel_message_id
  FROM channel_message_attachment reference
  WHERE reference.attachment_id = attachment.id
  ORDER BY reference.created_at, reference.channel_message_id
  LIMIT 1
)
WHERE EXISTS (
  SELECT 1
  FROM channel_message_attachment reference
  WHERE reference.attachment_id = attachment.id
);

CREATE INDEX idx_attachment_channel_message
  ON attachment(channel_message_id)
  WHERE channel_message_id IS NOT NULL;

DROP TABLE channel_message_attachment;

ALTER TABLE attachment
  DROP CONSTRAINT uq_attachment_workspace_id_id;

ALTER TABLE channel_message
  DROP CONSTRAINT uq_channel_message_workspace_id_id;

COMMIT;
