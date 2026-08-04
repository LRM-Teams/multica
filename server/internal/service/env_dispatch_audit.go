package service

import (
	"context"
	"sync"
	"time"
)

const (
	auditReasonResetFailed                  = "reset_failed"
	auditReasonResetRollback                = "reset_rollback"
	auditReasonDispatchFailed               = "dispatch_failed"
	auditReasonRollbackDeleteRequested      = "rollback_delete_requested"
	auditReasonRollbackDeleted              = "rollback_deleted"
	auditReasonDeleteObservationUnavailable = "delete_observation_unavailable"
)

type envDispatchAuditContextKey struct{}

// envDispatchAuditRecorder is deliberately best-effort. Dispatch lifecycle
// correctness must never depend on audit persistence, while a recorder that
// has not been explicitly enabled must perform no storage calls at all.
type envDispatchAuditRecorder struct {
	storage   EnvDispatchAuditStorage
	auditID   string
	report    EnvDispatchAuditReport
	resources map[envDispatchAuditResourceKey]EnvDispatchAuditResource
	mu        sync.Mutex
}

type envDispatchAuditResourceKey struct {
	kind       EnvDispatchAuditResourceKind
	resourceID string
}

func startEnvDispatchAudit(ctx context.Context, storage EnvDispatchAuditStorage, in EnvDispatchInput) (context.Context, *envDispatchAuditRecorder) {
	if storage == nil || in.Audit == nil || !in.Audit.Enabled {
		return ctx, nil
	}
	now := time.Now().UTC()
	deadline := now.Add(in.Audit.ReclamationWindow)
	report, err := storage.CreateAuditRun(ctx, EnvDispatchAuditReport{
		WorkspaceID:         in.WorkspaceID,
		InitiatorID:         in.UserID,
		DispatchType:        auditDispatchType(in.DispatchType),
		PrimaryScopeID:      in.EnvID,
		Outcome:             EnvDispatchAuditOutcomeRunning,
		Verdict:             EnvDispatchAuditVerdictPending,
		ReclamationDeadline: deadline,
		StartedAt:           now,
	})
	if err != nil || report.AuditID == "" {
		return ctx, nil
	}
	recorder := &envDispatchAuditRecorder{
		storage:   storage,
		auditID:   report.AuditID,
		report:    report,
		resources: make(map[envDispatchAuditResourceKey]EnvDispatchAuditResource),
	}
	return context.WithValue(ctx, envDispatchAuditContextKey{}, recorder), recorder
}

func auditDispatchType(dispatchType EnvDispatchType) EnvDispatchAuditDispatchType {
	if dispatchType == EnvDispatchIssue {
		return EnvDispatchAuditDispatchIssue
	}
	return EnvDispatchAuditDispatchMessage
}

func envDispatchAuditFromContext(ctx context.Context) *envDispatchAuditRecorder {
	recorder, _ := ctx.Value(envDispatchAuditContextKey{}).(*envDispatchAuditRecorder)
	return recorder
}

func (r *envDispatchAuditRecorder) complete(ctx context.Context, outcome EnvDispatchAuditOutcome) {
	if r == nil {
		return
	}
	now := time.Now().UTC()
	if report, err := r.storage.UpdateAuditOutcome(ctx, r.auditID, outcome, &now); err == nil && report.AuditID != "" {
		r.mu.Lock()
		r.report = report
		r.mu.Unlock()
	}
	r.recordEvent(ctx, EnvDispatchAuditEventDispatchOutcome, "", string(outcome))
}

func (r *envDispatchAuditRecorder) reportResult() *EnvDispatchAuditReport {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.report.AuditID == "" {
		return nil
	}
	report := r.report
	return &report
}

func (r *envDispatchAuditRecorder) recordResource(ctx context.Context, kind EnvDispatchAuditResourceKind, resourceID, environmentID, projectID, channelID, daemonID string) EnvDispatchAuditResource {
	if r == nil || resourceID == "" {
		return EnvDispatchAuditResource{}
	}
	key := envDispatchAuditResourceKey{kind: kind, resourceID: resourceID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.resources[key]; ok {
		merged := mergeEnvDispatchAuditResource(existing, environmentID, projectID, channelID, daemonID)
		if merged == existing {
			return existing
		}
		resource, err := r.storage.UpsertAuditResource(ctx, merged)
		if err != nil {
			return existing
		}
		r.resources[key] = resource
		return resource
	}
	now := time.Now().UTC()
	resource, err := r.storage.UpsertAuditResource(ctx, EnvDispatchAuditResource{
		AuditID:         r.auditID,
		Kind:            kind,
		ResourceID:      resourceID,
		DaemonID:        auditOptionalString(daemonID),
		EnvironmentID:   auditOptionalString(environmentID),
		ProjectID:       auditOptionalString(projectID),
		ChannelID:       auditOptionalString(channelID),
		OwnershipMode:   EnvDispatchAuditOwnershipExclusive,
		OwnerState:      EnvDispatchAuditOwnerActive,
		Classification:  EnvDispatchAuditClassificationPending,
		FirstObservedAt: now,
	})
	if err != nil {
		return EnvDispatchAuditResource{}
	}
	r.resources[key] = resource
	return resource
}

func mergeEnvDispatchAuditResource(existing EnvDispatchAuditResource, environmentID, projectID, channelID, daemonID string) EnvDispatchAuditResource {
	merged := existing
	merged.DaemonID = auditMergeOptionalString(existing.DaemonID, daemonID)
	merged.EnvironmentID = auditMergeOptionalString(existing.EnvironmentID, environmentID)
	merged.ProjectID = auditMergeOptionalString(existing.ProjectID, projectID)
	merged.ChannelID = auditMergeOptionalString(existing.ChannelID, channelID)
	return merged
}

func auditMergeOptionalString(existing *string, incoming string) *string {
	if existing != nil || incoming == "" {
		return existing
	}
	return auditOptionalString(incoming)
}

func (r *envDispatchAuditRecorder) recordEvent(ctx context.Context, eventType EnvDispatchAuditEventType, auditResourceID, reasonCode string) {
	if r == nil {
		return
	}
	_, _ = r.storage.AppendAuditEvent(ctx, EnvDispatchAuditEvent{
		AuditID:         r.auditID,
		AuditResourceID: auditOptionalString(auditResourceID),
		Type:            eventType,
		ReasonCode:      auditOptionalString(reasonCode),
		OccurredAt:      time.Now().UTC(),
	})
}

func (r *envDispatchAuditRecorder) recordEventForResource(ctx context.Context, eventType EnvDispatchAuditEventType, kind EnvDispatchAuditResourceKind, resourceID, reasonCode string) {
	resource := r.recordResource(ctx, kind, resourceID, "", "", "", "")
	r.recordEvent(ctx, eventType, resource.ID, reasonCode)
}

func (r *envDispatchAuditRecorder) classifyResource(ctx context.Context, kind EnvDispatchAuditResourceKind, resourceID string, ownerState EnvDispatchAuditOwnerState, classification EnvDispatchAuditClassification, eventType EnvDispatchAuditEventType, reasonCode string) {
	if r == nil {
		return
	}
	resource := r.recordResource(ctx, kind, resourceID, "", "", "", "")
	if resource.ID == "" {
		return
	}
	now := time.Now().UTC()
	updated, err := r.storage.UpdateAuditResourceClassification(ctx, r.auditID, resource.ID, ownerState, classification, now)
	if err == nil {
		r.mu.Lock()
		r.resources[envDispatchAuditResourceKey{kind: kind, resourceID: resourceID}] = updated
		r.mu.Unlock()
	}
	r.recordEvent(ctx, eventType, resource.ID, reasonCode)
}

func (r *envDispatchAuditRecorder) recordSandboxRef(ctx context.Context, ref SandboxInstanceRef, environmentID, projectID string) {
	if r == nil {
		return
	}
	r.recordResource(ctx, EnvDispatchAuditResourceSandbox, ref.InstanceID, environmentID, projectID, "", ref.DaemonID)
	if ref.RuntimeID != "" {
		r.recordResource(ctx, EnvDispatchAuditResourceRuntime, ref.RuntimeID, environmentID, projectID, "", ref.DaemonID)
	}
}

func (r *envDispatchAuditRecorder) recordChannelBindings(ctx context.Context, channelID, projectID, environmentID string, roster MessageRoster) {
	if r == nil {
		return
	}
	for _, agentID := range roster.AgentIDs {
		resource := r.recordResource(ctx, EnvDispatchAuditResourceBinding, channelID+":"+agentID, environmentID, projectID, channelID, "")
		r.recordEvent(ctx, EnvDispatchAuditEventBindingObserved, resource.ID, "binding_declared")
	}
}

func (r *envDispatchAuditRecorder) recordProvisionedBinding(ctx context.Context, provisioned EnvDispatchAgentProvisionResult, environmentID, projectID, channelID string) {
	if r == nil {
		return
	}
	r.recordResource(ctx, EnvDispatchAuditResourceSandbox, provisioned.SandboxInstanceID, environmentID, projectID, channelID, provisioned.DaemonID)
	r.recordResource(ctx, EnvDispatchAuditResourceRuntime, provisioned.RuntimeID, environmentID, projectID, channelID, provisioned.DaemonID)
	r.recordResource(ctx, EnvDispatchAuditResourceSession, provisioned.ChatSessionID, environmentID, projectID, channelID, "")
}

func (r *envDispatchAuditRecorder) recordSession(ctx context.Context, sessionID, environmentID, projectID, channelID string) {
	r.recordResource(ctx, EnvDispatchAuditResourceSession, sessionID, environmentID, projectID, channelID, "")
}

func (r *envDispatchAuditRecorder) recordTask(ctx context.Context, taskID, environmentID, projectID, channelID string) {
	r.recordResource(ctx, EnvDispatchAuditResourceTask, taskID, environmentID, projectID, channelID, "")
}

func auditOptionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// EnvDispatchAuditRequest enables correlation-scoped daemon reclamation
// auditing for a dispatch. It deliberately contains no client-provided audit
// identifier: the service creates the audit run after it has resolved the
// dispatch scope. It also carries no task or message content.
type EnvDispatchAuditRequest struct {
	Enabled           bool
	ReclamationWindow time.Duration
}

// EnvDispatchAuditDispatchType identifies the dispatch family that owns an
// audit run.
type EnvDispatchAuditDispatchType string

const (
	EnvDispatchAuditDispatchIssue   EnvDispatchAuditDispatchType = "issue"
	EnvDispatchAuditDispatchMessage EnvDispatchAuditDispatchType = "message"
)

// EnvDispatchAuditOutcome is the terminal result of the dispatch under audit.
type EnvDispatchAuditOutcome string

const (
	EnvDispatchAuditOutcomeRunning   EnvDispatchAuditOutcome = "running"
	EnvDispatchAuditOutcomeSucceeded EnvDispatchAuditOutcome = "succeeded"
	EnvDispatchAuditOutcomeRejected  EnvDispatchAuditOutcome = "rejected"
	EnvDispatchAuditOutcomeFailed    EnvDispatchAuditOutcome = "failed"
	EnvDispatchAuditOutcomeTimedOut  EnvDispatchAuditOutcome = "timed_out"
	EnvDispatchAuditOutcomeCancelled EnvDispatchAuditOutcome = "cancelled"
	EnvDispatchAuditOutcomeDeleted   EnvDispatchAuditOutcome = "deleted"
)

// EnvDispatchAuditVerdict is the audit's terminal evidence conclusion.
type EnvDispatchAuditVerdict string

const (
	EnvDispatchAuditVerdictPending        EnvDispatchAuditVerdict = "pending"
	EnvDispatchAuditVerdictNoLeakObserved EnvDispatchAuditVerdict = "no_leak_observed"
	EnvDispatchAuditVerdictLeakConfirmed  EnvDispatchAuditVerdict = "leak_confirmed"
	EnvDispatchAuditVerdictInconclusive   EnvDispatchAuditVerdict = "inconclusive"
)

// EnvDispatchAuditResourceKind identifies a reclaimable dispatch resource.
type EnvDispatchAuditResourceKind string

const (
	EnvDispatchAuditResourceSandbox EnvDispatchAuditResourceKind = "sandbox"
	EnvDispatchAuditResourceRuntime EnvDispatchAuditResourceKind = "runtime"
	EnvDispatchAuditResourceBinding EnvDispatchAuditResourceKind = "binding"
	EnvDispatchAuditResourceTask    EnvDispatchAuditResourceKind = "task"
	EnvDispatchAuditResourceSession EnvDispatchAuditResourceKind = "session"
)

// EnvDispatchAuditOwnershipMode declares whether reclamation is exclusive to
// this audit or must be deferred because another owner may still use it.
type EnvDispatchAuditOwnershipMode string

const (
	EnvDispatchAuditOwnershipExclusive EnvDispatchAuditOwnershipMode = "exclusive"
	EnvDispatchAuditOwnershipShared    EnvDispatchAuditOwnershipMode = "shared"
)

// EnvDispatchAuditOwnerState is the observed lifecycle state of a resource's
// dispatch owner.
type EnvDispatchAuditOwnerState string

const (
	EnvDispatchAuditOwnerActive   EnvDispatchAuditOwnerState = "active"
	EnvDispatchAuditOwnerTerminal EnvDispatchAuditOwnerState = "terminal"
	EnvDispatchAuditOwnerDeleted  EnvDispatchAuditOwnerState = "deleted"
	EnvDispatchAuditOwnerUnknown  EnvDispatchAuditOwnerState = "unknown"
)

// EnvDispatchAuditClassification is the audit's evidence-based classification
// of a resource. It is intentionally distinct from its owner state.
type EnvDispatchAuditClassification string

const (
	EnvDispatchAuditClassificationPending            EnvDispatchAuditClassification = "pending"
	EnvDispatchAuditClassificationReclaimed          EnvDispatchAuditClassification = "reclaimed"
	EnvDispatchAuditClassificationLegitimatelyActive EnvDispatchAuditClassification = "legitimately_active"
	EnvDispatchAuditClassificationUnreclaimed        EnvDispatchAuditClassification = "unreclaimed"
	EnvDispatchAuditClassificationInconclusive       EnvDispatchAuditClassification = "inconclusive"
)

// EnvDispatchAuditEventType is an append-only observation in an audit run.
type EnvDispatchAuditEventType string

const (
	EnvDispatchAuditEventProvisioned              EnvDispatchAuditEventType = "provisioned"
	EnvDispatchAuditEventBindingObserved          EnvDispatchAuditEventType = "binding_observed"
	EnvDispatchAuditEventCreationFailed           EnvDispatchAuditEventType = "creation_failed"
	EnvDispatchAuditEventRollbackStarted          EnvDispatchAuditEventType = "rollback_started"
	EnvDispatchAuditEventOwnerTerminal            EnvDispatchAuditEventType = "owner_terminal"
	EnvDispatchAuditEventOwnershipDeferred        EnvDispatchAuditEventType = "ownership_deferred"
	EnvDispatchAuditEventCleanupRequested         EnvDispatchAuditEventType = "cleanup_requested"
	EnvDispatchAuditEventCleanupAttempted         EnvDispatchAuditEventType = "cleanup_attempted"
	EnvDispatchAuditEventCleanupRetryScheduled    EnvDispatchAuditEventType = "cleanup_retry_scheduled"
	EnvDispatchAuditEventCleanupExhausted         EnvDispatchAuditEventType = "cleanup_exhausted"
	EnvDispatchAuditEventRuntimeOfflined          EnvDispatchAuditEventType = "runtime_offlined"
	EnvDispatchAuditEventSandboxDeletionRequested EnvDispatchAuditEventType = "sandbox_deletion_requested"
	EnvDispatchAuditEventReclaimed                EnvDispatchAuditEventType = "reclaimed"
	EnvDispatchAuditEventCleanupFailed            EnvDispatchAuditEventType = "cleanup_failed"
	EnvDispatchAuditEventObservationUnavailable   EnvDispatchAuditEventType = "observation_unavailable"
	EnvDispatchAuditEventClassificationUpdated    EnvDispatchAuditEventType = "classification_updated"
	EnvDispatchAuditEventDispatchOutcome          EnvDispatchAuditEventType = "dispatch_outcome"
)

// EnvDispatchAuditObligationTrigger identifies why a resource must be
// reclaimed.
type EnvDispatchAuditObligationTrigger string

const (
	EnvDispatchAuditObligationTerminal      EnvDispatchAuditObligationTrigger = "terminal"
	EnvDispatchAuditObligationFailure       EnvDispatchAuditObligationTrigger = "failure"
	EnvDispatchAuditObligationTimeout       EnvDispatchAuditObligationTrigger = "timeout"
	EnvDispatchAuditObligationCancellation  EnvDispatchAuditObligationTrigger = "cancellation"
	EnvDispatchAuditObligationProjectDelete EnvDispatchAuditObligationTrigger = "project_delete"
	EnvDispatchAuditObligationChannelDelete EnvDispatchAuditObligationTrigger = "channel_delete"
	EnvDispatchAuditObligationRollback      EnvDispatchAuditObligationTrigger = "rollback"
)

// EnvDispatchAuditObligationState is the durable state of one reclamation
// obligation.
type EnvDispatchAuditObligationState string

const (
	EnvDispatchAuditObligationPending     EnvDispatchAuditObligationState = "pending"
	EnvDispatchAuditObligationInProgress  EnvDispatchAuditObligationState = "in_progress"
	EnvDispatchAuditObligationSucceeded   EnvDispatchAuditObligationState = "succeeded"
	EnvDispatchAuditObligationExhausted   EnvDispatchAuditObligationState = "exhausted"
	EnvDispatchAuditObligationNotRequired EnvDispatchAuditObligationState = "not_required"
)

// EnvDispatchAuditResource is identifier- and state-only evidence for one
// resource. For sandbox resources, ResourceID is the sandbox instance ID; no
// task or message content is retained.
type EnvDispatchAuditResource struct {
	ID              string
	AuditID         string
	Kind            EnvDispatchAuditResourceKind
	ResourceID      string
	DaemonID        *string
	EnvironmentID   *string
	ProjectID       *string
	ChannelID       *string
	OwnershipMode   EnvDispatchAuditOwnershipMode
	OwnerState      EnvDispatchAuditOwnerState
	Classification  EnvDispatchAuditClassification
	FirstObservedAt time.Time
	LastObservedAt  *time.Time
	ReclaimedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EnvDispatchAuditReclamationResource is the reconciliation snapshot of one
// resource. It contains only the identity, ownership, and classification
// fields projected by reconciliation; it is not a report resource and never
// requires an adapter to fabricate observation timestamps.
type EnvDispatchAuditReclamationResource struct {
	AuditResourceID string
	Kind            EnvDispatchAuditResourceKind
	ResourceID      string
	DaemonID        *string
	EnvironmentID   *string
	ProjectID       *string
	ChannelID       *string
	OwnershipMode   EnvDispatchAuditOwnershipMode
	OwnerState      EnvDispatchAuditOwnerState
	Classification  EnvDispatchAuditClassification
}

// EnvDispatchAuditEvent is an append-only, sequence-ordered observation. A
// reason code must be sanitized by the storage adapter; no error detail,
// transport payload, task content, or message content belongs here.
type EnvDispatchAuditEvent struct {
	ID              string
	AuditID         string
	AuditResourceID *string
	Sequence        int64
	Type            EnvDispatchAuditEventType
	ReasonCode      *string
	OccurredAt      time.Time
}

// EnvDispatchAuditObligation is the current reclamation responsibility for a
// resource. LeaseAcquiredAt is the storage-issued lease token for an
// in-progress obligation.
type EnvDispatchAuditObligation struct {
	ID              string
	AuditResourceID string
	Trigger         EnvDispatchAuditObligationTrigger
	State           EnvDispatchAuditObligationState
	AttemptCount    int32
	LastErrorCode   *string
	NextAttemptAt   *time.Time
	LeaseAcquiredAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EnvDispatchAuditReclamationClaim is an eligible reclamation obligation plus
// the scoped resource and audit context needed to process it safely. It is
// returned only by reconciliation, whose storage query atomically leases the
// obligation and supplies LeaseAcquiredAt.
type EnvDispatchAuditReclamationClaim struct {
	Obligation          EnvDispatchAuditObligation
	Resource            EnvDispatchAuditReclamationResource
	AuditID             string
	WorkspaceID         string
	InitiatorID         string
	ReclamationDeadline time.Time
}

// EnvDispatchAuditReport is the correlation-scoped audit view. AsOf records
// when the report was assembled rather than a mutable database field.
type EnvDispatchAuditReport struct {
	AuditID             string
	WorkspaceID         string
	InitiatorID         string
	DispatchType        EnvDispatchAuditDispatchType
	PrimaryScopeID      string
	Outcome             EnvDispatchAuditOutcome
	Verdict             EnvDispatchAuditVerdict
	ReclamationDeadline time.Time
	StartedAt           time.Time
	CompletedAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	AsOf                time.Time
	Resources           []EnvDispatchAuditResource
	Events              []EnvDispatchAuditEvent
	Obligations         []EnvDispatchAuditObligation
}

// EnvDispatchAuditStorage persists the correlation-scoped audit ledger.
// Implementations must keep report reads scoped by audit, workspace, and
// initiator, and must serialize event sequence assignment inside the append
// transaction. The interface exposes structured identifiers, states, times,
// and sanitized reason codes only.
type EnvDispatchAuditStorage interface {
	CreateAuditRun(ctx context.Context, report EnvDispatchAuditReport) (EnvDispatchAuditReport, error)
	LoadAuditReport(ctx context.Context, auditID, workspaceID, initiatorID string, asOf time.Time) (EnvDispatchAuditReport, error)
	UpsertAuditResource(ctx context.Context, resource EnvDispatchAuditResource) (EnvDispatchAuditResource, error)
	UpdateAuditResourceClassification(ctx context.Context, auditID, auditResourceID string, ownerState EnvDispatchAuditOwnerState, classification EnvDispatchAuditClassification, observedAt time.Time) (EnvDispatchAuditResource, error)
	AppendAuditEvent(ctx context.Context, event EnvDispatchAuditEvent) (EnvDispatchAuditEvent, error)
	EnsureReclamationObligation(ctx context.Context, obligation EnvDispatchAuditObligation) (EnvDispatchAuditObligation, error)
	UpdateAuditOutcome(ctx context.Context, auditID string, outcome EnvDispatchAuditOutcome, completedAt *time.Time) (EnvDispatchAuditReport, error)
	UpdateAuditVerdict(ctx context.Context, auditID string, verdict EnvDispatchAuditVerdict, completedAt *time.Time) (EnvDispatchAuditReport, error)
	ReconcileEligibleReclamationObligations(ctx context.Context, eligibleAt, staleBefore time.Time, limit int32) ([]EnvDispatchAuditReclamationClaim, error)
	MarkReclamationObligationSucceeded(ctx context.Context, obligationID string, leaseAcquiredAt *time.Time) (EnvDispatchAuditObligation, error)
	MarkReclamationObligationNotRequired(ctx context.Context, obligationID string, leaseAcquiredAt *time.Time) (EnvDispatchAuditObligation, error)
	RescheduleReclamationObligation(ctx context.Context, obligationID string, leaseAcquiredAt time.Time, reasonCode *string, nextAttemptAt time.Time) (EnvDispatchAuditObligation, error)
	ExhaustReclamationObligation(ctx context.Context, obligationID string, leaseAcquiredAt *time.Time, reasonCode *string) (EnvDispatchAuditObligation, error)
}
