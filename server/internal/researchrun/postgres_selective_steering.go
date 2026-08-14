package researchrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

type selectiveSteeringEventPayload struct {
	Request    selectiveSteeringEventRequest `json:"request"`
	DecisionID string                        `json:"decision_id"`
	Plan       selectiveSteeringPlan         `json:"plan"`
}

type selectiveSteeringEventRequest struct {
	ExpectedStateVersion int64    `json:"expected_state_version"`
	FullReplan           bool     `json:"full_replan"`
	AffectedBranchIDs    []string `json:"affected_branch_ids"`
	AllowRunningFinish   bool     `json:"allow_running_finish"`
	Reason               string   `json:"reason"`
}

func canonicalSelectiveSteeringRequest(in SteerInput) selectiveSteeringEventRequest {
	branches := append([]string(nil), in.AffectedBranchIDs...)
	sort.Strings(branches)
	return selectiveSteeringEventRequest{
		ExpectedStateVersion: in.ExpectedStateVersion, FullReplan: in.FullReplan,
		AffectedBranchIDs: branches, AllowRunningFinish: in.AllowRunningFinish, Reason: strings.TrimSpace(in.Reason),
	}
}

func selectiveSteeringIdempotencyKey(userID string, request selectiveSteeringEventRequest) (string, error) {
	encoded, err := json.Marshal(struct {
		UserID  string                        `json:"user_id"`
		Request selectiveSteeringEventRequest `json:"request"`
	}{UserID: userID, Request: request})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "selective-steering:" + hex.EncodeToString(digest[:]), nil
}

func (s *PostgresStore) ApplySelectiveSteering(ctx context.Context, in SteerInput) (SelectiveSteeringOutcome, error) {
	if err := validateSelectiveSteerInput(in); err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	request := canonicalSelectiveSteeringRequest(in)
	idempotencyKey, err := selectiveSteeringIdempotencyKey(in.UserID, request)
	if err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	tx, err := s.beginResearchTx(ctx, txOpRunSelectiveSteer, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	defer tx.Rollback(ctx)
	if event, payload, found, loadErr := loadMatchingSelectiveSteeringEvent(ctx, tx, in, idempotencyKey, request); loadErr != nil {
		return SelectiveSteeringOutcome{}, loadErr
	} else if found {
		if err = s.commitResearchTx(ctx, txOpRunSelectiveSteer, tx); err != nil {
			return SelectiveSteeringOutcome{}, err
		}
		run, getErr := s.GetRun(ctx, in.SessionID, in.WorkspaceID)
		return SelectiveSteeringOutcome{Run: run, Event: event, Plan: payload.Plan, Replayed: true}, getErr
	}

	run, err := loadRunForUpdate(ctx, tx, in.SessionID, in.WorkspaceID)
	if err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	if run.Status != RunStatusRunning && run.Status != RunStatusAwaitingUserConfirm {
		return SelectiveSteeringOutcome{}, fmt.Errorf("%w: selective steering requires an active Run", ErrInvalidTransition)
	}
	state, err := loadSelectiveSteeringStateTx(ctx, tx, in.WorkspaceID, in.SessionID)
	if err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	plan, err := (selectiveSteeringModule{}).Plan(selectiveSteeringRequest{
		ExpectedStateVersion: request.ExpectedStateVersion, FullReplan: request.FullReplan,
		AffectedBranchIDs: request.AffectedBranchIDs, AllowRunningFinish: request.AllowRunningFinish,
	}, state)
	if err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	if err = applySelectiveSteeringPlanTx(ctx, tx, in, plan); err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	inputs, _ := json.Marshal(request)
	outcome, _ := json.Marshal(plan)
	var decisionID string
	if err = tx.QueryRow(ctx, `INSERT INTO research_decision
		(workspace_id,session_id,decision_kind,actor_type,actor_id,goal_version,plan_version,inputs,outcome,rationale)
		VALUES ($1::uuid,$2::uuid,'selective_steering','user',$3::uuid,$4,$5,$6::jsonb,$7::jsonb,$8)
		RETURNING id::text`, in.WorkspaceID, in.SessionID, in.UserID, run.GoalVersion, run.PlanVersion,
		inputs, outcome, request.Reason).Scan(&decisionID); err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	if err = registerProductionDecisionPassportTx(ctx, tx, in.WorkspaceID, in.SessionID, decisionID, "", ArtifactAccessRaw); err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	payload := selectiveSteeringEventPayload{Request: request, DecisionID: decisionID, Plan: plan}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "selective_steering_applied", idempotencyKey, "user", in.UserID, payload)
	if err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	if err = s.commitResearchTx(ctx, txOpRunSelectiveSteer, tx); err != nil {
		return SelectiveSteeringOutcome{}, err
	}
	run, err = s.GetRun(ctx, in.SessionID, in.WorkspaceID)
	return SelectiveSteeringOutcome{Run: run, Event: event, Plan: plan}, err
}

func applySelectiveSteeringPlanTx(ctx context.Context, tx pgx.Tx, in SteerInput, plan selectiveSteeringPlan) error {
	if len(plan.ObsoleteBranchIDs) > 0 {
		command, err := tx.Exec(ctx, `UPDATE research_branch SET status='obsolete',updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id::text=ANY($3::text[])
			  AND status IN ('proposed','active','paused')`, in.WorkspaceID, in.SessionID, plan.ObsoleteBranchIDs)
		if err != nil {
			return err
		}
		if command.RowsAffected() != int64(len(plan.ObsoleteBranchIDs)) {
			return fmt.Errorf("%w: selective steering Branch set changed", ErrControlTargetChanged)
		}
	}
	if len(plan.ObsoleteTaskIDs) > 0 {
		command, err := tx.Exec(ctx, `UPDATE research_task SET status='obsolete',terminal_reason='selective_steering',
			completed_at=now(),updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid
			AND id::text=ANY($3::text[]) AND status IN ('pending','ready')`,
			in.WorkspaceID, in.SessionID, plan.ObsoleteTaskIDs)
		if err != nil {
			return err
		}
		if command.RowsAffected() != int64(len(plan.ObsoleteTaskIDs)) {
			return fmt.Errorf("%w: selective steering pending Task set changed", ErrControlTargetChanged)
		}
	}
	if len(plan.CancelRunningTaskIDs) > 0 {
		command, err := tx.Exec(ctx, `UPDATE research_task_attempt SET status='cancelling',
			pending_failure_class='selective_steering',pending_failure_diagnostics=$4,
			pending_failure_retryable=false,updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND task_id::text=ANY($3::text[])
			  AND status IN ('dispatching','running')`, in.WorkspaceID, in.SessionID,
			plan.CancelRunningTaskIDs, requestBoundedReason(in.Reason))
		if err != nil {
			return err
		}
		if command.RowsAffected() != int64(len(plan.CancelRunningTaskIDs)) {
			return fmt.Errorf("%w: selective steering active Attempt set changed", ErrControlTargetChanged)
		}
		command, err = tx.Exec(ctx, `UPDATE research_task SET status='obsolete',terminal_reason='selective_steering',
			completed_at=now(),updated_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid
			AND id::text=ANY($3::text[]) AND status IN ('dispatching','running')`,
			in.WorkspaceID, in.SessionID, plan.CancelRunningTaskIDs)
		if err != nil {
			return err
		}
		if command.RowsAffected() != int64(len(plan.CancelRunningTaskIDs)) {
			return fmt.Errorf("%w: selective steering active Task set changed", ErrControlTargetChanged)
		}
	}
	command, err := tx.Exec(ctx, `UPDATE research_session SET status='running',current_stage='s1_plan',
		state_version=state_version+1,next_reconcile_at=now(),last_progress_at=now(),stop_reason='',updated_at=now()
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND state_version=$3`,
		in.WorkspaceID, in.SessionID, in.ExpectedStateVersion)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("%w: selective steering state version changed", ErrControlTargetChanged)
	}
	return nil
}

func requestBoundedReason(reason string) string {
	return truncateBytes(strings.TrimSpace(reason), 32768)
}

func loadMatchingSelectiveSteeringEvent(ctx context.Context, tx pgx.Tx, in SteerInput, key string,
	request selectiveSteeringEventRequest) (RunEvent, selectiveSteeringEventPayload, bool, error) {
	var event RunEvent
	err := tx.QueryRow(ctx, `SELECT id::text,workspace_id::text,session_id::text,sequence,event_type,idempotency_key,actor_type,
		COALESCE(actor_id::text,''),payload,projection_attempts,created_at FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND idempotency_key=$3 FOR UPDATE`,
		in.WorkspaceID, in.SessionID, key).Scan(&event.ID, &event.WorkspaceID, &event.SessionID, &event.Sequence,
		&event.Type, &event.IdempotencyKey, &event.ActorType, &event.ActorID, &event.Payload,
		&event.ProjectionAttempts, &event.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RunEvent{}, selectiveSteeringEventPayload{}, false, nil
	}
	if err != nil {
		return RunEvent{}, selectiveSteeringEventPayload{}, false, err
	}
	var payload selectiveSteeringEventPayload
	if event.Type != "selective_steering_applied" || event.ActorType != "user" || event.ActorID != in.UserID ||
		json.Unmarshal(event.Payload, &payload) != nil || !reflect.DeepEqual(payload.Request, request) {
		return RunEvent{}, selectiveSteeringEventPayload{}, false, fmt.Errorf("%w: selective steering idempotency key was reused", ErrResultConflict)
	}
	return event, payload, true, nil
}
