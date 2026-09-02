// SPDX-License-Identifier: Apache-2.0

package skillevolution

// Versioned evidence policy behind pattern status changes (spec §12.4/§12.5):
// a single trajectory, a single model's self-report, or a summary without
// an authoritative outcome can only form a tentative pattern; upgrading to
// supported demands multiple independent lineages and can be blocked by
// negative evidence; copies or renames of the same workbook never count
// twice. The policy is data, not code branches scattered over callers.

import (
	"fmt"
	"sort"
	"time"
)

// EvidencePolarity mirrors the ledger's skill_pattern_evidence.polarity.
type EvidencePolarity string

const (
	EvidencePositive EvidencePolarity = "positive"
	EvidenceNegative EvidencePolarity = "negative"
)

func (p EvidencePolarity) Valid() bool {
	return p == EvidencePositive || p == EvidenceNegative
}

// PatternEvidenceObservation is one outcome-backed evidence fact the
// policy evaluates. LineageID is the workbook/run lineage identity:
// observations that share it are ONE vote (copies, renames, and minor
// variants of the same workbook deduplicate here), and observations with
// an empty LineageID never count as independent evidence.
type PatternEvidenceObservation struct {
	Ref            SkillEvolutionRef
	Polarity       EvidencePolarity
	LineageID      string
	TaskType       string
	EnvironmentKey string
	RecordedAt     time.Time
}

func (o PatternEvidenceObservation) Validate() error {
	if !o.Polarity.Valid() {
		return fmt.Errorf("%w: evidence polarity %q is invalid", ErrInvalidContract, o.Polarity)
	}
	if err := o.Ref.Validate(); err != nil {
		return err
	}
	if o.RecordedAt.IsZero() {
		return fmt.Errorf("%w: evidence must carry a recorded time", ErrInvalidContract)
	}
	return nil
}

// PatternConsolidationPolicy is the versioned upgrade gate. Defaults
// follow the spec: at least two independent lineages to support, negative
// evidence blocks the upgrade, and two independent negative lineages
// contradict a pattern that also has positive support.
type PatternConsolidationPolicy struct {
	PolicyVersion                      string
	MinIndependentPositiveLineages     int
	NegativeEvidenceBlocksUpgrade      bool
	MinIndependentNegativeToContradict int
}

func DefaultPatternConsolidationPolicy() PatternConsolidationPolicy {
	return PatternConsolidationPolicy{
		PolicyVersion:                      "pattern-policy-1",
		MinIndependentPositiveLineages:     2,
		NegativeEvidenceBlocksUpgrade:      true,
		MinIndependentNegativeToContradict: 2,
	}
}

func (p PatternConsolidationPolicy) Validate() error {
	if p.PolicyVersion == "" {
		return fmt.Errorf("%w: policy version is required", ErrInvalidContract)
	}
	if p.MinIndependentPositiveLineages < 2 {
		return fmt.Errorf("%w: a single lineage may never support a pattern (min 2)", ErrInvalidContract)
	}
	if p.MinIndependentNegativeToContradict < 1 {
		return fmt.Errorf("%w: negative threshold must be at least 1", ErrInvalidContract)
	}
	return nil
}

// EvidenceTally is the deduplicated vote count the policy reasons over.
type EvidenceTally struct {
	PositiveLineages []string
	NegativeLineages []string
}

func (t EvidenceTally) PositiveCount() int { return len(t.PositiveLineages) }
func (t EvidenceTally) NegativeCount() int { return len(t.NegativeLineages) }

// TallyEvidence deduplicates observations by lineage. Unattributed
// observations (empty LineageID) are dropped from the vote: evidence that
// cannot name its lineage cannot support, contradict, or refute anything.
func TallyEvidence(observations []PatternEvidenceObservation) (EvidenceTally, error) {
	tally := EvidenceTally{}
	positive := map[string]bool{}
	negative := map[string]bool{}
	for _, observation := range observations {
		if err := observation.Validate(); err != nil {
			return tally, err
		}
		if observation.LineageID == "" {
			continue
		}
		switch observation.Polarity {
		case EvidencePositive:
			positive[observation.LineageID] = true
		case EvidenceNegative:
			negative[observation.LineageID] = true
		}
	}
	tally.PositiveLineages = sortedKeys(positive)
	tally.NegativeLineages = sortedKeys(negative)
	return tally, nil
}

// EvaluateStatus proposes the next status for a pattern revision given the
// full deduplicated evidence bundle (existing evidence included). The
// caller still has to pass the proposal through the revision transition
// rules — an illegal proposal (e.g. refuted→supported on the same
// pattern_id) is refused there.
func (p PatternConsolidationPolicy) EvaluateStatus(tally EvidenceTally, kind PatternKind) (PatternStatus, string) {
	switch {
	case tally.PositiveCount() == 0 && tally.NegativeCount() >= p.MinIndependentNegativeToContradict:
		return PatternStatusRefuted,
			fmt.Sprintf("no positive lineage survives; %d independent negative lineages", tally.NegativeCount())
	case tally.PositiveCount() >= 1 && tally.NegativeCount() >= p.MinIndependentNegativeToContradict:
		return PatternStatusContradicted,
			fmt.Sprintf("%d positive vs %d independent negative lineages", tally.PositiveCount(), tally.NegativeCount())
	case tally.NegativeCount() > 0 && p.NegativeEvidenceBlocksUpgrade:
		return PatternStatusTentative,
			fmt.Sprintf("upgrade blocked by %d negative lineage(s)", tally.NegativeCount())
	case tally.PositiveCount() >= p.MinIndependentPositiveLineages:
		return PatternStatusSupported,
			fmt.Sprintf("%d independent positive lineages", tally.PositiveCount())
	default:
		_ = kind
		return PatternStatusTentative,
			fmt.Sprintf("only %d independent positive lineage(s)", tally.PositiveCount())
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
