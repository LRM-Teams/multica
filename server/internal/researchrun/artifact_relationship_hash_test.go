package researchrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactRelationshipHashBindsIdentitySemanticsAndOrder(t *testing.T) {
	base := []artifactRelationshipHashRecord{
		{
			ownerVersionID: "owner", direction: "input", referenceID: "reference-a",
			otherVersionID: "input-a", relation: "supports", manifestID: "manifest-a",
			explicitlyUsed: true, purpose: "task_execution", ordinal: 0,
		},
		{
			ownerVersionID: "owner", direction: "input", referenceID: "reference-b",
			otherVersionID: "input-b", relation: "contradicts", manifestID: "manifest-a",
			explicitlyUsed: true, purpose: "task_execution", ordinal: 1,
		},
	}
	want := hashArtifactRelationshipRecords(base)
	if want == "" || want == hashArtifactRelationshipRecords(nil) {
		t.Fatalf("non-empty relationships hash=%q", want)
	}

	mutations := map[string]func([]artifactRelationshipHashRecord){
		"reference id":    func(in []artifactRelationshipHashRecord) { in[0].referenceID = "reference-c" },
		"endpoint":        func(in []artifactRelationshipHashRecord) { in[0].otherVersionID = "input-c" },
		"direction":       func(in []artifactRelationshipHashRecord) { in[0].direction = "output" },
		"relation":        func(in []artifactRelationshipHashRecord) { in[0].relation = "invalidates" },
		"manifest":        func(in []artifactRelationshipHashRecord) { in[0].manifestID = "manifest-b" },
		"explicit use":    func(in []artifactRelationshipHashRecord) { in[0].explicitlyUsed = false },
		"purpose":         func(in []artifactRelationshipHashRecord) { in[0].purpose = "evaluation" },
		"ordinal":         func(in []artifactRelationshipHashRecord) { in[0].ordinal = 2 },
		"same-count swap": func(in []artifactRelationshipHashRecord) { in[0], in[1] = in[1], in[0] },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := append([]artifactRelationshipHashRecord(nil), base...)
			mutate(changed)
			if got := hashArtifactRelationshipRecords(changed); got == want {
				t.Fatalf("mutation did not change relationship hash: %s", got)
			}
		})
	}
}

func TestMigration363AddsNullableFrozenRelationshipHash(t *testing.T) {
	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "363_research_manifest_relationship_hash.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ADD COLUMN selection_relationship_hash TEXT",
		"research_artifact_context_entry_selection_relationship_hash_check",
		"selection_relationship_hash IS NULL",
		"^sha256:[0-9a-f]{64}$",
		"NULL only for manifests created before migration 363",
	} {
		if !strings.Contains(string(up), required) {
			t.Fatalf("migration 363 missing %q", required)
		}
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "363_research_manifest_relationship_hash.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS selection_relationship_hash") {
		t.Fatal("migration 363 down does not remove relationship hash")
	}
}
