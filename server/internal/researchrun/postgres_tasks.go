package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ActivateReadyTasks(ctx context.Context, sessionID string) (int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		UPDATE research_task t
		SET status = 'blocked', completed_at = now(),
		    terminal_reason = 'dependency_terminal', updated_at = now()
		FROM research_session s
		WHERE t.session_id = $1::uuid
		  AND s.id = t.session_id
		  AND s.status = 'running'
		  AND t.status = 'pending'
		  AND t.goal_version = s.goal_version
		  AND t.plan_version = s.plan_version
		  AND EXISTS (
		    SELECT 1 FROM research_task_dependency d
		    JOIN research_task dependency ON dependency.id = d.depends_on_task_id
		    WHERE d.task_id = t.id AND dependency.status IN ('failed', 'blocked', 'cancelled')
		  )
		RETURNING t.id::text, t.workspace_id::text, t.goal_version, t.plan_version
	`, sessionID)
	if err != nil {
		return 0, err
	}
	type blockedTask struct {
		id, workspaceID          string
		goalVersion, planVersion int
	}
	blocked := []blockedTask{}
	for rows.Next() {
		var task blockedTask
		if err = rows.Scan(&task.id, &task.workspaceID, &task.goalVersion, &task.planVersion); err != nil {
			rows.Close()
			return 0, err
		}
		blocked = append(blocked, task)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, task := range blocked {
		if _, err = appendEvent(ctx, tx, task.workspaceID, sessionID, "task_blocked", fmt.Sprintf("task-blocked:%s:%d:%d", task.id, task.goalVersion, task.planVersion), "system", "", map[string]any{
			"task_id": task.id, "reason": "dependency_terminal",
		}); err != nil {
			return 0, err
		}
	}
	command, err := tx.Exec(ctx, `
		UPDATE research_task t
		SET status = 'ready', ready_at = now(), updated_at = now()
		FROM research_session s
		WHERE t.session_id = $1::uuid
		  AND s.id = t.session_id
		  AND s.status = 'running'
		  AND t.status = 'pending'
		  AND t.goal_version = s.goal_version
		  AND t.plan_version = s.plan_version
		  AND NOT EXISTS (
		    SELECT 1 FROM research_task_dependency d
		    JOIN research_task dependency ON dependency.id = d.depends_on_task_id
		    WHERE d.task_id = t.id AND dependency.status <> 'succeeded'
		  )
	`, sessionID)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(command.RowsAffected()), nil
}

func (s *PostgresStore) CreateAttempt(ctx context.Context, sessionID, taskID, agentID string) (Attempt, RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)

	var workspaceID, status string
	var goalVersion, planVersion, maxAttempts, attemptCount, maxParallel int
	var readyNow bool
	err = tx.QueryRow(ctx, `
		SELECT t.workspace_id::text, t.status, t.goal_version, t.plan_version,
		       t.max_attempts,
		       COALESCE((s.run_config->>'max_parallel_tasks')::int, 5),
		       COALESCE(t.ready_at <= now(), true)
		FROM research_task t
		JOIN research_session s ON s.id = t.session_id
		WHERE t.id = $1::uuid AND t.session_id = $2::uuid
		FOR UPDATE OF t
	`, taskID, sessionID).Scan(&workspaceID, &status, &goalVersion, &planVersion, &maxAttempts, &maxParallel, &readyNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, RunEvent{}, ErrRunNotFound
	}
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task_attempt WHERE task_id = $1::uuid`, taskID).Scan(&attemptCount); err != nil {
		return Attempt{}, RunEvent{}, err
	}
	var runStatus string
	var currentGoal, currentPlan int
	err = tx.QueryRow(ctx, `SELECT status, goal_version, plan_version FROM research_session WHERE id = $1::uuid FOR UPDATE`, sessionID).Scan(&runStatus, &currentGoal, &currentPlan)
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if runStatus != string(RunStatusRunning) || status != string(TaskStatusReady) || !readyNow || goalVersion != currentGoal || planVersion != currentPlan {
		return Attempt{}, RunEvent{}, fmt.Errorf("%w: task is not dispatchable", ErrInvalidTransition)
	}
	if attemptCount >= maxAttempts {
		return Attempt{}, RunEvent{}, fmt.Errorf("%w: task attempt budget exhausted", ErrInvalidTransition)
	}
	var activeCount int
	if err = tx.QueryRow(ctx, `
		SELECT count(*)::int FROM research_task_attempt
		WHERE session_id = $1::uuid AND status IN ('dispatching', 'running')
	`, sessionID).Scan(&activeCount); err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if activeCount >= maxParallel {
		return Attempt{}, RunEvent{}, fmt.Errorf("%w: run parallel task limit reached", ErrInvalidTransition)
	}
	attemptNumber := attemptCount + 1
	dispatchKey := fmt.Sprintf("research:%s:task:%s:attempt:%d", sessionID, taskID, attemptNumber)
	var attempt Attempt
	err = tx.QueryRow(ctx, `
		INSERT INTO research_task_attempt (
			workspace_id, session_id, task_id, attempt_number, assigned_agent_id,
			dispatch_key, status
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6, 'dispatching')
		RETURNING id::text, session_id::text, workspace_id::text, task_id::text,
		          attempt_number, assigned_agent_id::text, '', dispatch_key, '', status,
		          '', failure_class, diagnostics, dispatched_at, started_at,
		          result_submitted_at, completed_at
	`, workspaceID, sessionID, taskID, attemptNumber, agentID, dispatchKey).Scan(
		&attempt.ID, &attempt.SessionID, &attempt.WorkspaceID, &attempt.TaskID,
		&attempt.AttemptNumber, &attempt.AssignedAgentID, &attempt.InboxTaskID,
		&attempt.DispatchKey, &attempt.ClientRequestID, &attempt.Status,
		&attempt.ResultHash, &attempt.FailureClass, &attempt.Diagnostics,
		&attempt.DispatchedAt, &attempt.StartedAt, &attempt.ResultSubmittedAt,
		&attempt.CompletedAt,
	)
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_task SET status = 'dispatching', assigned_agent_id = $2::uuid,
		       updated_at = now() WHERE id = $1::uuid
	`, taskID, agentID); err != nil {
		return Attempt{}, RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "task_dispatching", dispatchKey, "system", "", map[string]any{
		"task_id": taskID, "attempt_id": attempt.ID, "attempt_number": attemptNumber, "agent_id": agentID,
	})
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Attempt{}, RunEvent{}, err
	}
	return attempt, event, nil
}

func (s *PostgresStore) AttachInboxTask(ctx context.Context, attemptID, inboxTaskID string) (Attempt, RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var attempt Attempt
	err = tx.QueryRow(ctx, `
		UPDATE research_task_attempt
		SET inbox_task_id = $2::uuid, status = 'running', started_at = COALESCE(started_at, now()), updated_at = now()
		WHERE id = $1::uuid AND status = 'dispatching'
		RETURNING id::text, session_id::text, workspace_id::text, task_id::text,
		          attempt_number, assigned_agent_id::text, inbox_task_id::text,
		          dispatch_key, COALESCE(client_request_id, ''), status,
		          COALESCE(result_hash, ''), failure_class, diagnostics, dispatched_at,
		          started_at, result_submitted_at, completed_at
	`, attemptID, inboxTaskID).Scan(&attempt.ID, &attempt.SessionID, &attempt.WorkspaceID,
		&attempt.TaskID, &attempt.AttemptNumber, &attempt.AssignedAgentID,
		&attempt.InboxTaskID, &attempt.DispatchKey, &attempt.ClientRequestID,
		&attempt.Status, &attempt.ResultHash, &attempt.FailureClass,
		&attempt.Diagnostics, &attempt.DispatchedAt, &attempt.StartedAt,
		&attempt.ResultSubmittedAt, &attempt.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		row := tx.QueryRow(ctx, attemptSelectSQL+` WHERE a.id = $1::uuid`, attemptID)
		attempt, err = scanAttempt(row)
		if err != nil {
			return Attempt{}, RunEvent{}, fmt.Errorf("%w: attach attempt", ErrInvalidTransition)
		}
		if attempt.InboxTaskID == inboxTaskID && (attempt.Status == AttemptStatusRunning || attempt.Status == AttemptStatusSucceeded) {
			return attempt, RunEvent{}, tx.Commit(ctx)
		}
		return Attempt{}, RunEvent{}, fmt.Errorf("%w: attempt is not dispatching", ErrInvalidTransition)
	}
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_task SET status = 'running', started_at = COALESCE(started_at, now()), updated_at = now() WHERE id = $1::uuid`, attempt.TaskID); err != nil {
		return Attempt{}, RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, attempt.WorkspaceID, attempt.SessionID, "task_started", "task-started:"+attempt.ID, "system", "", map[string]any{
		"task_id": attempt.TaskID, "attempt_id": attempt.ID, "inbox_task_id": inboxTaskID, "agent_id": attempt.AssignedAgentID,
	})
	if err != nil {
		return Attempt{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Attempt{}, RunEvent{}, err
	}
	return attempt, event, nil
}

func (s *PostgresStore) FailAttempt(ctx context.Context, in AttemptFailure) (RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var sessionID, workspaceID, taskID, assignedAgentID string
	var attemptNumber, maxAttempts int
	err = tx.QueryRow(ctx, `
		SELECT a.session_id::text, a.workspace_id::text, a.task_id::text,
		       a.assigned_agent_id::text,
		       a.attempt_number, t.max_attempts
		FROM research_task_attempt a JOIN research_task t ON t.id = a.task_id
		WHERE a.id = $1::uuid AND a.status IN ('dispatching', 'running')
		FOR UPDATE OF a, t
	`, in.AttemptID).Scan(&sessionID, &workspaceID, &taskID, &assignedAgentID, &attemptNumber, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, fmt.Errorf("%w: attempt is terminal", ErrInvalidTransition)
	}
	if err != nil {
		return RunEvent{}, err
	}
	diagnostics := truncateBytes(in.Diagnostics, 4096)
	if _, err = tx.Exec(ctx, `
		UPDATE research_task_attempt
		SET status = 'failed', failure_class = $2, diagnostics = $3,
		    completed_at = now(), updated_at = now()
		WHERE id = $1::uuid
	`, in.AttemptID, truncateBytes(in.FailureClass, 160), diagnostics); err != nil {
		return RunEvent{}, err
	}
	nextStatus := TaskStatusBlocked
	retryAt := time.Now().UTC()
	if in.Retryable && attemptNumber < maxAttempts {
		nextStatus = TaskStatusReady
		retryAt = retryAt.Add(taskRetryBackoff(attemptNumber))
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_task SET status = $2, ready_at = CASE WHEN $2 = 'ready' THEN $4 ELSE ready_at END,
		       terminal_reason = $3, updated_at = now(), completed_at = CASE WHEN $2 = 'blocked' THEN now() ELSE NULL END
		WHERE id = $1::uuid
	`, taskID, nextStatus, truncateBytes(in.FailureClass, 160), retryAt); err != nil {
		return RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "task_attempt_failed", "attempt-failed:"+in.AttemptID, "system", "", map[string]any{
		"task_id": taskID, "attempt_id": in.AttemptID, "failure_class": in.FailureClass,
		"agent_id": assignedAgentID, "diagnostics": diagnostics, "retryable": nextStatus == TaskStatusReady,
	})
	if err != nil {
		return RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return RunEvent{}, err
	}
	return event, nil
}

func taskRetryBackoff(attemptNumber int) time.Duration {
	if attemptNumber < 1 {
		attemptNumber = 1
	}
	return time.Duration(5*(1<<min(attemptNumber-1, 6))) * time.Second
}

func (s *PostgresStore) CreateControlTask(ctx context.Context, in ControlTaskInput) (Task, RunEvent, error) {
	if !validTaskKind(in.Kind) || in.Kind == TaskKindPlan {
		return Task{}, RunEvent{}, fmt.Errorf("%w: invalid control task kind", ErrInvalidTransition)
	}
	if strings.TrimSpace(in.SessionID) == "" || strings.TrimSpace(in.Objective) == "" || !validCapability(in.Capability) || in.Priority < 0 || in.Priority > 1 {
		return Task{}, RunEvent{}, fmt.Errorf("%w: invalid control task input", ErrInvalidTransition)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Task{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var workspaceID string
	var goalVersion, planVersion, maxTasks, maxAttempts, timeout int
	var status, orchestratorVersion string
	err = tx.QueryRow(ctx, `
		SELECT workspace_id::text, goal_version, plan_version, status, orchestrator_version,
		       COALESCE((run_config->>'max_tasks')::int, 60),
		       COALESCE((run_config->>'max_attempts_per_task')::int, 3),
		       COALESCE((run_config->>'task_timeout_seconds')::int, 1800)
		FROM research_session WHERE id = $1::uuid FOR UPDATE
	`, in.SessionID).Scan(&workspaceID, &goalVersion, &planVersion, &status, &orchestratorVersion, &maxTasks, &maxAttempts, &timeout)
	if err != nil {
		return Task{}, RunEvent{}, err
	}
	if status != string(RunStatusRunning) {
		return Task{}, RunEvent{}, fmt.Errorf("%w: run is not running", ErrInvalidTransition)
	}
	var questionArg any
	questionKey := ""
	if strings.TrimSpace(in.QuestionID) != "" {
		if in.Kind != TaskKindDiscover && in.Kind != TaskKindDeepRead && in.Kind != TaskKindVerify && in.Kind != TaskKindCounterSearch {
			return Task{}, RunEvent{}, fmt.Errorf("%w: control task kind cannot target a question", ErrInvalidTransition)
		}
		err = tx.QueryRow(ctx, `
			WITH supported AS (
			  SELECT DISTINCT evidence.claim_id
			  FROM research_claim_evidence evidence
			  JOIN research_observation observation ON observation.id = evidence.observation_id
			  JOIN research_source_snapshot source ON source.id = observation.source_snapshot_id
			  WHERE evidence.session_id = $2::uuid AND evidence.relation = 'supports'
			    AND evidence.verification_status = 'verified'
			    AND observation.verification_status = 'verified'
			    AND source.verification_status = 'verified'
			)
			SELECT question.client_key
			FROM research_question question
			WHERE question.id = $1::uuid AND question.session_id = $2::uuid
			  AND question.goal_version = $3 AND question.plan_version = $4 AND question.required
			  AND (question.status <> 'answered' OR question.coverage < 0.8 OR question.answer_claim_id IS NULL
			       OR NOT EXISTS (SELECT 1 FROM supported WHERE supported.claim_id = question.answer_claim_id))
			FOR UPDATE
		`, in.QuestionID, in.SessionID, goalVersion, planVersion).Scan(&questionKey)
		if errors.Is(err, pgx.ErrNoRows) {
			return Task{}, RunEvent{}, fmt.Errorf("%w: question is absent, stale, or already answered", ErrControlTargetChanged)
		}
		if err != nil {
			return Task{}, RunEvent{}, err
		}
		questionArg = in.QuestionID
	}
	row := tx.QueryRow(ctx, taskSelectSQL+`
		WHERE t.session_id = $1::uuid AND t.goal_version = $2 AND t.plan_version = $3
		  AND t.kind = $4 AND t.objective = $5
		  AND (($6::uuid IS NULL AND t.question_id IS NULL) OR t.question_id = $6::uuid)
		  AND t.status IN ('pending', 'ready', 'dispatching', 'running')
		ORDER BY t.created_at DESC LIMIT 1
	`, in.SessionID, goalVersion, planVersion, in.Kind, in.Objective, questionArg)
	if existing, scanErr := scanTask(row); scanErr == nil {
		return existing, RunEvent{}, tx.Commit(ctx)
	} else if !errors.Is(scanErr, pgx.ErrNoRows) {
		return Task{}, RunEvent{}, scanErr
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid`, in.SessionID).Scan(&count); err != nil {
		return Task{}, RunEvent{}, err
	}
	if count >= maxTasks {
		return Task{}, RunEvent{}, fmt.Errorf("%w: run task budget exhausted", ErrBudgetExhausted)
	}
	var kindSequence int
	if err = tx.QueryRow(ctx, `
		SELECT count(*)::int + 1 FROM research_task
		WHERE session_id = $1::uuid AND goal_version = $2 AND plan_version = $3 AND kind = $4
	`, in.SessionID, goalVersion, planVersion, in.Kind).Scan(&kindSequence); err != nil {
		return Task{}, RunEvent{}, err
	}
	clientKey := fmt.Sprintf("control:%s:%d:%d:%d", in.Kind, goalVersion, planVersion, kindSequence)
	expected := expectedResultForTaskVersion(orchestratorVersion, in.Kind)
	findingCodes := sortedFindingCodes(in.Findings)
	acceptanceCriteria, _ := json.Marshal(map[string]any{
		"schema_version": resultSchemaVersionForOrchestrator(orchestratorVersion),
		"remediation": map[string]any{
			"finding_codes": findingCodes, "question_id": in.QuestionID, "question_key": questionKey,
		},
	})
	var taskID string
	err = tx.QueryRow(ctx, `
		INSERT INTO research_task (
			workspace_id, session_id, question_id, client_key, kind, objective,
			required_capability, expected_result, acceptance_criteria, priority,
			status, goal_version, plan_version, max_attempts, timeout_seconds, ready_at
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8,
			          $9, $10, 'ready', $11, $12, $13, $14, now())
		RETURNING id::text
		`, workspaceID, in.SessionID, questionArg, clientKey, in.Kind, in.Objective, in.Capability, expected, acceptanceCriteria,
		in.Priority, goalVersion, planVersion, maxAttempts, timeout).Scan(&taskID)
	if err != nil {
		return Task{}, RunEvent{}, err
	}
	decisionInputs, _ := json.Marshal(map[string]any{
		"observed_findings": in.ObservedFindings, "target_findings": in.Findings,
	})
	decisionOutcome, _ := json.Marshal(map[string]any{
		"task_id": taskID, "task_kind": in.Kind, "required_capability": in.Capability,
		"question_id": in.QuestionID, "question_key": questionKey, "finding_codes": findingCodes,
	})
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_decision (
		  workspace_id, session_id, decision_kind, actor_type, goal_version, plan_version,
		  inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, 'remediation_routing', 'system', $3, $4, $5::jsonb, $6::jsonb, $7)
	`, workspaceID, in.SessionID, goalVersion, planVersion, decisionInputs, decisionOutcome, in.Rationale); err != nil {
		return Task{}, RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, in.SessionID, "control_task_created", "control-task:"+taskID, "system", "", map[string]any{
		"task_id": taskID, "kind": in.Kind, "objective": in.Objective,
		"required_capability": in.Capability, "expected_result": expected,
		"question_id": in.QuestionID, "question_key": questionKey, "finding_codes": findingCodes,
	})
	if err != nil {
		return Task{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, RunEvent{}, err
	}
	task, err := s.GetTask(ctx, taskID, in.SessionID)
	return task, event, err
}

func sortedFindingCodes(findings []GateFinding) []string {
	seen := make(map[string]struct{}, len(findings))
	for _, finding := range findings {
		if code := strings.TrimSpace(finding.Code); code != "" {
			seen[code] = struct{}{}
		}
	}
	codes := make([]string, 0, len(seen))
	for code := range seen {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func (s *PostgresStore) SetAwaitingConfirmation(ctx context.Context, sessionID string, gate GateResult) (Run, RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	var workspaceID string
	var status string
	err = tx.QueryRow(ctx, `SELECT workspace_id::text, status FROM research_session WHERE id = $1::uuid FOR UPDATE`, sessionID).Scan(&workspaceID, &status)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if status == string(RunStatusAwaitingUserConfirm) {
		if err = tx.Commit(ctx); err != nil {
			return Run{}, RunEvent{}, err
		}
		run, err := s.GetRun(ctx, sessionID, workspaceID)
		return run, RunEvent{}, err
	}
	if status != string(RunStatusRunning) || !gate.Passed {
		return Run{}, RunEvent{}, fmt.Errorf("%w: delivery gate has not passed", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET status = 'awaiting_user_confirm', current_stage = 's4_delivery', last_progress_at = now(), updated_at = now() WHERE id = $1::uuid`, sessionID); err != nil {
		return Run{}, RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "run_awaiting_confirmation", "awaiting-confirmation", "system", "", map[string]any{"gate": gate})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, err
	}
	run, err := s.GetRun(ctx, sessionID, workspaceID)
	return run, event, err
}

func (s *PostgresStore) Complete(ctx context.Context, sessionID, workspaceID, userID string) (Run, RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	run, err := loadRunForUpdate(ctx, tx, sessionID, workspaceID)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if run.Status == RunStatusCompleted {
		return run, RunEvent{}, tx.Commit(ctx)
	}
	if run.Status != RunStatusAwaitingUserConfirm {
		return Run{}, RunEvent{}, fmt.Errorf("%w: only a delivery-ready run can be completed", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET status = 'completed', stop_reason = 'user_confirmed', updated_at = now() WHERE id = $1::uuid`, sessionID); err != nil {
		return Run{}, RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "run_completed", "run-completed", "user", userID, map[string]any{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, err
	}
	run, err = s.GetRun(ctx, sessionID, workspaceID)
	return run, event, err
}

func (s *PostgresStore) Pause(ctx context.Context, sessionID, workspaceID, userID string) (Run, RunEvent, []string, error) {
	return s.transitionRun(ctx, sessionID, workspaceID, userID, RunStatusPaused, "user_paused", true)
}

func (s *PostgresStore) Resume(ctx context.Context, sessionID, workspaceID, userID string) (Run, RunEvent, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	defer tx.Rollback(ctx)
	run, err := loadRunForUpdate(ctx, tx, sessionID, workspaceID)
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if run.Status == RunStatusRunning {
		return run, RunEvent{}, tx.Commit(ctx)
	}
	if run.Status != RunStatusPaused {
		return Run{}, RunEvent{}, fmt.Errorf("%w: only paused runs can resume", ErrInvalidTransition)
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET status = 'running', stop_reason = '', next_reconcile_at = now(), last_progress_at = now(), updated_at = now() WHERE id = $1::uuid`, sessionID); err != nil {
		return Run{}, RunEvent{}, err
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, "run_resumed", fmt.Sprintf("run-resumed:%d", run.StateVersion+1), "user", userID, map[string]any{})
	if err != nil {
		return Run{}, RunEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, err
	}
	run, err = s.GetRun(ctx, sessionID, workspaceID)
	return run, event, err
}

func (s *PostgresStore) Cancel(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, RunEvent, []string, error) {
	return s.transitionRun(ctx, sessionID, workspaceID, userID, RunStatusCancelled, reason, false)
}

func (s *PostgresStore) Archive(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, RunEvent, []string, error) {
	return s.transitionRun(ctx, sessionID, workspaceID, userID, RunStatusArchived, reason, false)
}

func (s *PostgresStore) MarkFailed(ctx context.Context, sessionID, reason string) (Run, RunEvent, []string, error) {
	var workspaceID string
	if err := s.pool.QueryRow(ctx, `SELECT workspace_id::text FROM research_session WHERE id = $1::uuid`, sessionID).Scan(&workspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, RunEvent{}, nil, ErrRunNotFound
		}
		return Run{}, RunEvent{}, nil, err
	}
	return s.transitionRun(ctx, sessionID, workspaceID, "", RunStatusFailed, reason, false)
}

func (s *PostgresStore) transitionRun(ctx context.Context, sessionID, workspaceID, userID string, target RunStatus, reason string, retryTasks bool) (Run, RunEvent, []string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	defer tx.Rollback(ctx)
	run, err := loadRunForUpdate(ctx, tx, sessionID, workspaceID)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if run.Status == target {
		return run, RunEvent{}, nil, tx.Commit(ctx)
	}
	if run.Status == RunStatusCompleted || run.Status == RunStatusArchived || run.Status == RunStatusCancelled {
		return Run{}, RunEvent{}, nil, fmt.Errorf("%w: run is terminal", ErrInvalidTransition)
	}
	rows, err := tx.Query(ctx, `
		SELECT inbox_task_id::text FROM research_task_attempt
		WHERE session_id = $1::uuid AND status IN ('dispatching', 'running') AND inbox_task_id IS NOT NULL
	`, sessionID)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	var inboxIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Run{}, RunEvent{}, nil, err
		}
		inboxIDs = append(inboxIDs, id)
	}
	rows.Close()
	if _, err = tx.Exec(ctx, `UPDATE research_task_attempt SET status = 'cancelled', failure_class = $2, completed_at = now(), updated_at = now() WHERE session_id = $1::uuid AND status IN ('dispatching', 'running')`, sessionID, target); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if retryTasks {
		_, err = tx.Exec(ctx, `UPDATE research_task SET status = 'ready', ready_at = now(), assigned_agent_id = NULL, updated_at = now() WHERE session_id = $1::uuid AND status IN ('dispatching', 'running')`, sessionID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE research_task SET status = 'cancelled', terminal_reason = $2, completed_at = now(), updated_at = now() WHERE session_id = $1::uuid AND status IN ('pending', 'ready', 'dispatching', 'running')`, sessionID, truncateBytes(reason, 1024))
	}
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET status = $2, stop_reason = $3, reconcile_lease_token = NULL, reconcile_lease_expires_at = NULL, updated_at = now() WHERE id = $1::uuid`, sessionID, target, truncateBytes(reason, 1024)); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	eventType := "run_" + string(target)
	actorType := "user"
	if userID == "" {
		actorType = "system"
	}
	event, err := appendEvent(ctx, tx, workspaceID, sessionID, eventType, fmt.Sprintf("%s:%d", eventType, run.StateVersion+1), actorType, userID, map[string]any{"reason": reason, "cancelled_inbox_tasks": len(inboxIDs)})
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	run, err = s.GetRun(ctx, sessionID, workspaceID)
	return run, event, inboxIDs, err
}

func (s *PostgresStore) Steer(ctx context.Context, in SteerInput) (Run, RunEvent, []string, error) {
	goal := strings.TrimSpace(in.Goal)
	if goal == "" || len(goal) > 32<<10 || len(in.Reason) > 4096 {
		return Run{}, RunEvent{}, nil, fmt.Errorf("%w: invalid steering goal or reason", ErrInvalidContract)
	}
	for name, value := range map[string]*string{"audience": in.Audience, "freshness": in.Freshness} {
		if value != nil && len(strings.TrimSpace(*value)) > 1024 {
			return Run{}, RunEvent{}, nil, fmt.Errorf("%w: %s exceeds 1024 bytes", ErrInvalidContract, name)
		}
	}
	if in.Language != nil && len(strings.TrimSpace(*in.Language)) > 64 {
		return Run{}, RunEvent{}, nil, fmt.Errorf("%w: language exceeds 64 bytes", ErrInvalidContract)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	defer tx.Rollback(ctx)
	run, err := loadRunForUpdate(ctx, tx, in.SessionID, in.WorkspaceID)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if run.Status == RunStatusCompleted || run.Status == RunStatusArchived || run.Status == RunStatusCancelled {
		return Run{}, RunEvent{}, nil, fmt.Errorf("%w: terminal run cannot be steered", ErrInvalidTransition)
	}
	var currentScope, currentSourcePolicy []byte
	var currentAudience, currentFreshness, currentLanguage string
	if err = tx.QueryRow(ctx, `
		SELECT scope, audience, freshness, language, source_policy
		FROM research_contract_revision
		WHERE session_id = $1::uuid AND goal_version = $2
	`, in.SessionID, run.GoalVersion).Scan(
		&currentScope, &currentAudience, &currentFreshness, &currentLanguage, &currentSourcePolicy,
	); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	resolvedConfig, err := resolveRunConfig(run.Config, in.RunLimits)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	resolvedLimits, err := json.Marshal(resolvedConfig)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	resolvedScope, err := resolveContractObject(currentScope, in.Scope, "scope")
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	resolvedSourcePolicy, err := resolveContractObject(currentSourcePolicy, in.SourcePolicy, "source_policy")
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	resolvedAudience := currentAudience
	if in.Audience != nil {
		resolvedAudience = strings.TrimSpace(*in.Audience)
	}
	resolvedFreshness := currentFreshness
	if in.Freshness != nil {
		resolvedFreshness = strings.TrimSpace(*in.Freshness)
	}
	resolvedLanguage := currentLanguage
	if in.Language != nil {
		resolvedLanguage = strings.TrimSpace(*in.Language)
	}
	newGoalVersion := run.GoalVersion + 1
	newPlanVersion := run.PlanVersion + 1
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_contract_revision (
			workspace_id, session_id, goal_version, goal, scope, audience,
			freshness, language, source_policy, run_limits, authored_by, reason
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11::uuid, $12)
	`, in.WorkspaceID, in.SessionID, newGoalVersion, goal, resolvedScope, resolvedAudience,
		resolvedFreshness, resolvedLanguage, resolvedSourcePolicy, resolvedLimits, in.UserID, truncateBytes(in.Reason, 4096)); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT inbox_task_id::text FROM research_task_attempt WHERE session_id = $1::uuid AND status IN ('dispatching', 'running') AND inbox_task_id IS NOT NULL`, in.SessionID)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	var cancelIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Run{}, RunEvent{}, nil, err
		}
		cancelIDs = append(cancelIDs, id)
	}
	rows.Close()
	if in.AllowRunningFinish {
		cancelIDs = nil
	} else {
		if _, err = tx.Exec(ctx, `UPDATE research_task_attempt SET status = 'cancelled', failure_class = 'goal_steered', completed_at = now(), updated_at = now() WHERE session_id = $1::uuid AND status IN ('dispatching', 'running')`, in.SessionID); err != nil {
			return Run{}, RunEvent{}, nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE research_task SET status = 'obsolete', terminal_reason = 'goal_steered', completed_at = now(), updated_at = now() WHERE session_id = $1::uuid AND status IN ('dispatching', 'running')`, in.SessionID); err != nil {
			return Run{}, RunEvent{}, nil, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE research_task SET status = 'obsolete', terminal_reason = 'goal_steered', completed_at = now(), updated_at = now() WHERE session_id = $1::uuid AND status IN ('pending', 'ready')`, in.SessionID); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_question SET status = 'obsolete', terminal_explanation = 'goal_steered', updated_at = now() WHERE session_id = $1::uuid AND status IN ('open', 'in_progress')`, in.SessionID); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET goal = $2, goal_version = $3, plan_version = $4, run_config = $5, status = 'running', current_stage = 's1_plan', last_progress_at = now(), next_reconcile_at = now(), stop_reason = '', updated_at = now() WHERE id = $1::uuid`, in.SessionID, goal, newGoalVersion, newPlanVersion, resolvedLimits); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	var rootQuestionID string
	err = tx.QueryRow(ctx, `INSERT INTO research_question (workspace_id, session_id, client_key, kind, question, required, status, priority, impact, uncertainty, novelty, coverage, goal_version, plan_version) VALUES ($1::uuid, $2::uuid, 'root', 'dimension', $3, false, 'in_progress', 1, 1, 0.8, 1, 0, $4, $5) RETURNING id::text`, in.WorkspaceID, in.SessionID, goal, newGoalVersion, newPlanVersion).Scan(&rootQuestionID)
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "goal_steered", fmt.Sprintf("goal-steered:%d", newGoalVersion), "user", in.UserID, map[string]any{"goal": goal, "goal_version": newGoalVersion, "plan_version": newPlanVersion, "reason": in.Reason, "allow_running_finish": in.AllowRunningFinish})
	if err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, RunEvent{}, nil, err
	}
	run, err = s.GetRun(ctx, in.SessionID, in.WorkspaceID)
	return run, event, cancelIDs, err
}

func expectedResultForTaskVersion(version string, kind TaskKind) string {
	suffix := "v1"
	if version == OrchestratorVersionV2 {
		suffix = "v2"
	} else if version == OrchestratorVersionV3 {
		suffix = "v3"
	} else if version == OrchestratorVersionV4 {
		suffix = "v4"
	}
	switch kind {
	case TaskKindPlan, TaskKindReplan:
		return "research_plan_" + suffix
	case TaskKindSynthesize:
		return "research_report_" + suffix
	case TaskKindQualityGate:
		return "research_quality_evaluation_" + suffix
	case TaskKindCitationAudit:
		return "research_citation_audit_" + suffix
	default:
		return "research_evidence_" + suffix
	}
}

func resultSchemaVersionForOrchestrator(version string) int {
	if version == OrchestratorVersionV2 {
		return 2
	} else if version == OrchestratorVersionV3 {
		return 3
	} else if version == OrchestratorVersionV4 {
		return 4
	}
	return 1
}

func truncateBytes(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	for max > 0 && (value[max]&0xc0) == 0x80 {
		max--
	}
	return value[:max]
}
