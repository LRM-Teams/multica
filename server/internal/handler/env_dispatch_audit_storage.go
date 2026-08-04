package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// envDispatchAuditStorage is the SQLC implementation of the service audit
// ledger. Keeping it at the handler boundary makes the service depend only on
// durable audit semantics, not on pgx or generated query types.
type envDispatchAuditStorage struct {
	queries *db.Queries
	tx      txStarter
}

var _ service.EnvDispatchAuditStorage = (*envDispatchAuditStorage)(nil)

func newEnvDispatchAuditStorage(h *Handler) service.EnvDispatchAuditStorage {
	if h == nil || h.Queries == nil || h.TxStarter == nil {
		return nil
	}
	return &envDispatchAuditStorage{queries: h.Queries, tx: h.TxStarter}
}

func (s *envDispatchAuditStorage) CreateAuditRun(ctx context.Context, report service.EnvDispatchAuditReport) (service.EnvDispatchAuditReport, error) {
	workspaceID, err := auditUUID(report.WorkspaceID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, fmt.Errorf("audit workspace id: %w", err)
	}
	initiatorID, err := auditUUID(report.InitiatorID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, fmt.Errorf("audit initiator id: %w", err)
	}
	primaryScopeID, err := auditUUID(report.PrimaryScopeID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, fmt.Errorf("audit primary scope id: %w", err)
	}
	run, err := s.queries.CreateEnvDispatchAuditRun(ctx, db.CreateEnvDispatchAuditRunParams{
		WorkspaceID: workspaceID, InitiatorID: initiatorID,
		DispatchType: string(report.DispatchType), PrimaryScopeID: primaryScopeID,
		ReclamationDeadline: auditTime(report.ReclamationDeadline), StartedAt: auditTime(report.StartedAt),
	})
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	return mapAuditRun(run, report.AsOf), nil
}

func (s *envDispatchAuditStorage) LoadAuditReport(ctx context.Context, auditID, workspaceID, initiatorID string, asOf time.Time) (service.EnvDispatchAuditReport, error) {
	runUUID, err := auditUUID(auditID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	workspaceUUID, err := auditUUID(workspaceID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	initiatorUUID, err := auditUUID(initiatorID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	scope := db.GetEnvDispatchAuditRunForInitiatorParams{AuditID: runUUID, WorkspaceID: workspaceUUID, InitiatorID: initiatorUUID}
	run, err := s.queries.GetEnvDispatchAuditRunForInitiator(ctx, scope)
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	report := mapAuditRun(run, asOf)
	resources, err := s.queries.ListEnvDispatchAuditResourcesForInitiator(ctx, db.ListEnvDispatchAuditResourcesForInitiatorParams(scope))
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	for _, resource := range resources {
		report.Resources = append(report.Resources, mapAuditResource(resource))
	}
	events, err := s.queries.ListEnvDispatchAuditEventsForInitiator(ctx, db.ListEnvDispatchAuditEventsForInitiatorParams(scope))
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	for _, event := range events {
		report.Events = append(report.Events, mapAuditEvent(event))
	}
	obligations, err := s.queries.ListEnvDispatchReclamationObligationsForInitiator(ctx, db.ListEnvDispatchReclamationObligationsForInitiatorParams(scope))
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	for _, obligation := range obligations {
		report.Obligations = append(report.Obligations, mapAuditObligation(obligation, nil))
	}
	return report, nil
}

func (s *envDispatchAuditStorage) UpsertAuditResource(ctx context.Context, resource service.EnvDispatchAuditResource) (service.EnvDispatchAuditResource, error) {
	auditID, err := auditUUID(resource.AuditID)
	if err != nil {
		return service.EnvDispatchAuditResource{}, err
	}
	environmentID, err := auditOptionalUUID(resource.EnvironmentID)
	if err != nil {
		return service.EnvDispatchAuditResource{}, fmt.Errorf("audit environment id: %w", err)
	}
	projectID, err := auditOptionalUUID(resource.ProjectID)
	if err != nil {
		return service.EnvDispatchAuditResource{}, fmt.Errorf("audit project id: %w", err)
	}
	channelID, err := auditOptionalUUID(resource.ChannelID)
	if err != nil {
		return service.EnvDispatchAuditResource{}, fmt.Errorf("audit channel id: %w", err)
	}
	row, err := s.queries.UpsertEnvDispatchAuditResource(ctx, db.UpsertEnvDispatchAuditResourceParams{
		AuditID: auditID, ResourceKind: string(resource.Kind), ResourceID: resource.ResourceID,
		DaemonID: auditText(resource.DaemonID), EnvironmentID: environmentID,
		ProjectID: projectID, ChannelID: channelID,
		OwnershipMode: string(resource.OwnershipMode), OwnerState: string(resource.OwnerState),
		ObservedAt: auditTime(resource.FirstObservedAt),
	})
	if err != nil {
		return service.EnvDispatchAuditResource{}, err
	}
	return mapAuditResource(row), nil
}

func (s *envDispatchAuditStorage) UpdateAuditResourceClassification(ctx context.Context, auditID, auditResourceID string, ownerState service.EnvDispatchAuditOwnerState, classification service.EnvDispatchAuditClassification, observedAt time.Time) (service.EnvDispatchAuditResource, error) {
	runID, err := auditUUID(auditID)
	if err != nil {
		return service.EnvDispatchAuditResource{}, err
	}
	resourceID, err := auditUUID(auditResourceID)
	if err != nil {
		return service.EnvDispatchAuditResource{}, err
	}
	row, err := s.queries.UpdateEnvDispatchAuditResourceClassification(ctx, db.UpdateEnvDispatchAuditResourceClassificationParams{
		OwnerState: string(ownerState), Classification: string(classification), ObservedAt: auditTime(observedAt),
		AuditResourceID: resourceID, AuditID: runID,
	})
	if err != nil {
		return service.EnvDispatchAuditResource{}, err
	}
	return mapAuditResource(row), nil
}

// AppendAuditEvent serializes sequence assignment with the audit-run row lock.
// The lock, sequence read, and insert deliberately share one pgx transaction.
func (s *envDispatchAuditStorage) AppendAuditEvent(ctx context.Context, event service.EnvDispatchAuditEvent) (service.EnvDispatchAuditEvent, error) {
	auditID, err := auditUUID(event.AuditID)
	if err != nil {
		return service.EnvDispatchAuditEvent{}, err
	}
	resourceID, err := auditOptionalUUID(event.AuditResourceID)
	if err != nil {
		return service.EnvDispatchAuditEvent{}, fmt.Errorf("audit event resource id: %w", err)
	}
	tx, err := s.tx.Begin(ctx)
	if err != nil {
		return service.EnvDispatchAuditEvent{}, err
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)
	if _, err := qtx.LockEnvDispatchAuditRunForEventAppend(ctx, auditID); err != nil {
		return service.EnvDispatchAuditEvent{}, err
	}
	last, err := qtx.GetLastEnvDispatchAuditEventSequence(ctx, auditID)
	if err != nil {
		return service.EnvDispatchAuditEvent{}, err
	}
	row, err := qtx.CreateEnvDispatchAuditEvent(ctx, db.CreateEnvDispatchAuditEventParams{
		AuditID: auditID, AuditResourceID: resourceID, Sequence: last + 1,
		EventType: string(event.Type), ReasonCode: auditSafeCode(event.ReasonCode), OccurredAt: auditTime(event.OccurredAt),
	})
	if err != nil {
		return service.EnvDispatchAuditEvent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return service.EnvDispatchAuditEvent{}, err
	}
	return mapAuditEvent(row), nil
}

func (s *envDispatchAuditStorage) EnsureReclamationObligation(ctx context.Context, obligation service.EnvDispatchAuditObligation) (service.EnvDispatchAuditObligation, error) {
	resourceID, err := auditUUID(obligation.AuditResourceID)
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	row, err := s.queries.EnsureEnvDispatchReclamationObligation(ctx, db.EnsureEnvDispatchReclamationObligationParams{
		AuditResourceID: resourceID, Trigger: string(obligation.Trigger), NextAttemptAt: auditTimePtr(obligation.NextAttemptAt),
	})
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	return mapAuditObligation(row, nil), nil
}

func (s *envDispatchAuditStorage) UpdateAuditOutcome(ctx context.Context, auditID string, outcome service.EnvDispatchAuditOutcome, completedAt *time.Time) (service.EnvDispatchAuditReport, error) {
	id, err := auditUUID(auditID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	row, err := s.queries.UpdateEnvDispatchAuditRunOutcome(ctx, db.UpdateEnvDispatchAuditRunOutcomeParams{AuditID: id, Outcome: string(outcome), CompletedAt: auditTimePtr(completedAt)})
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	return mapAuditRun(row, time.Now().UTC()), nil
}

func (s *envDispatchAuditStorage) UpdateAuditVerdict(ctx context.Context, auditID string, verdict service.EnvDispatchAuditVerdict, completedAt *time.Time) (service.EnvDispatchAuditReport, error) {
	id, err := auditUUID(auditID)
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	row, err := s.queries.UpdateEnvDispatchAuditRunVerdict(ctx, db.UpdateEnvDispatchAuditRunVerdictParams{AuditID: id, Verdict: string(verdict), CompletedAt: auditTimePtr(completedAt)})
	if err != nil {
		return service.EnvDispatchAuditReport{}, err
	}
	return mapAuditRun(row, time.Now().UTC()), nil
}

func (s *envDispatchAuditStorage) ReconcileEligibleReclamationObligations(ctx context.Context, eligibleAt, staleBefore time.Time, limit int32) ([]service.EnvDispatchAuditReclamationClaim, error) {
	rows, err := s.queries.ReconcileEligibleEnvDispatchReclamationObligations(ctx, db.ReconcileEligibleEnvDispatchReclamationObligationsParams{EligibleAt: auditTime(eligibleAt), StaleBefore: auditTime(staleBefore), LimitCount: limit})
	if err != nil {
		return nil, err
	}
	claims := make([]service.EnvDispatchAuditReclamationClaim, 0, len(rows))
	for _, row := range rows {
		lease := auditTimeValue(row.ObligationUpdatedAt)
		claims = append(claims, service.EnvDispatchAuditReclamationClaim{
			Obligation: mapAuditObligation(db.EnvDispatchReclamationObligation{ID: row.ObligationID, AuditResourceID: row.AuditResourceID, Trigger: row.Trigger, State: row.State, AttemptCount: row.AttemptCount, LastErrorCode: row.LastErrorCode, NextAttemptAt: row.NextAttemptAt, CreatedAt: row.ObligationCreatedAt, UpdatedAt: row.ObligationUpdatedAt}, &lease),
			Resource:   service.EnvDispatchAuditReclamationResource{AuditResourceID: auditUUIDString(row.AuditResourceID), Kind: service.EnvDispatchAuditResourceKind(row.ResourceKind), ResourceID: row.ResourceID, DaemonID: auditTextPtr(row.DaemonID), EnvironmentID: auditUUIDPtr(row.EnvironmentID), ProjectID: auditUUIDPtr(row.ProjectID), ChannelID: auditUUIDPtr(row.ChannelID), OwnershipMode: service.EnvDispatchAuditOwnershipMode(row.OwnershipMode), OwnerState: service.EnvDispatchAuditOwnerState(row.OwnerState), Classification: service.EnvDispatchAuditClassification(row.Classification)},
			AuditID:    auditUUIDString(row.AuditID), WorkspaceID: auditUUIDString(row.WorkspaceID), InitiatorID: auditUUIDString(row.InitiatorID), ReclamationDeadline: auditTimeValue(row.ReclamationDeadline),
		})
	}
	return claims, nil
}

func (s *envDispatchAuditStorage) MarkReclamationObligationSucceeded(ctx context.Context, obligationID string, leaseAcquiredAt *time.Time) (service.EnvDispatchAuditObligation, error) {
	id, err := auditUUID(obligationID)
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	row, err := s.queries.MarkEnvDispatchReclamationObligationSucceeded(ctx, db.MarkEnvDispatchReclamationObligationSucceededParams{ObligationID: id, LeaseAcquiredAt: auditTimePtr(leaseAcquiredAt)})
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	return mapAuditObligation(row, nil), nil
}

func (s *envDispatchAuditStorage) MarkReclamationObligationNotRequired(ctx context.Context, obligationID string, leaseAcquiredAt *time.Time) (service.EnvDispatchAuditObligation, error) {
	id, err := auditUUID(obligationID)
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	row, err := s.queries.MarkEnvDispatchReclamationObligationNotRequired(ctx, db.MarkEnvDispatchReclamationObligationNotRequiredParams{ObligationID: id, LeaseAcquiredAt: auditTimePtr(leaseAcquiredAt)})
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	return mapAuditObligation(row, nil), nil
}

func (s *envDispatchAuditStorage) RescheduleReclamationObligation(ctx context.Context, obligationID string, leaseAcquiredAt time.Time, reasonCode *string, nextAttemptAt time.Time) (service.EnvDispatchAuditObligation, error) {
	id, err := auditUUID(obligationID)
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	row, err := s.queries.RescheduleEnvDispatchReclamationObligation(ctx, db.RescheduleEnvDispatchReclamationObligationParams{ObligationID: id, LeaseAcquiredAt: auditTime(leaseAcquiredAt), LastErrorCode: auditSafeCode(reasonCode), NextAttemptAt: auditTime(nextAttemptAt)})
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	return mapAuditObligation(row, nil), nil
}

func (s *envDispatchAuditStorage) ExhaustReclamationObligation(ctx context.Context, obligationID string, leaseAcquiredAt *time.Time, reasonCode *string) (service.EnvDispatchAuditObligation, error) {
	id, err := auditUUID(obligationID)
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	row, err := s.queries.ExhaustEnvDispatchReclamationObligation(ctx, db.ExhaustEnvDispatchReclamationObligationParams{ObligationID: id, LeaseAcquiredAt: auditTimePtr(leaseAcquiredAt), LastErrorCode: auditSafeCode(reasonCode)})
	if err != nil {
		return service.EnvDispatchAuditObligation{}, err
	}
	return mapAuditObligation(row, nil), nil
}

func auditUUID(raw string) (pgtype.UUID, error) {
	id, err := uuid.Parse(raw)
	return pgtype.UUID{Bytes: id, Valid: err == nil}, err
}
func auditOptionalUUID(raw *string) (pgtype.UUID, error) {
	if raw == nil {
		return pgtype.UUID{}, nil
	}
	id, err := auditUUID(*raw)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}
func auditUUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
func auditUUIDPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	value := auditUUIDString(id)
	return &value
}
func auditText(raw *string) pgtype.Text {
	if raw == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *raw, Valid: true}
}
func auditTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}
func auditTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}
func auditTimePtr(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return auditTime(*value)
}
func auditTimeValue(value pgtype.Timestamptz) time.Time { return value.Time }
func auditTimeValuePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	out := value.Time
	return &out
}
func auditSafeCode(code *string) pgtype.Text { return auditText(sanitizedEnvDispatchAuditCode(code)) }

func mapAuditRun(row db.EnvDispatchAuditRun, asOf time.Time) service.EnvDispatchAuditReport {
	return service.EnvDispatchAuditReport{AuditID: auditUUIDString(row.ID), WorkspaceID: auditUUIDString(row.WorkspaceID), InitiatorID: auditUUIDString(row.InitiatorID), DispatchType: service.EnvDispatchAuditDispatchType(row.DispatchType), PrimaryScopeID: auditUUIDString(row.PrimaryScopeID), Outcome: service.EnvDispatchAuditOutcome(row.Outcome), Verdict: service.EnvDispatchAuditVerdict(row.Verdict), ReclamationDeadline: auditTimeValue(row.ReclamationDeadline), StartedAt: auditTimeValue(row.StartedAt), CompletedAt: auditTimeValuePtr(row.CompletedAt), CreatedAt: auditTimeValue(row.CreatedAt), UpdatedAt: auditTimeValue(row.UpdatedAt), AsOf: asOf}
}
func mapAuditResource(row db.EnvDispatchAuditResource) service.EnvDispatchAuditResource {
	return service.EnvDispatchAuditResource{ID: auditUUIDString(row.ID), AuditID: auditUUIDString(row.AuditID), Kind: service.EnvDispatchAuditResourceKind(row.ResourceKind), ResourceID: row.ResourceID, DaemonID: auditTextPtr(row.DaemonID), EnvironmentID: auditUUIDPtr(row.EnvironmentID), ProjectID: auditUUIDPtr(row.ProjectID), ChannelID: auditUUIDPtr(row.ChannelID), OwnershipMode: service.EnvDispatchAuditOwnershipMode(row.OwnershipMode), OwnerState: service.EnvDispatchAuditOwnerState(row.OwnerState), Classification: service.EnvDispatchAuditClassification(row.Classification), FirstObservedAt: auditTimeValue(row.FirstObservedAt), LastObservedAt: auditTimeValuePtr(row.LastObservedAt), ReclaimedAt: auditTimeValuePtr(row.ReclaimedAt), CreatedAt: auditTimeValue(row.CreatedAt), UpdatedAt: auditTimeValue(row.UpdatedAt)}
}
func mapAuditEvent(row db.EnvDispatchAuditEvent) service.EnvDispatchAuditEvent {
	return service.EnvDispatchAuditEvent{ID: auditUUIDString(row.ID), AuditID: auditUUIDString(row.AuditID), AuditResourceID: auditUUIDPtr(row.AuditResourceID), Sequence: row.Sequence, Type: service.EnvDispatchAuditEventType(row.EventType), ReasonCode: sanitizedEnvDispatchAuditCode(auditTextPtr(row.ReasonCode)), OccurredAt: auditTimeValue(row.OccurredAt)}
}
func mapAuditObligation(row db.EnvDispatchReclamationObligation, lease *time.Time) service.EnvDispatchAuditObligation {
	return service.EnvDispatchAuditObligation{ID: auditUUIDString(row.ID), AuditResourceID: auditUUIDString(row.AuditResourceID), Trigger: service.EnvDispatchAuditObligationTrigger(row.Trigger), State: service.EnvDispatchAuditObligationState(row.State), AttemptCount: row.AttemptCount, LastErrorCode: sanitizedEnvDispatchAuditCode(auditTextPtr(row.LastErrorCode)), NextAttemptAt: auditTimeValuePtr(row.NextAttemptAt), LeaseAcquiredAt: lease, CreatedAt: auditTimeValue(row.CreatedAt), UpdatedAt: auditTimeValue(row.UpdatedAt)}
}
