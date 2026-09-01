package service

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// The production import-time dedup scorer: deterministic hash-cosine over
// the current research-graph node bodies. Near-identical bodies must clear
// the 0.95 dedup threshold, distinct bodies must not, the candidate's own id
// must never match itself, and a missing graph yields no match (fail-safe,
// never an error that would block the export).
func TestResearchHashSimilarity(t *testing.T) {
	dir := t.TempDir()
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	v, err := store.CreateVersionFrom(currentVersion(t, store), memorygraph.CreatorIngester)
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	for _, n := range []*memorygraph.Node{
		{NodeID: "research_node:dup", Body: "latency doubled after the config switch", Visibility: "research"},
		{NodeID: "research_node:other", Body: "unrelated notes about disk usage", Visibility: "research"},
	} {
		if err := store.SaveNode(v, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	if err := store.SwitchCurrent(v); err != nil {
		t.Fatalf("SwitchCurrent: %v", err)
	}

	sim := NewResearchHashSimilarity()
	ctx := context.Background()

	id, score, err := sim(ctx, dir, "latency doubled after the config switch", "research_node:new")
	if err != nil {
		t.Fatalf("sim: %v", err)
	}
	if id != "research_node:dup" || score < 0.95 {
		t.Fatalf("near-duplicate: id=%q score=%v, want research_node:dup at >=0.95", id, score)
	}

	id, score, err = sim(ctx, dir, "disk usage grew because of log retention", "research_node:new")
	if err != nil {
		t.Fatalf("sim: %v", err)
	}
	if score >= 0.95 {
		t.Fatalf("distinct body matched %q at %v, want below threshold", id, score)
	}

	id, score, err = sim(ctx, dir, "latency doubled after the config switch", "research_node:dup")
	if err != nil {
		t.Fatalf("sim: %v", err)
	}
	if id == "research_node:dup" {
		t.Fatalf("candidate matched itself: %q at %v", id, score)
	}

	empty := t.TempDir()
	if err := memorygraph.NewStore(empty).Init(); err != nil {
		t.Fatalf("Init empty: %v", err)
	}
	id, _, err = sim(ctx, empty, "anything", "research_node:new")
	if err != nil {
		t.Fatalf("sim on empty graph: %v", err)
	}
	if id != "" {
		t.Fatalf("empty graph returned match %q, want none", id)
	}
}

func currentVersion(t *testing.T, store *memorygraph.Store) int {
	t.Helper()
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	return v
}
