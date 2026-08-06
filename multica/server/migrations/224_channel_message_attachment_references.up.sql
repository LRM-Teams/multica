BEGIN;

-- An attachment is a workspace file resource, while a message attachment is
-- a reference to that resource. Keep those identities separate so the same
-- uploaded file can appear in multiple group, DM, and thread messages without
-- duplicating its storage object.
ALTER TABLE channel_message
  ADD CONSTRAINT uq_channel_message_workspace_id_id UNIQUE (workspace_id, id);

ALTER TABLE attachment
  ADD CONSTRAINT uq_attachment_workspace_id_id UNIQUE (workspace_id, id);

CREATE TABLE channel_message_attachment (
  workspace_id UUID NOT NULL,
  channel_message_id UUID NOT NULL,
  attachment_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_message_id, attachment_id),
  FOREIGN KEY (workspace_id, channel_message_id)
    REFERENCES channel_message(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, attachment_id)
    REFERENCES attachment(workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_channel_message_attachment_attachment
  ON channel_message_attachment(attachment_id, channel_message_id);

CREATE INDEX idx_channel_message_attachment_workspace_message
  ON channel_message_attachment(workspace_id, channel_message_id);

-- Preserve every historically linked message before removing the singular
-- attachment.channel_message_id relationship.
INSERT INTO channel_message_attachment (
  workspace_id, channel_message_id, attachment_id, created_at
)
SELECT attachment.workspace_id,
       attachment.channel_message_id,
       attachment.id,
       attachment.created_at
FROM attachment
WHERE attachment.channel_message_id IS NOT NULL
ON CONFLICT (channel_message_id, attachment_id) DO NOTHING;

-- Repair messages whose canonical parts reference an existing workspace
-- attachment but whose old single-owner link was missing or pointed at a
-- different message/channel. Invalid or deleted ids remain unavailable; the
-- migration never invents metadata from message text.
INSERT INTO channel_message_attachment (
  workspace_id, channel_message_id, attachment_id, created_at
)
SELECT message.workspace_id,
       message.id,
       attachment.id,
       message.created_at
FROM channel_message message
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(message.parts, '[]'::jsonb)) AS part(value)
JOIN attachment
  ON attachment.workspace_id = message.workspace_id
 AND attachment.id = CASE
       WHEN COALESCE(part.value->>'attachment_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       THEN (part.value->>'attachment_id')::uuid
       ELSE NULL
     END
WHERE part.value->>'type' IN ('attachment', 'voice')
ON CONFLICT (channel_message_id, attachment_id) DO NOTHING;

DROP INDEX IF EXISTS idx_attachment_channel_message;

ALTER TABLE attachment
  DROP COLUMN channel_message_id;

COMMIT;
