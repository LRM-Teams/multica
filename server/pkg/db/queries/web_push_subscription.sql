-- name: GetWebPushSubscriptionByEndpoint :one
SELECT * FROM web_push_subscription
WHERE endpoint = $1;

-- name: UpsertWebPushSubscription :one
INSERT INTO web_push_subscription (
    workspace_id, user_id, endpoint, p256dh, auth,
    expiration_time, device_id, user_agent, last_active_at, last_error, revoked_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), NULL, NULL)
ON CONFLICT (endpoint)
DO UPDATE SET
    workspace_id = EXCLUDED.workspace_id,
    user_id = EXCLUDED.user_id,
    p256dh = EXCLUDED.p256dh,
    auth = EXCLUDED.auth,
    expiration_time = EXCLUDED.expiration_time,
    device_id = EXCLUDED.device_id,
    user_agent = EXCLUDED.user_agent,
    last_active_at = now(),
    last_error = NULL,
    revoked_at = NULL,
    updated_at = now()
RETURNING *;

-- name: ListActiveWebPushSubscriptions :many
SELECT * FROM web_push_subscription
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteWebPushSubscription :execrows
DELETE FROM web_push_subscription
WHERE user_id = $1 AND endpoint = $2;

-- name: DeleteWebPushSubscriptionsByEndpoints :execrows
DELETE FROM web_push_subscription
WHERE user_id = $1 AND endpoint = ANY($2::text[]);

-- name: MarkWebPushSubscriptionsFailed :execrows
UPDATE web_push_subscription
SET last_error = $3, revoked_at = now(), updated_at = now()
WHERE user_id = $1 AND endpoint = ANY($2::text[]) AND revoked_at IS NULL;

-- name: GetWebPushChannelRecipientInfo :one
-- notify_level: NULL → default; legacy rows with muted_at but no level → mentions.
SELECT ch.name, ch.kind,
  CASE
    WHEN cm.notify_level IS NOT NULL THEN cm.notify_level
    WHEN COALESCE(vcm.muted_at, cm.muted_at) IS NOT NULL THEN 'mentions'
    ELSE 'default'
  END::text AS notify_level
FROM channel ch
JOIN channel_member cm
  ON cm.channel_id = ch.id
 AND cm.workspace_id = ch.workspace_id
 AND cm.member_type = 'user'
 AND cm.member_id = $3
JOIN conversation conv ON conv.channel_id = ch.id
LEFT JOIN conversation_member vcm
  ON vcm.conversation_id = conv.id
 AND vcm.member_type = 'user'
 AND vcm.member_id = $3
WHERE ch.workspace_id = $1 AND ch.id = $2;

-- name: ListWebPushChannelHumanMemberIDs :many
SELECT member_id::text
FROM channel_member
WHERE workspace_id = $1 AND channel_id = $2 AND member_type = 'user';
