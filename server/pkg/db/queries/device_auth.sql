-- name: CreateDeviceAuthorization :one
INSERT INTO device_authorization (device_code_hash, user_code, client_hint, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetDeviceAuthorizationByUserCode :one
SELECT * FROM device_authorization
WHERE user_code = $1
  AND status = 'pending'
  AND expires_at > now();

-- name: GetDeviceAuthorizationByDeviceCodeHash :one
SELECT * FROM device_authorization
WHERE device_code_hash = $1
  AND expires_at > now();

-- name: ApproveDeviceAuthorization :one
-- WHERE status='pending' makes a double-approve (double-click, back
-- button) a no-op: the second call matches zero rows, sqlc :one returns
-- pgx.ErrNoRows, and the handler treats that as "already resolved" rather
-- than re-minting a second PAT for the same device_code.
UPDATE device_authorization
SET status = 'approved',
    approved_by_user_id = $2,
    issued_token_id = $3
WHERE id = $1
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: DenyDeviceAuthorization :one
UPDATE device_authorization
SET status = 'denied'
WHERE id = $1
  AND status = 'pending'
  AND expires_at > now()
RETURNING *;

-- name: MarkDeviceAuthorizationPolled :one
-- Returns the row as it was *before* this poll (old last_polled_at) so the
-- handler can compute whether this poll arrived within the slow_down
-- window, then stamps the new poll time in the same statement.
WITH previous AS (
    SELECT da.last_polled_at FROM device_authorization da WHERE da.id = $1
)
UPDATE device_authorization
SET last_polled_at = now()
WHERE device_authorization.id = $1
RETURNING device_authorization.*, (SELECT previous.last_polled_at FROM previous) AS previous_polled_at;

-- name: ClaimDeviceAuthorizationToken :one
-- WHERE claimed_at IS NULL enforces single-claim: a replayed /api/device/token
-- call after a successful claim matches zero rows, and the handler reports
-- expired_token instead of reissuing the already-delivered PAT.
UPDATE device_authorization
SET claimed_at = now()
WHERE id = $1
  AND status = 'approved'
  AND claimed_at IS NULL
RETURNING *;

-- name: DeleteExpiredDeviceAuthorizations :exec
DELETE FROM device_authorization
WHERE expires_at < now() - interval '1 hour';
