package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGateResultJSONUsesEmptyFindings(t *testing.T) {
	raw, err := json.Marshal(GateResult{Passed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"findings":[]`) {
		t.Fatalf("nil findings must encode as [] , got %s", raw)
	}
}

func TestNormalizeRunSnapshotEmptiesNilSlices(t *testing.T) {
	snapshot := RunSnapshot{
		Method:  &ResearchMethod{},
		Claims:  []Claim{{ID: "c1"}},
		Sources: []SourceSnapshotView{{ID: "s1"}},
	}
	normalizeRunSnapshot(&snapshot)
	if snapshot.Questions == nil || snapshot.Gate.Findings == nil {
		t.Fatal("top-level slices must be empty, not nil")
	}
	if snapshot.Method.AnalysisMethods == nil || snapshot.Claims[0].Evidence == nil || snapshot.Sources[0].EvidenceTraits == nil {
		t.Fatal("nested slices must be empty, not nil")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"questions":[]`, `"findings":[]`, `"analysis_methods":[]`, `"evidence":[]`} {
		if !strings.Contains(string(raw), needle) {
			t.Fatalf("missing %s in %s", needle, raw)
		}
	}
}
