-- name: CreateDeviceAuthorization :one
INSERT INTO device_authorization (device_code_hash, user_code, client_hint, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetDeviceAuthorizationByUserCode :one
-- Status-agnostic on purpose: ConfirmDeviceAuthorization needs to find an
-- already-approved/denied row too (so its own idempotent no-op path can
-- run) — GetPendingDeviceAuthorization is the caller that cares about
-- status='pending' specifically, and checks it itself after this lookup.
SELECT * FROM device_authorization
WHERE user_code = $1
  AND expires_at > now();

-- name: GetDeviceAuthorizationByDeviceCodeHash :one
SELECT * FROM device_authorization
WHERE device_code_hash = $1
  AND expires_at > now();

-- name: ApproveDeviceAuthorization :one
-- Only flips status + records who approved — does NOT mint a token. The PAT
-- is minted later, at claim time (ClaimDeviceAuthorizationToken), because
-- the raw token value must never be persisted at rest (same discipline
-- every other PAT-minting path in this codebase follows: generate, hash,
-- store the hash, return the raw value exactly once). Minting here and
-- storing the raw value until the CLI's next poll would be the one place
-- that discipline breaks.
--
-- WHERE status='pending' makes a double-approve (double-click, back
-- button) a no-op: the second call matches zero rows, sqlc :one returns
-- pgx.ErrNoRows, and the handler treats that as "already resolved".
UPDATE device_authorization
SET status = 'approved',
    approved_by_user_id = $2
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
-- Called after the handler has already minted+hashed a fresh PAT (see
-- ApproveDeviceAuthorization's comment for why minting happens here, not at
-- approve time) — this statement atomically records the mint and marks the
-- row claimed. WHERE claimed_at IS NULL enforces single-claim: a replayed
-- /api/device/token call after a successful claim matches zero rows, and
-- the handler reports expired_token instead of reissuing the token (which
-- by that point isn't stored anywhere to reissue).
UPDATE device_authorization
SET claimed_at = now(),
    issued_token_id = $2
WHERE id = $1
  AND status = 'approved'
  AND claimed_at IS NULL
RETURNING *;

-- name: DeleteExpiredDeviceAuthorizations :exec
DELETE FROM device_authorization
WHERE expires_at < now() - interval '1 hour';
