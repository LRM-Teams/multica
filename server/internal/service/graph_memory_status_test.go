package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

func TestGraphMemoryStatusEmptyStartAndPopulated(t *testing.T) {
	root := t.TempDir()
	svc := NewGraphMemoryStatusService(nil, root) // nil queries: memory_type defaults to legacy

	ws := uuid.NewString()
	// No dirs at all: empty start, no graphs, no error (spec §11).
	st, err := svc.Status(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if !st.EmptyStart || len(st.Graphs) != 0 {
		t.Fatalf("empty start = %+v", st)
	}

	// One project graph with a version, a staging segment, and query log.
	pid := uuid.NewString()
	dir, err := memorygraph.EnsureScopedDir(root, ws, memorygraph.GraphDirKindProject, pid)
	if err != nil {
		t.Fatal(err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	v, err := store.CurrentVersion() // Init seeds v1 and points current at it
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStagingSegment("seg-1", []byte("body")); err != nil {
		t.Fatal(err)
	}
	window := time.Now().UTC().Format("2006-01-02T15") // match the store's window-id scheme
	if err := store.AppendQueryLog(window, &memorygraph.QueryLogEntry{
		TraceID: "t1", Query: "q", Timestamp: time.Now().UTC(), Found: true, Rounds: 2,
	}); err != nil {
		t.Fatal(err)
	}

	st, err = svc.Status(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if st.EmptyStart || len(st.Graphs) != 1 {
		t.Fatalf("populated status = %+v", st)
	}
	g := st.Graphs[0]
	if g.Kind != "project" || g.OwnerID != pid || g.CurrentVersion != v || g.StagingSegments != 1 {
		t.Fatalf("graph status = %+v", g)
	}
	if g.RecallQueries24h != 1 || g.RecallHitRate24h != 1.0 {
		t.Fatalf("recall stats = %+v", g)
	}
}
