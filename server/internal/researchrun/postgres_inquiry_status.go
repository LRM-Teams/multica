package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *PostgresStore) UpdateInquiryStatus(ctx context.Context, in UpdateInquiryStatusInput) (UpdateInquiryStatusResult, error) {
	if err := (inquiryStatusUpdateModule{}).Validate(in); err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	payload := inquiryStatusEventPayload(in)
	tx, err := s.beginResearchTx(ctx, txOpInquiryStatusUpdate, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	defer tx.Rollback(ctx)
	if event, found, loadErr := loadMatchingInquiryStatusEvent(ctx, tx, in, payload); loadErr != nil {
		return UpdateInquiryStatusResult{}, loadErr
	} else if found {
		if err = s.commitResearchTx(ctx, txOpInquiryStatusUpdate, tx); err != nil {
			return UpdateInquiryStatusResult{}, err
		}
		return UpdateInquiryStatusResult{TransitionID: in.TransitionID, Event: event, Replayed: true}, nil
	}

	var stateVersion int64
	var producerGoalVersion, producerPlanVersion int32
	var assignedAgent, attemptStatus string
	if err = tx.QueryRow(ctx, `SELECT session.state_version,task.goal_version,task.plan_version,
		attempt.assigned_agent_id::text,attempt.status
		FROM research_session session
		JOIN research_task_attempt attempt ON attempt.workspace_id=session.workspace_id AND attempt.session_id=session.id
		JOIN research_task task ON task.workspace_id=attempt.workspace_id AND task.session_id=attempt.session_id AND task.id=attempt.task_id
		WHERE session.workspace_id=$1::uuid AND session.id=$2::uuid AND attempt.id=$3::uuid
		FOR UPDATE OF session,attempt,task`, in.WorkspaceID, in.SessionID, in.AttemptID).Scan(
		&stateVersion, &producerGoalVersion, &producerPlanVersion, &assignedAgent, &attemptStatus,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpdateInquiryStatusResult{}, ErrRunNotFound
		}
		return UpdateInquiryStatusResult{}, err
	}
	if stateVersion != in.ExpectedStateVersion {
		return UpdateInquiryStatusResult{}, fmt.Errorf("%w: Inquiry status state version changed", ErrControlTargetChanged)
	}
	if assignedAgent != in.AgentID || (attemptStatus != string(AttemptStatusRunning) && attemptStatus != string(AttemptStatusSucceeded)) {
		return UpdateInquiryStatusResult{}, fmt.Errorf("%w: Inquiry status producer is not the assigned active attempt", ErrInvalidTransition)
	}
	var existingTransition bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM research_inquiry_status_transition
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`,
		in.WorkspaceID, in.SessionID, in.TransitionID).Scan(&existingTransition); err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	if existingTransition {
		return UpdateInquiryStatusResult{}, fmt.Errorf("%w: Inquiry status transition ID was reused", ErrResultConflict)
	}

	currentStatus, targetGoalVersion, targetPlanVersion, err := lockInquiryStatusTarget(ctx, tx, in)
	if err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	if currentStatus != in.Before {
		return UpdateInquiryStatusResult{}, fmt.Errorf("%w: Inquiry status target changed from %q to %q", ErrControlTargetChanged, in.Before, currentStatus)
	}
	if targetGoalVersion != producerGoalVersion || targetPlanVersion != producerPlanVersion {
		return UpdateInquiryStatusResult{}, fmt.Errorf("%w: Inquiry status update crosses Goal or Plan versions", ErrInvalidTransition)
	}
	for _, ref := range payload.EvidenceRefs {
		if err = requireInquiryStatusEvidenceTx(ctx, tx, in.WorkspaceID, in.SessionID, ref); err != nil {
			return UpdateInquiryStatusResult{}, err
		}
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, in.SessionID, "inquiry_status_updated", in.IdempotencyKey, "agent", in.AgentID, payload)
	if err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_inquiry_status_transition
		(id,workspace_id,session_id,target_kind,target_entity_id,before_status,after_status,reason,
		 goal_version,plan_version,produced_by_attempt_id,event_sequence)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8,$9,$10,$11::uuid,$12)`,
		in.TransitionID, in.WorkspaceID, in.SessionID, string(in.Target.Kind), in.Target.ID, in.Before, in.After,
		payload.Reason, producerGoalVersion, producerPlanVersion, in.AttemptID, event.Sequence); err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	for ordinal, ref := range payload.EvidenceRefs {
		if _, err = tx.Exec(ctx, `INSERT INTO research_inquiry_status_evidence
			(workspace_id,session_id,transition_id,ordinal,evidence_kind,evidence_id)
			VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid)`,
			in.WorkspaceID, in.SessionID, in.TransitionID, ordinal, ref.Kind, ref.ID); err != nil {
			return UpdateInquiryStatusResult{}, err
		}
	}
	if err = applyInquiryStatusTargetTx(ctx, tx, in, payload.Reason, event.Sequence); err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	if err = s.commitResearchTx(ctx, txOpInquiryStatusUpdate, tx); err != nil {
		return UpdateInquiryStatusResult{}, err
	}
	return UpdateInquiryStatusResult{TransitionID: in.TransitionID, Event: event}, nil
}

func lockInquiryStatusTarget(ctx context.Context, tx pgx.Tx, in UpdateInquiryStatusInput) (string, int32, int32, error) {
	queries := map[InquiryEntityKind]string{
		InquiryKindQuestion: `SELECT entity.status,entity.goal_version,entity.plan_version FROM research_question entity
			WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid AND entity.id=$3::uuid FOR UPDATE OF entity`,
		InquiryKindHypothesis: `SELECT entity.status,task.goal_version,task.plan_version FROM research_hypothesis entity
			JOIN research_task task ON task.workspace_id=entity.workspace_id AND task.session_id=entity.session_id AND task.id=entity.created_by_task_id
			WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid AND entity.id=$3::uuid FOR UPDATE OF entity,task`,
		InquiryKindBranch: `SELECT entity.status,task.goal_version,task.plan_version FROM research_branch entity
			JOIN research_task task ON task.workspace_id=entity.workspace_id AND task.session_id=entity.session_id AND task.id=entity.created_by_task_id
			WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid AND entity.id=$3::uuid FOR UPDATE OF entity,task`,
		InquiryKindInsight: `SELECT entity.status,task.goal_version,task.plan_version FROM research_insight entity
			JOIN research_task_attempt attempt ON attempt.workspace_id=entity.workspace_id AND attempt.session_id=entity.session_id AND attempt.id=entity.created_by_attempt_id
			JOIN research_task task ON task.workspace_id=attempt.workspace_id AND task.session_id=attempt.session_id AND task.id=attempt.task_id
			WHERE entity.workspace_id=$1::uuid AND entity.session_id=$2::uuid AND entity.id=$3::uuid FOR UPDATE OF entity,attempt,task`,
	}
	query := queries[in.Target.Kind]
	var status string
	var goalVersion, planVersion int32
	if err := tx.QueryRow(ctx, query, in.WorkspaceID, in.SessionID, in.Target.ID).Scan(&status, &goalVersion, &planVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, 0, fmt.Errorf("%w: Inquiry status target is outside the current Run graph", ErrInvalidContract)
		}
		return "", 0, 0, err
	}
	return status, goalVersion, planVersion, nil
}

func requireInquiryStatusEvidenceTx(ctx context.Context, tx pgx.Tx, workspaceID, sessionID string, ref InquiryStatusEvidenceRef) error {
	var exists bool
	var err error
	switch ref.Kind {
	case "task":
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM research_task WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`, workspaceID, sessionID, ref.ID).Scan(&exists)
	case "source":
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM research_source_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid)`, workspaceID, sessionID, ref.ID).Scan(&exists)
	default:
		err = tx.QueryRow(ctx, `SELECT research_inquiry_entity_exists($1::uuid,$2::uuid,$3,$4::uuid)`, workspaceID, sessionID, ref.Kind, ref.ID).Scan(&exists)
	}
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: Inquiry status evidence is outside the Run", ErrInvalidContract)
	}
	return nil
}

func applyInquiryStatusTargetTx(ctx context.Context, tx pgx.Tx, in UpdateInquiryStatusInput, reason string, eventSequence int64) error {
	var command pgconn.CommandTag
	var err error
	switch in.Target.Kind {
	case InquiryKindQuestion:
		command, err = tx.Exec(ctx, `UPDATE research_question SET status=$4,
			terminal_explanation=CASE WHEN $4 IN ('answered','unresolved','obsolete') THEN $5 ELSE '' END,updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status=$6`,
			in.WorkspaceID, in.SessionID, in.Target.ID, in.After, reason, in.Before)
	case InquiryKindHypothesis:
		command, err = tx.Exec(ctx, `UPDATE research_hypothesis SET status=$4,last_evaluated_state_version=$5,updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status=$6`,
			in.WorkspaceID, in.SessionID, in.Target.ID, in.After, eventSequence, in.Before)
	case InquiryKindBranch:
		command, err = tx.Exec(ctx, `UPDATE research_branch SET status=$4,
			termination_reason=CASE WHEN $4='terminated' THEN $5 ELSE termination_reason END,updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status=$6`,
			in.WorkspaceID, in.SessionID, in.Target.ID, in.After, reason, in.Before)
	case InquiryKindInsight:
		command, err = tx.Exec(ctx, `UPDATE research_insight SET status=$4,updated_at=now()
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND status=$5`,
			in.WorkspaceID, in.SessionID, in.Target.ID, in.After, in.Before)
	}
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("%w: Inquiry status target changed during update", ErrControlTargetChanged)
	}
	return nil
}

func loadMatchingInquiryStatusEvent(ctx context.Context, tx pgx.Tx, in UpdateInquiryStatusInput, payload any) (RunEvent, bool, error) {
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
	if event.Type != "inquiry_status_updated" || event.ActorType != "agent" || event.ActorID != in.AgentID || !semanticJSONEqual(event.Payload, encoded) {
		return RunEvent{}, false, fmt.Errorf("%w: Inquiry status idempotency key was reused", ErrResultConflict)
	}
	return event, true, nil
}
