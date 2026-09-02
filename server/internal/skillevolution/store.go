// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Ledger store signals. The PostgreSQL implementation maps unique-violation
// and CAS misses onto these so callers never branch on driver errors.
var (
	// ErrLedgerNotFound marks a run/pattern/candidate id that does not
	// resolve inside the requested workspace.
	ErrLedgerNotFound = errors.New("skill evolution ledger row not found")
	// ErrLedgerConflict marks a CAS miss or a concurrent writer: the row
	// moved between read and write.
	ErrLedgerConflict = errors.New("skill evolution ledger conflict")
	// ErrActiveRunExists marks a refused run start: the evolution key
	// already has a non-terminal run (ADR 0021 D4).
	ErrActiveRunExists = errors.New("evolution key already has an active run")
	// ErrApprovalActorConflict marks a refused approval: the approver is
	// the run's proposer or the evaluation's evaluator (spec §12.7 role
	// isolation).
	ErrApprovalActorConflict = errors.New("approver is conflicted with the proposal or evaluation")
	// ErrApprovalNotUsable marks a deployment refused because its approval
	// is missing, rejected, or expired.
	ErrApprovalNotUsable = errors.New("approval is not an unexpired approval")
)

// EvolutionKey is the agent-scoped identity of one mutation lane (ADR 0021
// D4): workspace + target agent + task type + environment major version.
// The workspace scope travels as its own column; Body() is the stored key
// suffix and must stay byte-identical to the migration 492 generated
// column (target_agent_id::text || ':' || task_type || ':' ||
// environment_major_version).
type EvolutionKey struct {
	TargetAgentID           string
	TaskType                string
	EnvironmentMajorVersion string
}

func (k EvolutionKey) Body() string {
	return k.TargetAgentID + ":" + k.TaskType + ":" + k.EnvironmentMajorVersion
}

// CandidateStore is the candidate-plane port of the ledger (migration
// 492, plan Phase 3 wrap-up): admission is needs_review-only (the
// proposer never stamps later lifecycle states), and transitions are
// CAS'd against the domain lifecycle with the DB terminal guard as the
// floor.
type CandidateStore interface {
	// InsertCandidate persists one fresh candidate with its immutable
	// contract document and motivating-pattern links. Replaying the
	// identical candidate is a no-op; the same id with a different
	// contract is ErrLedgerConflict.
	InsertCandidate(ctx context.Context, record SkillCandidateRecord) error
	// GetCandidate resolves one candidate, or ErrLedgerNotFound.
	GetCandidate(ctx context.Context, workspaceID, candidateID string) (SkillCandidateRecord, error)
	// TransitionCandidateStatus CAS-updates the status; from must match
	// the stored status or the store returns ErrLedgerConflict.
	TransitionCandidateStatus(ctx context.Context, workspaceID, candidateID string, from, to CandidateStatus) error
}

// EvolutionRunRecord is the orchestrator run row of migration 492. Status
// transitions follow EvolutionRunStatus.CanTransition; terminal statuses
// are additionally guarded at the DB level.
type EvolutionRunRecord struct {
	RunID                   string
	WorkspaceID             string
	TargetAgentID           string
	TaskType                string
	EnvironmentMajorVersion string
	Status                  EvolutionRunStatus
	PinnedInputs            json.RawMessage
	CreatedByActor          string
	CreatedAt, UpdatedAt    time.Time
	TerminalAt              *time.Time
}

// LedgerStore is the storage port of the evolution ledger (ADR 0021 D7):
// this package defines the port and keeps zero storage or provider
// imports; the PostgreSQL implementation lives in the service layer.
type LedgerStore interface {
	// InsertRun persists a new run (status queued) under its evolution key,
	// refusing with ErrActiveRunExists while a non-terminal run holds the
	// key.
	InsertRun(ctx context.Context, run EvolutionRunRecord) error
	GetRun(ctx context.Context, workspaceID, runID string) (EvolutionRunRecord, error)
	// TransitionRun CAS-updates the run status; from must match the stored
	// status or the store returns ErrLedgerConflict.
	TransitionRun(ctx context.Context, workspaceID, runID string, from, to EvolutionRunStatus) error
	// InsertPatternRevision appends one immutable revision (and its
	// evidence rows) and advances the parent's current-revision pointer in
	// a single transaction; a non-linear revision is ErrLedgerConflict.
	InsertPatternRevision(ctx context.Context, record PatternRecord) error
	// LatestPatternRevision resolves the current revision of a pattern, or
	// ErrLedgerNotFound.
	LatestPatternRevision(ctx context.Context, workspaceID, patternID string) (PatternRecord, error)
}

// EvaluationStore is the evaluation-plane ledger port (migration 493,
// ADR 0021 D7): manifests and evaluation runs are append-only; the
// PostgreSQL implementation lives in the service layer next to
// LedgerStore.
type EvaluationStore interface {
	// InsertManifest persists one immutable manifest version. Replaying the
	// identical version is a no-op; the same version with a different
	// payload is ErrLedgerConflict.
	InsertManifest(ctx context.Context, manifest AssertionManifest) error
	// GetManifest resolves one manifest version with its declared
	// assertions, or ErrLedgerNotFound.
	GetManifest(ctx context.Context, workspaceID, manifestID string, version int) (AssertionManifest, error)
	// InsertEvaluationRun appends one immutable evaluation run and its
	// per-assertion results in a single transaction. Results may only
	// reference assertions the pinned manifest version declares.
	InsertEvaluationRun(ctx context.Context, record EvaluationRunRecord) error
	// GetEvaluationRun rebuilds the record with its per-assertion results,
	// or ErrLedgerNotFound.
	GetEvaluationRun(ctx context.Context, workspaceID, evaluationID string) (EvaluationRunRecord, error)
	// ListEvaluationRunsByCandidate returns the candidate's runs, newest
	// first.
	ListEvaluationRunsByCandidate(ctx context.Context, workspaceID, candidateID string) ([]EvaluationRunRecord, error)
}

// DecisionStore is the approval/deployment/rollback port (migration 494,
// ADR 0021 D7). Approvals and rollbacks are append-only; deployments
// resolve their materialization status by CAS; nothing here grants
// execution — binding/grant authority stays with the Skill catalog.
type DecisionStore interface {
	// InsertApproval appends the human-gate decision. The store refuses
	// approvers who created the run or performed the evaluation
	// (spec §12.7) with ErrApprovalActorConflict.
	InsertApproval(ctx context.Context, record ApprovalRecord) error
	GetApproval(ctx context.Context, workspaceID, approvalID string) (ApprovalRecord, error)
	// InsertDeployment appends one activation; the referenced approval
	// must exist, be approved, and be unexpired (ErrApprovalNotUsable
	// otherwise).
	InsertDeployment(ctx context.Context, record DeploymentRecord) error
	GetDeployment(ctx context.Context, workspaceID, deploymentID string) (DeploymentRecord, error)
	// TransitionDeploymentMaterialization CAS-updates the materialization
	// status; converged/fenced are terminal.
	TransitionDeploymentMaterialization(ctx context.Context, workspaceID, deploymentID string, from, to MaterializationStatus) error
	// InsertRollback appends the rollback record; the active-safe pointer
	// moves, binding history is never deleted.
	InsertRollback(ctx context.Context, record RollbackRecord) error
	GetRollback(ctx context.Context, workspaceID, rollbackID string) (RollbackRecord, error)
	// SetRollForwardStatus CAS-advances the only mutable rollback field.
	SetRollForwardStatus(ctx context.Context, workspaceID, rollbackID string, from, to RollForwardStatus) error
}

// OutboxEvent is one transactionally-published activation side effect
// (migration 494); dispatchers deliver it at least once and reconcile via
// the pending slice. ID is the outbox row identity (zero until listed).
type OutboxEvent struct {
	ID            int64
	WorkspaceID   string
	AggregateKind string
	AggregateID   string
	EventType     string
	Payload       json.RawMessage
}

// OutboxStore publishes and drains activation side effects (ADR 0021 D7
// port; the PostgreSQL implementation lives in the service layer).
type OutboxStore interface {
	// InsertOutboxEvent enqueues one event in the caller's transaction
	// scope (plain insert; callers pair it with their ledger write).
	InsertOutboxEvent(ctx context.Context, event OutboxEvent) error
	// ListPendingOutboxEvents returns undispatched events oldest-first.
	ListPendingOutboxEvents(ctx context.Context, workspaceID string, limit int) ([]OutboxEvent, error)
	// MarkOutboxEventDispatched flips one event to dispatched; false means
	// a concurrent dispatcher already claimed it (benign replay).
	MarkOutboxEventDispatched(ctx context.Context, workspaceID string, id int64) (bool, error)
	// NoteOutboxEventFailure bumps the attempt counter and records the
	// last error for reconciliation.
	NoteOutboxEventFailure(ctx context.Context, workspaceID string, id int64, reason string) error
}

// TrajectoryEligibilityStore is the durable pin ledger behind
// TrajectoryEligibility (migration 496): pins are written once at run
// start, read for projection gating, and only ever revoked.
type TrajectoryEligibilityStore interface {
	// PinEligibility persists the run-start snapshot. Re-pinning the same
	// run is a no-op; re-pinning with different content is
	// ErrLedgerConflict.
	PinEligibility(ctx context.Context, eligibility TrajectoryEligibility) error
	// GetEligibility resolves the pin for one run, or ErrLedgerNotFound.
	GetEligibility(ctx context.Context, workspaceID, runID string) (TrajectoryEligibility, error)
	// RevokeEligibility CAS-revokes a live pin; revoking an already
	// revoked pin reports ErrLedgerConflict (no second actor, no rewrite).
	RevokeEligibility(ctx context.Context, workspaceID, runID, actor, reason string, at time.Time) error
}

// BackfillCheckpointKind names the audited migration jobs that report
// through the backfill checkpoint ledger (spec §12.12).
type BackfillCheckpointKind string

const (
	// BackfillTrajectoryEligibility pins eligibility for historical runs
	// selected by an explicit, audited job.
	BackfillTrajectoryEligibility BackfillCheckpointKind = "trajectory_eligibility"
	// BackfillLegacySkillProjection projects pre-ledger skill rows into
	// the evolution ledger.
	BackfillLegacySkillProjection BackfillCheckpointKind = "legacy_skill_projection"
)

func (k BackfillCheckpointKind) Valid() bool {
	switch k {
	case BackfillTrajectoryEligibility, BackfillLegacySkillProjection:
		return true
	default:
		return false
	}
}

// BackfillMode distinguishes report-only passes from executed ones.
type BackfillMode string

const (
	BackfillModeDryRun   BackfillMode = "dry_run"
	BackfillModeExecuted BackfillMode = "executed"
)

func (m BackfillMode) Valid() bool {
	switch m {
	case BackfillModeDryRun, BackfillModeExecuted:
		return true
	default:
		return false
	}
}

// BackfillCheckpoint is one immutable job report (spec §12.12): what was
// selected, what was rejected, under which policy and watermark, by whom,
// and in which mode. Dry-run first; executed rows keep the audit trail of
// the real pass.
type BackfillCheckpoint struct {
	WorkspaceID     string                 `json:"workspace_id"`
	JobID           string                 `json:"job_id"`
	Kind            BackfillCheckpointKind `json:"kind"`
	Mode            BackfillMode           `json:"mode"`
	Actor           string                 `json:"actor"`
	PolicyVersion   string                 `json:"policy_version"`
	SourceWatermark string                 `json:"source_watermark"`
	SelectedCount   int                    `json:"selected_count"`
	RejectedCount   int                    `json:"rejected_count"`
	Reason          string                 `json:"reason"`
	CreatedAt       time.Time              `json:"created_at"`
}

// Validate enforces the report contract; counts and the watermark are
// factual claims, so only shape is checked here.
func (c BackfillCheckpoint) Validate() error {
	if err := validateOpaqueID("workspace_id", c.WorkspaceID); err != nil {
		return err
	}
	if c.JobID == "" || len(c.JobID) > 256 {
		return fmt.Errorf("backfill job_id is invalid")
	}
	if !c.Kind.Valid() {
		return fmt.Errorf("backfill kind %q is invalid", c.Kind)
	}
	if !c.Mode.Valid() {
		return fmt.Errorf("backfill mode %q is invalid", c.Mode)
	}
	if c.Actor == "" {
		return fmt.Errorf("backfill checkpoint requires an actor")
	}
	if len(c.SourceWatermark) > 256 {
		return fmt.Errorf("backfill source watermark is too long")
	}
	if c.SelectedCount < 0 || c.RejectedCount < 0 {
		return fmt.Errorf("backfill counts must not be negative")
	}
	return nil
}

// BackfillCheckpointStore appends and lists job reports.
type BackfillCheckpointStore interface {
	// RecordBackfillCheckpoint appends one report; replaying the same
	// job_id is a no-op, replaying it with different content is
	// ErrLedgerConflict.
	RecordBackfillCheckpoint(ctx context.Context, checkpoint BackfillCheckpoint) error
	// ListBackfillCheckpoints returns the newest reports first.
	ListBackfillCheckpoints(ctx context.Context, workspaceID string, limit int) ([]BackfillCheckpoint, error)
}
