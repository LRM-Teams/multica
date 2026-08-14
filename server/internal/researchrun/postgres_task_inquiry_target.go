package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) BindTaskInquiryTargets(ctx context.Context, in BindTaskInquiryTargetsInput) (BindTaskInquiryTargetsResult, error) {
	if err := (taskInquiryTargetModule{}).ValidateBind(in); err != nil {
		return BindTaskInquiryTargetsResult{}, err
	}
	payload := taskInquiryTargetsEventPayload(in)
	tx, err := s.beginResearchTx(ctx, txOpTaskInquiryTargetsBind, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return BindTaskInquiryTargetsResult{}, err
	}
	defer tx.Rollback(ctx)

	if event, found, loadErr := loadMatchingTaskInquiryTargetsEvent(ctx, tx, in, payload); loadErr != nil {
		return BindTaskInquiryTargetsResult{}, loadErr
	} else if found {
		if err = s.commitResearchTx(ctx, txOpTaskInquiryTargetsBind, tx); err != nil {
			return BindTaskInquiryTargetsResult{}, err
		}
		return BindTaskInquiryTargetsResult{Event: event, Replayed: true}, nil
	}

	var stateVersion int64
	var goalVersion, planVersion int32
	var assignedAgent, attemptStatus string
	if err = tx.QueryRow(ctx, `
		SELECT session.state_version, task.goal_version, task.plan_version,
		       attempt.assigned_agent_id::text, attempt.status
		FROM research_session session
		JOIN research_task_attempt attempt
		  ON attempt.workspace_id=session.workspace_id AND attempt.session_id=session.id
		JOIN research_task task
		  ON task.workspace_id=attempt.workspace_id AND task.session_id=attempt.session_id AND task.id=attempt.task_id
		WHERE session.workspace_id=$1::uuid AND session.id=$2::uuid AND attempt.id=$3::uuid
		FOR UPDATE OF session, attempt, task
	`, in.WorkspaceID, in.SessionID, in.AttemptID).Scan(
		&stateVersion, &goalVersion, &planVersion, &assignedAgent, &attemptStatus,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BindTaskInquiryTargetsResult{}, ErrRunNotFound
		}
		return BindTaskInquiryTargetsResult{}, err
	}
	if stateVersion != in.ExpectedStateVersion {
		return BindTaskInquiryTargetsResult{}, fmt.Errorf("%w: Task Inquiry target state version changed", ErrControlTargetChanged)
	}
	if assignedAgent != in.AgentID || (attemptStatus != string(AttemptStatusRunning) && attemptStatus != string(AttemptStatusSucceeded)) {
		return BindTaskInquiryTargetsResult{}, fmt.Errorf("%w: Task Inquiry target producer is not the assigned active attempt", ErrInvalidTransition)
	}

	for _, target := range payload.Targets {
		var targetGoalVersion, targetPlanVersion int32
		if err = tx.QueryRow(ctx, `SELECT goal_version,plan_version FROM research_task
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`,
			in.WorkspaceID, in.SessionID, target.TaskID).Scan(&targetGoalVersion, &targetPlanVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return BindTaskInquiryTargetsResult{}, fmt.Errorf("%w: Task Inquiry target task is outside the Run", ErrInvalidContract)
			}
			return BindTaskInquiryTargetsResult{}, err
		}
		if targetGoalVersion != goalVersion || targetPlanVersion != planVersion {
			return BindTaskInquiryTargetsResult{}, fmt.Errorf("%w: Task Inquiry target crosses Goal or Plan versions", ErrInvalidTransition)
		}
		command, execErr := tx.Exec(ctx, `INSERT INTO research_task_inquiry_target
			(workspace_id,session_id,task_id,target_kind,target_entity_id,goal_version,plan_version,bound_by_attempt_id,ordinal)
			SELECT $1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8::uuid,
			       COALESCE((
			         SELECT max(existing.ordinal)+1
			         FROM research_task_inquiry_target existing
			         WHERE existing.workspace_id=$1::uuid AND existing.session_id=$2::uuid AND existing.task_id=$3::uuid
			       ), 0)
			ON CONFLICT (workspace_id,session_id,task_id,target_kind,target_entity_id) DO NOTHING`,
			in.WorkspaceID, in.SessionID, target.TaskID, string(target.Kind), target.EntityID, goalVersion, planVersion, in.AttemptID)
		if execErr != nil {
			return BindTaskInquiryTargetsResult{}, execErr
		}
		if command.RowsAffected() == 0 {
			var storedGoalVersion, storedPlanVersion int32
			var storedAttemptID string
			if err = tx.QueryRow(ctx, `SELECT goal_version,plan_version,bound_by_attempt_id::text
				FROM research_task_inquiry_target
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND task_id=$3::uuid
				  AND target_kind=$4 AND target_entity_id=$5::uuid FOR UPDATE`,
				in.WorkspaceID, in.SessionID, target.TaskID, string(target.Kind), target.EntityID).Scan(
				&storedGoalVersion, &storedPlanVersion, &storedAttemptID,
			); err != nil {
				return BindTaskInquiryTargetsResult{}, err
			}
			if storedGoalVersion != goalVersion || storedPlanVersion != planVersion || storedAttemptID != in.AttemptID {
				return BindTaskInquiryTargetsResult{}, fmt.Errorf("%w: Task Inquiry target already has different provenance", ErrResultConflict)
			}
		}
	}

	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "task_inquiry_targets_bound", in.IdempotencyKey, "agent", in.AgentID, payload)
	if err != nil {
		return BindTaskInquiryTargetsResult{}, err
	}
	if err = s.commitResearchTx(ctx, txOpTaskInquiryTargetsBind, tx); err != nil {
		return BindTaskInquiryTargetsResult{}, err
	}
	return BindTaskInquiryTargetsResult{Event: event}, nil
}

func loadMatchingTaskInquiryTargetsEvent(ctx context.Context, tx pgx.Tx, in BindTaskInquiryTargetsInput, payload any) (RunEvent, bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return RunEvent{}, false, err
	}
	var event RunEvent
	err = tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,session_id::text,sequence,event_type,idempotency_key,actor_type,
		COALESCE(actor_id::text,''),payload,projection_attempts,created_at FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND idempotency_key=$3 FOR UPDATE`,
		in.WorkspaceID, in.SessionID, in.IdempotencyKey).Scan(
		&event.ID, &event.WorkspaceID, &event.SessionID, &event.Sequence, &event.Type, &event.IdempotencyKey,
		&event.ActorType, &event.ActorID, &event.Payload, &event.ProjectionAttempts, &event.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, false, nil
	}
	if err != nil {
		return RunEvent{}, false, err
	}
	if event.Type != "task_inquiry_targets_bound" || event.ActorType != "agent" || event.ActorID != in.AgentID ||
		!semanticJSONEqual(event.Payload, encoded) {
		return RunEvent{}, false, fmt.Errorf("%w: Task Inquiry target idempotency key was reused", ErrResultConflict)
	}
	return event, true, nil
}

// loadSelectiveSteeringStateTx resolves canonical Branch ownership for every
// Task while holding the Run state lock used by the future mutation adapter.
// Tasks without Branch targets remain visible so a full replan can still act
// on them; selective steering never guesses their ownership.
func loadSelectiveSteeringStateTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) (selectiveSteeringState, error) {
	var state selectiveSteeringState
	var goalVersion, planVersion int32
	if err := tx.QueryRow(ctx, `SELECT state_version,goal_version,plan_version FROM research_session
		WHERE workspace_id=$1::uuid AND id=$2::uuid FOR UPDATE`, workspaceID, sessionID).Scan(
		&state.StateVersion, &goalVersion, &planVersion,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return selectiveSteeringState{}, ErrRunNotFound
		}
		return selectiveSteeringState{}, err
	}
	branchRows, err := tx.Query(ctx, `SELECT branch.id::text,COALESCE(branch.parent_branch_id::text,''),branch.status
		FROM research_branch branch
		JOIN research_task creator
		  ON creator.workspace_id=branch.workspace_id AND creator.session_id=branch.session_id AND creator.id=branch.created_by_task_id
		WHERE branch.workspace_id=$1::uuid AND branch.session_id=$2::uuid
		  AND creator.goal_version=$3 AND creator.plan_version=$4
		ORDER BY branch.id`, workspaceID, sessionID, goalVersion, planVersion)
	if err != nil {
		return selectiveSteeringState{}, err
	}
	for branchRows.Next() {
		var branch steeringBranchState
		if err = branchRows.Scan(&branch.ID, &branch.ParentID, &branch.Status); err != nil {
			branchRows.Close()
			return selectiveSteeringState{}, err
		}
		state.Branches = append(state.Branches, branch)
	}
	if err = branchRows.Err(); err != nil {
		branchRows.Close()
		return selectiveSteeringState{}, err
	}
	branchRows.Close()

	taskRows, err := tx.Query(ctx, `SELECT task.id::text,task.status,COALESCE(target.target_entity_id::text,'')
		FROM research_task task
		LEFT JOIN research_task_inquiry_target target
		  ON target.workspace_id=task.workspace_id AND target.session_id=task.session_id AND target.task_id=task.id
		 AND target.target_kind='branch' AND target.goal_version=$3 AND target.plan_version=$4
		WHERE task.workspace_id=$1::uuid AND task.session_id=$2::uuid
		  AND task.goal_version=$3 AND task.plan_version=$4
		ORDER BY task.id,target.target_entity_id`, workspaceID, sessionID, goalVersion, planVersion)
	if err != nil {
		return selectiveSteeringState{}, err
	}
	tasks := make(map[string]*steeringTaskState)
	var order []string
	for taskRows.Next() {
		var taskID, status, branchID string
		if err = taskRows.Scan(&taskID, &status, &branchID); err != nil {
			taskRows.Close()
			return selectiveSteeringState{}, err
		}
		task := tasks[taskID]
		if task == nil {
			task = &steeringTaskState{ID: taskID, Status: TaskStatus(status)}
			tasks[taskID] = task
			order = append(order, taskID)
		}
		if branchID != "" {
			task.BranchIDs = append(task.BranchIDs, branchID)
		}
	}
	if err = taskRows.Err(); err != nil {
		taskRows.Close()
		return selectiveSteeringState{}, err
	}
	taskRows.Close()
	sort.Strings(order)
	for _, taskID := range order {
		state.Tasks = append(state.Tasks, *tasks[taskID])
	}
	return state, nil
}
