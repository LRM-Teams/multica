-- name: CreateAgentCredential :one
INSERT INTO agent_credential (token_hash, token_prefix, agent_id, workspace_id, user_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAgentCredentialByHash :one
SELECT ac.*
FROM agent_credential AS ac
JOIN agent AS a
  ON a.id = ac.agent_id
 AND a.workspace_id = ac.workspace_id
 AND a.archived_at IS NULL
JOIN member AS m
  ON m.workspace_id = ac.workspace_id
 AND m.user_id = ac.user_id
WHERE ac.token_hash = $1
  AND ac.revoked_at IS NULL
  AND ac.disabled_at IS NULL
  AND (ac.expires_at IS NULL OR ac.expires_at > now());

-- name: GetAgentCredentialForDaemonEnsure :one
SELECT *
FROM agent_credential
WHERE id = $1
  AND agent_id = $2
  AND workspace_id = $3
  AND user_id = $4;

-- name: TouchAgentCredentialLastUsed :exec
UPDATE agent_credential
SET last_used_at = now(), updated_at = now()
WHERE id = $1;

-- name: RevokeAgentCredential :one
UPDATE agent_credential
SET revoked_at = COALESCE(revoked_at, now()), updated_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeAgentCredentialForDaemonRotation :execrows
UPDATE agent_credential
SET revoked_at = now(), updated_at = now()
WHERE id = $1
  AND agent_id = $2
  AND workspace_id = $3
  AND user_id = $4
  AND revoked_at IS NULL;

-- name: DisableAgentCredentialsByAgent :exec
UPDATE agent_credential
SET disabled_at = COALESCE(disabled_at, now()), updated_at = now()
WHERE agent_id = $1 AND revoked_at IS NULL AND disabled_at IS NULL;

-- name: RevokeAgentCredentialsByAgent :exec
UPDATE agent_credential
SET revoked_at = COALESCE(revoked_at, now()), updated_at = now()
WHERE agent_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredAgentCredentials :execrows
WITH expired AS (
  SELECT candidate.id
  FROM agent_credential AS candidate
  WHERE candidate.expires_at < $1
    AND candidate.updated_at < $1
  ORDER BY candidate.expires_at ASC
  LIMIT 500
)
DELETE FROM agent_credential AS ac
USING expired
WHERE ac.id = expired.id;
