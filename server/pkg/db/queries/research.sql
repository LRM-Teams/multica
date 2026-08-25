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

-- name: ListResearchSessionProgress :many
WITH task_progress AS (
  SELECT
    t.session_id,
    count(*) FILTER (WHERE t.status <> 'obsolete') AS task_total,
    count(*) FILTER (WHERE t.status = 'succeeded') AS task_completed,
    count(*) FILTER (WHERE t.status IN ('dispatching', 'running')) AS task_running,
    count(*) FILTER (WHERE t.status IN ('blocked', 'failed')) AS task_blocked
  FROM research_task t
  JOIN research_session s ON s.id = t.session_id
  WHERE t.workspace_id = $1
    AND t.goal_version = s.goal_version
    AND t.plan_version = s.plan_version
    AND s.orchestrator_version <> 'research-run-v6'
  GROUP BY t.session_id
), work_item_progress AS (
  SELECT
    w.session_id,
    count(*) FILTER (WHERE w.status <> 'cancelled') AS task_total,
    count(*) FILTER (WHERE w.status = 'succeeded') AS task_completed,
    count(*) FILTER (WHERE w.status IN ('dispatching', 'running', 'ready', 'enqueued', 'pending')) AS task_running,
    count(*) FILTER (WHERE w.status IN ('failed', 'stale')) AS task_blocked
  FROM research_work_item w
  JOIN research_session s ON s.id = w.session_id
  WHERE w.workspace_id = $1
    AND s.orchestrator_version = 'research-run-v6'
    AND w.goal_version = s.goal_version
  GROUP BY w.session_id
), evidence_progress AS (
  SELECT o.session_id, count(*) AS evidence_count,
    count(*) FILTER (WHERE o.created_at >= now() - interval '24 hours') AS today_evidence_count
  FROM research_observation o
  JOIN research_task t ON t.id = o.produced_by_task_id
  JOIN research_session s ON s.id = o.session_id
  WHERE o.workspace_id = $1
    AND o.verification_status <> 'rejected'
    AND t.goal_version = s.goal_version
    AND t.plan_version = s.plan_version
    AND s.orchestrator_version <> 'research-run-v6'
  GROUP BY o.session_id
), v6_evidence_progress AS (
  -- V6 runs never write research_observation; accepted atomic results live in
  -- research_result_node, so evidence counters must read from there instead.
  SELECT n.session_id, count(*) AS evidence_count,
    count(*) FILTER (WHERE n.created_at >= now() - interval '24 hours') AS today_evidence_count
  FROM research_result_node n
  JOIN research_work_item_attempt a ON a.id = n.work_item_attempt_id
  JOIN research_work_item w ON w.id = a.work_item_id
  JOIN research_session s ON s.id = n.session_id
  WHERE n.workspace_id = $1
    AND s.orchestrator_version = 'research-run-v6'
    AND w.goal_version = s.goal_version
    AND n.conclusion_state NOT IN ('refuted', 'invalid')
  GROUP BY n.session_id
), node_progress AS (
  SELECT session_id, count(*) AS node_count
  FROM research_graph_node
  WHERE workspace_id = $1
  GROUP BY session_id
), question_progress AS (
  SELECT q.session_id, count(*) AS open_question_count
  FROM research_question q
  JOIN research_session s ON s.id = q.session_id
  WHERE q.workspace_id = $1
    AND q.goal_version = s.goal_version
    AND q.plan_version = s.plan_version
    AND q.status IN ('open', 'in_progress', 'unresolved')
  GROUP BY q.session_id
)
SELECT
  s.id AS session_id,
  COALESCE(wp.task_total, tp.task_total, 0)::bigint AS task_total,
  COALESCE(wp.task_completed, tp.task_completed, 0)::bigint AS task_completed,
  COALESCE(wp.task_running, tp.task_running, 0)::bigint AS task_running,
  COALESCE(wp.task_blocked, tp.task_blocked, 0)::bigint AS task_blocked,
  COALESCE(vep.evidence_count, ep.evidence_count, 0)::bigint AS evidence_count,
  COALESCE(vep.today_evidence_count, ep.today_evidence_count, 0)::bigint AS today_evidence_count,
  COALESCE(np.node_count, 0)::bigint AS node_count,
  COALESCE(qp.open_question_count, 0)::bigint AS open_question_count,
  (s.status = 'awaiting_user_confirm') AS awaiting_user_action,
  COALESCE((CASE
    WHEN s.status = 'awaiting_user_confirm' THEN 'user_confirmation'
    WHEN COALESCE(wp.task_blocked, tp.task_blocked, 0) > 0 THEN 'blocked_tasks'
    WHEN s.status = 'failed' AND length(s.last_error) > 0 THEN 'recoverable_failure'
    WHEN s.status = 'running' AND COALESCE(wp.task_running, tp.task_running, 0) = 0 AND s.last_progress_at < now() - interval '15 minutes' THEN 'stalled'
    ELSE NULL
  END)::text, '')::text AS attention_kind,
  COALESCE((s.status = 'failed' AND length(s.last_error) > 0) OR COALESCE(wp.task_blocked, tp.task_blocked, 0) > 0, false)::boolean AS recoverable,
  s.last_progress_at
FROM research_session s
LEFT JOIN task_progress tp ON tp.session_id = s.id
LEFT JOIN work_item_progress wp ON wp.session_id = s.id
LEFT JOIN evidence_progress ep ON ep.session_id = s.id
LEFT JOIN v6_evidence_progress vep ON vep.session_id = s.id
LEFT JOIN node_progress np ON np.session_id = s.id
LEFT JOIN question_progress qp ON qp.session_id = s.id
WHERE s.workspace_id = $1
ORDER BY s.updated_at DESC;

-- name: ListResearchActiveAssignments :many
WITH ranked AS (
  SELECT
    t.session_id,
    t.assigned_agent_id AS agent_id,
    COALESCE(fm.role, '')::text AS role,
    t.id AS task_id,
    t.objective AS task_title,
    t.status AS state,
    row_number() OVER (PARTITION BY t.session_id ORDER BY t.priority DESC, t.started_at DESC NULLS LAST, t.created_at DESC) AS position
  FROM research_task t
  JOIN research_session s ON s.id = t.session_id
  LEFT JOIN research_fleet_member fm ON fm.fleet_id = s.fleet_id AND fm.agent_id = t.assigned_agent_id
  WHERE t.workspace_id = $1
    AND t.goal_version = s.goal_version
    AND t.plan_version = s.plan_version
    AND t.status IN ('dispatching', 'running')
    AND t.assigned_agent_id IS NOT NULL
    AND s.orchestrator_version <> 'research-run-v6'
  UNION ALL
  SELECT
    w.session_id,
    w.assigned_agent_id AS agent_id,
    'member'::text AS role,
    w.id AS task_id,
    COALESCE(NULLIF(w.reason, ''), w.kind)::text AS task_title,
    w.status AS state,
    row_number() OVER (PARTITION BY w.session_id ORDER BY w.priority DESC, w.started_at DESC NULLS LAST, w.created_at DESC) AS position
  FROM research_work_item w
  JOIN research_session s ON s.id = w.session_id
  WHERE w.workspace_id = $1
    AND s.orchestrator_version = 'research-run-v6'
    AND w.goal_version = s.goal_version
    AND w.status IN ('dispatching', 'running')
    AND w.assigned_agent_id IS NOT NULL
)
SELECT session_id, agent_id, role, task_id, task_title, state
FROM ranked
WHERE position <= 4
ORDER BY session_id, position;

-- name: ListResearchLatestOutcomes :many
WITH outcomes AS (
  SELECT c.session_id, c.id, 0 AS outcome_priority, 'claim'::text AS kind, c.claim_text AS title,
    NULLIF(c.resolution, '')::text AS summary, c.status AS verification_state, c.created_at
  FROM research_claim c
  JOIN research_session s ON s.id = c.session_id
  WHERE c.workspace_id = $1 AND c.goal_version = s.goal_version AND c.plan_version = s.plan_version AND c.status = 'supported'
  UNION ALL
  SELECT o.session_id, o.id, 1, 'observation'::text, COALESCE(NULLIF(o.interpretation, ''), NULLIF(o.quote, ''), 'Verified observation'),
    NULLIF(o.quote, '')::text, o.verification_status, o.created_at
  FROM research_observation o
  JOIN research_task ot ON ot.id = o.produced_by_task_id
  JOIN research_session os ON os.id = o.session_id
  WHERE o.workspace_id = $1 AND o.verification_status = 'verified'
    AND ot.goal_version = os.goal_version AND ot.plan_version = os.plan_version
    AND os.orchestrator_version <> 'research-run-v6'
  UNION ALL
  SELECT t.session_id, t.id, 2, 'task'::text, t.objective, NULLIF(t.terminal_reason, '')::text, t.status, t.completed_at
  FROM research_task t
  JOIN research_session s ON s.id = t.session_id
  WHERE t.workspace_id = $1 AND t.goal_version = s.goal_version AND t.plan_version = s.plan_version AND t.status = 'succeeded' AND t.completed_at IS NOT NULL
  UNION ALL
  SELECT n.session_id, n.id, 0, 'result'::text, n.catalog_summary,
    COALESCE(NULLIF(n.brief_summary, ''), NULLIF(n.conclusion, ''))::text,
    n.conclusion_state, n.created_at
  FROM research_result_node n
  JOIN research_work_item_attempt a ON a.id = n.work_item_attempt_id
  JOIN research_work_item w ON w.id = a.work_item_id
  JOIN research_session s ON s.id = n.session_id
  WHERE n.workspace_id = $1
    AND s.orchestrator_version = 'research-run-v6'
    AND w.goal_version = s.goal_version
    AND n.conclusion_state NOT IN ('refuted', 'invalid')
), ranked AS (
  SELECT outcomes.*, row_number() OVER (PARTITION BY session_id ORDER BY outcome_priority, created_at DESC, id) AS position
  FROM outcomes
)
SELECT session_id, id, kind, title, COALESCE(summary, '')::text AS summary, verification_state, created_at
FROM ranked
WHERE position <= 3
ORDER BY session_id, position;

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
  e.wait_reason, e.initiator_user_id, e.agent_dm_exchange_id, e.agent_dm_turn,
  e.issue_run_kind, e.issue_execution_revision, e.issue_execution_attempt_number;

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
SELECT rs.*
FROM research_session AS rs
JOIN workspace AS w ON w.id = rs.workspace_id
WHERE rs.status = 'running'
  AND rs.unattended_enabled = true
ORDER BY rs.updated_at ASC
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
