package researchrun

import "testing"

func TestTaskArtifactContentCanonicalizesCriteriaAndBindsDependencies(t *testing.T) {
	first, err := ArtifactContentHash(ArtifactKindTask, taskArtifactContent(
		"question-1", "task-parent", "task-1", "discover", "Find evidence", "scout", "sources",
		[]byte(`{"b":2,"a":1}`), 0.9, 2, 3, 4, 60, []string{"dep-1", "dep-2"},
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ArtifactContentHash(ArtifactKindTask, taskArtifactContent(
		"question-1", "task-parent", "task-1", "discover", "Find evidence", "scout", "sources",
		[]byte(`{"a":1,"b":2}`), 0.9, 2, 3, 4, 60, []string{"dep-1", "dep-2"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical task hashes differ: %q != %q", first, second)
	}
	changed, err := ArtifactContentHash(ArtifactKindTask, taskArtifactContent(
		"question-1", "task-parent", "task-1", "discover", "Find evidence", "scout", "sources",
		[]byte(`{"a":1,"b":2}`), 0.9, 2, 3, 4, 60, []string{"dep-1", "dep-3"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if first == changed {
		t.Fatal("different dependency sets must not share a task version hash")
	}
}
