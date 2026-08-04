package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) NodeCommand(ctx context.Context, in NodeCommandInput) (NodeCommandOutcome, error) {
	if err := validateNodeCommandInput(in); err != nil {
		return NodeCommandOutcome{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	defer tx.Rollback(ctx)

	var (
		workspaceID, status, orchestratorVersion string
		goalVersion, planVersion, maxTasks, maxAttempts, timeout int
		stateVersion int64
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

	switch RunStatus(status) {
	case RunStatusCompleted, RunStatusCancelled, RunStatusArchived, RunStatusFailed:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeSessionTerminal, "调研已结束，无法继续或分叉")
	case RunStatusPaused:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeRunNotRunning, "调研已暂停，请先恢复后再操作")
	case RunStatusDrafting, RunStatusAwaitingUserConfirm:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeRunNotRunning, "当前阶段不可续研或分叉，请稍后再试")
	case RunStatusRunning:
		// ok
	default:
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeRunNotRunning, "当前会话状态不允许该操作")
	}

	if in.ExpectedStateVersion != nil && *in.ExpectedStateVersion != stateVersion {
		return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeStateVersionConflict, "画布已更新，请刷新后重试")
	}

	eventKey := nodeCommandClientKey(in.ClientRequestID, "event")
	if existing, loadErr := loadNodeCommandEvent(ctx, tx, in.SessionID, eventKey); loadErr == nil {
		outcome, decodeErr := decodeNodeCommandOutcome(existing)
		if decodeErr != nil {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeIdempotencyConflict, "相同请求标识已用于其他操作，请换新标识重试")
		}
		outcome.Replayed = true
		outcome.Event = existing
		return outcome, tx.Commit(ctx)
	} else if !errors.Is(loadErr, pgx.ErrNoRows) {
		return NodeCommandOutcome{}, loadErr
	}

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
	if err = tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid`, in.SessionID).Scan(&taskCount); err != nil {
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
		"strategy":           strings.TrimSpace(in.Strategy),
		"source_constraints": jsonRawOrNil(in.SourceConstraints),
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
		err = tx.QueryRow(ctx, `
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
	if strat := strings.TrimSpace(in.Strategy); strat != "" {
		capability = truncateRunes(strat, 64)
	}
	var taskID string
	err = tx.QueryRow(ctx, `
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
		"source_node_id":     in.NodeID,
		"question_id":        questionID,
		"task_id":            taskID,
		"parent_question_id": outcome.ParentLineage.ParentQuestionID,
		"parent_task_id":     outcome.ParentLineage.ParentTaskID,
		"queued":             true,
	}
	event, err := appendEvent(ctx, tx, workspaceID, in.SessionID, "node_command_"+in.Action, eventKey, in.ActorType, in.ActorID, payload)
	if err != nil {
		if errors.Is(err, ErrResultConflict) {
			return NodeCommandOutcome{}, denyNodeCommand(NodeCmdCodeIdempotencyConflict, "相同请求标识已用于其他操作，请换新标识重试")
		}
		return NodeCommandOutcome{}, err
	}
	outcome.CommandID = event.ID
	outcome.StateVersion = event.Sequence
	outcome.Event = event
	// Re-embed final command_id into a consistent replay payload by updating is hard;
	// replay loads from event payload — patch payload.command.command_id in memory for response.
	if err = tx.Commit(ctx); err != nil {
		return NodeCommandOutcome{}, err
	}
	return outcome, nil
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
	var wrapper struct {
		Command NodeCommandOutcome `json:"command"`
	}
	if err := json.Unmarshal(event.Payload, &wrapper); err != nil {
		return NodeCommandOutcome{}, err
	}
	if wrapper.Command.ClientRequestID == "" && wrapper.Command.Task == nil {
		return NodeCommandOutcome{}, fmt.Errorf("empty node command payload")
	}
	wrapper.Command.CommandID = event.ID
	wrapper.Command.StateVersion = event.Sequence
	wrapper.Command.Event = event
	return wrapper.Command, nil
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
