package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) NodeCommand(ctx context.Context, in NodeCommandInput) (NodeCommandOutcome, error) {
	if err := validateNodeCommandInput(in); err != nil {
		return NodeCommandOutcome{}, err
	}
	requestHash, err := HashNodeCommandRequest(in)
	if err != nil {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeInvalidRequest, "请求内容无效，请刷新后重试")
	}
	tx, err := s.beginResearchTx(ctx, txOpNodeCommand, pgx.TxOptions{})
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.SessionID, in.WorkspaceID); err != nil {
		return NodeCommandOutcome{}, err
	}

	var (
		workspaceID, status, orchestratorVersion                 string
		goalVersion, planVersion, maxTasks, maxAttempts, timeout int
		stateVersion                                             int64
	)
	err = tx.QueryRow(ctx, `
		SELECT workspace_id::text, status, orchestrator_version,
		       goal_version, plan_version, state_version,
		       COALESCE((run_config->>'max_tasks')::int, 60),
		       COALESCE((run_config->>'max_attempts_per_task')::int, 3),
		       COALESCE((run_config->>'task_timeout_seconds')::int, 1800)
		FROM research_session
		WHERE id = $1::uuid AND workspace_id = $2::uuid
		FOR UPDATE
	`, in.SessionID, in.WorkspaceID).Scan(
		&workspaceID, &status, &orchestratorVersion,
		&goalVersion, &planVersion, &stateVersion,
		&maxTasks, &maxAttempts, &timeout,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeCommandOutcome{}, ErrRunNotFound
	}
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	eventKey := nodeCommandClientKey(in.ClientRequestID, "event")
	if existing, loadErr := loadNodeCommandEvent(ctx, tx, in.SessionID, eventKey); loadErr == nil {
		outcome, storedHash, decodeErr := decodeNodeCommandEvent(existing)
		if decodeErr != nil || storedHash == "" || storedHash != requestHash ||
			existing.ActorType != in.ActorType || existing.ActorID != in.ActorID {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeIdempotencyConflict, "相同请求标识已用于其他操作，请换新标识重试")
		}
		outcome.Replayed = true
		outcome.Event = existing
		return outcome, s.commitResearchTx(ctx, txOpNodeCommand, tx)
	} else if !errors.Is(loadErr, pgx.ErrNoRows) {
		return NodeCommandOutcome{}, loadErr
	}

	switch RunStatus(status) {
	case RunStatusCompleted, RunStatusCancelled, RunStatusArchived, RunStatusFailed:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeSessionTerminal, "调研已结束，无法继续该操作")
	case RunStatusPaused:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeRunNotRunning, "调研已暂停，请先恢复后再操作")
	case RunStatusDrafting, RunStatusAwaitingUserConfirm:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeRunNotRunning, "当前阶段不可执行该操作，请稍后再试")
	case RunStatusRunning:
		// ok
	default:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeRunNotRunning, "当前会话状态不允许该操作")
	}

	if in.ExpectedStateVersion != nil && *in.ExpectedStateVersion != stateVersion {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeStateVersionConflict, "画布已更新，请刷新后重试")
	}

	switch in.Action {
	case NodeActionRetry:
		return s.nodeCommandRetry(ctx, tx, in, requestHash, workspaceID, goalVersion, planVersion, maxAttempts)
	case NodeActionReassign:
		return s.nodeCommandReassign(ctx, tx, in, requestHash, workspaceID, goalVersion, planVersion)
	}

	return s.nodeCommandContinueFork(ctx, tx, in, requestHash, workspaceID, orchestratorVersion, goalVersion, planVersion, maxTasks, maxAttempts, timeout)
}

func (s *PostgresStore) nodeCommandContinueFork(
	ctx context.Context, tx pgx.Tx, in NodeCommandInput, requestHash string,
	workspaceID, orchestratorVersion string,
	goalVersion, planVersion, maxTasks, maxAttempts, timeout int,
) (NodeCommandOutcome, error) {
	questionID := strings.TrimSpace(in.AnchorQuestionID)
	parentTaskID := strings.TrimSpace(in.AnchorTaskID)
	if parentTaskID != "" && questionID == "" {
		_ = tx.QueryRow(ctx, `
			SELECT COALESCE(question_id::text, '') FROM research_task
			WHERE id = $1::uuid AND session_id = $2::uuid
		`, parentTaskID, in.SessionID).Scan(&questionID)
	}
	if questionID != "" {
		var qStatus string
		qErr := tx.QueryRow(ctx, `
			SELECT status FROM research_question
			WHERE id = $1::uuid AND session_id = $2::uuid
		`, questionID, in.SessionID).Scan(&qStatus)
		if errors.Is(qErr, pgx.ErrNoRows) {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNodeStale, "节点已失效或不属于当前研究图，请刷新后重试")
		}
		if qErr != nil {
			return NodeCommandOutcome{}, qErr
		}
		if qStatus == string(QuestionStatusObsolete) {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNodeStale, "该问题已废弃，无法继续或分叉")
		}
	}

	var taskCount int
	if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid`, in.SessionID).Scan(&taskCount); err != nil {
		return NodeCommandOutcome{}, err
	}
	if taskCount >= maxTasks {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeActionNotAllowed, "任务配额已用尽，无法再追加")
	}

	objective := strings.TrimSpace(in.Objective)
	if objective == "" {
		objective = strings.TrimSpace(in.AnchorTitle)
	}
	if objective == "" {
		if in.Action == NodeActionFork {
			objective = "分叉续研"
		} else {
			objective = "沿当前问题继续调研"
		}
	}
	if patch := strings.TrimSpace(in.GoalPatch); patch != "" && in.Action == NodeActionFork {
		objective = truncateRunes(patch, 240)
	}

	acceptance, _ := json.Marshal(map[string]any{
		"schema_version":     resultSchemaVersionForOrchestrator(orchestratorVersion),
		"node_command":       in.Action,
		"source_node_id":     in.NodeID,
		"goal_patch":         strings.TrimSpace(in.GoalPatch),
		"strategy":           strategyForNodeCommand(in),
		"source_constraints": jsonRawOrNil(sourcePatchForNodeCommand(in)),
		// goal_patch is proposal-only — never write research_session.goal (LRM-898).
		"goal_authoritative": false,
	})

	var createdQuestion *Question
	lineageParentQuestion := questionID
	if in.Action == NodeActionFork {
		qClientKey := nodeCommandClientKey(in.ClientRequestID, "question")
		questionText := objective
		if patch := strings.TrimSpace(in.GoalPatch); patch != "" {
			questionText = truncateRunes(patch, 512)
		}
		parentForInsert := questionID
		if parentForInsert == "" {
			// Fork from root / task without question: attach under root question if present.
			_ = tx.QueryRow(ctx, `
				SELECT id::text FROM research_question
				WHERE session_id = $1::uuid AND client_key = 'root'
				ORDER BY plan_version DESC LIMIT 1
			`, in.SessionID).Scan(&parentForInsert)
		}
		var newQID string
		err := tx.QueryRow(ctx, `
			INSERT INTO research_question (
				workspace_id, session_id, parent_question_id, client_key, kind,
				question, required, status, priority, impact, uncertainty, novelty,
				goal_version, plan_version
			) VALUES (
				$1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, $5,
				$6, true, 'open', 0.9, 0.8, 0.7, 0.8,
				$7, $8
			)
			ON CONFLICT (session_id, goal_version, plan_version, client_key)
			DO UPDATE SET updated_at = now()
			RETURNING id::text
		`, workspaceID, in.SessionID, parentForInsert, qClientKey, QuestionKindFollowUp,
			questionText, goalVersion, planVersion).Scan(&newQID)
		if err != nil {
			return NodeCommandOutcome{}, err
		}
		if err = registerProductionQuestionPassportTx(ctx, tx, workspaceID, in.SessionID, newQID, "", ArtifactAccessRaw); err != nil {
			return NodeCommandOutcome{}, err
		}
		q := Question{
			ID:               newQID,
			SessionID:        in.SessionID,
			ParentQuestionID: parentForInsert,
			ClientKey:        qClientKey,
			Kind:             QuestionKindFollowUp,
			Question:         questionText,
			Required:         true,
			Status:           QuestionStatusOpen,
			Priority:         0.9,
			Impact:           0.8,
			Uncertainty:      0.7,
			Novelty:          0.8,
			GoalVersion:      goalVersion,
			PlanVersion:      planVersion,
		}
		createdQuestion = &q
		questionID = newQID
		lineageParentQuestion = parentForInsert
		// Fork creates a new branch — do not inherit parent_task_id (preserves original branch history).
		parentTaskID = ""
	}

	if questionID == "" {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNodeStale, "无法定位可续研的问题节点，请从问题或任务节点发起")
	}

	tClientKey := nodeCommandClientKey(in.ClientRequestID, "task")
	expected := expectedResultForTaskVersion(orchestratorVersion, TaskKindDiscover)
	capability := "scout"
	if strat := strategyForNodeCommand(in); strat != "" {
		capability = truncateRunes(strat, 64)
	}
	var taskID string
	err := tx.QueryRow(ctx, `
		INSERT INTO research_task (
			workspace_id, session_id, question_id, parent_task_id, client_key,
			kind, objective, required_capability, expected_result,
			acceptance_criteria, priority, status, goal_version, plan_version,
			max_attempts, timeout_seconds, ready_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, $5,
			$6, $7, $8, $9,
			$10, 0.85, 'ready', $11, $12,
			$13, $14, now()
		)
		ON CONFLICT (session_id, goal_version, plan_version, client_key)
		DO UPDATE SET updated_at = now()
		RETURNING id::text
	`, workspaceID, in.SessionID, questionID, parentTaskID, tClientKey,
		TaskKindDiscover, objective, capability, expected,
		acceptance, goalVersion, planVersion,
		maxAttempts, timeout).Scan(&taskID)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	if err = ensureDomainArtifactPassportTx(ctx, tx, ArtifactKindTask, workspaceID, in.SessionID, taskID, time.Now(), int32Ptr(int32(goalVersion)), int32Ptr(int32(planVersion))); err != nil {
		return NodeCommandOutcome{}, err
	}

	task, err := scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.id = $1::uuid`, taskID))
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	outcome := NodeCommandOutcome{
		Action:          in.Action,
		ClientRequestID: in.ClientRequestID,
		Question:        createdQuestion,
		Task:            &task,
		ParentLineage: ParentLineage{
			ParentQuestionID: lineageParentQuestion,
			ParentTaskID:     parentTaskID,
			SourceNodeID:     in.NodeID,
		},
		Queued:     true,
		Acceptance: acceptance,
	}
	if createdQuestion == nil && questionID != "" {
		// continue: expose anchor question id via lineage only; optional fetch
		var q Question
		qErr := tx.QueryRow(ctx, `
			SELECT id::text, session_id::text, COALESCE(parent_question_id::text, ''),
			       COALESCE(created_by_task_id::text, ''), client_key, kind, question,
			       required, status, priority, impact, uncertainty, novelty,
			       goal_version, plan_version, COALESCE(answer_claim_id::text, ''),
			       COALESCE(terminal_explanation, '')
			FROM research_question WHERE id = $1::uuid
		`, questionID).Scan(
			&q.ID, &q.SessionID, &q.ParentQuestionID, &q.CreatedByTaskID, &q.ClientKey,
			&q.Kind, &q.Question, &q.Required, &q.Status, &q.Priority, &q.Impact,
			&q.Uncertainty, &q.Novelty, &q.GoalVersion, &q.PlanVersion, &q.AnswerClaimID,
			&q.TerminalExplanation,
		)
		if qErr == nil {
			outcome.Question = &q
			outcome.ParentLineage.ParentQuestionID = q.ParentQuestionID
			if outcome.ParentLineage.ParentQuestionID == "" {
				outcome.ParentLineage.ParentQuestionID = q.ID
			}
		}
	}

	payload := map[string]any{
		"command":            outcome,
		"action":             in.Action,
		"client_request_id":  in.ClientRequestID,
		"request_hash":       requestHash,
		"source_node_id":     in.NodeID,
		"question_id":        questionID,
		"task_id":            taskID,
		"parent_question_id": outcome.ParentLineage.ParentQuestionID,
		"parent_task_id":     outcome.ParentLineage.ParentTaskID,
		"queued":             true,
	}
	event, err := appendEvent(ctx, tx, workspaceID, in.SessionID, "node_command_"+in.Action, nodeCommandClientKey(in.ClientRequestID, "event"), in.ActorType, in.ActorID, payload)
	if err != nil {
		if errors.Is(err, ErrResultConflict) {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeIdempotencyConflict, "相同请求标识已用于其他操作，请换新标识重试")
		}
		return NodeCommandOutcome{}, err
	}
	outcome.CommandID = event.ID
	outcome.StateVersion = event.Sequence
	outcome.Event = event
	if err = s.commitResearchTx(ctx, txOpNodeCommand, tx); err != nil {
		return NodeCommandOutcome{}, err
	}
	return outcome, nil
}

func (s *PostgresStore) nodeCommandRetry(
	ctx context.Context, tx pgx.Tx, in NodeCommandInput, requestHash string,
	workspaceID string, goalVersion, planVersion, defaultMaxAttempts int,
) (NodeCommandOutcome, error) {
	taskID := strings.TrimSpace(in.AnchorTaskID)
	task, err := scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.id = $1::uuid AND t.session_id = $2::uuid FOR UPDATE`, taskID, in.SessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNodeStale, "任务节点已失效，请刷新后重试")
	}
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	latest, hasLatest, err := loadLatestAttemptForUpdate(ctx, tx, taskID)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	if deny := retryEligibility(task, latest, hasLatest); deny != nil {
		return NodeCommandOutcome{}, deny
	}

	// Preserve original attempts; only reopen the task for a new attempt.
	if hasLatest && (latest.Status == AttemptStatusDispatching || latest.Status == AttemptStatusRunning || latest.Status == AttemptStatusCancelling) {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNotRetryable, "任务仍在执行，请等待结束后再重试，或先改派")
	}

	acceptance := mergeTaskAcceptancePatch(task.AcceptanceCriteria, map[string]any{
		"node_command":        NodeActionRetry,
		"source_node_id":      in.NodeID,
		"strategy_patch":      strategyForNodeCommand(in),
		"source_patch":        jsonRawOrNil(sourcePatchForNodeCommand(in)),
		"previous_attempt_id": "",
	})
	if hasLatest {
		acceptance = mergeTaskAcceptancePatch(acceptance, map[string]any{
			"previous_attempt_id": latest.ID,
		})
	}
	if strat := strategyForNodeCommand(in); strat != "" {
		if _, err = tx.Exec(ctx, `
			UPDATE research_task SET required_capability = $2, updated_at = now()
			WHERE id = $1::uuid
		`, taskID, truncateRunes(strat, 64)); err != nil {
			return NodeCommandOutcome{}, err
		}
	}

	nextMax := task.MaxAttempts
	var attemptCount int
	if err = tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task_attempt WHERE task_id = $1::uuid`, taskID).Scan(&attemptCount); err != nil {
		return NodeCommandOutcome{}, err
	}
	if attemptCount >= nextMax {
		nextMax = attemptCount + 1
	}
	if nextMax < defaultMaxAttempts {
		nextMax = defaultMaxAttempts
	}

	if _, err = tx.Exec(ctx, `
		UPDATE research_task
		SET status = 'ready',
		    ready_at = now(),
		    terminal_reason = '',
		    completed_at = NULL,
		    max_attempts = $2,
		    acceptance_criteria = $3,
		    goal_version = $4,
		    plan_version = $5,
		    updated_at = now()
		WHERE id = $1::uuid
	`, taskID, nextMax, acceptance, goalVersion, planVersion); err != nil {
		return NodeCommandOutcome{}, err
	}

	task, err = scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.id = $1::uuid`, taskID))
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	lineage := &RetryLineage{
		TaskID:            taskID,
		NextAttemptNumber: attemptCount + 1,
	}
	if hasLatest {
		lineage.PreviousAttemptID = latest.ID
	}
	outcome := NodeCommandOutcome{
		Action:          NodeActionRetry,
		ClientRequestID: in.ClientRequestID,
		Task:            &task,
		ParentLineage: ParentLineage{
			ParentQuestionID: task.QuestionID,
			ParentTaskID:     task.ParentTaskID,
			SourceNodeID:     in.NodeID,
		},
		RetryLineage: lineage,
		Queued:       true,
		Acceptance:   acceptance,
	}
	if aid := strings.TrimSpace(task.AssignedAgentID); aid != "" {
		outcome.Assigned = &aid
	}

	payload := map[string]any{
		"command":             outcome,
		"action":              NodeActionRetry,
		"client_request_id":   in.ClientRequestID,
		"request_hash":        requestHash,
		"source_node_id":      in.NodeID,
		"task_id":             taskID,
		"previous_attempt_id": lineage.PreviousAttemptID,
		"queued":              true,
	}
	event, err := appendEvent(ctx, tx, workspaceID, in.SessionID, "node_command_retry", nodeCommandClientKey(in.ClientRequestID, "event"), in.ActorType, in.ActorID, payload)
	if err != nil {
		if errors.Is(err, ErrResultConflict) {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeIdempotencyConflict, "相同请求标识已用于其他操作，请换新标识重试")
		}
		return NodeCommandOutcome{}, err
	}
	outcome.CommandID = event.ID
	outcome.StateVersion = event.Sequence
	outcome.Event = event
	if err = s.commitResearchTx(ctx, txOpNodeCommand, tx); err != nil {
		return NodeCommandOutcome{}, err
	}
	return outcome, nil
}

func (s *PostgresStore) nodeCommandReassign(
	ctx context.Context, tx pgx.Tx, in NodeCommandInput, requestHash string,
	workspaceID string, goalVersion, planVersion int,
) (NodeCommandOutcome, error) {
	taskID := strings.TrimSpace(in.AnchorTaskID)
	task, err := scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.id = $1::uuid AND t.session_id = $2::uuid FOR UPDATE`, taskID, in.SessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNodeStale, "任务节点已失效，请刷新后重试")
	}
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	fromAgent := strings.TrimSpace(task.AssignedAgentID)
	latest, hasLatest, err := loadLatestAttemptForUpdate(ctx, tx, taskID)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	if hasLatest && fromAgent == "" {
		fromAgent = strings.TrimSpace(latest.AssignedAgentID)
	}
	if hasLatest && latest.CancelCompletedAt == nil && (latest.Status == AttemptStatusCancelling || latest.Status == AttemptStatusCancelled) {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNotRetryable, "原执行仍在停止中，请等待取消确认后再改派")
	}

	// Cancel in-flight attempt so a new assignee can take over; keep the row.
	if hasLatest && (latest.Status == AttemptStatusDispatching || latest.Status == AttemptStatusRunning) {
		if err = abandonAttemptCircuitProbesTx(ctx, tx, latest.ID); err != nil {
			return NodeCommandOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `
			UPDATE research_task_attempt attempt
			SET cancellation_completed_at = now(), updated_at = now()
			FROM research_dispatch_outbox outbox
			WHERE attempt.id = $1::uuid
			  AND outbox.attempt_id = attempt.id
			  AND outbox.status = 'pending'
			  AND outbox.delivery_attempts = 0
		`, latest.ID); err != nil {
			return NodeCommandOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `
			UPDATE research_task_attempt
			SET status = 'cancelled', failure_class = 'reassigned',
			    diagnostics = '改派取消原执行', completed_at = now(), updated_at = now()
			WHERE id = $1::uuid
		`, latest.ID); err != nil {
			return NodeCommandOutcome{}, err
		}
		if _, err = tx.Exec(ctx, `
			UPDATE research_dispatch_outbox
			SET status = 'cancelled', lease_token = NULL, lease_expires_at = NULL,
			    last_error = 'reassigned', updated_at = now()
			WHERE attempt_id = $1::uuid AND status IN ('pending', 'delivering')
		`, latest.ID); err != nil {
			return NodeCommandOutcome{}, err
		}
	}

	members, err := listFleetMembersTx(ctx, tx, in.SessionID, in.WorkspaceID)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	activeLoad := map[string]int{}
	rows, qerr := tx.Query(ctx, `
		SELECT assigned_agent_id::text FROM research_task_attempt
		WHERE session_id = $1::uuid AND status IN ('dispatching', 'running', 'cancelling')
	`, in.SessionID)
	if qerr != nil {
		return NodeCommandOutcome{}, qerr
	}
	for rows.Next() {
		var aid string
		if scanErr := rows.Scan(&aid); scanErr != nil {
			rows.Close()
			return NodeCommandOutcome{}, scanErr
		}
		activeLoad[aid]++
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return NodeCommandOutcome{}, err
	}

	toAgent := strings.TrimSpace(in.TargetAgentID)
	reason := ""
	if toAgent != "" {
		member, ok := findActiveFleetMember(members, toAgent)
		if !ok {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNoEligibleMember, "指定成员不可用（已归档或未激活）")
		}
		if activeLoad[toAgent] > 0 {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNoEligibleMember, "指定成员当前任务已满，请稍后再改派或选择其他人")
		}
		_ = member
		reason = "用户指定成员"
	} else {
		// Auto-select: prefer role match, exclude from-agent and overloaded.
		pickTask := task
		if strat := strategyForNodeCommand(in); strat != "" {
			pickTask.RequiredCapability = truncateRunes(strat, 64)
		}
		filteredLoad := map[string]int{}
		for k, v := range activeLoad {
			filteredLoad[k] = v
		}
		if fromAgent != "" {
			// Force exclude current assignee from idle preference by marking busy.
			filteredLoad[fromAgent] = filteredLoad[fromAgent] + 100
		}
		toAgent = selectAgent(pickTask, members, filteredLoad)
		if toAgent == "" || toAgent == fromAgent {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeNoEligibleMember, "没有可改派的空闲成员，请先扩编或等待产能释放")
		}
		reason = "自动选择空闲且能力匹配的成员"
	}

	acceptance := mergeTaskAcceptancePatch(task.AcceptanceCriteria, map[string]any{
		"node_command":    NodeActionReassign,
		"source_node_id":  in.NodeID,
		"strategy_patch":  strategyForNodeCommand(in),
		"source_patch":    jsonRawOrNil(sourcePatchForNodeCommand(in)),
		"from_agent_id":   fromAgent,
		"to_agent_id":     toAgent,
		"reassign_reason": reason,
	})

	if _, err = tx.Exec(ctx, `
		UPDATE research_task
		SET status = 'ready',
		    assigned_agent_id = $2::uuid,
		    ready_at = now(),
		    terminal_reason = '',
		    completed_at = NULL,
		    acceptance_criteria = $3,
		    goal_version = $4,
		    plan_version = $5,
		    updated_at = now()
		WHERE id = $1::uuid
	`, taskID, toAgent, acceptance, goalVersion, planVersion); err != nil {
		return NodeCommandOutcome{}, err
	}

	queuePos, err := countReadyQueuePosition(ctx, tx, in.SessionID, taskID, task.Priority)
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	task, err = scanTask(tx.QueryRow(ctx, taskSelectSQL+` WHERE t.id = $1::uuid`, taskID))
	if err != nil {
		return NodeCommandOutcome{}, err
	}

	reassign := &ReassignInfo{
		FromAgentID:   fromAgent,
		ToAgentID:     toAgent,
		Reason:        reason,
		QueuePosition: queuePos,
	}
	outcome := NodeCommandOutcome{
		Action:          NodeActionReassign,
		ClientRequestID: in.ClientRequestID,
		Task:            &task,
		ParentLineage: ParentLineage{
			ParentQuestionID: task.QuestionID,
			ParentTaskID:     task.ParentTaskID,
			SourceNodeID:     in.NodeID,
		},
		Reassign:   reassign,
		Assigned:   &toAgent,
		Queued:     true,
		Acceptance: acceptance,
	}
	if hasLatest {
		outcome.RetryLineage = &RetryLineage{
			PreviousAttemptID: latest.ID,
			TaskID:            taskID,
		}
	}

	payload := map[string]any{
		"command":           outcome,
		"action":            NodeActionReassign,
		"client_request_id": in.ClientRequestID,
		"request_hash":      requestHash,
		"source_node_id":    in.NodeID,
		"task_id":           taskID,
		"from_agent_id":     fromAgent,
		"to_agent_id":       toAgent,
		"queue_position":    queuePos,
		"queued":            true,
	}
	event, err := appendEvent(ctx, tx, workspaceID, in.SessionID, "node_command_reassign", nodeCommandClientKey(in.ClientRequestID, "event"), in.ActorType, in.ActorID, payload)
	if err != nil {
		if errors.Is(err, ErrResultConflict) {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeIdempotencyConflict, "相同请求标识已用于其他操作，请换新标识重试")
		}
		return NodeCommandOutcome{}, err
	}
	outcome.CommandID = event.ID
	outcome.StateVersion = event.Sequence
	outcome.Event = event
	if err = s.commitResearchTx(ctx, txOpNodeCommand, tx); err != nil {
		return NodeCommandOutcome{}, err
	}
	return outcome, nil
}

func loadLatestAttemptForUpdate(ctx context.Context, tx pgx.Tx, taskID string) (Attempt, bool, error) {
	row := tx.QueryRow(ctx, attemptSelectSQL+`
		WHERE a.task_id = $1::uuid
		ORDER BY a.attempt_number DESC
		LIMIT 1
		FOR UPDATE OF a
	`, taskID)
	attempt, err := scanAttempt(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, false, nil
	}
	if err != nil {
		return Attempt{}, false, err
	}
	return attempt, true, nil
}

func retryEligibility(task Task, latest Attempt, hasLatest bool) *NodeCommandDenied {
	switch task.Status {
	case TaskStatusSucceeded, TaskStatusObsolete, TaskStatusCancelled:
		return denyNodeCommand(NodeCmdCodeNotRetryable, "该任务已结束，无法重试")
	case TaskStatusPending:
		return denyNodeCommand(NodeCmdCodeNotRetryable, "任务尚未就绪，无法重试")
	}
	if !hasLatest {
		if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed {
			return nil
		}
		return denyNodeCommand(NodeCmdCodeNotRetryable, "没有可重试的失败记录")
	}
	switch latest.Status {
	case AttemptStatusFailed, AttemptStatusCancelled, AttemptStatusLost:
		return nil
	case AttemptStatusSucceeded:
		if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed {
			return nil
		}
		return denyNodeCommand(NodeCmdCodeNotRetryable, "最近一次尝试已成功，无需重试")
	case AttemptStatusDispatching, AttemptStatusRunning, AttemptStatusCancelling:
		return denyNodeCommand(NodeCmdCodeNotRetryable, "任务仍在执行，请等待结束后再重试，或先改派")
	default:
		if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed {
			return nil
		}
		return denyNodeCommand(NodeCmdCodeNotRetryable, "当前状态不可重试")
	}
}

func mergeTaskAcceptancePatch(existing json.RawMessage, patch map[string]any) json.RawMessage {
	base := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &base)
	}
	for k, v := range patch {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		base[k] = v
	}
	out, err := json.Marshal(base)
	if err != nil {
		return existing
	}
	return out
}

func listFleetMembersTx(ctx context.Context, tx pgx.Tx, sessionID, workspaceID string) ([]FleetMember, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.agent_id::text, m.role, m.status, m.is_lead
		FROM research_fleet_member m
		JOIN research_session s ON s.fleet_id = m.fleet_id
		WHERE s.id = $1::uuid AND s.workspace_id = $2::uuid AND m.workspace_id = s.workspace_id
		ORDER BY m.is_lead DESC, m.created_at, m.id
	`, sessionID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FleetMember{}
	for rows.Next() {
		var item FleetMember
		if err := rows.Scan(&item.AgentID, &item.Role, &item.Status, &item.IsLead); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func findActiveFleetMember(members []FleetMember, agentID string) (FleetMember, bool) {
	for _, m := range members {
		if m.AgentID == agentID && m.Status == "active" {
			return m, true
		}
	}
	return FleetMember{}, false
}

func countReadyQueuePosition(ctx context.Context, tx pgx.Tx, sessionID, taskID string, priority float64) (int, error) {
	var ahead int
	err := tx.QueryRow(ctx, `
		SELECT count(*)::int FROM research_task
		WHERE session_id = $1::uuid AND status = 'ready' AND id <> $2::uuid
		  AND (priority > $3 OR (priority = $3 AND id < $2::uuid))
	`, sessionID, taskID, priority).Scan(&ahead)
	if err != nil {
		return 0, err
	}
	return ahead + 1, nil
}

func loadNodeCommandEvent(ctx context.Context, tx pgx.Tx, sessionID, key string) (RunEvent, error) {
	var existing RunEvent
	err := tx.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, session_id::text, sequence,
		       event_type, idempotency_key, actor_type, COALESCE(actor_id::text, ''),
		       payload, projection_attempts, created_at
		FROM research_run_event
		WHERE session_id = $1::uuid AND idempotency_key = $2
	`, sessionID, key).Scan(
		&existing.ID, &existing.WorkspaceID, &existing.SessionID, &existing.Sequence,
		&existing.Type, &existing.IdempotencyKey, &existing.ActorType, &existing.ActorID,
		&existing.Payload, &existing.ProjectionAttempts, &existing.CreatedAt,
	)
	return existing, err
}

func decodeNodeCommandOutcome(event RunEvent) (NodeCommandOutcome, error) {
	outcome, _, err := decodeNodeCommandEvent(event)
	return outcome, err
}

func decodeNodeCommandEvent(event RunEvent) (NodeCommandOutcome, string, error) {
	var wrapper struct {
		Command     NodeCommandOutcome `json:"command"`
		RequestHash string             `json:"request_hash"`
	}
	if err := json.Unmarshal(event.Payload, &wrapper); err != nil {
		return NodeCommandOutcome{}, "", err
	}
	if wrapper.Command.ClientRequestID == "" && wrapper.Command.Task == nil {
		return NodeCommandOutcome{}, "", fmt.Errorf("empty node command payload")
	}
	wrapper.Command.CommandID = event.ID
	wrapper.Command.StateVersion = event.Sequence
	wrapper.Command.Event = event
	return wrapper.Command, wrapper.RequestHash, nil
}

func jsonRawOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
