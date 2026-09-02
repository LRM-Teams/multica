// SPDX-License-Identifier: Apache-2.0

package service

// PostgreSQL implementation of the skillevolution.DecisionStore,
// IdempotencyStore, and OutboxStore ports (migration 495). Approvals
// enforce actor isolation against the run's proposer and the evaluation's
// evaluator; deployments require an unexpired approval; rollbacks only
// advance their roll-forward status (ADR 0021 D7 package boundary).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/skillevolution"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

func optionalUUID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return parseLedgerUUID("target id", value)
}

// InsertApproval appends the human-gate decision after checking §12.7
// actor isolation: the approver may not be the run's creator (the
// proposer-side actor the ledger knows) nor the evaluation's evaluator.
func (l *PostgresSkillEvolutionLedger) InsertApproval(ctx context.Context, record skillevolution.ApprovalRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", record.WorkspaceID)
	if err != nil {
		return err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	candidate, err := q.GetSkillCandidate(ctx, db.GetSkillCandidateParams{
		WorkspaceID: workspaceID, CandidateID: record.CandidateID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: candidate %s", skillevolution.ErrLedgerNotFound, record.CandidateID)
		}
		return fmt.Errorf("skill decision ledger: candidate read: %w", err)
	}
	run, err := q.GetSkillEvolutionRun(ctx, db.GetSkillEvolutionRunParams{
		WorkspaceID: workspaceID, ID: candidate.RunID,
	})
	if err != nil {
		return fmt.Errorf("skill decision ledger: run read: %w", err)
	}
	if run.CreatedByActor == record.ApproverActor {
		return fmt.Errorf("%w: approver %s created run %s",
			skillevolution.ErrApprovalActorConflict, record.ApproverActor, run.ID)
	}
	evaluation, err := q.GetSkillEvaluationRun(ctx, db.GetSkillEvaluationRunParams{
		WorkspaceID: workspaceID, EvaluationID: record.EvaluationRef.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: evaluation %s", skillevolution.ErrLedgerNotFound, record.EvaluationRef.ID)
		}
		return fmt.Errorf("skill decision ledger: evaluation read: %w", err)
	}
	if evaluation.CreatedByActor == record.ApproverActor {
		return fmt.Errorf("%w: approver %s evaluated %s",
			skillevolution.ErrApprovalActorConflict, record.ApproverActor, evaluation.EvaluationID)
	}
	if evaluation.CandidateID != record.CandidateID {
		return fmt.Errorf("%w: evaluation %s scores candidate %s, not %s",
			skillevolution.ErrLedgerConflict, evaluation.EvaluationID, evaluation.CandidateID, record.CandidateID)
	}
	var expiresAt pgtype.Timestamptz
	if !record.ExpiresAt.IsZero() {
		expiresAt = pgtype.Timestamptz{Time: record.ExpiresAt, Valid: true}
	}
	if _, err := q.InsertSkillApproval(ctx, db.InsertSkillApprovalParams{
		WorkspaceID: workspaceID, ApprovalID: record.ApprovalID,
		CandidateID: record.CandidateID, EvaluationID: record.EvaluationRef.ID,
		ManifestHash: record.ManifestHash, PolicyHash: record.PolicyHash,
		ArtifactHash: record.ArtifactHash, TargetScope: record.TargetScope,
		Decision: string(record.Decision), ApproverActor: record.ApproverActor,
		ApproverRole: record.ApproverRole, Reason: record.Reason,
		RiskAcknowledged:  record.RiskAcknowledged,
		AllowAutoRollback: record.AllowAutoRollback, ExpiresAt: expiresAt,
	}); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: approval %s already exists", skillevolution.ErrLedgerConflict, record.ApprovalID)
		}
		return fmt.Errorf("skill decision ledger: insert approval: %w", err)
	}
	return tx.Commit(ctx)
}

func approvalRecordFromRow(row db.SkillApproval) skillevolution.ApprovalRecord {
	record := skillevolution.ApprovalRecord{
		ContractKind: "approval", SchemaVersion: 1,
		ApprovalID: row.ApprovalID, WorkspaceID: row.WorkspaceID.String(),
		CandidateID: row.CandidateID,
		EvaluationRef: skillevolution.SkillEvolutionRef{
			Kind: skillevolution.RefEvaluationRun, ID: row.EvaluationID,
			WorkspaceID: row.WorkspaceID.String(),
		},
		ManifestHash: row.ManifestHash, PolicyHash: row.PolicyHash,
		ArtifactHash: row.ArtifactHash, TargetScope: row.TargetScope,
		Decision:      skillevolution.ApprovalDecision(row.Decision),
		ApproverActor: row.ApproverActor, ApproverRole: row.ApproverRole,
		Reason: row.Reason, RiskAcknowledged: row.RiskAcknowledged,
		AllowAutoRollback: row.AllowAutoRollback, CreatedAt: row.CreatedAt.Time,
	}
	if row.ExpiresAt.Valid {
		record.ExpiresAt = row.ExpiresAt.Time
	}
	return record
}

func (l *PostgresSkillEvolutionLedger) GetApproval(ctx context.Context, workspaceIDStr, approvalID string) (skillevolution.ApprovalRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.ApprovalRecord{}, err
	}
	row, err := db.New(l.pool).GetSkillApproval(ctx, db.GetSkillApprovalParams{
		WorkspaceID: workspaceID, ApprovalID: approvalID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.ApprovalRecord{}, fmt.Errorf("%w: approval %s", skillevolution.ErrLedgerNotFound, approvalID)
		}
		return skillevolution.ApprovalRecord{}, fmt.Errorf("skill decision ledger: get approval: %w", err)
	}
	return approvalRecordFromRow(row), nil
}

// InsertDeployment appends one activation. The referenced approval must
// be an unexpired approval — a rejection or a stale approval never
// activates anything.
func (l *PostgresSkillEvolutionLedger) InsertDeployment(ctx context.Context, record skillevolution.DeploymentRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", record.WorkspaceID)
	if err != nil {
		return err
	}
	agentID, err := optionalUUID(record.TargetAgentID)
	if err != nil {
		return err
	}
	channelID, err := optionalUUID(record.TargetChannelID)
	if err != nil {
		return err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	approval, err := q.GetSkillApproval(ctx, db.GetSkillApprovalParams{
		WorkspaceID: workspaceID, ApprovalID: record.ApprovalID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: approval %s", skillevolution.ErrApprovalNotUsable, record.ApprovalID)
		}
		return fmt.Errorf("skill decision ledger: approval read: %w", err)
	}
	if approval.Decision != "approved" {
		return fmt.Errorf("%w: approval %s is %s", skillevolution.ErrApprovalNotUsable, record.ApprovalID, approval.Decision)
	}
	if !approval.ExpiresAt.Valid || !approval.ExpiresAt.Time.After(time.Now()) {
		return fmt.Errorf("%w: approval %s expired", skillevolution.ErrApprovalNotUsable, record.ApprovalID)
	}
	if approval.CandidateID != record.CandidateID {
		return fmt.Errorf("%w: approval %s covers candidate %s, not %s",
			skillevolution.ErrLedgerConflict, record.ApprovalID, approval.CandidateID, record.CandidateID)
	}
	if _, err := q.InsertSkillDeployment(ctx, db.InsertSkillDeploymentParams{
		WorkspaceID: workspaceID, DeploymentID: record.DeploymentID,
		CandidateID: record.CandidateID, ApprovalID: record.ApprovalID,
		TargetScope: record.TargetScope, TargetAgentID: agentID, TargetChannelID: channelID,
		BindingStateBefore: record.BindingStateBefore, BindingStateAfter: record.BindingStateAfter,
		FromArtifactHash: record.FromArtifactHash, ToArtifactHash: record.ToArtifactHash,
		MaterializationStatus: string(record.MaterializationStatus),
		CreatedByActor:        record.CreatedByActor,
	}); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: deployment %s already exists", skillevolution.ErrLedgerConflict, record.DeploymentID)
		}
		return fmt.Errorf("skill decision ledger: insert deployment: %w", err)
	}
	return tx.Commit(ctx)
}

func deploymentRecordFromRow(row db.SkillDeployment) skillevolution.DeploymentRecord {
	return skillevolution.DeploymentRecord{
		ContractKind: "deployment", SchemaVersion: 1,
		DeploymentID: row.DeploymentID, WorkspaceID: row.WorkspaceID.String(),
		CandidateID: row.CandidateID, ApprovalID: row.ApprovalID,
		TargetScope: row.TargetScope,
		TargetAgentID: func() string {
			if row.TargetAgentID.Valid {
				return row.TargetAgentID.String()
			}
			return ""
		}(),
		TargetChannelID: func() string {
			if row.TargetChannelID.Valid {
				return row.TargetChannelID.String()
			}
			return ""
		}(),
		BindingStateBefore: row.BindingStateBefore, BindingStateAfter: row.BindingStateAfter,
		FromArtifactHash: row.FromArtifactHash, ToArtifactHash: row.ToArtifactHash,
		MaterializationStatus: skillevolution.MaterializationStatus(row.MaterializationStatus),
		CreatedByActor:        row.CreatedByActor, CreatedAt: row.CreatedAt.Time,
	}
}

func (l *PostgresSkillEvolutionLedger) GetDeployment(ctx context.Context, workspaceIDStr, deploymentID string) (skillevolution.DeploymentRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.DeploymentRecord{}, err
	}
	row, err := db.New(l.pool).GetSkillDeployment(ctx, db.GetSkillDeploymentParams{
		WorkspaceID: workspaceID, DeploymentID: deploymentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.DeploymentRecord{}, fmt.Errorf("%w: deployment %s", skillevolution.ErrLedgerNotFound, deploymentID)
		}
		return skillevolution.DeploymentRecord{}, fmt.Errorf("skill decision ledger: get deployment: %w", err)
	}
	return deploymentRecordFromRow(row), nil
}

func (l *PostgresSkillEvolutionLedger) TransitionDeploymentMaterialization(ctx context.Context, workspaceIDStr, deploymentID string, from, to skillevolution.MaterializationStatus) error {
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("%w: materialization cannot move %s -> %s", skillevolution.ErrLedgerConflict, from, to)
	}
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return err
	}
	rows, err := db.New(l.pool).TransitionSkillDeploymentMaterialization(ctx, db.TransitionSkillDeploymentMaterializationParams{
		WorkspaceID: workspaceID, DeploymentID: deploymentID,
		MaterializationStatus: string(to), MaterializationStatus_2: string(from),
	})
	if err != nil {
		// The terminal-materialization trigger raises on revival.
		return fmt.Errorf("%w: skill decision ledger: transition materialization: %v", skillevolution.ErrLedgerConflict, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: CAS miss on deployment %s (%s)", skillevolution.ErrLedgerConflict, deploymentID, from)
	}
	return nil
}

func (l *PostgresSkillEvolutionLedger) InsertRollback(ctx context.Context, record skillevolution.RollbackRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", record.WorkspaceID)
	if err != nil {
		return err
	}
	if _, err := db.New(l.pool).InsertSkillRollback(ctx, db.InsertSkillRollbackParams{
		WorkspaceID: workspaceID, RollbackID: record.RollbackID,
		DeploymentID: record.DeploymentID, RollbackTrigger: string(record.Trigger),
		FromArtifactHash: record.FromArtifactHash, ToArtifactHash: record.ToArtifactHash,
		InFlightPolicy: record.InFlightPolicy, Actor: record.Actor,
		PolicyVersion: record.PolicyVersion, RollForwardStatus: string(record.RollForwardStatus),
	}); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: rollback %s already exists", skillevolution.ErrLedgerConflict, record.RollbackID)
		}
		return fmt.Errorf("skill decision ledger: insert rollback: %w", err)
	}
	return nil
}

func rollbackRecordFromRow(row db.SkillRollback) skillevolution.RollbackRecord {
	return skillevolution.RollbackRecord{
		ContractKind: "rollback", SchemaVersion: 1,
		RollbackID: row.RollbackID, WorkspaceID: row.WorkspaceID.String(),
		DeploymentID:     row.DeploymentID,
		Trigger:          skillevolution.RollbackTrigger(row.RollbackTrigger),
		FromArtifactHash: row.FromArtifactHash, ToArtifactHash: row.ToArtifactHash,
		InFlightPolicy: row.InFlightPolicy, Actor: row.Actor,
		PolicyVersion:     row.PolicyVersion,
		RollForwardStatus: skillevolution.RollForwardStatus(row.RollForwardStatus),
		CreatedAt:         row.CreatedAt.Time,
	}
}

func (l *PostgresSkillEvolutionLedger) GetRollback(ctx context.Context, workspaceIDStr, rollbackID string) (skillevolution.RollbackRecord, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return skillevolution.RollbackRecord{}, err
	}
	row, err := db.New(l.pool).GetSkillRollback(ctx, db.GetSkillRollbackParams{
		WorkspaceID: workspaceID, RollbackID: rollbackID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillevolution.RollbackRecord{}, fmt.Errorf("%w: rollback %s", skillevolution.ErrLedgerNotFound, rollbackID)
		}
		return skillevolution.RollbackRecord{}, fmt.Errorf("skill decision ledger: get rollback: %w", err)
	}
	return rollbackRecordFromRow(row), nil
}

func (l *PostgresSkillEvolutionLedger) SetRollForwardStatus(ctx context.Context, workspaceIDStr, rollbackID string, from, to skillevolution.RollForwardStatus) error {
	if !from.CanTransitionTo(to) {
		return fmt.Errorf("%w: roll-forward cannot move %s -> %s", skillevolution.ErrLedgerConflict, from, to)
	}
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return err
	}
	rows, err := db.New(l.pool).SetSkillRollForwardStatus(ctx, db.SetSkillRollForwardStatusParams{
		WorkspaceID: workspaceID, RollbackID: rollbackID,
		RollForwardStatus: string(to), RollForwardStatus_2: string(from),
	})
	if err != nil {
		return fmt.Errorf("%w: skill decision ledger: set roll-forward: %v", skillevolution.ErrLedgerConflict, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: CAS miss on rollback %s (%s)", skillevolution.ErrLedgerConflict, rollbackID, from)
	}
	return nil
}

// RunOnce executes work exactly once per (workspace, idempotency key).
// The row-locked claim serializes concurrent replays through this store;
// the PK plus payload hash comparison are the hard fence.
func (l *PostgresSkillEvolutionLedger) RunOnce(ctx context.Context, request skillevolution.IdempotentRequest, work func(context.Context) (json.RawMessage, error)) (json.RawMessage, bool, error) {
	if err := request.Validate(); err != nil {
		return nil, false, err
	}
	workspaceID, err := parseLedgerUUID("workspace_id", request.WorkspaceID)
	if err != nil {
		return nil, false, err
	}
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	claim, err := q.GetSkillEvolutionIdempotency(ctx, db.GetSkillEvolutionIdempotencyParams{
		WorkspaceID: workspaceID, IdempotencyKey: request.Key,
	})
	if err == nil {
		if claim.PayloadHash != request.PayloadHash {
			return nil, false, fmt.Errorf("%w: key %s", skillevolution.ErrIdempotencyPayloadConflict, request.Key)
		}
		return json.RawMessage(claim.Response), true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("skill evolution idempotency: claim read: %w", err)
	}
	response, err := work(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(response) == 0 {
		response = json.RawMessage(`{}`)
	}
	if _, err := q.InsertSkillEvolutionIdempotency(ctx, db.InsertSkillEvolutionIdempotencyParams{
		WorkspaceID: workspaceID, IdempotencyKey: request.Key,
		RequestKind: request.RequestKind, PayloadHash: request.PayloadHash,
		Response: response,
	}); err != nil {
		return nil, false, fmt.Errorf("skill evolution idempotency: claim insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return response, false, nil
}

func (l *PostgresSkillEvolutionLedger) InsertOutboxEvent(ctx context.Context, event skillevolution.OutboxEvent) error {
	workspaceID, err := parseLedgerUUID("workspace_id", event.WorkspaceID)
	if err != nil {
		return err
	}
	if _, err := db.New(l.pool).InsertSkillEvolutionOutbox(ctx, db.InsertSkillEvolutionOutboxParams{
		WorkspaceID: workspaceID, AggregateKind: event.AggregateKind,
		AggregateID: event.AggregateID, EventType: event.EventType,
		Payload: event.Payload,
	}); err != nil {
		return fmt.Errorf("skill evolution outbox: insert: %w", err)
	}
	return nil
}

func (l *PostgresSkillEvolutionLedger) ListPendingOutboxEvents(ctx context.Context, workspaceIDStr string, limit int) ([]skillevolution.OutboxEvent, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return nil, err
	}
	rows, err := db.New(l.pool).ListPendingSkillEvolutionOutbox(ctx, db.ListPendingSkillEvolutionOutboxParams{
		WorkspaceID: workspaceID, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("skill evolution outbox: list pending: %w", err)
	}
	events := make([]skillevolution.OutboxEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, skillevolution.OutboxEvent{
			ID: row.ID, WorkspaceID: row.WorkspaceID.String(), AggregateKind: row.AggregateKind,
			AggregateID: row.AggregateID, EventType: row.EventType,
			Payload: row.Payload,
		})
	}
	return events, nil
}

func (l *PostgresSkillEvolutionLedger) MarkOutboxEventDispatched(ctx context.Context, workspaceIDStr string, id int64) (bool, error) {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return false, err
	}
	rows, err := db.New(l.pool).MarkSkillEvolutionOutboxDispatched(ctx, db.MarkSkillEvolutionOutboxDispatchedParams{
		ID: id, WorkspaceID: workspaceID,
	})
	if err != nil {
		return false, fmt.Errorf("skill evolution outbox: mark dispatched: %w", err)
	}
	return rows == 1, nil
}

func (l *PostgresSkillEvolutionLedger) NoteOutboxEventFailure(ctx context.Context, workspaceIDStr string, id int64, reason string) error {
	workspaceID, err := parseLedgerUUID("workspace_id", workspaceIDStr)
	if err != nil {
		return err
	}
	if _, err := db.New(l.pool).NoteSkillEvolutionOutboxFailure(ctx, db.NoteSkillEvolutionOutboxFailureParams{
		ID: id, WorkspaceID: workspaceID, LastError: reason,
	}); err != nil {
		return fmt.Errorf("skill evolution outbox: note failure: %w", err)
	}
	return nil
}
