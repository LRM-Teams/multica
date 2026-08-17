package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// Per-task reviewer-type precedence (design §1/A4): the server-sent
// per-workspace profile value overrides the daemon's MULTICA_MEMORY_TYPE
// env default; empty or unrecognized task values fall back to the env
// default, then legacy.
func TestEffectiveMemoryTypePrecedence(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		taskScoped string
		want       string
	}{
		{"task graph overrides env legacy", MemoryTypeLegacy, MemoryTypeGraph, MemoryTypeGraph},
		{"task legacy overrides env graph", MemoryTypeGraph, MemoryTypeLegacy, MemoryTypeLegacy},
		{"empty task falls back to env graph", MemoryTypeGraph, "", MemoryTypeGraph},
		{"empty task falls back to env legacy", MemoryTypeLegacy, "", MemoryTypeLegacy},
		{"unrecognized task value falls back to env", MemoryTypeGraph, "bogus", MemoryTypeGraph},
		{"task value is case/space tolerant", MemoryTypeLegacy, " Graph ", MemoryTypeGraph},
		{"unrecognized env and no task value defaults legacy", "bogus", "", MemoryTypeLegacy},
		{"unrecognized both defaults legacy", "bogus", "bogus", MemoryTypeLegacy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveMemoryType(tc.configured, tc.taskScoped); got != tc.want {
				t.Fatalf("effectiveMemoryType(%q, %q) = %q, want %q", tc.configured, tc.taskScoped, got, tc.want)
			}
		})
	}
}

// A task-scoped legacy override disables the graph recall path even when the
// daemon env selects the graph reviewer — and the override is task-scoped,
// not process-global: the lazy provider must stay uninitialized.
func TestGraphExecutionMemoriesTaskOverrideBeatsEnv(t *testing.T) {
	d := &Daemon{
		cfg:    Config{MemoryType: MemoryTypeGraph},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	out := d.graphExecutionMemories(context.Background(), Task{
		MemoryType:  MemoryTypeLegacy,
		ChatMessage: "hello",
	}, d.logger)
	if out != nil {
		t.Fatalf("graphExecutionMemories = %v, want nil under task-scoped legacy override", out)
	}
	if d.graphMemoryProv != nil {
		t.Fatalf("graph memory provider initialized despite the legacy override")
	}
}

// Without a task override the env default applies; with the graph reviewer
// selected but no pi CLI configured, the provider init fails once and the
// daemon falls back to legacy (existing behavior, now exercised through the
// task-scoped resolution path).
func TestGraphExecutionMemoriesEnvDefaultApplies(t *testing.T) {
	d := &Daemon{
		cfg:    Config{MemoryType: MemoryTypeLegacy},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if out := d.graphExecutionMemories(context.Background(), Task{ChatMessage: "hello"}, d.logger); out != nil {
		t.Fatalf("graphExecutionMemories = %v, want nil under env legacy", out)
	}
	if d.graphMemoryProv != nil {
		t.Fatalf("graph memory provider initialized under env legacy")
	}
}

// Staging visibility (design §5.1 step 3, review P0-6): a segment staged
// after the initial retriever build becomes searchable on the next recall
// once the staging re-stat throttle expires — without any version switch.
func TestEnsureRetrieverRebuildsOnStagingChange(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memory_graph")
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	retr := memorygraph.NewHybridRetriever(store, nil, memorygraph.DefaultRetrievalConfig())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := &graphMemoryProvider{
		store:   store,
		retr:    retr,
		logger:  logger,
		metrics: noopGraphMemoryMetrics{},
	}
	ctx := context.Background()

	// Initial build: no docs, no staging.
	if err := p.ensureRetriever(ctx); err != nil {
		t.Fatalf("initial ensureRetriever: %v", err)
	}
	if hits, err := retr.Search(ctx, "zebra stripe deploy runbook"); err != nil || len(hits) != 0 {
		t.Fatalf("initial search = %v hits, err %v; want 0 hits", len(hits), err)
	}

	// Stage a new segment after the initial build.
	if err := store.WriteStagingSegment("seg-1", []byte("zebra stripe deploy runbook notes")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}

	// Within the re-stat throttle window the provider does not re-stat: the
	// freshly staged segment is not indexed yet.
	if err := p.ensureRetriever(ctx); err != nil {
		t.Fatalf("throttled ensureRetriever: %v", err)
	}
	if hits, err := retr.Search(ctx, "zebra stripe deploy runbook"); err != nil || len(hits) != 0 {
		t.Fatalf("throttled search = %v hits, err %v; want 0 (throttle window)", len(hits), err)
	}

	// After the throttle window the staging change is detected and the
	// retriever rebuilds: the seg: doc is searchable without a version
	// switch.
	p.lastStagingCheck = time.Now().Add(-time.Hour)
	if err := p.ensureRetriever(ctx); err != nil {
		t.Fatalf("ensureRetriever after staging change: %v", err)
	}
	hits, err := retr.Search(ctx, "zebra stripe deploy runbook")
	if err != nil {
		t.Fatalf("search after rebuild: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "seg:seg-1" {
		t.Fatalf("hits = %+v, want [seg:seg-1]", hits)
	}
	if v, _ := store.CurrentVersion(); v != 1 {
		t.Fatalf("current version = %d, want 1 (no version switch involved)", v)
	}
}
