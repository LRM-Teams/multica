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
