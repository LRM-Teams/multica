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

-- name: LinkAttachmentsToComment :exec
UPDATE attachment
SET comment_id = $1
WHERE issue_id = $2
  AND comment_id IS NULL
  AND id = ANY($3::uuid[]);

-- name: ReplaceCommentAttachments :exec
UPDATE attachment
SET comment_id = CASE
  WHEN id = ANY(sqlc.arg(attachment_ids)::uuid[]) THEN $1
  ELSE NULL
END
WHERE issue_id = $2
  AND (
    comment_id = $1
    OR (comment_id IS NULL AND id = ANY(sqlc.arg(attachment_ids)::uuid[]))
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

-- name: LinkAttachmentsToChannelMessage :exec
UPDATE attachment
SET channel_message_id = $1
WHERE channel_id = $2
  AND channel_message_id IS NULL
  AND id = ANY($3::uuid[]);

-- name: ListAttachmentsByChannelMessageIDs :many
SELECT * FROM attachment
WHERE channel_message_id = ANY($1::uuid[]) AND workspace_id = $2
ORDER BY created_at ASC;

-- name: LinkOwnedAttachmentsToChannelMessage :exec
-- Binds channel_id + channel_message_id for attachments the agent owns.
-- Accepts either:
--   1) unbound uploads (channel_id IS NULL) — channel resolved at send time, or
--   2) uploads already scoped to this channel via `attachment upload --target`
--      (channel_id already set, channel_message_id still NULL).
-- Previously requiring channel_id IS NULL broke the documented --target flow:
-- send succeeded with attachment parts, but LinkOwned matched 0 rows → UI
-- rendered "Attachment unavailable".
UPDATE attachment
SET channel_id = $1, channel_message_id = $2
WHERE workspace_id = $3
  AND uploader_type = $4
  AND uploader_id = $5
  AND channel_message_id IS NULL
  AND (channel_id IS NULL OR channel_id = $1)
  AND id = ANY(sqlc.arg(attachment_ids)::uuid[]);

-- name: ListAttachmentsByChannel :many
SELECT * FROM attachment
WHERE channel_id = $1 AND workspace_id = $2
ORDER BY created_at DESC;

-- name: LinkAttachmentsToIssue :exec
UPDATE attachment
SET issue_id = $1
WHERE workspace_id = $2
  AND issue_id IS NULL
  AND id = ANY($3::uuid[]);

-- name: DeleteAttachment :exec
DELETE FROM attachment WHERE id = $1 AND workspace_id = $2;
