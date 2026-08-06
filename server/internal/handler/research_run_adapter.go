package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/researchwake"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type researchRunDispatcher struct {
	handler *Handler
}

func (d *researchRunDispatcher) Dispatch(ctx context.Context, request researchrun.DispatchRequest) (result researchrun.DispatchResult, retErr error) {
	defer func() {
		retErr = classifyResearchDispatchError(retErr)
	}()
	h := d.handler
	if h == nil || h.TaskService == nil || h.TxStarter == nil {
		return researchrun.DispatchResult{}, researchrun.NonRetryableDispatchError(errors.New("research task dispatcher is unavailable"))
	}
	requestHash, err := researchrun.HashDispatchRequest(request)
	if err != nil {
		return researchrun.DispatchResult{}, researchrun.NonRetryableDispatchError(fmt.Errorf("hash research dispatch request: %w", err))
	}
	if request.RequestHash != "" && request.RequestHash != requestHash {
		return researchrun.DispatchResult{}, researchrun.NonRetryableDispatchError(errors.New("research dispatch request hash does not match its payload"))
	}
	request.RequestHash = requestHash
	var existingID pgtype.UUID
	var existingHash string
	if err := h.DB.QueryRow(ctx, `
		SELECT id, COALESCE(context->>'research_dispatch_request_hash', '')
		FROM agent_inbox_event
		WHERE context->>'research_dispatch_key' = $1
		LIMIT 1
	`, request.Key).Scan(&existingID, &existingHash); err == nil {
		if existingHash != "" && existingHash != requestHash {
			return researchrun.DispatchResult{}, researchrun.NonRetryableDispatchError(fmt.Errorf("research dispatch key %q was reused for a different request", request.Key))
		}
		return researchrun.DispatchResult{InboxTaskID: uuidToString(existingID)}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return researchrun.DispatchResult{}, err
	}

	workspaceID := parseUUID(request.Run.WorkspaceID)
	agentID := parseUUID(request.AgentID)
	member, err := h.Queries.GetResearchFleetMemberByAgent(ctx, db.GetResearchFleetMemberByAgentParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
	})
	if statusErr := requireActiveResearchFleetMember(member, err); statusErr != nil {
		return researchrun.DispatchResult{}, statusErr
	}
	agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("load research agent: %w", err)
	}
	if agent.ArchivedAt.Valid {
		return researchrun.DispatchResult{}, service.ErrChatTaskAgentArchived
	}
	if !agent.RuntimeID.Valid {
		return researchrun.DispatchResult{}, service.ErrChatTaskAgentNoRuntime
	}
	runtime, err := h.Queries.GetAgentRuntime(ctx, agent.RuntimeID)
	if err != nil {
		return researchrun.DispatchResult{}, researchrun.NewDispatchFailure(fmt.Errorf("load research agent runtime: %w", err), researchrun.FailureConfiguration, false)
	}
	agent, err = ensureAgentHasExplicitModel(ctx, h.Queries, agent, runtime.Provider)
	if err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("ensure research agent model: %w", err)
	}
	if request.Target != (researchrun.ExecutionTarget{}) {
		currentTarget, targetErr := researchExecutionTarget(ctx, h, agent, runtime)
		if targetErr != nil {
			return researchrun.DispatchResult{}, targetErr
		}
		if currentTarget != request.Target {
			return researchrun.DispatchResult{}, researchrun.NewDispatchFailure(
				fmt.Errorf("research execution target changed after attempt creation"),
				researchrun.FailureTargetChanged, true,
			)
		}
	}
	session, err := h.Queries.GetResearchSession(ctx, db.GetResearchSessionParams{
		ID:          parseUUID(request.Run.SessionID),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("load research session: %w", err)
	}
	chatSession, err := h.ensureResearchAgentChatSession(ctx, workspaceID, session, agentID, session.CreatedBy)
	if err != nil {
		return researchrun.DispatchResult{}, err
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return researchrun.DispatchResult{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize the second idempotency check with creation. This covers lease
	// expiry while the first worker is still inside the external mutation.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, request.Key); err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("lock research task dispatch: %w", err)
	}
	existingID = pgtype.UUID{}
	existingHash = ""
	if err = tx.QueryRow(ctx, `
		SELECT id, COALESCE(context->>'research_dispatch_request_hash', '')
		FROM agent_inbox_event
		WHERE context->>'research_dispatch_key' = $1
		LIMIT 1
	`, request.Key).Scan(&existingID, &existingHash); err == nil {
		if existingHash != "" && existingHash != requestHash {
			return researchrun.DispatchResult{}, researchrun.NonRetryableDispatchError(fmt.Errorf("research dispatch key %q was reused for a different request", request.Key))
		}
		if err = tx.Commit(ctx); err != nil {
			return researchrun.DispatchResult{}, err
		}
		return researchrun.DispatchResult{InboxTaskID: uuidToString(existingID)}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return researchrun.DispatchResult{}, err
	}
	qtx := db.New(tx)
	message, err := qtx.CreateChatMessage(ctx, db.CreateChatMessageParams{
		ChatSessionID: chatSession.ID,
		Role:          "user",
		Content:       request.Prompt,
		Parts:         []byte("[]"),
	})
	if err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("create research task prompt: %w", err)
	}
	task, err := h.TaskService.CreateFreshChatTaskRow(ctx, qtx, chatSession, session.CreatedBy)
	if err != nil {
		return researchrun.DispatchResult{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE agent_inbox_event
		SET context = COALESCE(context, '{}'::jsonb) || jsonb_build_object(
		  'type', 'research_run_task',
		  'research_dispatch_key', $2::text,
		  'research_dispatch_request_hash', $3::text,
		  'research_session_id', $4::text,
		  'research_task_id', $5::text,
		  'research_attempt_id', $6::text,
		  'research_task_timeout_seconds', $7::integer,
		  'research_task_acceptance_criteria', $8::jsonb
		), updated_at = now()
		WHERE id = $1
	`, task.ID, request.Key, requestHash, request.Run.SessionID, request.Task.ID, request.AttemptID,
		request.Task.TimeoutSeconds, request.Task.AcceptanceCriteria); err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("bind research task dispatch: %w", err)
	}
	if err = qtx.LinkChatMessageToTask(ctx, db.LinkChatMessageToTaskParams{ID: message.ID, TaskID: task.ID}); err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("link research task prompt: %w", err)
	}
	if err = qtx.TouchChatSession(ctx, chatSession.ID); err != nil {
		return researchrun.DispatchResult{}, fmt.Errorf("touch research chat session: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return researchrun.DispatchResult{}, err
	}
	h.TaskService.PublishChatTaskQueued(ctx, task, false)
	return researchrun.DispatchResult{InboxTaskID: uuidToString(task.ID)}, nil
}

func classifyResearchDispatchError(err error) error {
	if err == nil {
		return nil
	}
	var alreadyClassified interface{ Retryable() bool }
	if errors.As(err, &alreadyClassified) {
		return err
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && strings.HasPrefix(pgErr.Code, "42") {
		return researchrun.NewDispatchFailure(err, researchrun.FailureInternal, false)
	}
	var wakeErr *researchwake.Error
	if errors.As(err, &wakeErr) {
		switch wakeErr.Reason {
		case researchwake.ReasonAgentNoRuntime, researchwake.ReasonAgentModelRequired:
			return researchrun.NewDispatchFailure(err, researchrun.FailureConfiguration, false)
		case researchwake.ReasonRuntimeOffline:
			return researchrun.NewDispatchFailure(err, researchrun.FailureRuntimeLost, true)
		default:
			return researchrun.NewDispatchFailure(err, researchrun.FailureCapability, false)
		}
	}
	if errors.Is(err, service.ErrChatTaskAgentArchived) ||
		errors.Is(err, service.ErrChatTaskAgentNoRuntime) ||
		errors.Is(err, service.ErrAgentModelRequired) {
		return researchrun.NewDispatchFailure(err, researchrun.FailureConfiguration, false)
	}
	return err
}

func researchExecutionTarget(ctx context.Context, h *Handler, agent db.Agent, runtime db.AgentRuntime) (researchrun.ExecutionTarget, error) {
	providerFingerprint := ""
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(provider_config_fingerprint, '')
		FROM agent_runtime_state
		WHERE agent_id = $1 AND runtime_id = $2
	`, agent.ID, runtime.ID).Scan(&providerFingerprint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return researchrun.ExecutionTarget{}, fmt.Errorf("load research target configuration: %w", err)
	}
	target := researchrun.ExecutionTarget{
		Adapter:   "agent_inbox",
		AgentID:   uuidToString(agent.ID),
		RuntimeID: uuidToString(runtime.ID),
		Provider:  runtime.Provider,
		Model:     strings.TrimSpace(agent.Model.String),
	}
	target.ConfigFingerprint = researchrun.ExecutionTargetFingerprint(
		target.AgentID, target.RuntimeID, target.Provider, target.Model,
		agent.RuntimeMode, runtime.PinnedVersion.String, providerFingerprint,
		string(agent.RuntimeConfig), string(agent.CustomEnv), string(agent.CustomArgs),
		string(agent.McpConfig), agent.ThinkingLevel.String,
	)
	return target, nil
}

func (d *researchRunDispatcher) Inspect(ctx context.Context, keys []string) (map[string]researchrun.InboxTaskState, error) {
	out := map[string]researchrun.InboxTaskState{}
	if len(keys) == 0 {
		return out, nil
	}
	// The daemon fence rejects a delivery once any newer active-state delivery
	// row exists. Inspect the same newest row before deciding whether execution
	// ownership is still live; an older unexpired row is no longer authoritative.
	rows, err := d.handler.DB.Query(ctx, `
		SELECT event.id::text, COALESCE(event.context->>'research_dispatch_key', ''), event.status,
		       COALESCE(event.terminal_outcome, ''), event.retryable,
		       COALESCE(event.failure_reason, COALESCE(event.error, '')),
		       event.terminal_at IS NOT NULL, event.started_at, event.completed_at,
		       now(), delivery.lease_expires_at,
		       COALESCE(delivery.lease_expires_at > now(), false)
		FROM agent_inbox_event event
		LEFT JOIN LATERAL (
		  SELECT candidate.lease_expires_at
		  FROM agent_event_delivery candidate
		  WHERE candidate.inbox_event_id = event.id
		    AND candidate.status IN ('leased', 'processing')
		  ORDER BY candidate.created_at DESC, candidate.id DESC
		  LIMIT 1
		) delivery ON true
		WHERE event.id::text = ANY($1::text[])
		   OR event.context->>'research_dispatch_key' = ANY($1::text[])
	`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, dispatchKey, status, outcome, failure string
		var retryable, terminal, activeLease bool
		var startedAt, completedAt, observedAt, leaseExpiresAt pgtype.Timestamptz
		if err = rows.Scan(&id, &dispatchKey, &status, &outcome, &retryable, &failure, &terminal,
			&startedAt, &completedAt, &observedAt, &leaseExpiresAt, &activeLease); err != nil {
			return nil, err
		}
		state := researchrun.InboxTaskState{
			ID: id, FailureReason: failure, Retryable: retryable,
			ObservedAt: observedAt.Time, HasActiveLease: activeLease,
		}
		if startedAt.Valid {
			started := startedAt.Time
			state.StartedAt = &started
		}
		if completedAt.Valid {
			completed := completedAt.Time
			state.CompletedAt = &completed
		}
		if leaseExpiresAt.Valid {
			expires := leaseExpiresAt.Time
			state.LeaseExpiresAt = &expires
		}
		state.Status, state.Retryable, state.FailureReason = normalizeResearchInboxTaskState(status, outcome, retryable, terminal, failure)
		out[id] = state
		if dispatchKey != "" {
			out[dispatchKey] = state
		}
	}
	return out, rows.Err()
}

func normalizeResearchInboxTaskState(status, outcome string, retryable, terminal bool, failure string) (string, bool, string) {
	switch status {
	case "pending":
		return "queued", retryable, failure
	case "draining":
		return "running", retryable, failure
	case "acked":
		switch outcome {
		case "failed", "expired":
			return "failed", retryable, failure
		case "cancelled":
			return "cancelled", false, failure
		default:
			// replied/no_reply/held/sent/skipped/completed all mean the agent
			// execution ended. The research attempt decides separately whether a
			// structured result was accepted; absent one, it retries the same task.
			return "completed", retryable, failure
		}
	case "suppressed":
		return "cancelled", false, failure
	case "failed":
		if terminal || !retryable {
			return "failed", retryable, failure
		}
		return "queued", retryable, failure
	default:
		return status, retryable, failure
	}
}

func (d *researchRunDispatcher) Cancel(ctx context.Context, inboxTaskIDs []string, _ string) error {
	if d.handler == nil || d.handler.TaskService == nil {
		return errors.New("research task cancellation is unavailable")
	}
	errs := []error{}
	for _, id := range inboxTaskIDs {
		parsed, err := parseUUIDString(id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err = d.handler.TaskService.CancelTask(ctx, parsed); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func parseUUIDString(value string) (pgtype.UUID, error) {
	var out pgtype.UUID
	if err := out.Scan(value); err != nil || !out.Valid {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q", value)
	}
	return out, nil
}

type researchRunProjector struct {
	handler *Handler
}

func (p *researchRunProjector) Project(ctx context.Context, event researchrun.RunEvent) error {
	h := p.handler
	if h == nil {
		return errors.New("research projector is unavailable")
	}
	workspaceID := parseUUID(event.WorkspaceID)
	sessionID := parseUUID(event.SessionID)
	session, err := h.Queries.GetResearchSession(ctx, db.GetResearchSessionParams{ID: sessionID, WorkspaceID: workspaceID})
	if err != nil {
		return err
	}
	var payload map[string]any
	if len(event.Payload) > 0 {
		_ = json.Unmarshal(event.Payload, &payload)
	}

	// LRM-1401: canvas truth is the run-v2 ledger projection. Keep writing
	// event→node rows only as recoverable audit fallback; live WS carries the
	// deterministic semantic graph so the UI stops treating dispatch events as
	// the research map.
	if h.ResearchRun != nil {
		if snap, snapErr := h.ResearchRun.Snapshot(ctx, event.SessionID, event.WorkspaceID); snapErr == nil {
			nodes, edges := projectRunV2Graph(snap)
			if err = publishProjectedRunGraph(ctx, h, event.WorkspaceID, event.ActorType, event.ActorID, event.SessionID, event.Sequence, nodes, edges); err != nil {
				return err
			}
		} else {
			// Snapshot unavailable: retain legacy single-event node insert.
			if nodeType, title, summary, status := projectResearchEvent(event, session, payload); nodeType != "" {
				actorAgentID := projectedResearchActorAgentID(event, payload)
				nodePayload := map[string]any{
					"run_event_id": event.ID,
					"event_type":   event.Type,
					"sequence":     event.Sequence,
					"details":      payload,
				}
				encoded, _ := json.Marshal(nodePayload)
				node, insertErr := insertProjectedResearchNode(ctx, h.DB, workspaceID, sessionID, event.ID, nodeType, title, summary, status, actorAgentID, encoded)
				if insertErr != nil {
					return insertErr
				}
				if err = assertResearchProjectionLease(ctx, h.DB, event.SessionID); err != nil {
					return err
				}
				h.publishResearchGraph(event.WorkspaceID, event.ActorType, event.ActorID, sessionID, node, nil)
			}
		}
	} else if nodeType, title, summary, status := projectResearchEvent(event, session, payload); nodeType != "" {
		actorAgentID := projectedResearchActorAgentID(event, payload)
		nodePayload := map[string]any{
			"run_event_id": event.ID,
			"event_type":   event.Type,
			"sequence":     event.Sequence,
			"details":      payload,
		}
		encoded, _ := json.Marshal(nodePayload)
		node, insertErr := insertProjectedResearchNode(ctx, h.DB, workspaceID, sessionID, event.ID, nodeType, title, summary, status, actorAgentID, encoded)
		if insertErr != nil {
			return insertErr
		}
		if err = assertResearchProjectionLease(ctx, h.DB, event.SessionID); err != nil {
			return err
		}
		h.publishResearchGraph(event.WorkspaceID, event.ActorType, event.ActorID, sessionID, node, nil)
	}

	if err = assertResearchProjectionLease(ctx, h.DB, event.SessionID); err != nil {
		return err
	}
	h.publish(protocol.EventResearchSessionStatusChanged, event.WorkspaceID, event.ActorType, event.ActorID, map[string]any{
		"session":      researchSessionToResponse(session),
		"run_event_id": event.ID,
		"event_type":   event.Type,
	})
	if event.Type == "task_result_accepted" {
		if reportID, _ := payload["report_id"].(string); strings.TrimSpace(reportID) != "" {
			if report, reportErr := h.Queries.GetLatestResearchReport(ctx, db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: workspaceID}); reportErr == nil {
				if err = assertResearchProjectionLease(ctx, h.DB, event.SessionID); err != nil {
					return err
				}
				h.publish(protocol.EventResearchSessionReportUpdated, event.WorkspaceID, event.ActorType, event.ActorID, map[string]any{
					"session_id": event.SessionID,
					"report":     researchReportToResp(report),
				})
			}
		}
	}
	return nil
}

// publishProjectedRunGraph upserts the full run-v2 projected graph over WS.
// Stable node/edge IDs let the client replace prior semantic nodes in place.
func publishProjectedRunGraph(ctx context.Context, h *Handler, workspaceID, actorType, actorID, sessionID string, eventSequence int64, nodes []ResearchGraphNodeResp, edges []ResearchGraphEdgeResp) error {
	if h == nil {
		return nil
	}
	lease, _ := researchrun.ReconcileLeaseFromContext(ctx)
	edgeByTo := map[string]ResearchGraphEdgeResp{}
	for _, e := range edges {
		if e.EdgeType != researchTreeEdgeType {
			continue
		}
		if _, exists := edgeByTo[e.ToNodeID]; exists {
			continue
		}
		edgeByTo[e.ToNodeID] = e
	}
	for _, node := range nodes {
		if err := assertResearchProjectionLease(ctx, h.DB, sessionID); err != nil {
			return err
		}
		payload := map[string]any{
			"session_id":           sessionID,
			"node":                 node,
			"run_event_sequence":   eventSequence,
			"reconcile_generation": lease.Generation,
		}
		if edge, ok := edgeByTo[node.ID]; ok {
			payload["edge"] = edge
		} else {
			payload["edge"] = nil
		}
		h.publish(protocol.EventResearchSessionGraphUpdated, workspaceID, actorType, actorID, payload)
	}
	return nil
}

func assertResearchProjectionLease(ctx context.Context, executor dbExecutor, sessionID string) error {
	lease, ok := researchrun.ReconcileLeaseFromContext(ctx)
	if !ok || lease.SessionID != sessionID {
		return researchrun.ErrRunLeaseLost
	}
	var one int
	err := executor.QueryRow(ctx, `
		SELECT 1 FROM research_session
		WHERE id = $1::uuid
		  AND reconcile_lease_token = $2::uuid
		  AND reconcile_lease_generation = $3
		  AND reconcile_lease_expires_at > now()
	`, sessionID, lease.Token, lease.Generation).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return researchrun.ErrRunLeaseLost
	}
	return err
}

func projectedResearchActorAgentID(event researchrun.RunEvent, payload map[string]any) pgtype.UUID {
	if event.ActorType == "agent" && strings.TrimSpace(event.ActorID) != "" {
		return parseUUID(event.ActorID)
	}
	if agentID := valueString(payload, "agent_id"); agentID != "" {
		return parseUUID(agentID)
	}
	return pgtype.UUID{}
}

func projectResearchEvent(event researchrun.RunEvent, session db.ResearchSession, payload map[string]any) (nodeType, title, summary, status string) {
	status = "active"
	switch event.Type {
	case "run_started":
		return "goal", session.Title, session.Goal, "active"
	case "task_dispatching":
		return "agent_activity", "调研任务已分派", valueString(payload, "task_id"), "active"
	case "task_dispatched":
		return "agent_activity", "调研任务等待 Agent 执行", valueString(payload, "task_id"), "active"
	case "task_started":
		return "agent_activity", "Agent 开始执行调研任务", valueString(payload, "task_id"), "active"
	case "task_attempt_cancelling":
		return "agent_activity", "正在停止超时任务", valueString(payload, "task_id"), "active"
	case "task_result_accepted":
		return "finding", "调研结果已入账", valueString(payload, "summary"), "done"
	case "task_attempt_failed":
		summary := valueString(payload, "diagnostics")
		if summary == "" {
			summary = valueString(payload, "failure_class")
		}
		return "dead_end", "调研任务尝试失败", summary, "done"
	case "task_blocked":
		return "dead_end", "调研任务因前置失败而阻塞", valueString(payload, "task_id"), "done"
	case "control_task_created":
		return "probe", "已创建补救任务", valueString(payload, "kind"), "active"
	case "goal_steered":
		return "pivot", "用户调整调研目标", valueString(payload, "goal"), "done"
	case "run_awaiting_confirmation":
		return "stage_gate", "交付门槛已通过", "等待用户确认调研结果", "done"
	case "budget_exhausted":
		return "dead_end", "调研预算已耗尽", valueString(payload, "budget_kind"), "done"
	case "run_completed":
		return "stage_gate", "调研已确认完成", "最终报告和证据账本已锁定为本次交付", "done"
	case "run_paused":
		return "stage_gate", "调研已暂停", valueString(payload, "reason"), "active"
	case "run_resumed":
		return "stage_gate", "调研已恢复", "执行器继续处理未完成任务", "done"
	case "run_failed":
		return "dead_end", "调研执行失败", valueString(payload, "reason"), "done"
	case "run_cancelled":
		return "dead_end", "调研已取消", valueString(payload, "reason"), "done"
	default:
		return "", "", "", ""
	}
}

func valueString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func insertProjectedResearchNode(ctx context.Context, executor dbExecutor, workspaceID, sessionID pgtype.UUID, eventID, nodeType, title, summary, status string, actorAgentID pgtype.UUID, payload []byte) (db.ResearchGraphNode, error) {
	lease, ok := researchrun.ReconcileLeaseFromContext(ctx)
	if !ok || lease.SessionID != uuidToString(sessionID) {
		return db.ResearchGraphNode{}, researchrun.ErrRunLeaseLost
	}
	query := `
		INSERT INTO research_graph_node (
			workspace_id, session_id, node_type, title, summary, status,
			actor_agent_id, payload, run_event_id
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9::uuid
		FROM research_session
		WHERE id = $2
		  AND reconcile_lease_token = $10::uuid
		  AND reconcile_lease_generation = $11
		  AND reconcile_lease_expires_at > now()
		ON CONFLICT DO NOTHING
		RETURNING id, workspace_id, session_id, node_type, title, summary, status,
		          actor_agent_id, payload, created_at, updated_at
	`
	var node db.ResearchGraphNode
	err := executor.QueryRow(ctx, query, workspaceID, sessionID, nodeType,
		strings.TrimSpace(title), summary, status, actorAgentID, payload, eventID,
		lease.Token, lease.Generation).Scan(
		&node.ID, &node.WorkspaceID, &node.SessionID, &node.NodeType, &node.Title,
		&node.Summary, &node.Status, &node.ActorAgentID, &node.Payload,
		&node.CreatedAt, &node.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if leaseErr := assertResearchProjectionLease(ctx, executor, uuidToString(sessionID)); leaseErr != nil {
			return db.ResearchGraphNode{}, leaseErr
		}
		err = executor.QueryRow(ctx, `
			SELECT id, workspace_id, session_id, node_type, title, summary, status,
			       actor_agent_id, payload, created_at, updated_at
			FROM research_graph_node WHERE run_event_id = $1::uuid
		`, eventID).Scan(
			&node.ID, &node.WorkspaceID, &node.SessionID, &node.NodeType, &node.Title,
			&node.Summary, &node.Status, &node.ActorAgentID, &node.Payload,
			&node.CreatedAt, &node.UpdatedAt,
		)
	}
	return node, err
}
