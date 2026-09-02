// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"errors"
	"fmt"
	"time"
)

// OutcomeKind is the authoritative outcome vocabulary (spec §12.2).
// Infrastructure damage, adapter/evaluator errors, unsupported features,
// and policy denials are never agent failures: an agent must not be
// punished — or taught a "bypass" Skill — for breaks it did not cause.
type OutcomeKind string

const (
	OutcomePass                  OutcomeKind = "pass"
	OutcomeAgentFailure          OutcomeKind = "agent_failure"
	OutcomePartial               OutcomeKind = "partial"
	OutcomeInfrastructureInvalid OutcomeKind = "infrastructure_invalid"
	OutcomePolicyDenied          OutcomeKind = "policy_denied"
)

func (o OutcomeKind) Valid() bool {
	switch o {
	case OutcomePass, OutcomeAgentFailure, OutcomePartial, OutcomeInfrastructureInvalid, OutcomePolicyDenied:
		return true
	default:
		return false
	}
}

// OutcomeSignal is the distilled terminal signal of one durable run. The
// service layer derives Status from the authoritative run state and
// ErrorClass from the authoritative error record; classification itself
// stays here so the "never blame the agent for infra/policy" rule cannot
// drift per caller.
type OutcomeSignal struct {
	Status     string
	ErrorClass string
	Partial    bool
}

// ClassifyOutcome maps a terminal signal to the outcome vocabulary. An
// unclassified failure fails closed: the caller must classify the error
// (infrastructure, adapter, evaluator, policy, unsupported, or agent)
// before the outcome may enter the evolution corpus.
func ClassifyOutcome(signal OutcomeSignal) (OutcomeKind, error) {
	switch signal.ErrorClass {
	case "infrastructure", "adapter", "evaluator":
		return OutcomeInfrastructureInvalid, nil
	case "policy", "unsupported":
		return OutcomePolicyDenied, nil
	case "agent":
		return OutcomeAgentFailure, nil
	case "":
		switch signal.Status {
		case "completed":
			if signal.Partial {
				return OutcomePartial, nil
			}
			return OutcomePass, nil
		default:
			return "", fmt.Errorf("outcome signal %q is unclassified: classify the error before it enters the corpus", signal.Status)
		}
	default:
		return "", fmt.Errorf("unknown error class %q", signal.ErrorClass)
	}
}

// OutcomeRecord is the authoritative outcome attached to a trajectory.
type OutcomeRecord struct {
	Outcome    OutcomeKind
	Reason     string
	SourceRef  string
	RecordedAt time.Time
}

func (o OutcomeRecord) Validate() error {
	if !o.Outcome.Valid() {
		return fmt.Errorf("invalid trajectory outcome %q", o.Outcome)
	}
	if o.SourceRef == "" || len(o.SourceRef) > 256 {
		return fmt.Errorf("outcome source ref is invalid")
	}
	if o.RecordedAt.IsZero() {
		return fmt.Errorf("outcome recorded_at is required")
	}
	return nil
}

// TrajectoryPurpose names who may consume a trajectory. Task recall is
// deliberately absent: trajectories never feed the task-recall corpus.
type TrajectoryPurpose string

const (
	TrajectoryPurposeSkillEvolution  TrajectoryPurpose = "skill_evolution"
	TrajectoryPurposeEvaluationAudit TrajectoryPurpose = "evaluation_audit"
	TrajectoryPurposeCuratorReview   TrajectoryPurpose = "curator_review"
)

func (p TrajectoryPurpose) Valid() bool {
	switch p {
	case TrajectoryPurposeSkillEvolution, TrajectoryPurposeEvaluationAudit, TrajectoryPurposeCuratorReview:
		return true
	default:
		return false
	}
}

// ErrTrajectoryNotEligible marks a projection refused because the run's
// eligibility snapshot is missing, revoked, or was never fixed at start.
var ErrTrajectoryNotEligible = errors.New("trajectory run is not evolution eligible")

// TrajectoryEligibility is the run-start pin (spec §12.2): run kind,
// eligibility, allowed purposes, and lineage are fixed when the run
// starts and can only be revoked afterwards — never granted or widened
// post hoc. The persisted ledger for these snapshots lands with
// migration 496; this type is the contract both sides share.
type TrajectoryEligibility struct {
	RunID             string
	WorkspaceID       string
	RunKind           string
	EvolutionEligible bool
	AllowedPurposes   []TrajectoryPurpose
	TaskType          string
	LineageID         string
	FixedAt           time.Time
	FixedByActor      string

	RevokedByActor string
	RevokedAt      time.Time
	RevokedReason  string
}

func (e TrajectoryEligibility) Validate() error {
	if err := validateOpaqueID("run_id", e.RunID); err != nil {
		return err
	}
	if err := validateOpaqueID("workspace_id", e.WorkspaceID); err != nil {
		return err
	}
	if e.RunKind == "" || len(e.RunKind) > 128 {
		return fmt.Errorf("run_kind is invalid")
	}
	if e.FixedAt.IsZero() || e.FixedByActor == "" {
		return fmt.Errorf("eligibility must be pinned with a time and an actor at run start")
	}
	if e.EvolutionEligible && len(e.AllowedPurposes) == 0 {
		return fmt.Errorf("an eligible run must name at least one allowed purpose")
	}
	seen := make(map[TrajectoryPurpose]struct{}, len(e.AllowedPurposes))
	for _, purpose := range e.AllowedPurposes {
		if !purpose.Valid() {
			return fmt.Errorf("trajectory purpose %q is invalid", purpose)
		}
		if _, duplicate := seen[purpose]; duplicate {
			return fmt.Errorf("trajectory purpose %q appears twice", purpose)
		}
		seen[purpose] = struct{}{}
	}
	if e.RevokedAt.IsZero() != (e.RevokedByActor == "") {
		return fmt.Errorf("revocation must carry both an actor and a time")
	}
	return nil
}

// Revoked reports whether an administrator withdrew eligibility.
func (e TrajectoryEligibility) Revoked() bool {
	return !e.RevokedAt.IsZero()
}

// RevokeEligibility withdraws eligibility. The original pin (FixedAt,
// FixedByActor) is preserved for audit; there is intentionally no API to
// grant or widen eligibility after run start — backfill-style granting is
// a separate, audited migration path (spec §12.2).
func (e TrajectoryEligibility) RevokeEligibility(actor, reason string, at time.Time) (TrajectoryEligibility, error) {
	if actor == "" || reason == "" || at.IsZero() {
		return TrajectoryEligibility{}, fmt.Errorf("revocation needs an actor, a reason, and a time")
	}
	if e.Revoked() {
		return TrajectoryEligibility{}, fmt.Errorf("eligibility for run %s is already revoked", e.RunID)
	}
	revoked := e
	revoked.EvolutionEligible = false
	revoked.RevokedByActor = actor
	revoked.RevokedAt = at
	revoked.RevokedReason = reason
	return revoked, nil
}
