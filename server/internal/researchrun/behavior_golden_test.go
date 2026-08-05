package researchrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

const researchBehaviorGoldenSchemaVersion = "research-behavior-goldens-v1"

type researchBehaviorGoldenManifest struct {
	SchemaVersion string                     `json:"schema_version"`
	Scenarios     map[string]json.RawMessage `json:"scenarios"`
}

func TestResearchBehaviorGoldenManifestIsComplete(t *testing.T) {
	manifest := loadResearchBehaviorGoldenManifest(t)
	want := []string{"cancellation", "evidence_acceptance", "report_materialization", "result_recovery", "retry_exhaustion", "review_remediation"}
	got := make([]string, 0, len(manifest.Scenarios))
	for name := range manifest.Scenarios {
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("golden scenarios=%v want=%v", got, want)
	}
}

func assertResearchBehaviorGolden(t *testing.T, scenario string, got any) {
	t.Helper()
	manifest := loadResearchBehaviorGoldenManifest(t)
	want, exists := manifest.Scenarios[scenario]
	if !exists {
		t.Fatalf("research behavior golden %q is not declared", scenario)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal research behavior %q: %v", scenario, err)
	}
	var gotValue any
	var wantValue any
	if err = json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatalf("decode actual research behavior %q: %v", scenario, err)
	}
	if err = json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode golden research behavior %q: %v", scenario, err)
	}
	if reflect.DeepEqual(gotValue, wantValue) {
		return
	}
	prettyGot, _ := json.MarshalIndent(gotValue, "", "  ")
	prettyWant, _ := json.MarshalIndent(wantValue, "", "  ")
	t.Fatalf("research behavior %q changed\nwant:\n%s\ngot:\n%s", scenario, prettyWant, prettyGot)
}

func loadResearchBehaviorGoldenManifest(t *testing.T) researchBehaviorGoldenManifest {
	t.Helper()
	raw, err := os.ReadFile("testdata/golden/research_behaviors.json")
	if err != nil {
		t.Fatalf("read research behavior goldens: %v", err)
	}
	var manifest researchBehaviorGoldenManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode research behavior goldens: %v", err)
	}
	if manifest.SchemaVersion != researchBehaviorGoldenSchemaVersion {
		t.Fatalf("research behavior golden schema=%q want=%q", manifest.SchemaVersion, researchBehaviorGoldenSchemaVersion)
	}
	return manifest
}

func researchBehaviorContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
