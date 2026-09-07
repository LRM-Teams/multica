// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// PatternKind classifies the observable outcome distribution behind a
// pattern (spec §12.4).
type PatternKind string

const (
	PatternKindSuccess PatternKind = "success"
	PatternKindFailure PatternKind = "failure"
	PatternKindMixed   PatternKind = "mixed"
)

func (k PatternKind) Valid() bool {
	switch k {
	case PatternKindSuccess, PatternKindFailure, PatternKindMixed:
		return true
	default:
		return false
	}
}

// PatternStatus is the evidence-gated lifecycle of a pattern revision.
// Supported requires multiple independent lineage/task evidence; negative
// evidence must be able to demote or refute (spec §12.4).
type PatternStatus string

const (
	PatternStatusTentative    PatternStatus = "tentative"
	PatternStatusSupported    PatternStatus = "supported"
	PatternStatusContradicted PatternStatus = "contradicted"
	PatternStatusRefuted      PatternStatus = "refuted"
	PatternStatusStale        PatternStatus = "stale"
)

func (s PatternStatus) Valid() bool {
	switch s {
	case PatternStatusTentative, PatternStatusSupported, PatternStatusContradicted, PatternStatusRefuted, PatternStatusStale:
		return true
	default:
		return false
	}
}

func (s PatternStatus) Terminal() bool {
	return s == PatternStatusRefuted || s == PatternStatusStale
}

// patternStatusTransitions encodes the spec §12.5 rules: merges and new
// negative evidence may demote, refutation is terminal, and re-validation
// happens on a new revision rather than by resurrecting a terminal status.
var patternStatusTransitions = map[PatternStatus][]PatternStatus{
	PatternStatusTentative:    {PatternStatusSupported, PatternStatusContradicted, PatternStatusRefuted, PatternStatusStale},
	PatternStatusSupported:    {PatternStatusContradicted, PatternStatusRefuted, PatternStatusTentative, PatternStatusStale},
	PatternStatusContradicted: {PatternStatusTentative, PatternStatusStale},
	PatternStatusRefuted:      {},
	PatternStatusStale:        {},
}

// CanTransition reports whether a pattern status change is legal for the
// same pattern_id. Stale/refuted recovery requires a new revision.
func (s PatternStatus) CanTransition(next PatternStatus) bool {
	for _, allowed := range patternStatusTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// CandidateStatus is the proposal-decision lifecycle of a SkillCandidate
// (spec §12.4). Accepted means the proposal decision only; grants and
// bindings live in Approval/Deployment records.
type CandidateStatus string

const (
	CandidateStatusNeedsReview CandidateStatus = "needs_review"
	CandidateStatusShadow      CandidateStatus = "shadow"
	CandidateStatusEvaluating  CandidateStatus = "evaluating"
	CandidateStatusAccepted    CandidateStatus = "accepted"
	CandidateStatusRejected    CandidateStatus = "rejected"
	CandidateStatusStale       CandidateStatus = "stale"
	CandidateStatusWithdrawn   CandidateStatus = "withdrawn"
	CandidateStatusSuperseded  CandidateStatus = "superseded"
)

func (s CandidateStatus) Valid() bool {
	switch s {
	case CandidateStatusNeedsReview, CandidateStatusShadow, CandidateStatusEvaluating,
		CandidateStatusAccepted, CandidateStatusRejected, CandidateStatusStale,
		CandidateStatusWithdrawn, CandidateStatusSuperseded:
		return true
	default:
		return false
	}
}

func (s CandidateStatus) Terminal() bool {
	switch s {
	case CandidateStatusRejected, CandidateStatusStale, CandidateStatusWithdrawn, CandidateStatusSuperseded:
		return true
	default:
		return false
	}
}

// candidateStatusTransitions keeps rejected and withdrawn terminal: a
// materially changed proposal is a new candidate with a new fingerprint,
// not a status change (spec §12.5).
var candidateStatusTransitions = map[CandidateStatus][]CandidateStatus{
	CandidateStatusNeedsReview: {CandidateStatusShadow, CandidateStatusEvaluating, CandidateStatusRejected, CandidateStatusWithdrawn, CandidateStatusStale},
	CandidateStatusShadow:      {CandidateStatusEvaluating, CandidateStatusRejected, CandidateStatusWithdrawn, CandidateStatusStale},
	CandidateStatusEvaluating:  {CandidateStatusAccepted, CandidateStatusRejected, CandidateStatusWithdrawn, CandidateStatusStale},
	CandidateStatusAccepted:    {CandidateStatusSuperseded, CandidateStatusStale},
	CandidateStatusRejected:    {},
	CandidateStatusStale:       {},
	CandidateStatusWithdrawn:   {},
	CandidateStatusSuperseded:  {},
}

func (s CandidateStatus) CanTransition(next CandidateStatus) bool {
	for _, allowed := range candidateStatusTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// EvolutionRunStatus is the orchestrator run lifecycle (spec §12.6):
// queued → snapshotting → consolidating_patterns → proposing_candidate →
// awaiting_review → evaluating → awaiting_approval → completed, plus
// terminal no_action | rejected | cancelled | failed | stale | fenced.
type EvolutionRunStatus string

const (
	EvolutionRunQueued                EvolutionRunStatus = "queued"
	EvolutionRunSnapshotting          EvolutionRunStatus = "snapshotting"
	EvolutionRunConsolidatingPatterns EvolutionRunStatus = "consolidating_patterns"
	EvolutionRunProposingCandidate    EvolutionRunStatus = "proposing_candidate"
	EvolutionRunAwaitingReview        EvolutionRunStatus = "awaiting_review"
	EvolutionRunEvaluating            EvolutionRunStatus = "evaluating"
	EvolutionRunAwaitingApproval      EvolutionRunStatus = "awaiting_approval"
	EvolutionRunCompleted             EvolutionRunStatus = "completed"
	EvolutionRunNoAction              EvolutionRunStatus = "no_action"
	EvolutionRunRejected              EvolutionRunStatus = "rejected"
	EvolutionRunCancelled             EvolutionRunStatus = "cancelled"
	EvolutionRunFailed                EvolutionRunStatus = "failed"
	EvolutionRunStale                 EvolutionRunStatus = "stale"
	EvolutionRunFenced                EvolutionRunStatus = "fenced"
)

func (s EvolutionRunStatus) Valid() bool {
	switch s {
	case EvolutionRunQueued, EvolutionRunSnapshotting, EvolutionRunConsolidatingPatterns,
		EvolutionRunProposingCandidate, EvolutionRunAwaitingReview, EvolutionRunEvaluating,
		EvolutionRunAwaitingApproval, EvolutionRunCompleted, EvolutionRunNoAction,
		EvolutionRunRejected, EvolutionRunCancelled, EvolutionRunFailed,
		EvolutionRunStale, EvolutionRunFenced:
		return true
	default:
		return false
	}
}

func (s EvolutionRunStatus) Terminal() bool {
	switch s {
	case EvolutionRunCompleted, EvolutionRunNoAction, EvolutionRunRejected,
		EvolutionRunCancelled, EvolutionRunFailed, EvolutionRunStale, EvolutionRunFenced:
		return true
	default:
		return false
	}
}

// interruptionTerminals are reachable from every non-terminal status:
// safety fences and operator cancellation must never be blocked by the
// happy-path transition table.
var interruptionTerminals = []EvolutionRunStatus{
	EvolutionRunCancelled, EvolutionRunFailed, EvolutionRunStale, EvolutionRunFenced,
}

// evolutionRunTransitions is the happy path of spec §12.6. Every
// non-terminal status may additionally transition into any of
// interruptionTerminals.
var evolutionRunTransitions = map[EvolutionRunStatus][]EvolutionRunStatus{
	EvolutionRunQueued:                {EvolutionRunSnapshotting},
	EvolutionRunSnapshotting:          {EvolutionRunConsolidatingPatterns, EvolutionRunNoAction},
	EvolutionRunConsolidatingPatterns: {EvolutionRunProposingCandidate, EvolutionRunNoAction},
	EvolutionRunProposingCandidate:    {EvolutionRunAwaitingReview, EvolutionRunNoAction},
	EvolutionRunAwaitingReview:        {EvolutionRunEvaluating, EvolutionRunRejected},
	EvolutionRunEvaluating:            {EvolutionRunAwaitingApproval, EvolutionRunRejected},
	EvolutionRunAwaitingApproval:      {EvolutionRunCompleted, EvolutionRunRejected},
}

func (s EvolutionRunStatus) CanTransition(next EvolutionRunStatus) bool {
	for _, allowed := range evolutionRunTransitions[s] {
		if allowed == next {
			return true
		}
	}
	if !s.Terminal() {
		for _, allowed := range interruptionTerminals {
			if allowed == next {
				return true
			}
		}
	}
	return false
}

// PatternRecord is the canonical durable-ledger pattern record (spec
// §12.4). Graph NodeRole=pattern nodes are projections of this record.
type PatternRecord struct {
	ContractKind      string              `json:"contract_kind"`
	SchemaVersion     int                 `json:"schema_version"`
	PatternID         string              `json:"pattern_id"`
	Revision          int64               `json:"revision"`
	WorkspaceID       string              `json:"workspace_id"`
	EvolutionKey      string              `json:"evolution_key"`
	PatternKind       PatternKind         `json:"pattern_kind"`
	Status            PatternStatus       `json:"status"`
	Problem           string              `json:"problem"`
	Applicability     string              `json:"applicability"`
	RootCauseSummary  string              `json:"root_cause_summary"`
	RecommendedAction string              `json:"recommended_action"`
	PositiveEvidence  []SkillEvolutionRef `json:"positive_evidence_refs"`
	NegativeEvidence  []SkillEvolutionRef `json:"negative_evidence_refs"`
	TaskType          string              `json:"task_type"`
	SourceModelID     string              `json:"source_model_id"`
	TargetModelID     string              `json:"target_model_id"`
	ProviderID        string              `json:"provider_id"`
	ToolCapabilityID  string              `json:"tool_capability_id"`
	RuntimeID         string              `json:"runtime_id"`
	EnvironmentKey    string              `json:"environment_key"`
	GeneratorVersion  string              `json:"generator_version"`
	PolicyVersion     string              `json:"policy_version"`
	ContentHash       string              `json:"content_hash"`
	CreatedByActor    string              `json:"created_by_actor"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedByActor    string              `json:"updated_by_actor"`
	UpdatedAt         time.Time           `json:"updated_at"`
}

func (p PatternRecord) Validate() error {
	if p.ContractKind != "pattern" || p.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=pattern and schema_version=1 are required", ErrInvalidContract)
	}
	if err := validateOpaqueID("pattern_id", p.PatternID); err != nil {
		return err
	}
	if err := validateOpaqueID("workspace_id", p.WorkspaceID); err != nil {
		return err
	}
	if p.Revision < 1 {
		return fmt.Errorf("%w: revision must be >= 1", ErrInvalidContract)
	}
	if !p.PatternKind.Valid() {
		return fmt.Errorf("%w: pattern_kind %q is invalid", ErrInvalidContract, p.PatternKind)
	}
	if !p.Status.Valid() {
		return fmt.Errorf("%w: pattern status %q is invalid", ErrInvalidContract, p.Status)
	}
	if p.Problem == "" || p.Applicability == "" || p.RootCauseSummary == "" || p.RecommendedAction == "" {
		return fmt.Errorf("%w: problem, applicability, root_cause_summary, and recommended_action are required", ErrInvalidContract)
	}
	if !validSHA256(p.ContentHash) {
		return fmt.Errorf("%w: content_hash must be a sha256 hash", ErrInvalidContract)
	}
	if err := validateEvidenceRefs("positive_evidence_refs", p.PositiveEvidence); err != nil {
		return err
	}
	if err := validateEvidenceRefs("negative_evidence_refs", p.NegativeEvidence); err != nil {
		return err
	}
	if p.Status == PatternStatusSupported && len(p.PositiveEvidence) == 0 {
		return fmt.Errorf("%w: supported patterns require positive evidence", ErrInvalidContract)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() || p.UpdatedAt.Before(p.CreatedAt) {
		return fmt.Errorf("%w: created_at/updated_at are invalid", ErrInvalidContract)
	}
	return nil
}

func validateEvidenceRefs(field string, refs []SkillEvolutionRef) error {
	if len(refs) > 64 {
		return fmt.Errorf("%w: %s must contain at most 64 refs", ErrInvalidContract, field)
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return err
		}
		key := string(ref.Kind) + "\x00" + ref.WorkspaceID + "\x00" + ref.ID
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: %s contains a duplicate ref", ErrInvalidContract, field)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// EvaluationAssertionResult is one per-assertion outcome inside an
// append-only EvaluationRun (spec §12.4).
type EvaluationAssertionResult string

const (
	AssertionPassed  EvaluationAssertionResult = "pass"
	AssertionFailed  EvaluationAssertionResult = "fail"
	AssertionErrored EvaluationAssertionResult = "error"
	AssertionNotRun  EvaluationAssertionResult = "not_run"
)

func (r EvaluationAssertionResult) Valid() bool {
	switch r {
	case AssertionPassed, AssertionFailed, AssertionErrored, AssertionNotRun:
		return true
	default:
		return false
	}
}

// AssertionResult pairs an assertion with its terminal outcome.
type AssertionResult struct {
	AssertionID  string                    `json:"assertion_id"`
	Result       EvaluationAssertionResult `json:"result"`
	EvidenceHash string                    `json:"evidence_hash"`
}

// ContaminationStatus describes dataset lineage health for a run.
type ContaminationStatus string

const (
	ContaminationClean     ContaminationStatus = "clean"
	ContaminationSuspected ContaminationStatus = "suspected"
	ContaminationConfirmed ContaminationStatus = "confirmed"
)

func (c ContaminationStatus) Valid() bool {
	switch c {
	case ContaminationClean, ContaminationSuspected, ContaminationConfirmed:
		return true
	default:
		return false
	}
}

// EvaluationTerminalResult is the run-level verdict. Gate dimensions stay
// separate inside the run; this is only the terminal rollup.
type EvaluationTerminalResult string

const (
	EvaluationPassed       EvaluationTerminalResult = "passed"
	EvaluationFailed       EvaluationTerminalResult = "failed"
	EvaluationInconclusive EvaluationTerminalResult = "inconclusive"
	EvaluationInfraInvalid EvaluationTerminalResult = "infrastructure_invalid"
)

func (r EvaluationTerminalResult) Valid() bool {
	switch r {
	case EvaluationPassed, EvaluationFailed, EvaluationInconclusive, EvaluationInfraInvalid:
		return true
	default:
		return false
	}
}

// EvaluationRunRecord is the append-only evaluation ledger entry (spec
// §12.4). Retries or changed scorer/policy/manifest create a new run.
type EvaluationRunRecord struct {
	ContractKind          string                   `json:"contract_kind"`
	SchemaVersion         int                      `json:"schema_version"`
	EvaluationID          string                   `json:"evaluation_id"`
	WorkspaceID           string                   `json:"workspace_id"`
	CandidateID           string                   `json:"candidate_id"`
	ManifestID            string                   `json:"manifest_id"`
	ManifestVersion       int                      `json:"manifest_version"`
	BaseArtifactHash      string                   `json:"base_artifact_hash"`
	CandidateArtifactHash string                   `json:"candidate_artifact_hash"`
	ManifestHash          string                   `json:"manifest_hash"`
	TargetAgentID         string                   `json:"target_agent_id"`
	TargetModelID         string                   `json:"target_model_id"`
	ProviderID            string                   `json:"provider_id"`
	ToolCapabilityID      string                   `json:"tool_capability_id"`
	RuntimeID             string                   `json:"runtime_id"`
	EnvironmentKey        string                   `json:"environment_key"`
	AssertionResults      []AssertionResult        `json:"assertion_results"`
	Metrics               json.RawMessage          `json:"metrics,omitempty"`
	Contamination         ContaminationStatus      `json:"contamination_status"`
	DecisionPolicyVersion string                   `json:"decision_policy_version"`
	TerminalResult        EvaluationTerminalResult `json:"terminal_result"`
	TerminalReason        string                   `json:"terminal_reason"`
	CreatedByActor        string                   `json:"created_by_actor"`
	CreatedAt             time.Time                `json:"created_at"`
}

func (e EvaluationRunRecord) Validate() error {
	if e.ContractKind != "evaluation_run" || e.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=evaluation_run and schema_version=1 are required", ErrInvalidContract)
	}
	if err := validateOpaqueID("evaluation_id", e.EvaluationID); err != nil {
		return err
	}
	if err := validateOpaqueID("workspace_id", e.WorkspaceID); err != nil {
		return err
	}
	if err := validateOpaqueID("candidate_id", e.CandidateID); err != nil {
		return err
	}
	if err := validateOpaqueID("manifest_id", e.ManifestID); err != nil {
		return err
	}
	if e.ManifestVersion < 1 {
		return fmt.Errorf("%w: manifest_version must be >= 1", ErrInvalidContract)
	}
	if len(e.Metrics) > maxContractBytes {
		return fmt.Errorf("%w: metrics exceed the contract size budget", ErrInvalidContract)
	}
	if e.CreatedByActor == "" {
		return fmt.Errorf("%w: created_by_actor is required", ErrInvalidContract)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"base_artifact_hash", e.BaseArtifactHash},
		{"candidate_artifact_hash", e.CandidateArtifactHash},
		{"manifest_hash", e.ManifestHash},
	} {
		if !validSHA256(field.value) {
			return fmt.Errorf("%w: %s must be a sha256 hash", ErrInvalidContract, field.name)
		}
	}
	if !e.Contamination.Valid() {
		return fmt.Errorf("%w: contamination_status %q is invalid", ErrInvalidContract, e.Contamination)
	}
	if !e.TerminalResult.Valid() {
		return fmt.Errorf("%w: terminal_result %q is invalid", ErrInvalidContract, e.TerminalResult)
	}
	if e.Contamination == ContaminationConfirmed && e.TerminalResult == EvaluationPassed {
		return fmt.Errorf("%w: a contaminated run cannot pass", ErrInvalidContract)
	}
	seen := make(map[string]struct{}, len(e.AssertionResults))
	for _, result := range e.AssertionResults {
		if err := validateOpaqueID("assertion_id", result.AssertionID); err != nil {
			return err
		}
		if !result.Result.Valid() {
			return fmt.Errorf("%w: assertion %q has invalid result %q", ErrInvalidContract, result.AssertionID, result.Result)
		}
		if !validSHA256(result.EvidenceHash) {
			return fmt.Errorf("%w: assertion %q evidence_hash must be a sha256 hash", ErrInvalidContract, result.AssertionID)
		}
		if _, duplicate := seen[result.AssertionID]; duplicate {
			return fmt.Errorf("%w: assertion %q appears twice", ErrInvalidContract, result.AssertionID)
		}
		seen[result.AssertionID] = struct{}{}
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidContract)
	}
	return nil
}

// AssertionSpec is one machine-checkable assertion declared by an
// AssertionManifest version (spec §12.4). Kind stays open here; domain
// profiles (e.g. spreadsheet) pin their own closed vocabularies on top.
type AssertionSpec struct {
	AssertionID   string `json:"assertion_id"`
	Kind          string `json:"kind"`
	OracleRefHash string `json:"oracle_ref_hash"`
	Severity      string `json:"severity"`
	Hard          bool   `json:"hard"`
	Required      bool   `json:"required"`
	Tolerance     string `json:"tolerance"`
}

// AssertionManifest is the immutable, versioned machine-judged contract an
// EvaluationRun is scored against (spec §12.4). A changed contract ships as
// a new version row: manifests never mutate in place, and evaluation
// results may only reference assertions the pinned version declares.
type AssertionManifest struct {
	ContractKind         string          `json:"contract_kind"`
	SchemaVersion        int             `json:"schema_version"`
	ManifestID           string          `json:"manifest_id"`
	Version              int             `json:"version"`
	WorkspaceID          string          `json:"workspace_id"`
	ManifestHash         string          `json:"manifest_hash"`
	DatasetIdentity      string          `json:"dataset_identity"`
	DatasetVersion       string          `json:"dataset_version"`
	LineageSplit         string          `json:"lineage_split"`
	DomainProfile        string          `json:"domain_profile"`
	TaskSlices           json.RawMessage `json:"task_slices,omitempty"`
	EvaluatorVersion     string          `json:"evaluator_version"`
	ScorerVersion        string          `json:"scorer_version"`
	EnvironmentKey       string          `json:"environment_key"`
	RequiredCapabilities json.RawMessage `json:"required_capabilities,omitempty"`
	DataResidency        string          `json:"data_residency"`
	Assertions           []AssertionSpec `json:"assertions"`
	CreatedByActor       string          `json:"created_by_actor"`
	CreatedAt            time.Time       `json:"created_at"`
}

func (m AssertionManifest) Validate() error {
	if m.ContractKind != "assertion_manifest" || m.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=assertion_manifest and schema_version=1 are required", ErrInvalidContract)
	}
	if err := validateOpaqueID("manifest_id", m.ManifestID); err != nil {
		return err
	}
	if err := validateOpaqueID("workspace_id", m.WorkspaceID); err != nil {
		return err
	}
	if m.Version < 1 {
		return fmt.Errorf("%w: manifest version must be >= 1", ErrInvalidContract)
	}
	if !validSHA256(m.ManifestHash) {
		return fmt.Errorf("%w: manifest_hash must be a sha256 hash", ErrInvalidContract)
	}
	if err := validateOpaqueID("dataset_identity", m.DatasetIdentity); err != nil {
		return err
	}
	if len(m.TaskSlices) > maxContractBytes || len(m.RequiredCapabilities) > maxContractBytes {
		return fmt.Errorf("%w: task_slices/required_capabilities exceed the contract size budget", ErrInvalidContract)
	}
	if len(m.Assertions) == 0 || len(m.Assertions) > 512 {
		return fmt.Errorf("%w: assertions must contain 1 to 512 specs", ErrInvalidContract)
	}
	seen := make(map[string]struct{}, len(m.Assertions))
	for _, spec := range m.Assertions {
		if err := validateOpaqueID("assertion_id", spec.AssertionID); err != nil {
			return err
		}
		if spec.Kind == "" || len(spec.Kind) > 128 {
			return fmt.Errorf("%w: assertion %q kind is invalid", ErrInvalidContract, spec.AssertionID)
		}
		if !validSHA256(spec.OracleRefHash) {
			return fmt.Errorf("%w: assertion %q oracle_ref_hash must be a sha256 hash", ErrInvalidContract, spec.AssertionID)
		}
		if spec.Severity == "" || len(spec.Severity) > 64 {
			return fmt.Errorf("%w: assertion %q severity is invalid", ErrInvalidContract, spec.AssertionID)
		}
		if len(spec.Tolerance) > 256 {
			return fmt.Errorf("%w: assertion %q tolerance is invalid", ErrInvalidContract, spec.AssertionID)
		}
		if _, duplicate := seen[spec.AssertionID]; duplicate {
			return fmt.Errorf("%w: assertion %q appears twice", ErrInvalidContract, spec.AssertionID)
		}
		seen[spec.AssertionID] = struct{}{}
	}
	if m.CreatedByActor == "" {
		return fmt.Errorf("%w: created_by_actor is required", ErrInvalidContract)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidContract)
	}
	return nil
}

// ApprovalDecision records the human gate decision (spec §12.9).
type ApprovalDecision string

const (
	ApprovalApproved ApprovalDecision = "approved"
	ApprovalRejected ApprovalDecision = "rejected"
)

func (d ApprovalDecision) Valid() bool {
	return d == ApprovalApproved || d == ApprovalRejected
}

// ApprovalRecord is the human-approval ledger entry. Proposer, evaluator,
// and candidate creator can never hold this record's actor (spec §12.7).
type ApprovalRecord struct {
	ContractKind      string            `json:"contract_kind"`
	SchemaVersion     int               `json:"schema_version"`
	ApprovalID        string            `json:"approval_id"`
	WorkspaceID       string            `json:"workspace_id"`
	CandidateID       string            `json:"candidate_id"`
	EvaluationRef     SkillEvolutionRef `json:"evaluation_ref"`
	ManifestHash      string            `json:"manifest_hash"`
	PolicyHash        string            `json:"policy_hash"`
	ArtifactHash      string            `json:"artifact_hash"`
	TargetScope       string            `json:"target_scope"`
	Decision          ApprovalDecision  `json:"decision"`
	ApproverActor     string            `json:"approver_actor"`
	ApproverRole      string            `json:"approver_role"`
	Reason            string            `json:"reason"`
	RiskAcknowledged  bool              `json:"risk_acknowledged"`
	AllowAutoRollback bool              `json:"allow_auto_rollback"`
	ExpiresAt         time.Time         `json:"expires_at"`
	CreatedAt         time.Time         `json:"created_at"`
}

func (a ApprovalRecord) Validate() error {
	if a.ContractKind != "approval" || a.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=approval and schema_version=1 are required", ErrInvalidContract)
	}
	if err := validateOpaqueID("approval_id", a.ApprovalID); err != nil {
		return err
	}
	if err := validateOpaqueID("workspace_id", a.WorkspaceID); err != nil {
		return err
	}
	if err := validateOpaqueID("candidate_id", a.CandidateID); err != nil {
		return err
	}
	if a.EvaluationRef.Kind != RefEvaluationRun {
		return fmt.Errorf("%w: evaluation_ref must be an evaluation_run ref", ErrInvalidContract)
	}
	if err := a.EvaluationRef.Validate(); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"manifest_hash", a.ManifestHash},
		{"policy_hash", a.PolicyHash},
		{"artifact_hash", a.ArtifactHash},
	} {
		if !validSHA256(field.value) {
			return fmt.Errorf("%w: %s must be a sha256 hash", ErrInvalidContract, field.name)
		}
	}
	switch a.TargetScope {
	case "agent", "channel", "workspace":
	default:
		return fmt.Errorf("%w: target_scope %q is invalid", ErrInvalidContract, a.TargetScope)
	}
	if !a.Decision.Valid() {
		return fmt.Errorf("%w: decision %q is invalid", ErrInvalidContract, a.Decision)
	}
	if a.Decision == ApprovalApproved {
		if err := validateOpaqueID("approver_actor", a.ApproverActor); err != nil {
			return err
		}
		if a.ApproverRole == "" {
			return fmt.Errorf("%w: approver_role is required for approvals", ErrInvalidContract)
		}
		if !a.RiskAcknowledged {
			return fmt.Errorf("%w: approvals must acknowledge risk", ErrInvalidContract)
		}
		if a.ExpiresAt.IsZero() || a.CreatedAt.IsZero() || !a.ExpiresAt.After(a.CreatedAt) {
			return fmt.Errorf("%w: approved records need a future expiry", ErrInvalidContract)
		}
	}
	if a.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidContract)
	}
	return nil
}

// MaterializationStatus tracks provider convergence of a deployment
// (spec §12.9): materialization itself is outbox-driven, this is the
// authoritative observation.
type MaterializationStatus string

const (
	MaterializationPending   MaterializationStatus = "pending"
	MaterializationConverged MaterializationStatus = "converged"
	MaterializationFailed    MaterializationStatus = "failed"
	MaterializationFenced    MaterializationStatus = "fenced"
)

func (m MaterializationStatus) Valid() bool {
	switch m {
	case MaterializationPending, MaterializationConverged, MaterializationFailed, MaterializationFenced:
		return true
	default:
		return false
	}
}

// CanTransitionTo is the materialization state machine: pending may
// resolve any way, failed may still converge or fence, and converged/
// fenced are terminal (the DB trigger guards the same floor).
func (m MaterializationStatus) CanTransitionTo(next MaterializationStatus) bool {
	switch m {
	case MaterializationPending:
		return next == MaterializationConverged || next == MaterializationFailed || next == MaterializationFenced
	case MaterializationFailed:
		return next == MaterializationConverged || next == MaterializationFenced
	default:
		return false
	}
}

// DeploymentRecord captures one activation into a target scope, including
// binding/grant before/after state for audit (spec §12.4/§12.9).
type DeploymentRecord struct {
	ContractKind          string                `json:"contract_kind"`
	SchemaVersion         int                   `json:"schema_version"`
	DeploymentID          string                `json:"deployment_id"`
	WorkspaceID           string                `json:"workspace_id"`
	CandidateID           string                `json:"candidate_id"`
	ApprovalID            string                `json:"approval_id"`
	TargetScope           string                `json:"target_scope"`
	TargetAgentID         string                `json:"target_agent_id,omitempty"`
	TargetChannelID       string                `json:"target_channel_id,omitempty"`
	BindingStateBefore    string                `json:"binding_state_before"`
	BindingStateAfter     string                `json:"binding_state_after"`
	FromArtifactHash      string                `json:"from_artifact_hash"`
	ToArtifactHash        string                `json:"to_artifact_hash"`
	MaterializationStatus MaterializationStatus `json:"materialization_status"`
	CreatedByActor        string                `json:"created_by_actor"`
	CreatedAt             time.Time             `json:"created_at"`
}

func (d DeploymentRecord) Validate() error {
	if d.ContractKind != "deployment" || d.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=deployment and schema_version=1 are required", ErrInvalidContract)
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"deployment_id", d.DeploymentID},
		{"workspace_id", d.WorkspaceID},
		{"candidate_id", d.CandidateID},
		{"approval_id", d.ApprovalID},
	} {
		if err := validateOpaqueID(field.name, field.value); err != nil {
			return err
		}
	}
	switch d.TargetScope {
	case "agent":
		if err := validateOpaqueID("target_agent_id", d.TargetAgentID); err != nil {
			return err
		}
	case "channel":
		if err := validateOpaqueID("target_channel_id", d.TargetChannelID); err != nil {
			return err
		}
	case "workspace":
		if d.TargetAgentID != "" || d.TargetChannelID != "" {
			return fmt.Errorf("%w: workspace deployments must not carry agent or channel targets", ErrInvalidContract)
		}
	default:
		return fmt.Errorf("%w: target_scope %q is invalid", ErrInvalidContract, d.TargetScope)
	}
	if !validSHA256(d.FromArtifactHash) || !validSHA256(d.ToArtifactHash) {
		return fmt.Errorf("%w: from/to artifact hashes must be sha256 hashes", ErrInvalidContract)
	}
	if !d.MaterializationStatus.Valid() {
		return fmt.Errorf("%w: materialization_status %q is invalid", ErrInvalidContract, d.MaterializationStatus)
	}
	if d.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidContract)
	}
	return nil
}

// RollbackTrigger is why an activation was rolled back (spec §12.9).
type RollbackTrigger string

const (
	RollbackSafetyFence           RollbackTrigger = "safety_fence"
	RollbackPerformanceRegression RollbackTrigger = "performance_regression"
	RollbackManual                RollbackTrigger = "manual"
	RollbackSourceRetraction      RollbackTrigger = "source_retraction"
)

func (t RollbackTrigger) Valid() bool {
	switch t {
	case RollbackSafetyFence, RollbackPerformanceRegression, RollbackManual, RollbackSourceRetraction:
		return true
	default:
		return false
	}
}

// RollForwardStatus records whether a follow-up fix is expected (spec
// §12.9: rollback records the later roll-forward state).
type RollForwardStatus string

const (
	RollForwardNone       RollForwardStatus = "none"
	RollForwardPending    RollForwardStatus = "pending"
	RollForwardOpened     RollForwardStatus = "opened"
	RollForwardSuperseded RollForwardStatus = "superseded"
)

func (s RollForwardStatus) Valid() bool {
	switch s {
	case RollForwardNone, RollForwardPending, RollForwardOpened, RollForwardSuperseded:
		return true
	default:
		return false
	}
}

// CanTransitionTo is the roll-forward progression: status only advances
// toward a fix, never back.
func (s RollForwardStatus) CanTransitionTo(next RollForwardStatus) bool {
	switch s {
	case RollForwardNone:
		return next == RollForwardPending || next == RollForwardOpened
	case RollForwardPending:
		return next == RollForwardOpened || next == RollForwardSuperseded
	case RollForwardOpened:
		return next == RollForwardSuperseded
	default:
		return false
	}
}

// RollbackRecord is the rollback ledger entry. Rollback never deletes
// binding history; it moves the authoritative active-safe pointer.
type RollbackRecord struct {
	ContractKind      string            `json:"contract_kind"`
	SchemaVersion     int               `json:"schema_version"`
	RollbackID        string            `json:"rollback_id"`
	WorkspaceID       string            `json:"workspace_id"`
	DeploymentID      string            `json:"deployment_id"`
	Trigger           RollbackTrigger   `json:"trigger"`
	FromArtifactHash  string            `json:"from_artifact_hash"`
	ToArtifactHash    string            `json:"to_artifact_hash"`
	InFlightPolicy    string            `json:"in_flight_policy"`
	Actor             string            `json:"actor"`
	PolicyVersion     string            `json:"policy_version"`
	RollForwardStatus RollForwardStatus `json:"roll_forward_status"`
	CreatedAt         time.Time         `json:"created_at"`
}

func (r RollbackRecord) Validate() error {
	if r.ContractKind != "rollback" || r.SchemaVersion != 1 {
		return fmt.Errorf("%w: contract_kind=rollback and schema_version=1 are required", ErrInvalidContract)
	}
	if err := validateOpaqueID("rollback_id", r.RollbackID); err != nil {
		return err
	}
	if err := validateOpaqueID("workspace_id", r.WorkspaceID); err != nil {
		return err
	}
	if err := validateOpaqueID("deployment_id", r.DeploymentID); err != nil {
		return err
	}
	if !r.Trigger.Valid() {
		return fmt.Errorf("%w: trigger %q is invalid", ErrInvalidContract, r.Trigger)
	}
	if !validSHA256(r.FromArtifactHash) || !validSHA256(r.ToArtifactHash) {
		return fmt.Errorf("%w: from/to artifact hashes must be sha256 hashes", ErrInvalidContract)
	}
	if r.FromArtifactHash == r.ToArtifactHash {
		return fmt.Errorf("%w: rollback must change the artifact hash", ErrInvalidContract)
	}
	switch r.InFlightPolicy {
	case "fenced", "drain", "pin":
	default:
		return fmt.Errorf("%w: in_flight_policy %q is invalid", ErrInvalidContract, r.InFlightPolicy)
	}
	if !r.RollForwardStatus.Valid() {
		return fmt.Errorf("%w: roll_forward_status %q is invalid", ErrInvalidContract, r.RollForwardStatus)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at is required", ErrInvalidContract)
	}
	return nil
}

// Validatable is implemented by every strict contract in this package.
type Validatable interface {
	Validate() error
}

// DecodeStrictContract decodes raw JSON into a contract with the same
// fail-closed rules as DecodeSkillCandidateContract: size bounds, unknown
// fields, and trailing JSON are rejected before Validate runs.
func DecodeStrictContract(raw []byte, target Validatable) error {
	if len(raw) == 0 || len(raw) > maxContractBytes {
		return fmt.Errorf("%w: payload size must be between 1 and %d bytes", ErrInvalidContract, maxContractBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode contract: %v", ErrInvalidContract, err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return err
	}
	return target.Validate()
}
