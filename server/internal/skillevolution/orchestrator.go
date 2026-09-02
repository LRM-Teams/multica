// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Manual Orchestrator mechanics (spec §12.6, plan Slice 3.3): curator-only
// run creation with a complete pinned input set, attempt-fenced leases so
// an old owner can never advance a re-acquired run, and checkpoints whose
// resume never revalidates against a different pin. Everything here is
// pure; the ledger and lease store apply the results.

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrLeaseHeld means another owner holds a live lease on the run.
	ErrLeaseHeld = errors.New("run lease is held by another owner")
	// ErrLeaseSuperseded means the presented lease was fenced off by a
	// later re-acquisition — the classic zombie-writer case.
	ErrLeaseSuperseded = errors.New("run lease was superseded by a later attempt")
)

// OrchestratorPinnedInputs is the complete pin every run must freeze at
// admission (spec §12.6): source/evidence set, Graph identity/version and
// watermark, base Skill hash, manifest/dataset, model/provider/runtime,
// policy, scope, budget, and data residency. An incomplete pin is refused
// — a run that cannot name everything it decided on cannot be revalidated
// later, so it may not start.
type OrchestratorPinnedInputs struct {
	SourceEvidenceSetHash string            `json:"source_evidence_set_hash"`
	GraphVersion          string            `json:"graph_version"`
	GraphWatermark        string            `json:"graph_watermark"`
	BaseSkillHash         string            `json:"base_skill_hash"`
	ManifestHash          string            `json:"manifest_hash"`
	ModelID               string            `json:"model_id"`
	ProviderID            string            `json:"provider_id"`
	RuntimeID             string            `json:"runtime_id"`
	PolicyVersion         string            `json:"policy_version"`
	TargetScope           string            `json:"target_scope"`
	Budget                ExplorationBudget `json:"budget"`
	DataResidency         string            `json:"data_residency"`
}

func (p OrchestratorPinnedInputs) Validate() error {
	for field, value := range map[string]string{
		"source_evidence_set_hash": p.SourceEvidenceSetHash,
		"base_skill_hash":          p.BaseSkillHash,
		"manifest_hash":            p.ManifestHash,
	} {
		if !validSHA256(value) {
			return fmt.Errorf("%w: pinned %s must be a sha256 hash", ErrInvalidContract, field)
		}
	}
	for field, value := range map[string]string{
		"graph_version":   p.GraphVersion,
		"graph_watermark": p.GraphWatermark,
		"model_id":        p.ModelID,
		"provider_id":     p.ProviderID,
		"runtime_id":      p.RuntimeID,
		"policy_version":  p.PolicyVersion,
		"data_residency":  p.DataResidency,
	} {
		if value == "" {
			return fmt.Errorf("%w: pinned %s is required", ErrInvalidContract, field)
		}
	}
	switch p.TargetScope {
	case "agent", "channel", "workspace":
	default:
		return fmt.Errorf("%w: pinned target_scope %q is invalid", ErrInvalidContract, p.TargetScope)
	}
	if err := p.Budget.Validate(); err != nil {
		return fmt.Errorf("%w: pinned budget: %v", ErrInvalidContract, err)
	}
	return nil
}

// Hash pins the whole input set: identical decisions hash identically, and
// any later change to any facet produces a different hash — which is what
// checkpoint resume compares against.
func (p OrchestratorPinnedInputs) Hash() string {
	payload, err := json.Marshal(p)
	if err != nil {
		// Plain value types: marshal-infallible by construction.
		return "sha256:unhashable"
	}
	return HashCanonicalPayload(payload)
}

// RunPinnedInputsHash revalidates the raw pinned_inputs JSON stored on a
// run and returns its pin hash. A corrupted or partial pin fails closed:
// such a run can never be resumed, only marked stale.
func RunPinnedInputsHash(raw json.RawMessage) (string, error) {
	var pin OrchestratorPinnedInputs
	if err := json.Unmarshal(raw, &pin); err != nil {
		return "", fmt.Errorf("%w: pinned inputs are not a valid pin document: %v", ErrInvalidContract, err)
	}
	if err := pin.Validate(); err != nil {
		return "", err
	}
	return pin.Hash(), nil
}

// ManualRunCreation is the curator-only admission path of the first
// milestone (spec §12.6): no scheduler may create runs yet, so every
// creation names its curator and its reason. The reason is an audit
// floor, not a formality — "why did a human start this" is the one fact
// the ledger cannot reconstruct later.
type ManualRunCreation struct {
	RunID                   string
	WorkspaceID             string
	TargetAgentID           string
	TaskType                string
	EnvironmentMajorVersion string
	PinnedInputs            OrchestratorPinnedInputs
	CuratorActor            string
	Reason                  string
	CreatedAt               time.Time
}

// CreateManualRun validates a manual admission and produces the queued run
// record. It refuses incomplete pins, nameless curators, and unexplained
// creations; single-active enforcement stays with the ledger (the DB fence
// of migration 495 is the floor, not this function).
func CreateManualRun(creation ManualRunCreation) (EvolutionRunRecord, error) {
	for field, value := range map[string]string{
		"run_id":       creation.RunID,
		"workspace_id": creation.WorkspaceID,
		"agent_id":     creation.TargetAgentID,
	} {
		if err := validateOpaqueID(field, value); err != nil {
			return EvolutionRunRecord{}, err
		}
	}
	if creation.TaskType == "" || len(creation.TaskType) > 128 ||
		creation.EnvironmentMajorVersion == "" || len(creation.EnvironmentMajorVersion) > 128 {
		return EvolutionRunRecord{}, fmt.Errorf(
			"%w: task type and environment major version are required (max 128 chars)", ErrInvalidContract)
	}
	if err := creation.PinnedInputs.Validate(); err != nil {
		return EvolutionRunRecord{}, err
	}
	if creation.CuratorActor == "" {
		return EvolutionRunRecord{}, fmt.Errorf("%w: a manual creation names its curator", ErrInvalidContract)
	}
	if creation.Reason == "" {
		return EvolutionRunRecord{}, fmt.Errorf("%w: a manual creation carries its reason", ErrInvalidContract)
	}
	if creation.CreatedAt.IsZero() {
		return EvolutionRunRecord{}, fmt.Errorf("%w: a creation carries its time", ErrInvalidContract)
	}
	pinnedJSON, err := json.Marshal(creation.PinnedInputs)
	if err != nil {
		return EvolutionRunRecord{}, fmt.Errorf("%w: marshal pinned inputs: %v", ErrInvalidContract, err)
	}
	return EvolutionRunRecord{
		RunID:                   creation.RunID,
		WorkspaceID:             creation.WorkspaceID,
		TargetAgentID:           creation.TargetAgentID,
		TaskType:                creation.TaskType,
		EnvironmentMajorVersion: creation.EnvironmentMajorVersion,
		Status:                  EvolutionRunQueued,
		PinnedInputs:            json.RawMessage(pinnedJSON),
		CreatedByActor:          creation.CuratorActor,
		CreatedAt:               creation.CreatedAt,
		UpdatedAt:               creation.CreatedAt,
	}, nil
}

// RunLease is the durable execution fence of one run: exactly one
// (owner, attempt) may drive the run, and only while the lease is active.
// Attempt is the fencing token — re-acquisition after crash, response
// loss, or operator seizure always increments it, so a writer holding an
// older attempt is refused even if its own view of the expiry has not
// lapsed.
type RunLease struct {
	RunID      string    `json:"run_id"`
	OwnerID    string    `json:"owner_id"`
	Attempt    int64     `json:"attempt"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (l RunLease) Validate() error {
	if err := validateOpaqueID("run_id", l.RunID); err != nil {
		return err
	}
	if l.OwnerID == "" || l.Attempt < 1 {
		return fmt.Errorf("%w: a lease names its owner and a positive attempt", ErrInvalidContract)
	}
	if l.AcquiredAt.IsZero() || !l.ExpiresAt.After(l.AcquiredAt) {
		return fmt.Errorf("%w: a lease expires strictly after it is acquired", ErrInvalidContract)
	}
	return nil
}

// ActiveAt reports whether the lease still authorizes work at now.
func (l RunLease) ActiveAt(now time.Time) bool {
	return !now.After(l.ExpiresAt)
}

// LeaseMatches is the fencing rule: only the durable lease's exact
// (owner, attempt) may act, and only while the durable lease is still
// active. An old owner presenting a superseded attempt never matches —
// no matter what its local clock believes about expiry.
func LeaseMatches(durable, presented RunLease, now time.Time) bool {
	return presented.RunID == durable.RunID &&
		presented.OwnerID == durable.OwnerID &&
		presented.Attempt == durable.Attempt &&
		durable.ActiveAt(now)
}

// ClassifyLeaseFailure explains why a presented lease does not authorize
// work against the durable lease: a lower attempt was fenced off by a
// re-acquisition, anything else is a live foreign owner.
func ClassifyLeaseFailure(durable, presented RunLease) error {
	if presented.Attempt < durable.Attempt {
		return fmt.Errorf("%w: attempt %d predates the current attempt %d",
			ErrLeaseSuperseded, presented.Attempt, durable.Attempt)
	}
	return fmt.Errorf("%w: owner %q does not hold attempt %d", ErrLeaseHeld, presented.OwnerID, durable.Attempt)
}

// RunCheckpoint is the durable resume point of one phase: what the phase
// already established when it was interrupted, recorded under the lease
// attempt that wrote it. Checkpoints never carry partial candidates — a
// candidate is only ever submitted whole through the atomic proposer
// path; the checkpoint only lets the next attempt skip re-deriving what
// is already pinned.
type RunCheckpoint struct {
	RunID            string             `json:"run_id"`
	Phase            EvolutionRunStatus `json:"phase"`
	Attempt          int64              `json:"attempt"`
	PinnedInputsHash string             `json:"pinned_inputs_hash"`
	Summary          string             `json:"summary"`
	RecordedAt       time.Time          `json:"recorded_at"`
}

func (c RunCheckpoint) Validate() error {
	if err := validateOpaqueID("run_id", c.RunID); err != nil {
		return err
	}
	if !c.Phase.Valid() || c.Phase.Terminal() {
		return fmt.Errorf("%w: a checkpoint is recorded inside a non-terminal phase (got %s)", ErrInvalidContract, c.Phase)
	}
	if c.Attempt < 1 {
		return fmt.Errorf("%w: a checkpoint names the lease attempt that wrote it", ErrInvalidContract)
	}
	if !validSHA256(c.PinnedInputsHash) {
		return fmt.Errorf("%w: a checkpoint pins the inputs it was derived from", ErrInvalidContract)
	}
	if c.Summary == "" || c.RecordedAt.IsZero() {
		return fmt.Errorf("%w: a checkpoint carries its summary and time", ErrInvalidContract)
	}
	return nil
}

// ResumeFromCheckpoint selects the newest usable checkpoint for a run:
// same run, same phase the run is currently in (an older phase's
// checkpoint is already superseded by the run's own status), and — the
// revalidation floor — derived from the same pinned inputs. A checkpoint
// from a different pin is a hard error, not a skip: spec §12.6 forbids
// auto-rebasing onto changed validation.
func ResumeFromCheckpoint(run EvolutionRunRecord, checkpoints []RunCheckpoint) (RunCheckpoint, bool, error) {
	if run.Status.Terminal() {
		return RunCheckpoint{}, false, nil
	}
	pinHash, err := RunPinnedInputsHash(run.PinnedInputs)
	if err != nil {
		return RunCheckpoint{}, false, err
	}
	var newest *RunCheckpoint
	for i := range checkpoints {
		checkpoint := checkpoints[i]
		if err := checkpoint.Validate(); err != nil {
			return RunCheckpoint{}, false, err
		}
		if checkpoint.RunID != run.RunID {
			continue
		}
		if checkpoint.PinnedInputsHash != pinHash {
			return RunCheckpoint{}, false, fmt.Errorf(
				"%w: checkpoint of phase %s was derived from a different pinned input set", ErrLedgerConflict, checkpoint.Phase)
		}
		if checkpoint.Phase != run.Status {
			continue
		}
		if newest == nil || checkpoint.RecordedAt.After(newest.RecordedAt) {
			checkpointCopy := checkpoint
			newest = &checkpointCopy
		}
	}
	if newest == nil {
		return RunCheckpoint{}, false, nil
	}
	return *newest, true, nil
}

// InterruptRun validates an operator interruption (cancel/fail/stale/
// fence) against the run. Terminal runs refuse every interruption —
// terminal is stable; no_action/rejected/completed are flow outcomes of
// specific phases, not interrupts, and are refused here.
func InterruptRun(run EvolutionRunRecord, terminal EvolutionRunStatus, actor string, at time.Time) error {
	switch terminal {
	case EvolutionRunCancelled, EvolutionRunFailed, EvolutionRunStale, EvolutionRunFenced:
	default:
		return fmt.Errorf("%w: %s is not an interruption terminal", ErrInvalidContract, terminal)
	}
	if actor == "" || at.IsZero() {
		return fmt.Errorf("%w: an interruption names its actor and time", ErrInvalidContract)
	}
	if run.Status.Terminal() {
		return fmt.Errorf("%w: run %s is already terminal (%s)", ErrLedgerConflict, run.RunID, run.Status)
	}
	if !run.Status.CanTransition(terminal) {
		return fmt.Errorf("%w: run %s cannot move %s -> %s", ErrLedgerConflict, run.RunID, run.Status, terminal)
	}
	return nil
}
