package memorygraph

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Spec §3 step 8: the adopted recall returns qualified citations — each
// cited node carries its level and epistemic status from the pinned graph
// version — so the daemon can render a bounded, qualified injection without
// waiting for Dive.
func TestExploreReturnsQualifiedCitations(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	n := &Node{
		NodeID:         "n-target",
		Level:          1,
		Epistemic:      EpistemicAsserted,
		Body:           "the dispatch router retries failed batch jobs with exponential backoff",
		CreatedBy:      CreatorIngester,
		CreatedVersion: 1,
		UpdatedVersion: 1,
		ObservedAt:     time.Now().UTC(),
	}
	if err := store.SaveNode(1, n); err != nil {
		t.Fatalf("SaveNode: %v", err)
	}
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{t: t}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if !res.Found {
		t.Fatalf("Found = false, want true (runs: %+v)", res.AgentRuns)
	}
	if len(res.Citations) != 1 {
		t.Fatalf("Citations = %+v, want exactly one", res.Citations)
	}
	c := res.Citations[0]
	if c.NodeID != "n-target" || c.Level != 1 || c.Epistemic != EpistemicAsserted {
		t.Fatalf("Citation = %+v, want {node_id:n-target level:1 epistemic_status:asserted}", c)
	}
}
