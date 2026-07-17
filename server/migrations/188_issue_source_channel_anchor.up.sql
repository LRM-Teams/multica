-- An issue may be associated with a group even when no single message caused
-- its creation. Keep the existing one-row-per-issue anchor and make only the
-- message locator optional; channel_id remains the canonical group context.
ALTER TABLE issue_source_message
  ALTER COLUMN message_id DROP NOT NULL;
