-- name: CreateAgentCredential :one
INSERT INTO agent_credential (token_hash, token_prefix, agent_id, workspace_id, user_id, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreateDaemonAgentCredential :one
INSERT INTO agent_credential (
  token_hash,
  token_prefix,
  agent_id,
  workspace_id,
  user_id,
  expires_at,
  issuance_source
)
VALUES ($1, $2, $3, $4, $5, $6, 'daemon')
RETURNING *;

-- name: LockAgentForDaemonCredentialEnsure :one
-- A daemon may mint/hold a credential for an agent only while that agent's
-- current runtime lives on a machine the requesting owner has an active
-- binding for in this workspace (ownership is machine-level, not stored on
-- the runtime row).
SELECT agent.id
FROM agent
JOIN agent_runtime AS runtime
  ON runtime.id = agent.runtime_id
 AND runtime.workspace_id = agent.workspace_id
WHERE agent.id = sqlc.arg(agent_id)
  AND agent.workspace_id = sqlc.arg(workspace_id)
  AND runtime.id = sqlc.arg(runtime_id)
  AND EXISTS (
      SELECT 1 FROM computer_workspace_bindings b
      WHERE b.workspace_id = runtime.workspace_id
        AND b.daemon_id = runtime.daemon_id
        AND b.user_id = sqlc.arg(owner_id)
        AND b.active = TRUE
  )
  AND agent.archived_at IS NULL
  AND agent.stopped_at IS NULL
FOR UPDATE OF agent, runtime;

-- name: ClearAgentRuntimeReassignedAt :exec
-- The first successful EnsureDaemonAgentCredential call on the (new)
-- current runtime is treated as confirmation the transition finished —
-- clears the grace-window marker MarkAgentRuntimeReassigned set, so a
-- later, unrelated reassignment starts its own fresh window rather than
-- inheriting a stale timestamp. WHERE runtime_reassigned_at IS NOT NULL
-- keeps this a no-op write on the common case (no transition in flight).
UPDATE agent SET runtime_reassigned_at = NULL
WHERE id = $1 AND runtime_reassigned_at IS NOT NULL;

-- name: GetAgentCredentialByHash :one
SELECT ac.*
FROM agent_credential AS ac
JOIN agent AS a
  ON a.id = ac.agent_id
 AND a.workspace_id = ac.workspace_id
 AND a.archived_at IS NULL
 AND a.stopped_at IS NULL
LEFT JOIN agent_runtime AS runtime
  ON runtime.id = a.runtime_id
 AND runtime.workspace_id = a.workspace_id
LEFT JOIN computer_workspace_bindings AS binding
  ON binding.daemon_id = runtime.daemon_id
 AND binding.workspace_id = ac.workspace_id
 AND binding.user_id = ac.user_id
 AND binding.active = TRUE
 AND binding.revoked_at IS NULL
JOIN member AS m
  ON m.workspace_id = ac.workspace_id
 AND m.user_id = ac.user_id
WHERE ac.token_hash = $1
  AND (ac.issuance_source <> 'daemon' OR binding.daemon_id IS NOT NULL)
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

-- name: HasOtherLiveDaemonAgentCredential :one
SELECT EXISTS (
  SELECT 1
  FROM agent_credential
  WHERE agent_id = $1
    AND workspace_id = $2
    AND user_id = $3
    AND issuance_source = 'daemon'
    AND revoked_at IS NULL
    AND disabled_at IS NULL
    AND (expires_at IS NULL OR expires_at > now())
    AND id <> $4
);

-- name: HasLiveDaemonAgentCredential :one
SELECT EXISTS (
  SELECT 1
  FROM agent_credential
  WHERE agent_id = $1
    AND workspace_id = $2
    AND user_id = $3
    AND issuance_source = 'daemon'
    AND revoked_at IS NULL
    AND disabled_at IS NULL
    AND (expires_at IS NULL OR expires_at > now())
);

-- name: TouchAgentCredentialLastUsed :exec
UPDATE agent_credential
SET last_used_at = now(), updated_at = now()
WHERE id = $1;

-- name: RevokeAgentCredential :one
UPDATE agent_credential
SET revoked_at = COALESCE(revoked_at, now()), updated_at = now()
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeDaemonAgentCredentialForLaunch :execrows
UPDATE agent_credential
SET revoked_at = now(), updated_at = now()
WHERE id = $1
  AND agent_id = $2
  AND workspace_id = $3
  AND user_id = $4
  AND issuance_source = 'daemon'
  AND revoked_at IS NULL;

-- name: RevokeDaemonAgentCredentialsForSubject :execrows
UPDATE agent_credential
SET revoked_at = now(), updated_at = now()
WHERE agent_id = $1
  AND workspace_id = $2
  AND user_id = $3
  AND issuance_source = 'daemon'
  AND revoked_at IS NULL;

-- name: RevokeOtherDaemonAgentCredentialsForSubject :execrows
UPDATE agent_credential
SET revoked_at = now(), updated_at = now()
WHERE agent_id = $1
  AND workspace_id = $2
  AND user_id = $3
  AND id <> $4
  AND issuance_source = 'daemon'
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
