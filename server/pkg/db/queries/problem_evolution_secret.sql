-- name: CreateProblemEvolutionSecret :one
INSERT INTO problem_evolution_secret (
    workspace_id, run_id, kind, label, ciphertext, nonce, wrapped_key,
    wrapped_key_nonce, key_id, content_hash, created_by
) VALUES (
    @workspace_id, @run_id, @kind, @label, @ciphertext, @nonce, @wrapped_key,
    @wrapped_key_nonce, @key_id, @content_hash, @created_by
)
RETURNING *;

-- name: GetProblemEvolutionSecret :one
SELECT * FROM problem_evolution_secret
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListProblemEvolutionSecretsByRun :many
-- Metadata only: the ciphertext columns are excluded so a listing endpoint
-- cannot become an accidental read path for sealed material.
SELECT id, workspace_id, run_id, kind, label, key_id, content_hash,
       revoked_at, created_at, updated_at
FROM problem_evolution_secret
WHERE workspace_id = @workspace_id AND run_id = @run_id
ORDER BY created_at DESC;

-- name: RevokeProblemEvolutionSecret :one
UPDATE problem_evolution_secret
SET revoked_at = now(), updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND revoked_at IS NULL
RETURNING *;

-- name: CreateProblemEvolutionSecretCapability :one
INSERT INTO problem_evolution_secret_capability (
    secret_id, run_id, workspace_id, token_hash, audience, max_uses,
    expires_at, issued_to
) VALUES (
    @secret_id, @run_id, @workspace_id, @token_hash, @audience, @max_uses,
    @expires_at, @issued_to
)
RETURNING *;

-- name: GetProblemEvolutionSecretCapabilityByTokenHash :one
-- Joined with the secret so a revoked secret denies every capability already
-- issued against it, without a second round trip.
SELECT capability.*, secret.revoked_at AS secret_revoked_at
FROM problem_evolution_secret_capability capability
JOIN problem_evolution_secret secret ON secret.id = capability.secret_id
WHERE capability.token_hash = @token_hash;

-- name: ConsumeProblemEvolutionSecretCapability :one
-- The use counter is incremented in the same statement that checks it, so two
-- concurrent redemptions cannot both pass a single-use capability.
UPDATE problem_evolution_secret_capability
SET uses = uses + 1
WHERE token_hash = @token_hash
  AND revoked_at IS NULL
  AND expires_at > now()
  AND uses < max_uses
RETURNING *;

-- name: RevokeProblemEvolutionSecretCapability :one
UPDATE problem_evolution_secret_capability
SET revoked_at = now()
WHERE id = @id AND workspace_id = @workspace_id AND revoked_at IS NULL
RETURNING *;

-- name: RevokeProblemEvolutionSecretCapabilitiesForRun :execrows
UPDATE problem_evolution_secret_capability
SET revoked_at = now()
WHERE run_id = @run_id AND revoked_at IS NULL;

-- name: ListProblemEvolutionSecretCapabilities :many
SELECT * FROM problem_evolution_secret_capability
WHERE run_id = @run_id
ORDER BY created_at DESC
LIMIT @result_limit;

-- name: InsertProblemEvolutionSecretAudit :one
INSERT INTO problem_evolution_secret_audit (
    workspace_id, secret_id, capability_id, run_id, action, reason,
    actor_type, actor_id
) VALUES (
    @workspace_id, @secret_id, @capability_id, @run_id, @action, @reason,
    @actor_type, @actor_id
)
RETURNING *;

-- name: ListProblemEvolutionSecretAudit :many
SELECT * FROM problem_evolution_secret_audit
WHERE run_id = @run_id
ORDER BY created_at DESC
LIMIT @result_limit;

-- name: CountProblemEvolutionSecretDenials :one
SELECT COUNT(*)::bigint FROM problem_evolution_secret_audit
WHERE run_id = @run_id AND action = 'denied';
