-- name: CreateAgentCredential :one
INSERT INTO agent_credential (token_hash, token_prefix, agent_id, workspace_id, user_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAgentCredentialByHash :one
SELECT * FROM agent_credential
WHERE token_hash = $1
  AND revoked_at IS NULL
  AND disabled_at IS NULL
  AND (expires_at IS NULL OR expires_at > now());

-- name: TouchAgentCredentialLastUsed :exec
UPDATE agent_credential
SET last_used_at = now(), updated_at = now()
WHERE id = $1;

-- name: RevokeAgentCredential :one
UPDATE agent_credential
SET revoked_at = COALESCE(revoked_at, now()), updated_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: DisableAgentCredentialsByAgent :exec
UPDATE agent_credential
SET disabled_at = COALESCE(disabled_at, now()), updated_at = now()
WHERE agent_id = $1 AND revoked_at IS NULL AND disabled_at IS NULL;

-- name: RevokeAgentCredentialsByAgent :exec
UPDATE agent_credential
SET revoked_at = COALESCE(revoked_at, now()), updated_at = now()
WHERE agent_id = $1 AND revoked_at IS NULL;
