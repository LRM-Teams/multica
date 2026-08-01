-- Skill CRUD

-- name: ListSkillsByWorkspace :many
SELECT * FROM skill
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: ListSkillSummariesByWorkspace :many
-- Same as ListSkillsByWorkspace but omits the SKILL.md `content` column. Used
-- by list endpoints (CLI table, web list page) where the body is never read;
-- shipping it everywhere blew up payload size on workspaces with many skills
-- and caused 15s CLI timeouts from high-latency regions (GH multica-ai/multica#2174).
SELECT id, workspace_id, name, description, config, created_by, created_at, updated_at,
       grant_level, channel_id
FROM skill
WHERE workspace_id = $1
ORDER BY name ASC;

-- name: GetSkill :one
SELECT * FROM skill
WHERE id = $1;

-- name: GetSkillInWorkspace :one
SELECT * FROM skill
WHERE id = $1 AND workspace_id = $2;

-- name: GetSkillByWorkspaceAndName :one
-- Used by agent-template materialization to implement find-or-create: when a
-- template references a skill by name that already exists in the workspace,
-- reuse the existing skill_id rather than INSERT (which would fail the
-- UNIQUE(workspace_id, name) constraint from migration 008).
SELECT * FROM skill
WHERE workspace_id = $1 AND name = $2;

-- name: CreateSkill :one
INSERT INTO skill (workspace_id, name, description, content, config, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateSkill :one
UPDATE skill SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    content = COALESCE(sqlc.narg('content'), content),
    config = COALESCE(sqlc.narg('config'), config),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteSkill :exec
-- Defense-in-depth: workspace_id is a SQL-layer tenant guard. See DeleteIssue.
DELETE FROM skill WHERE id = $1 AND workspace_id = $2;

-- Skill File CRUD

-- name: ListSkillFiles :many
SELECT * FROM skill_file
WHERE skill_id = $1
ORDER BY path ASC;

-- name: GetSkillFile :one
SELECT * FROM skill_file
WHERE id = $1;

-- name: UpsertSkillFile :one
INSERT INTO skill_file (skill_id, path, content)
VALUES ($1, $2, $3)
ON CONFLICT (skill_id, path) DO UPDATE SET
    content = EXCLUDED.content,
    updated_at = now()
RETURNING *;

-- name: DeleteSkillFile :exec
DELETE FROM skill_file WHERE id = $1;

-- name: DeleteSkillFilesBySkill :exec
DELETE FROM skill_file WHERE skill_id = $1;

-- Agent-Skill junction

-- name: ListAgentSkills :many
SELECT s.id, s.workspace_id, s.name, s.description, s.content, s.config,
       s.created_by, s.created_at, s.updated_at, s.source_evolution_unit_id,
       u.current_version_id AS source_evolution_unit_version_id,
       s.grant_level, s.channel_id
FROM skill s
JOIN agent_skill ask ON ask.skill_id = s.id
LEFT JOIN shared_evolution_unit u
  ON u.id = s.source_evolution_unit_id AND u.workspace_id = s.workspace_id AND u.unit_type = 'skill'
WHERE ask.agent_id = $1
ORDER BY s.name ASC;

-- name: ListAgentSkillSummaries :many
-- Summary variant for the agent skills list endpoint — omits `content` for
-- the same reason as ListSkillSummariesByWorkspace.
SELECT s.id, s.workspace_id, s.name, s.description, s.config, s.created_by, s.created_at, s.updated_at,
       s.grant_level, s.channel_id
FROM skill s
JOIN agent_skill ask ON ask.skill_id = s.id
WHERE ask.agent_id = $1
ORDER BY s.name ASC;

-- name: AddAgentSkill :exec
INSERT INTO agent_skill (agent_id, skill_id, source)
VALUES ($1, $2, 'manual')
ON CONFLICT (agent_id, skill_id) DO NOTHING;

-- name: RemoveAgentSkill :exec
DELETE FROM agent_skill
WHERE agent_id = $1 AND skill_id = $2;

-- name: RemoveAllAgentSkills :exec
DELETE FROM agent_skill WHERE agent_id = $1;

-- name: ListAgentSkillsByWorkspace :many
SELECT ask.agent_id, s.id, s.name, s.description
FROM agent_skill ask
JOIN skill s ON s.id = ask.skill_id
WHERE s.workspace_id = $1
ORDER BY s.name ASC;

-- Agent-bound shared skill CRUD

-- name: ListAgentSharedSkillsByAgent :many
SELECT * FROM agent_shared_skill
WHERE agent_id = $1
ORDER BY name ASC;

-- name: GetAgentSharedSkillByAgentAndSyncKey :one
SELECT * FROM agent_shared_skill
WHERE agent_id = $1 AND sync_key = $2;

-- name: GetAgentSharedSkillByAgentAndName :one
SELECT * FROM agent_shared_skill
WHERE agent_id = $1 AND name = $2;

-- name: CreateAgentSharedSkill :one
INSERT INTO agent_shared_skill (
    workspace_id, agent_id, name, description, content, config, sync_key, content_hash, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateAgentSharedSkill :one
UPDATE agent_shared_skill SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    content = COALESCE(sqlc.narg('content'), content),
    config = COALESCE(sqlc.narg('config'), config),
    content_hash = COALESCE(sqlc.narg('content_hash'), content_hash),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAgentSharedSkill :exec
DELETE FROM agent_shared_skill WHERE id = $1 AND agent_id = $2;

-- name: DeleteAgentSharedSkillFilesBySkill :exec
DELETE FROM agent_shared_skill_file WHERE agent_shared_skill_id = $1;

-- name: UpsertAgentSharedSkillFile :one
INSERT INTO agent_shared_skill_file (agent_shared_skill_id, agent_id, path, content)
VALUES ($1, $2, $3, $4)
ON CONFLICT (agent_shared_skill_id, path) DO UPDATE SET
    content = EXCLUDED.content,
    updated_at = now()
RETURNING *;

-- Agent memory CRUD

-- name: ListAgentMemoriesByAgent :many
SELECT * FROM agent_memory
WHERE agent_id = $1
ORDER BY updated_at DESC, id
LIMIT 48;

-- name: GetAgentMemoryByAgentAndSyncKey :one
SELECT * FROM agent_memory
WHERE agent_id = $1 AND sync_key = $2;

-- name: GetAgentMemoryByAgentAndName :one
SELECT * FROM agent_memory
WHERE agent_id = $1 AND name = $2;

-- name: CreateAgentMemory :one
INSERT INTO agent_memory (
    workspace_id, agent_id, name, content, config, sync_key, content_hash, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateAgentMemory :one
UPDATE agent_memory SET
    name = COALESCE(sqlc.narg('name'), name),
    content = COALESCE(sqlc.narg('content'), content),
    config = COALESCE(sqlc.narg('config'), config),
    content_hash = COALESCE(sqlc.narg('content_hash'), content_hash),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAgentMemory :exec
DELETE FROM agent_memory WHERE id = $1 AND agent_id = $2;

-- Skill grant promotion (LRM-961 / LRM-954)

-- name: UpdateSkillGrantLevel :one
UPDATE skill SET
    grant_level = $2,
    channel_id = $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $4
RETURNING *;

-- name: CreateSkillPromotion :one
INSERT INTO skill_promotion (
    skill_id, workspace_id, from_level, to_level, channel_id, actor_type, actor_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListSkillPromotionsBySkill :many
SELECT
    p.id,
    p.skill_id,
    p.workspace_id,
    p.from_level,
    p.to_level,
    p.channel_id,
    p.actor_type,
    p.actor_id,
    p.created_at,
    CASE
        WHEN p.actor_type = 'member' THEN COALESCE(u.display_name, u.name, '')
        WHEN p.actor_type = 'agent' THEN COALESCE(a.display_name, a.name, '')
        ELSE ''
    END AS actor_display_name
FROM skill_promotion p
LEFT JOIN "user" u ON p.actor_type = 'member' AND u.id = p.actor_id
LEFT JOIN agent a ON p.actor_type = 'agent' AND a.id = p.actor_id
WHERE p.skill_id = $1 AND p.workspace_id = $2
ORDER BY p.created_at DESC, p.id DESC;

-- name: ActorCanPromoteSkillToChannel :one
-- Channel owner (human) or channel manager (human/agent) may grant L2.
SELECT EXISTS (
    SELECT 1
    FROM channel_member cm
    JOIN channel c ON c.id = cm.channel_id AND c.workspace_id = cm.workspace_id
    WHERE cm.workspace_id = $1
      AND cm.channel_id = $2
      AND cm.member_type = $3
      AND cm.member_id = $4
      AND cm.role IN ('owner', 'manager')
      AND c.kind = 'group'
      AND c.archived_at IS NULL
) AS allowed;

-- name: ActorCanPromoteSkillToAnyChannel :one
-- Capability gate for the promote-to-channel button (no target channel yet).
SELECT EXISTS (
    SELECT 1
    FROM channel_member cm
    JOIN channel c ON c.id = cm.channel_id AND c.workspace_id = cm.workspace_id
    WHERE cm.workspace_id = $1
      AND cm.member_type = $2
      AND cm.member_id = $3
      AND cm.role IN ('owner', 'manager')
      AND c.kind = 'group'
      AND c.archived_at IS NULL
) AS allowed;
