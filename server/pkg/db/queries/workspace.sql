-- name: ListWorkspaces :many
SELECT w.id, w.name, w.slug, w.description, w.settings,
       w.created_at, w.updated_at, w.context, w.repos,
       w.issue_prefix, w.issue_counter, w.avatar_url,
       m.last_active_at,
       w.default_self_play_env_id
FROM member m
JOIN workspace w ON w.id = m.workspace_id
WHERE m.user_id = $1
ORDER BY w.created_at ASC;

-- name: GetWorkspace :one
SELECT * FROM workspace
WHERE id = $1;

-- name: GetWorkspaceBySlug :one
SELECT * FROM workspace
WHERE slug = $1;

-- name: CreateWorkspace :one
INSERT INTO workspace (name, slug, description, context, issue_prefix)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateWorkspace :one
UPDATE workspace SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    context = COALESCE(sqlc.narg('context'), context),
    settings = COALESCE(sqlc.narg('settings'), settings),
    repos = COALESCE(sqlc.narg('repos'), repos),
    issue_prefix = COALESCE(sqlc.narg('issue_prefix'), issue_prefix),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: IncrementIssueCounter :one
UPDATE workspace SET issue_counter = issue_counter + 1
WHERE id = $1
RETURNING issue_counter;

-- name: DeleteWorkspace :exec
DELETE FROM workspace WHERE id = $1;

-- name: GetDefaultSelfPlayEnv :one
SELECT default_self_play_env_id
  FROM workspace
 WHERE id = $1;

-- name: SetDefaultSelfPlayEnv :exec
-- Conditionally sets the per-workspace default self_play base env only when it
-- is still NULL, so the first of N concurrent auto-create writers wins and the
-- rest are no-ops (the service re-reads GetDefaultSelfPlayEnv to pick up the
-- canonical winner and clean up any losing env). envID may be NULL to no-op.
UPDATE workspace
   SET default_self_play_env_id = $2,
       updated_at = now()
 WHERE id = $1
   AND default_self_play_env_id IS NULL;

-- name: GetWorkspaceOnboardingAgentID :one
SELECT onboarding_agent_id
  FROM workspace
 WHERE id = $1;

-- name: SetWorkspaceOnboardingAgentID :exec
-- Conditionally binds the per-workspace onboarding agent only when unset, so
-- the first of N concurrent ensure() callers wins and the rest are no-ops
-- (the caller re-reads GetWorkspaceOnboardingAgentID to pick up the canonical
-- winner and archives its own losing agent). Mirrors SetDefaultSelfPlayEnv.
UPDATE workspace
   SET onboarding_agent_id = $2,
       updated_at = now()
 WHERE id = $1
   AND onboarding_agent_id IS NULL;

-- name: GetFirstWorkspaceOwnerUserID :one
-- "First" by member.created_at is a pragmatic, stable tie-break for the rare
-- multi-owner case; it is not a claim that workspaces have a canonical owner
-- column (see docs/engineering-principles.md on that open question).
SELECT user_id
  FROM member
 WHERE workspace_id = $1 AND role = 'owner'
 ORDER BY created_at ASC
 LIMIT 1;
