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
  unattended_enabled = COALESCE(sqlc.narg('unattended_enabled'), unattended_enabled),
  max_open_branches = COALESCE(sqlc.narg('max_open_branches'), max_open_branches),
  single_line_confirmed = COALESCE(sqlc.narg('single_line_confirmed'), single_line_confirmed),
  unattended_auto_steps = COALESCE(sqlc.narg('unattended_auto_steps'), unattended_auto_steps),
  last_user_activity_at = COALESCE(sqlc.narg('last_user_activity_at'), last_user_activity_at),
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

-- name: GetResearchMessage :one
SELECT * FROM research_message
WHERE id = $1 AND session_id = $2 AND workspace_id = $3;

-- name: SetResearchMessageMatchDecision :one
-- LRM-1330: persist utterance-scoped match_decision under meta.match_decision.
UPDATE research_message
SET meta = jsonb_set(COALESCE(meta, '{}'::jsonb), '{match_decision}', $4::jsonb, true)
WHERE id = $1 AND session_id = $2 AND workspace_id = $3
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

-- name: ListRunningUnattendedResearchSessions :many
-- LRM-1076: scanner input — running sessions with unattended default on.
SELECT * FROM research_session
WHERE status = 'running'
  AND unattended_enabled = true
ORDER BY updated_at ASC
LIMIT $1;

-- name: IncrementResearchUnattendedAutoSteps :one
UPDATE research_session
SET unattended_auto_steps = unattended_auto_steps + $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: TouchResearchSessionUserActivity :one
UPDATE research_session
SET last_user_activity_at = now(),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: CreateResearchWorkItem :one
INSERT INTO research_work_item (
  workspace_id, session_id, kind, target_node_id, assignee_agent_id, status, reason, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateResearchWorkItemStatus :one
UPDATE research_work_item
SET status = $3,
    enqueued_at = CASE WHEN $3 = 'enqueued' THEN COALESCE(enqueued_at, now()) ELSE enqueued_at END,
    completed_at = CASE WHEN $3 IN ('done', 'cancelled', 'failed') THEN COALESCE(completed_at, now()) ELSE completed_at END,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ListOpenResearchWorkItems :many
SELECT * FROM research_work_item
WHERE session_id = $1
  AND workspace_id = $2
  AND status IN ('pending', 'enqueued')
ORDER BY created_at ASC;

-- name: CountOpenResearchWorkItems :one
SELECT COUNT(*)::int AS count
FROM research_work_item
WHERE session_id = $1
  AND workspace_id = $2
  AND status IN ('pending', 'enqueued');

-- name: CreateResearchSchedulerEvent :one
INSERT INTO research_scheduler_event (
  workspace_id, session_id, event_type, detail
) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CountResearchOpenBranches :one
-- Open branch ≈ active exploration leaves: active subquestion/probe without
-- a child edge of type leads_to to another active subquestion/probe/finding.
SELECT COUNT(*)::int AS count
FROM research_graph_node n
WHERE n.session_id = $1
  AND n.workspace_id = $2
  AND n.status = 'active'
  AND n.node_type IN ('subquestion', 'probe')
  AND NOT EXISTS (
    SELECT 1
    FROM research_graph_edge e
    JOIN research_graph_node c ON c.id = e.to_node_id
    WHERE e.session_id = n.session_id
      AND e.from_node_id = n.id
      AND e.edge_type = 'leads_to'
      AND c.status = 'active'
      AND c.node_type IN ('subquestion', 'probe', 'finding', 'conflict')
  );

-- name: ListResearchGraphClusters :many
SELECT * FROM research_graph_cluster
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: GetResearchGraphCluster :one
SELECT * FROM research_graph_cluster
WHERE id = $1 AND workspace_id = $2;

-- name: CreateResearchGraphNodeTyped :one
INSERT INTO research_graph_node (
  workspace_id, session_id, node_type, title, summary, status, actor_agent_id, level,
  round, cluster_id, confidence, document_count, conclusion_count, goal_version_id,
  derived_from, merged_from, superseded_by, restart_of, invalidated_by, payload
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
RETURNING *;

-- name: UpdateResearchGraphNodeTyped :one
UPDATE research_graph_node SET
  status = COALESCE(sqlc.narg('status'), status),
  level = COALESCE(sqlc.narg('level'), level),
  cluster_id = COALESCE(sqlc.narg('cluster_id'), cluster_id),
  confidence = COALESCE(sqlc.narg('confidence'), confidence),
  title = COALESCE(sqlc.narg('title'), title),
  summary = COALESCE(sqlc.narg('summary'), summary),
  superseded_by = COALESCE(sqlc.narg('superseded_by'), superseded_by),
  superseded_at = COALESCE(sqlc.narg('superseded_at'), superseded_at),
  merged_from = COALESCE(sqlc.narg('merged_from'), merged_from),
  restart_of = COALESCE(sqlc.narg('restart_of'), restart_of),
  invalidated_by = COALESCE(sqlc.narg('invalidated_by'), invalidated_by),
  updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ResearchGraphCommandExists :one
SELECT EXISTS(
  SELECT 1 FROM research_graph_command
  WHERE workspace_id = $1 AND session_id = $2 AND idempotency_key = $3
) AS exists;

-- name: GetResearchGraphCommandByKey :one
SELECT * FROM research_graph_command
WHERE workspace_id = $1 AND session_id = $2 AND idempotency_key = $3;

-- name: CreateResearchGraphCommand :one
INSERT INTO research_graph_command (
  workspace_id, session_id, op, idempotency_key, result_node_id,
  input_node_ids, reason, actor_type, actor_id, meta
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListResearchGraphCommands :many
SELECT * FROM research_graph_command
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at DESC;

-- name: BumpResearchGraphVersion :one
UPDATE research_session SET graph_version = graph_version + 1
WHERE id = $1 AND workspace_id = $2
RETURNING graph_version;

-- name: GetResearchSessionGraphVersion :one
SELECT graph_version FROM research_session
WHERE id = $1 AND workspace_id = $2;

-- name: CountResearchGraphNodes :one
SELECT COUNT(*)::bigint AS count
FROM research_graph_node
WHERE session_id = $1 AND workspace_id = $2;

-- name: ListResearchGraphNodesTyped :many
SELECT id, workspace_id, session_id, node_type, title, summary, status, actor_agent_id,
       payload, created_at, updated_at, run_event_id, level, round, cluster_id, confidence,
       document_count, conclusion_count, goal_version_id, derived_from, merged_from,
       superseded_by, restart_of, invalidated_by, superseded_at, invalidated_at
FROM research_graph_node
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at ASC;

-- name: ListResearchGraphNodesTypedPaginated :many
SELECT id, workspace_id, session_id, node_type, title, summary, status, actor_agent_id,
       payload, created_at, updated_at, run_event_id, level, round, cluster_id, confidence,
       document_count, conclusion_count, goal_version_id, derived_from, merged_from,
       superseded_by, restart_of, invalidated_by, superseded_at, invalidated_at
FROM research_graph_node
WHERE session_id = $1 AND workspace_id = $2
ORDER BY created_at ASC
LIMIT $3 OFFSET $4;
