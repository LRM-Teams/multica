package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

func TestDetectDeterministicConflictsUsesExactComparisonFrame(t *testing.T) {
	base := ConflictFact{ClaimID: "c2", EntityKey: "company", MetricKey: "revenue", TimeWindowKey: "2025", ScopeHash: "global", PropositionHash: "p", Polarity: ConflictPolarityAffirms, UnitKey: "USD"}
	facts := []ConflictFact{
		base,
		{ClaimID: "c1", EntityKey: "company", MetricKey: "revenue", TimeWindowKey: "2025", ScopeHash: "global", PropositionHash: "p", Polarity: ConflictPolarityDenies, UnitKey: "USD"},
		{ClaimID: "different-scope", EntityKey: "company", MetricKey: "revenue", TimeWindowKey: "2025", ScopeHash: "region", PropositionHash: "p", Polarity: ConflictPolarityDenies, UnitKey: "USD"},
	}
	got, err := DetectDeterministicConflicts(facts)
	if err != nil {
		t.Fatal(err)
	}
	want := []DeterministicConflict{{LeftClaimID: "c1", RightClaimID: "c2", Kind: DeterministicConflictLogical}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts=%+v want %+v", got, want)
	}
}

func TestDetectDeterministicConflictsClassifiesStructuralMismatch(t *testing.T) {
	frame := ConflictFact{EntityKey: "entity", MetricKey: "metric", TimeWindowKey: "window", ScopeHash: "scope", PropositionHash: "p", Polarity: ConflictPolarityAffirms}
	tests := []struct {
		name  string
		left  ConflictFact
		right ConflictFact
		kind  DeterministicConflictKind
	}{
		{name: "unit", left: ConflictFact{UnitKey: "USD"}, right: ConflictFact{UnitKey: "EUR"}, kind: DeterministicConflictUnit},
		{name: "version", left: ConflictFact{VersionKey: "v1"}, right: ConflictFact{VersionKey: "v2"}, kind: DeterministicConflictVersion},
		{name: "source interpretation", left: ConflictFact{SourceSnapshotID: "s1", CitationMeaningHash: "m1"}, right: ConflictFact{SourceSnapshotID: "s1", CitationMeaningHash: "m2"}, kind: DeterministicConflictSourceInterpretation},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, right := frame, frame
			left.ClaimID, right.ClaimID = "a", "b"
			left.UnitKey, right.UnitKey = tt.left.UnitKey, tt.right.UnitKey
			left.VersionKey, right.VersionKey = tt.left.VersionKey, tt.right.VersionKey
			left.SourceSnapshotID, right.SourceSnapshotID = tt.left.SourceSnapshotID, tt.right.SourceSnapshotID
			left.CitationMeaningHash, right.CitationMeaningHash = tt.left.CitationMeaningHash, tt.right.CitationMeaningHash
			got, err := DetectDeterministicConflicts([]ConflictFact{left, right})
			if err != nil || len(got) != 1 || got[0].Kind != tt.kind {
				t.Fatalf("case %d conflicts=%+v err=%v", index, got, err)
			}
		})
	}
}

func TestDetectDeterministicConflictsRejectsUnnormalizedFacts(t *testing.T) {
	_, err := DetectDeterministicConflicts([]ConflictFact{{ClaimID: "c1"}})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v want ErrInvalidContract", err)
	}
}

func TestValidateDeclaredDeterministicConflictRejectsAgentMisclassification(t *testing.T) {
	base := ConflictFact{EntityKey: "company", MetricKey: "revenue", TimeWindowKey: "2025", ScopeHash: "scope", PropositionHash: "p", Polarity: ConflictPolarityAffirms}
	left, right := base, base
	left.ClaimID, left.UnitKey = "a", "USD"
	right.ClaimID, right.UnitKey = "b", "EUR"
	if err := ValidateDeclaredDeterministicConflict(DisputeKindUnit, []ConflictFact{left, right}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeclaredDeterministicConflict(DisputeKindLogical, []ConflictFact{left, right}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("misclassified err=%v", err)
	}
}
