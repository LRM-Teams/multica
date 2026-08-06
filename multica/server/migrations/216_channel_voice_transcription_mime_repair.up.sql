BEGIN;

WITH recoverable AS MATERIALIZED (
  SELECT
    transcription.message_id,
    transcription.attachment_id
  FROM channel_voice_transcription transcription
  JOIN attachment
    ON attachment.id = transcription.attachment_id
  WHERE transcription.status = 'failed'
    AND transcription.last_error_code = 'invalid_recording'
    AND lower(split_part(trim(attachment.content_type), ';', 1)) IN (
      'audio/wave',
      'audio/x-wav',
      'audio/vnd.wave'
    )
    AND lower(attachment.filename) LIKE '%.wav'
),
canonicalized_attachments AS (
  UPDATE attachment
  SET content_type = 'audio/wav'
  FROM recoverable
  WHERE attachment.id = recoverable.attachment_id
  RETURNING attachment.id
),
pending_messages AS (
  UPDATE channel_message message
  SET parts = (
    SELECT COALESCE(
      jsonb_agg(
        CASE
          WHEN part.value->>'type' = 'voice'
            AND part.value->>'attachment_id' = recoverable.attachment_id::text
          THEN jsonb_set(
            part.value,
            '{transcription_status}',
            to_jsonb('pending'::text),
            true
          )
          ELSE part.value
        END
        ORDER BY part.ordinality
      ),
      '[]'::jsonb
    )
    FROM jsonb_array_elements(message.parts) WITH ORDINALITY AS part(value, ordinality)
  )
  FROM recoverable
  WHERE message.id = recoverable.message_id
  RETURNING message.id
)
UPDATE channel_voice_transcription transcription
SET
  status = 'pending',
  attempts = 0,
  next_attempt_at = now(),
  claimed_at = NULL,
  last_error_code = '',
  updated_at = now()
FROM recoverable
WHERE transcription.message_id = recoverable.message_id
  AND EXISTS (
    SELECT 1
    FROM canonicalized_attachments
    WHERE canonicalized_attachments.id = recoverable.attachment_id
  )
  AND EXISTS (
    SELECT 1
    FROM pending_messages
    WHERE pending_messages.id = recoverable.message_id
  );

COMMIT;
