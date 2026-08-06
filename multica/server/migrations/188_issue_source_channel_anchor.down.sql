-- Group-only anchors cannot be represented by the original message-required
-- schema, so remove them before restoring that invariant on rollback.
DELETE FROM issue_source_message
WHERE message_id IS NULL;

ALTER TABLE issue_source_message
  ALTER COLUMN message_id SET NOT NULL;
