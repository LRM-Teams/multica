// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Pattern lifecycle mechanics (spec §12.5): drafting the always-tentative
// first revision, re-evaluating status under the versioned evidence
// policy on later revisions, and planning merges that never overwrite a
// conflict. Every function here is pure: the append-only ledger (Slice
// 2.1) applies the results as new revisions.

import (
	"encoding/json"
	"fmt"
	"time"
)

// PatternDraftInput is what a maintainer submits after comparing success
// and failure trajectories. The draft is ALWAYS tentative: spec §12.4 —
// a single trajectory, a single model self-report, or a summary without
// an authoritative outcome can only form a tentative pattern.
type PatternDraftInput struct {
	PatternID         string
	WorkspaceID       string
	EvolutionKey      string
	Kind              PatternKind
	Problem           string
	Applicability     string
	RootCauseSummary  string
	RecommendedAction string
	TaskType          string
	EnvironmentKey    string
	ToolCapabilityID  string
	GeneratorVersion  string
	PolicyVersion     string
	PositiveEvidence  []SkillEvolutionRef
	NegativeEvidence  []SkillEvolutionRef
	CreatedByActor    string
	CreatedAt         time.Time
}

// DraftTentativePattern builds revision 1 of a pattern. It refuses to
// stamp anything but tentative, no matter how much evidence the caller
// claims — upgrading is a separate, policy-evaluated revision.
func DraftTentativePattern(input PatternDraftInput) (PatternRecord, error) {
	record := PatternRecord{
		ContractKind:      "pattern",
		SchemaVersion:     1,
		PatternID:         input.PatternID,
		Revision:          1,
		WorkspaceID:       input.WorkspaceID,
		EvolutionKey:      input.EvolutionKey,
		PatternKind:       input.Kind,
		Status:            PatternStatusTentative,
		Problem:           input.Problem,
		Applicability:     input.Applicability,
		RootCauseSummary:  input.RootCauseSummary,
		RecommendedAction: input.RecommendedAction,
		PositiveEvidence:  input.PositiveEvidence,
		NegativeEvidence:  input.NegativeEvidence,
		TaskType:          input.TaskType,
		ToolCapabilityID:  input.ToolCapabilityID,
		EnvironmentKey:    input.EnvironmentKey,
		GeneratorVersion:  input.GeneratorVersion,
		PolicyVersion:     input.PolicyVersion,
		CreatedByActor:    input.CreatedByActor,
		CreatedAt:         input.CreatedAt,
		UpdatedByActor:    input.CreatedByActor,
		UpdatedAt:         input.CreatedAt,
	}
	record.ContentHash = HashPatternContent(record)
	if err := record.Validate(); err != nil {
		return PatternRecord{}, err
	}
	return record, nil
}

// ReevaluatePattern produces the next revision of an existing pattern
// under the evidence policy. The evidence bundle must be the FULL
// deduplicated set (existing revision evidence plus new observations):
// the policy reasons over lineage independence, not over deltas. Refused
// transitions (refuted/stale resurrect, illegal jumps) fail closed
// instead of silently keeping the old status.
func ReevaluatePattern(
	existing PatternRecord,
	observations []PatternEvidenceObservation,
	policy PatternConsolidationPolicy,
	actor string,
	at time.Time,
) (PatternRecord, string, error) {
	if err := policy.Validate(); err != nil {
		return PatternRecord{}, "", err
	}
	if actor == "" || at.IsZero() {
		return PatternRecord{}, "", fmt.Errorf("%w: re-evaluation needs an actor and a time", ErrInvalidContract)
	}
	if existing.Status.Terminal() {
		return PatternRecord{}, "", fmt.Errorf(
			"%w: pattern %s is %s; re-validation happens as a new pattern, not by resurrecting a terminal revision",
			ErrInvalidContract, existing.PatternID, existing.Status)
	}
	tally, err := TallyEvidence(observations)
	if err != nil {
		return PatternRecord{}, "", err
	}
	next, rationale := policy.EvaluateStatus(tally, existing.PatternKind)
	if next != existing.Status && !existing.Status.CanTransition(next) {
		return PatternRecord{}, "", fmt.Errorf("%w: illegal pattern transition %s -> %s",
			ErrInvalidContract, existing.Status, next)
	}
	nextRecord := existing
	nextRecord.Revision = existing.Revision + 1
	nextRecord.Status = next
	nextRecord.PolicyVersion = policy.PolicyVersion
	nextRecord.UpdatedByActor = actor
	nextRecord.UpdatedAt = at
	nextRecord.CreatedAt = at
	nextRecord.CreatedByActor = actor
	nextRecord.ContentHash = HashPatternContent(nextRecord)
	if err := nextRecord.Validate(); err != nil {
		return PatternRecord{}, "", err
	}
	return nextRecord, rationale, nil
}

// PatternMergeAction enumerates what a merge comparison may conclude.
type PatternMergeAction string

const (
	// MergeActionMerge unions two fingerprint-identical, semantically
	// agreeing patterns; the absorbed pattern keeps its id, evidence, and
	// audit trail in a final stale revision.
	MergeActionMerge PatternMergeAction = "merge"
	// MergeActionLink keeps both patterns but links them (same lineage
	// scope, related semantics — not identical enough to merge).
	MergeActionLink PatternMergeAction = "link"
	// MergeActionConflict records conflicts_with: same fingerprint class
	// but irreconcilable semantics. New content never overwrites; both
	// sides are demoted to tentative with narrowed applicability.
	MergeActionConflict PatternMergeAction = "conflict"
)

func (a PatternMergeAction) Valid() bool {
	switch a {
	case MergeActionMerge, MergeActionLink, MergeActionConflict:
		return true
	default:
		return false
	}
}

// PatternMergePlan is the reversible, auditable merge decision (spec
// §12.5): original ids, evidence, and audit are preserved; undo is a new
// revision that restores the absorbed pattern (the ledger is
// append-only, so reversal never destroys history).
type PatternMergePlan struct {
	Action                PatternMergeAction
	SurvivingPatternID    string
	AbsorbedPatternID     string
	SurvivingFromRevision int64
	AbsorbedFromRevision  int64
	// MergedPositive/MergedNegative are the union of both sides' evidence
	// with duplicates removed (set semantics on kind+workspace+id).
	MergedPositive []SkillEvolutionRef
	MergedNegative []SkillEvolutionRef
	// ConflictsWith lists the pattern ids this pattern must record a
	// conflicts_with relation to (conflict outcome only).
	ConflictsWith []string
	// ApplicabilityNarrowing is the refined applicability both sides must
	// adopt when they conflict (may be empty when semantics already agree).
	ApplicabilityNarrowing string
	// DemoteToTentative is set when a conflict cannot be resolved: both
	// patterns drop to tentative/unknown rather than overwrite each other.
	DemoteToTentative bool
	Reason            string
}

// PlanPatternMerge compares two live patterns from the same lineage scope.
// The precondition is deliberate: a merge is only ever planned between
// candidates recalled by the deterministic fingerprint — embedding
// similarity or a shared name alone must never merge anything
// automatically. Semantic agreement means normalized problem and root
// cause equality; anything else in the same fingerprint bucket is a
// conflict, and conflicts never overwrite.
func PlanPatternMerge(a, b PatternRecord, fingerprint string) (PatternMergePlan, error) {
	if fingerprint == "" {
		return PatternMergePlan{}, fmt.Errorf("%w: merge requires the shared deterministic fingerprint", ErrInvalidContract)
	}
	for _, pattern := range []PatternRecord{a, b} {
		if pattern.Status.Terminal() {
			return PatternMergePlan{}, fmt.Errorf("%w: pattern %s is terminal (%s) and cannot merge",
				ErrInvalidContract, pattern.PatternID, pattern.Status)
		}
		if pattern.WorkspaceID != a.WorkspaceID {
			return PatternMergePlan{}, fmt.Errorf("%w: cross-workspace merges are forbidden", ErrInvalidContract)
		}
	}
	if a.PatternID == b.PatternID {
		return PatternMergePlan{}, fmt.Errorf("%w: a pattern cannot merge with itself", ErrInvalidContract)
	}

	plan := PatternMergePlan{
		Action:                MergeActionMerge,
		SurvivingPatternID:    a.PatternID,
		AbsorbedPatternID:     b.PatternID,
		SurvivingFromRevision: a.Revision,
		AbsorbedFromRevision:  b.Revision,
		MergedPositive:        unionEvidence(a.PositiveEvidence, b.PositiveEvidence),
		MergedNegative:        unionEvidence(a.NegativeEvidence, b.NegativeEvidence),
		Reason:                "fingerprint-identical merge",
	}

	problemsAgree := normalizeFingerprintText(a.Problem) == normalizeFingerprintText(b.Problem)
	rootCausesAgree := normalizeFingerprintText(a.RootCauseSummary) == normalizeFingerprintText(b.RootCauseSummary)
	switch {
	case problemsAgree && rootCausesAgree:
		return plan, nil
	case problemsAgree && !rootCausesAgree:
		// Same observed problem, different root cause: keep both, record
		// the conflict, narrow applicability, demote both to tentative.
		plan.Action = MergeActionConflict
		plan.ConflictsWith = []string{b.PatternID}
		plan.DemoteToTentative = true
		plan.ApplicabilityNarrowing = narrowApplicability(a, b)
		plan.Reason = "same fingerprint and problem but divergent root causes: kept both, no overwrite"
		return plan, nil
	default:
		// Different problem statements in one fingerprint bucket: link,
		// do not merge — the recall key was coarse, not authoritative.
		plan.Action = MergeActionLink
		plan.MergedPositive = nil
		plan.MergedNegative = nil
		plan.Reason = "same lineage scope but different problems: linked, not merged"
		return plan, nil
	}
}

// narrowApplicability intersects the two applicability statements so each
// pattern only claims the environment both independently observed.
func narrowApplicability(a, b PatternRecord) string {
	if normalizeFingerprintText(a.Applicability) == normalizeFingerprintText(b.Applicability) {
		return a.Applicability
	}
	return a.Applicability + " ∩ " + b.Applicability
}

func unionEvidence(groups ...[]SkillEvolutionRef) []SkillEvolutionRef {
	seen := map[string]bool{}
	union := make([]SkillEvolutionRef, 0)
	for _, group := range groups {
		for _, ref := range group {
			key := string(ref.Kind) + "\x1f" + ref.WorkspaceID + "\x1f" + ref.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			union = append(union, ref)
		}
	}
	return union
}

// patternContentCanonical is the hash preimage of a pattern revision: the
// semantic fields only. Revision numbers, actors, and timestamps are
// audit metadata — content identity is what the hash pins.
type patternContentCanonical struct {
	Kind              PatternKind         `json:"kind"`
	Status            PatternStatus       `json:"status"`
	Problem           string              `json:"problem"`
	Applicability     string              `json:"applicability"`
	RootCauseSummary  string              `json:"root_cause_summary"`
	RecommendedAction string              `json:"recommended_action"`
	TaskType          string              `json:"task_type"`
	EnvironmentKey    string              `json:"environment_key"`
	ToolCapabilityID  string              `json:"tool_capability_id"`
	PositiveEvidence  []SkillEvolutionRef `json:"positive_evidence_refs"`
	NegativeEvidence  []SkillEvolutionRef `json:"negative_evidence_refs"`
}

// HashPatternContent pins the semantic content of a pattern revision.
// Two revisions with equal hashes carry equal meaning regardless of who
// appended them when.
func HashPatternContent(record PatternRecord) string {
	payload, err := json.Marshal(patternContentCanonical{
		Kind:              record.PatternKind,
		Status:            record.Status,
		Problem:           record.Problem,
		Applicability:     record.Applicability,
		RootCauseSummary:  record.RootCauseSummary,
		RecommendedAction: record.RecommendedAction,
		TaskType:          record.TaskType,
		EnvironmentKey:    record.EnvironmentKey,
		ToolCapabilityID:  record.ToolCapabilityID,
		PositiveEvidence:  record.PositiveEvidence,
		NegativeEvidence:  record.NegativeEvidence,
	})
	if err != nil {
		// patternContentCanonical is marshal-infallible (plain types).
		return "sha256:unhashable"
	}
	return HashCanonicalPayload(payload)
}
