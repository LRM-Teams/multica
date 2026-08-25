package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

func TestGraphMemoryAuditSummary(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	ws, pid := uuid.NewString(), uuid.NewString()
	dir, err := memorygraph.EnsureScopedDir(root, ws, memorygraph.GraphDirKindProject, pid)
	if err != nil {
		t.Fatal(err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	window := time.Now().UTC().Format("2006-01-02T15") // store's window-id scheme
	entries := []*memorygraph.QueryLogEntry{
		{TraceID: "t1", Query: "a", Timestamp: time.Now().UTC(), Found: true, Rounds: 2},
		{TraceID: "t2", Query: "b", Timestamp: time.Now().UTC(), Found: false, Rounds: 4},
		{TraceID: "old", Query: "c", Timestamp: time.Now().UTC().Add(-48 * time.Hour), Found: true, Rounds: 1},
	}
	for _, e := range entries {
		if err := store.AppendQueryLog(window, e); err != nil {
			t.Fatal(err)
		}
	}
	// Judge write-back marks t1 as judged (the store's update mutation).
	if _, err := store.UpdateQueryLogEntry(window, "t1", func(e *memorygraph.QueryLogEntry) {
		e.JudgeDone = true
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewGraphMemoryAuditService("")
	sum, err := svc.Summary(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Queries24h != 2 || sum.RecallHits24h != 1 || sum.RecallHitRate24h != 0.5 {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.AvgExploreRounds24h != 3.0 || sum.JudgedQueries24h != 1 {
		t.Fatalf("summary = %+v", sum)
	}
}
