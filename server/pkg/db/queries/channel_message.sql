-- name: GetChannelMessageByID :one
-- Full channel_message row for sqlc → ChannelMessage (task #85 P1).
-- Column list must stay aligned with models.ChannelMessage; S4 test
-- asserts seq/parts/deleted_at flow without hand-written Scan.
SELECT
  id,
  channel_id,
  workspace_id,
  author_type,
  author_id,
  author_name,
  content,
  source,
  external_message_id,
  created_at,
  thread_id,
  trigger_depth,
  reply_to_message_id,
  thread_root_message_id,
  parts,
  conversation_id,
  seq,
  client_message_id,
  edited_at,
  deleted_at,
  quote_message_id,
  quote_snapshot,
  membership_generation_id
FROM channel_message
WHERE id = $1
  AND workspace_id = $2;
