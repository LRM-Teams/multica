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
WHERE workspace_id = $1 AND agent_id = $2;

-- name: ListActiveResearchFleetMemberAgentIDsByWorkspace :many
SELECT agent_id FROM research_fleet_member
WHERE workspace_id = $1 AND status != 'archived';

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

-- name: ListResearchSessions :many
SELECT * FROM research_session
WHERE workspace_id = $1
ORDER BY updated_at DESC;

-- name: GetResearchSession :one
SELECT * FROM research_session
WHERE id = $1 AND workspace_id = $2;

-- name: CreateResearchSession :one
INSERT INTO research_session (
  workspace_id, fleet_id, created_by, title, goal, status, current_stage,
  depth_tier, product_round, product_round_budget
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
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
  depth_tier = COALESCE(sqlc.narg('depth_tier'), depth_tier),
  product_round = COALESCE(sqlc.narg('product_round'), product_round),
  product_round_budget = COALESCE(sqlc.narg('product_round_budget'), product_round_budget),
  updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CreateResearchProductRoundCard :one
INSERT INTO research_product_round_card (
  workspace_id, session_id, round_number, decision, coverage_gaps,
  confidence_note, budget_used, budget_remaining,
  goal_patch_proposal, next_round_focus, decided_by_agent_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ListResearchProductRoundCards :many
SELECT * FROM research_product_round_card
WHERE session_id = $1 AND workspace_id = $2
ORDER BY round_number ASC;

-- name: GetResearchProductRoundCard :one
SELECT * FROM research_product_round_card
WHERE session_id = $1 AND workspace_id = $2 AND round_number = $3;

-- name: DeleteResearchSession :exec
DELETE FROM research_session
WHERE id = $1 AND workspace_id = $2;

-- name: CancelInFlightChatTasksByResearchTitle :many
-- Stop research fleet wakes: suppress active inbox tasks tied to the
-- research:<sessionUUID> chat session(s) for this workspace.
UPDATE agent_inbox_event e
SET status = 'suppressed',
    terminal_outcome = 'cancelled',
    completed_at = now(),
    terminal_at = now(),
    acked_at = now(),
    failure_reason = 'research_session_stopped'
FROM chat_session cs
WHERE e.chat_session_id = cs.id
  AND cs.workspace_id = $1
  AND cs.title = $2
  AND e.status IN ('pending', 'draining', 'failed')
RETURNING
  e.id, e.workspace_id, e.agent_session_id, e.conversation_id, e.channel_id,
  e.chat_session_id, e.agent_id, e.source_message_id, e.reason, e.requires_wake,
  e.status, e.priority, e.seq_from, e.seq_to, e.attempt, e.last_error,
  e.claimed_at, e.acked_at, e.created_at, e.updated_at, e.terminal_outcome,
  e.terminal_delivery_id, e.retryable, e.terminal_at, e.runtime_id,
  e.execution_config, e.delivery_mode, e.response_mode, e.channel_onboarding_id,
  e.issue_id, e.source_chat_message_id, e.context, e.dispatched_at, e.started_at,
  e.completed_at, e.result, e.error, e.session_id, e.work_dir,
  e.trigger_comment_id, e.autopilot_run_id, e.max_attempts, e.parent_task_id,
  e.failure_reason, e.trigger_summary, e.force_fresh_session, e.is_leader_task,
  e.wait_reason, e.initiator_user_id, e.agent_dm_exchange_id, e.agent_dm_turn;

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
  workspace_id, session_id, sender_type, sender_id, target_agent_id, body, card_kind, meta
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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
