-- name: CreateAttachment :one
INSERT INTO attachment (
  id, workspace_id, issue_id, comment_id, chat_session_id, channel_id,
  uploader_type, uploader_id, filename, url, content_type, size_bytes
)
VALUES (
  $1, $2, sqlc.narg(issue_id), sqlc.narg(comment_id), sqlc.narg(chat_session_id), sqlc.narg(channel_id),
  $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: ListAttachmentsByIssue :many
SELECT * FROM attachment
WHERE issue_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: ListAttachmentsByComment :many
SELECT * FROM attachment
WHERE comment_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: GetAttachment :one
SELECT * FROM attachment
WHERE id = $1 AND workspace_id = $2;

-- name: GetAttachmentByIDOnly :one
-- Used by the download endpoint, which derives workspace context from the
-- attachment row itself rather than from request headers/query params. The
-- caller still has to verify the requester is a member of the returned
-- workspace_id before serving the bytes — this query is access-neutral on
-- purpose so a self-contained URL like /api/attachments/{id}/download can
-- work as a native <img>/<video> resource load (no header attachment).
SELECT * FROM attachment
WHERE id = $1;

-- name: ListAttachmentsByCommentIDs :many
SELECT * FROM attachment
WHERE comment_id = ANY($1::uuid[]) AND workspace_id = $2
ORDER BY created_at ASC;

-- name: ListAttachmentURLsByIssueOrComments :many
SELECT a.url FROM attachment a
WHERE a.issue_id = $1
   OR a.comment_id IN (SELECT c.id FROM comment c WHERE c.issue_id = $1);

-- name: ListAttachmentURLsByCommentID :many
SELECT url FROM attachment
WHERE comment_id = $1;

-- name: LinkAttachmentsToComment :execrows
-- Bind attachments to a comment. Accepts:
--   - already issue-scoped rows (issue_id = $2, comment_id IS NULL)
--   - workspace-scoped unbound uploads (issue_id IS NULL) — same contract as
--     LinkAttachmentsToIssue / `multica attachment upload` then `--attachment-id`
UPDATE attachment
SET comment_id = $1,
    issue_id = $2
WHERE workspace_id = $3
  AND comment_id IS NULL
  AND (issue_id IS NULL OR issue_id = $2)
  AND id = ANY($4::uuid[]);

-- name: ReplaceCommentAttachments :exec
-- Replace the attachment set for a comment. Newly added ids may be unbound
-- workspace uploads (issue_id IS NULL) or already scoped to this issue.
UPDATE attachment
SET comment_id = CASE
  WHEN id = ANY(sqlc.arg(attachment_ids)::uuid[]) THEN $1
  ELSE NULL
END,
    issue_id = CASE
  WHEN id = ANY(sqlc.arg(attachment_ids)::uuid[]) THEN $2
  ELSE issue_id
END
WHERE workspace_id = $3
  AND (
    comment_id = $1
    OR (
      comment_id IS NULL
      AND id = ANY(sqlc.arg(attachment_ids)::uuid[])
      AND (issue_id IS NULL OR issue_id = $2)
    )
  );

-- name: LinkAttachmentsToChatMessage :exec
UPDATE attachment
SET chat_message_id = $1
WHERE chat_session_id = $2
  AND chat_message_id IS NULL
  AND id = ANY($3::uuid[]);

-- name: ListAttachmentsByChatMessage :many
SELECT * FROM attachment
WHERE chat_message_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: ListAttachmentsByChatMessageIDs :many
SELECT * FROM attachment
WHERE chat_message_id = ANY($1::uuid[]) AND workspace_id = $2
ORDER BY created_at ASC;

-- name: ListAttachmentsByChannelMessageIDs :many
SELECT reference.channel_message_id AS linked_channel_message_id,
       sqlc.embed(attachment)
FROM channel_message_attachment reference
JOIN attachment ON attachment.id = reference.attachment_id
WHERE reference.channel_message_id = ANY($1::uuid[])
  AND reference.workspace_id = $2
ORDER BY reference.created_at, attachment.created_at;

-- name: LinkOwnedAttachmentsToChannelMessage :one
-- Creates message references for attachment resources owned by the sender.
-- A resource may be reused across group, DM, thread, and multiple messages;
-- channel_id remains upload provenance and never limits later references.
WITH owned AS (
  SELECT attachment.workspace_id, attachment.id
  FROM attachment
  WHERE attachment.workspace_id = $2
    AND attachment.uploader_type = $3
    AND attachment.uploader_id = $4
    AND attachment.id = ANY(sqlc.arg(attachment_ids)::uuid[])
), linked AS (
  INSERT INTO channel_message_attachment (
    workspace_id, channel_message_id, attachment_id
  )
  SELECT owned.workspace_id, $1, owned.id
  FROM owned
  ON CONFLICT (channel_message_id, attachment_id) DO NOTHING
)
SELECT count(*) FROM owned;

-- name: ListAttachmentsByChannel :many
SELECT attachment.*
FROM attachment
WHERE attachment.workspace_id = $2
  AND (
    attachment.channel_id = $1
    OR attachment.id IN (
      SELECT reference.attachment_id
      FROM channel_message_attachment reference
      JOIN channel_message message ON message.id = reference.channel_message_id
      WHERE reference.workspace_id = $2
        AND message.channel_id = $1
    )
  )
ORDER BY attachment.created_at DESC;

-- name: LinkAttachmentsToIssue :exec
UPDATE attachment
SET issue_id = $1
WHERE workspace_id = $2
  AND issue_id IS NULL
  AND id = ANY($3::uuid[]);

-- name: DeleteAttachment :exec
DELETE FROM attachment WHERE id = $1 AND workspace_id = $2;
