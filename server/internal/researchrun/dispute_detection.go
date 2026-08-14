package researchrun

import (
	"fmt"
	"sort"
)

type ConflictPolarity string

const (
	ConflictPolarityAffirms ConflictPolarity = "affirms"
	ConflictPolarityDenies  ConflictPolarity = "denies"
)

type DeterministicConflictKind string

const (
	DeterministicConflictLogical              DeterministicConflictKind = "logical"
	DeterministicConflictSourceInterpretation DeterministicConflictKind = "source_interpretation"
	DeterministicConflictVersion              DeterministicConflictKind = "version"
	DeterministicConflictUnit                 DeterministicConflictKind = "unit"
)

// ConflictFact is a server-normalized comparison key, not a wire Claim shape.
// Normalization must resolve entity, metric, time window, and scope before
// deterministic detection so superficially similar claims are not conflated.
type ConflictFact struct {
	ClaimID             string
	EntityKey           string
	MetricKey           string
	TimeWindowKey       string
	ScopeHash           string
	PropositionHash     string
	Polarity            ConflictPolarity
	UnitKey             string
	VersionKey          string
	SourceSnapshotID    string
	CitationMeaningHash string
}

type DeterministicConflict struct {
	LeftClaimID  string
	RightClaimID string
	Kind         DeterministicConflictKind
}

func DetectDeterministicConflicts(facts []ConflictFact) ([]DeterministicConflict, error) {
	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		if fact.ClaimID == "" || fact.EntityKey == "" || fact.MetricKey == "" || fact.TimeWindowKey == "" || fact.ScopeHash == "" || fact.PropositionHash == "" {
			return nil, fmt.Errorf("%w: normalized conflict fact is incomplete", ErrInvalidContract)
		}
		if fact.Polarity != ConflictPolarityAffirms && fact.Polarity != ConflictPolarityDenies {
			return nil, fmt.Errorf("%w: unsupported conflict polarity %q", ErrInvalidContract, fact.Polarity)
		}
		if _, exists := seen[fact.ClaimID]; exists {
			return nil, fmt.Errorf("%w: duplicate conflict fact for Claim %q", ErrInvalidContract, fact.ClaimID)
		}
		seen[fact.ClaimID] = struct{}{}
	}

	conflicts := make([]DeterministicConflict, 0)
	for leftIndex := 0; leftIndex < len(facts); leftIndex++ {
		for rightIndex := leftIndex + 1; rightIndex < len(facts); rightIndex++ {
			left, right := facts[leftIndex], facts[rightIndex]
			if !sameComparisonFrame(left, right) {
				continue
			}
			kind, conflicting := deterministicConflictKind(left, right)
			if !conflicting {
				continue
			}
			leftID, rightID := left.ClaimID, right.ClaimID
			if rightID < leftID {
				leftID, rightID = rightID, leftID
			}
			conflicts = append(conflicts, DeterministicConflict{LeftClaimID: leftID, RightClaimID: rightID, Kind: kind})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].LeftClaimID != conflicts[j].LeftClaimID {
			return conflicts[i].LeftClaimID < conflicts[j].LeftClaimID
		}
		if conflicts[i].RightClaimID != conflicts[j].RightClaimID {
			return conflicts[i].RightClaimID < conflicts[j].RightClaimID
		}
		return conflicts[i].Kind < conflicts[j].Kind
	})
	return conflicts, nil
}

func sameComparisonFrame(left, right ConflictFact) bool {
	return left.EntityKey == right.EntityKey && left.MetricKey == right.MetricKey && left.TimeWindowKey == right.TimeWindowKey && left.ScopeHash == right.ScopeHash
}

func deterministicConflictKind(left, right ConflictFact) (DeterministicConflictKind, bool) {
	if left.UnitKey != "" && right.UnitKey != "" && left.UnitKey != right.UnitKey {
		return DeterministicConflictUnit, true
	}
	if left.VersionKey != "" && right.VersionKey != "" && left.VersionKey != right.VersionKey {
		return DeterministicConflictVersion, true
	}
	if left.SourceSnapshotID != "" && left.SourceSnapshotID == right.SourceSnapshotID && left.CitationMeaningHash != "" && right.CitationMeaningHash != "" && left.CitationMeaningHash != right.CitationMeaningHash {
		return DeterministicConflictSourceInterpretation, true
	}
	if left.PropositionHash == right.PropositionHash && left.Polarity != right.Polarity {
		return DeterministicConflictLogical, true
	}
	return "", false
}
