-- Agent skill suggestions (evolution catalog matching).

-- name: DeletePendingAgentSkillSuggestions :exec
DELETE FROM agent_skill_suggestion
WHERE agent_id = $1 AND status = 'pending';

-- name: UpsertAgentSkillSuggestion :one
INSERT INTO agent_skill_suggestion (
  workspace_id, agent_id, skill_id, action, reason, matcher_score, matcher_details, status
) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending')
ON CONFLICT (agent_id, skill_id, action) DO UPDATE SET
  reason = EXCLUDED.reason,
  matcher_score = EXCLUDED.matcher_score,
  matcher_details = EXCLUDED.matcher_details,
  status = 'pending',
  decided_at = NULL,
  updated_at = now()
RETURNING *;

-- name: ListPendingAgentSkillSuggestionsByAgent :many
SELECT
  s.id, s.workspace_id, s.agent_id, s.skill_id, s.action, s.reason,
  s.matcher_score, s.matcher_details, s.status, s.decided_at, s.created_at, s.updated_at,
  sk.name AS skill_name, sk.description AS skill_description
FROM agent_skill_suggestion s
JOIN skill sk ON sk.id = s.skill_id
WHERE s.agent_id = $1 AND s.workspace_id = $2 AND s.status = 'pending'
ORDER BY s.action ASC, s.matcher_score DESC, sk.name ASC;

-- name: GetAgentSkillSuggestionInWorkspace :one
SELECT * FROM agent_skill_suggestion
WHERE id = $1 AND workspace_id = $2 AND agent_id = $3;

-- name: UpdateAgentSkillSuggestionStatus :one
UPDATE agent_skill_suggestion
SET status = $4, decided_at = now(), updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND agent_id = $3 AND status = 'pending'
RETURNING *;

-- name: ListEvolutionSkillsByWorkspace :many
SELECT
  sk.id, sk.workspace_id, sk.name, sk.description, sk.content, sk.config,
  sk.created_by, sk.created_at, sk.updated_at, sk.source_evolution_unit_id,
  u.tags, u.tools, u.task_types, u.project_types, u.languages, u.frameworks,
  u.title AS unit_title, u.canonical_summary AS unit_summary, u.content AS unit_content,
  u.metadata AS unit_metadata
FROM skill sk
JOIN shared_evolution_unit u ON u.id = sk.source_evolution_unit_id AND u.workspace_id = sk.workspace_id
WHERE sk.workspace_id = $1 AND u.status = 'active' AND u.unit_type = 'skill'
ORDER BY sk.name ASC;

-- name: GetSkillBySourceEvolutionUnit :one
SELECT * FROM skill
WHERE workspace_id = $1 AND source_evolution_unit_id = $2;

-- name: CreateEvolutionSkill :one
INSERT INTO skill (workspace_id, name, description, content, config, created_by, source_evolution_unit_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateEvolutionSkillContent :one
UPDATE skill SET
  description = $3,
  content = $4,
  updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ListAgentSkillIDsWithSource :many
SELECT skill_id, source FROM agent_skill WHERE agent_id = $1;

-- name: AddAgentSkillWithSource :exec
INSERT INTO agent_skill (agent_id, skill_id, source)
VALUES ($1, $2, $3)
ON CONFLICT (agent_id, skill_id) DO UPDATE SET source = EXCLUDED.source;

-- name: ListActiveAgentsByWorkspace :many
SELECT * FROM agent
WHERE workspace_id = $1 AND archived_at IS NULL
ORDER BY created_at ASC;
