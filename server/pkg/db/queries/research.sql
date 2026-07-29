-- name: GetResearchFleetByWorkspace :one
SELECT * FROM research_fleet
WHERE workspace_id = $1;

-- name: CreateResearchFleet :one
INSERT INTO research_fleet (workspace_id, lead_agent_id)
VALUES ($1, $2)
RETURNING *;

-- name: SetResearchFleetLead :one
UPDATE research_fleet
SET lead_agent_id = $2, updated_at = now()
WHERE id = $1 AND workspace_id = $3
RETURNING *;

-- name: ListResearchFleetMembers :many
SELECT * FROM research_fleet_member
WHERE fleet_id = $1 AND workspace_id = $2
ORDER BY is_lead DESC, created_at ASC;

-- name: GetResearchFleetMemberByAgent :one
SELECT * FROM research_fleet_member
WHERE workspace_id = $1 AND agent_id = $2
  AND status <> 'archived';

-- name: CreateResearchFleetMember :one
INSERT INTO research_fleet_member (
  workspace_id, fleet_id, agent_id, role, status, is_lead
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateResearchFleetMemberStatus :one
UPDATE research_fleet_member
SET status = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ArchiveResearchFleetMember :one
UPDATE research_fleet_member
SET status = 'archived', updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: SetAgentManagedRoleResearchFleet :exec
UPDATE agent
SET managed_role = 'research_fleet', updated_at = now()
WHERE id = $1 AND workspace_id = $2;

-- name: ListResearchSessions :many
SELECT * FROM research_session
WHERE workspace_id = $1
ORDER BY updated_at DESC;

-- name: GetResearchSession :one
SELECT * FROM research_session
WHERE id = $1 AND workspace_id = $2;

-- name: CreateResearchSession :one
INSERT INTO research_session (
  workspace_id, fleet_id, created_by, title, goal, status, current_stage
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateResearchSession :one
UPDATE research_session SET
  title = COALESCE(sqlc.narg('title'), title),
  goal = COALESCE(sqlc.narg('goal'), goal),
  status = COALESCE(sqlc.narg('status'), status),
  current_stage = COALESCE(sqlc.narg('current_stage'), current_stage),
  project_id = COALESCE(sqlc.narg('project_id'), project_id),
  channel_id = COALESCE(sqlc.narg('channel_id'), channel_id),
  handoff_summary = COALESCE(sqlc.narg('handoff_summary'), handoff_summary),
  updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ListResearchGraphNodes :many
SELECT * FROM research_graph_node
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: GetResearchGraphNode :one
SELECT * FROM research_graph_node
WHERE id = $1 AND workspace_id = $2;

-- name: CreateResearchGraphNode :one
INSERT INTO research_graph_node (
  workspace_id, session_id, node_type, title, summary, status, actor_agent_id, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateResearchGraphNode :one
UPDATE research_graph_node SET
  title = COALESCE(sqlc.narg('title'), title),
  summary = COALESCE(sqlc.narg('summary'), summary),
  status = COALESCE(sqlc.narg('status'), status),
  payload = COALESCE(sqlc.narg('payload'), payload),
  updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ListResearchGraphEdges :many
SELECT * FROM research_graph_edge
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: CreateResearchGraphEdge :one
INSERT INTO research_graph_edge (
  workspace_id, session_id, from_node_id, to_node_id, edge_type
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListResearchSources :many
SELECT * FROM research_source
WHERE session_id = $1 AND workspace_id = $2
ORDER BY credibility_weight DESC, created_at ASC;

-- name: UpsertResearchSource :one
INSERT INTO research_source (
  workspace_id, session_id, url, title, source_class, credibility_weight,
  stance, relevance, summary, excerpt, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: UpdateResearchSource :one
UPDATE research_source SET
  url = COALESCE(sqlc.narg('url'), url),
  title = COALESCE(sqlc.narg('title'), title),
  source_class = COALESCE(sqlc.narg('source_class'), source_class),
  credibility_weight = COALESCE(sqlc.narg('credibility_weight'), credibility_weight),
  stance = COALESCE(sqlc.narg('stance'), stance),
  relevance = COALESCE(sqlc.narg('relevance'), relevance),
  summary = COALESCE(sqlc.narg('summary'), summary),
  excerpt = COALESCE(sqlc.narg('excerpt'), excerpt),
  payload = COALESCE(sqlc.narg('payload'), payload),
  updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: GetLatestResearchReport :one
SELECT * FROM research_report
WHERE session_id = $1 AND workspace_id = $2
ORDER BY revision DESC
LIMIT 1;

-- name: ListResearchReports :many
SELECT * FROM research_report
WHERE session_id = $1 AND workspace_id = $2
ORDER BY revision DESC;

-- name: CreateResearchReport :one
INSERT INTO research_report (
  workspace_id, session_id, revision, content_md, structured
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListResearchStageEvals :many
SELECT * FROM research_stage_eval
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at DESC;

-- name: CreateResearchStageEval :one
INSERT INTO research_stage_eval (
  workspace_id, session_id, stage, passed, score, findings, remediation
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListResearchMessages :many
SELECT * FROM research_message
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: CreateResearchMessage :one
INSERT INTO research_message (
  workspace_id, session_id, sender_type, sender_id, target_agent_id, body
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetLatestResearchPlaybook :one
SELECT * FROM research_fleet_playbook
WHERE fleet_id = $1 AND workspace_id = $2 AND domain = $3
ORDER BY version DESC
LIMIT 1;

-- name: CreateResearchPlaybook :one
INSERT INTO research_fleet_playbook (
  workspace_id, fleet_id, domain, version, content_md, research_fleet_only
) VALUES ($1, $2, $3, $4, $5, true)
RETURNING *;

-- name: CreateResearchFleetFeedback :one
INSERT INTO research_fleet_feedback (
  workspace_id, fleet_id, session_id, stage, score, notes, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;
