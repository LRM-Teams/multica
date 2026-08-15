package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

func TestGraphMemoryConsolidationSkipsWhenReviewerNotGraph(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "")
	res, err := GraphMemoryJobs(nil, nil).Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Result["skipped"] != true || res.Result["reason"] != "memory_type_not_graph" {
		t.Fatalf("result = %v, want skipped/memory_type_not_graph", res.Result)
	}
}

// A graph dir below the dual trigger thresholds (design Q10) with a fresh
// last-success timestamp must not be consolidated (the A3 time-based
// fallback only fires when the last success is stale), and the loop must
// still walk every found dir.
func TestGraphMemoryConsolidationBelowThreshold(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)

	dir := root + "/ws-1/memory_graph"
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.WriteStagingSegment("seg-1", []byte("staging body")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	// Fresh consolidation state: the dual threshold is not met and the
	// time-based fallback is not eligible.
	state := &graphConsolidationState{Dirs: map[string]*graphDirState{
		dir: {LastConsolidated: time.Now().UTC(), LastSuccessAt: time.Now().UTC()},
	}}
	if err := saveGraphConsolidationState(root, state); err != nil {
		t.Fatalf("saveGraphConsolidationState: %v", err)
	}

	res, err := GraphMemoryJobs(nil, nil).Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Result["graph_dirs"] != 1 {
		t.Fatalf("graph_dirs = %v, want 1", res.Result["graph_dirs"])
	}
	if res.Result["consolidated"] != 0 {
		t.Fatalf("consolidated = %v, want 0 (below trigger thresholds, fresh last success)", res.Result["consolidated"])
	}
}

// A3 time-based fallback: at least one new staging segment and no
// successful consolidation in over 24h triggers even below the dual
// threshold. A fresh last success does not.
func TestGraphMemoryConsolidationTimeFallback(t *testing.T) {
	dir := t.TempDir()
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.WriteStagingSegment("seg-1", []byte("staging body")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}

	cfg := memorygraph.DefaultConsolidateConfig() // thresholds 50/200: unreachable with 1 segment
	now := time.Now().UTC()

	runs := 0
	run := func(context.Context) (*memorygraph.ConsolidateResult, error) {
		runs++
		return &memorygraph.ConsolidateResult{Switched: true}, nil
	}

	// Fresh last success: neither the threshold nor the fallback fires.
	ds := &graphDirState{LastConsolidated: now.Add(-time.Hour), LastSuccessAt: now.Add(-time.Hour)}
	ran, err := consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, run, now)
	if err != nil {
		t.Fatalf("consolidateOneGraphWith: %v", err)
	}
	if ran || runs != 0 {
		t.Fatalf("ran=%v runs=%d, want no trigger on fresh last success", ran, runs)
	}

	// Stale last success (>24h) with one new segment: the fallback fires.
	ds.LastSuccessAt = now.Add(-25 * time.Hour)
	ran, err = consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, run, now)
	if err != nil {
		t.Fatalf("consolidateOneGraphWith: %v", err)
	}
	if !ran || runs != 1 {
		t.Fatalf("ran=%v runs=%d, want time-fallback trigger", ran, runs)
	}
	if ds.LastSuccessAt != now || ds.LastConsolidated != now {
		t.Fatalf("state = %+v, want watermarks at now after a successful run", ds)
	}
	if ds.ConsecutiveNoSwitch != 0 || ds.Backoff {
		t.Fatalf("state = %+v, want no backoff after a switching run", ds)
	}

	// Missing LastSuccessAt (pre-A3 state file) counts as maximally stale.
	ds2 := &graphDirState{LastConsolidated: now.Add(-time.Hour)}
	ran, err = consolidateOneGraphWith(context.Background(), dir, store, cfg, ds2, nil, run, now)
	if err != nil {
		t.Fatalf("consolidateOneGraphWith: %v", err)
	}
	if !ran || runs != 2 {
		t.Fatalf("ran=%v runs=%d, want time-fallback trigger on missing last_success_at", ran, runs)
	}
}

// A3 failure backoff: three consecutive no-switch TTT consolidations engage
// the backoff; the dir is skipped until the total staging count grows past
// the watermark recorded at backoff entry.
func TestGraphMemoryConsolidationFailureBackoff(t *testing.T) {
	dir := t.TempDir()
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfg := memorygraph.DefaultConsolidateConfig()
	cfg.TriggerSegments = 1 // one new segment per round keeps the trigger firing
	ds := &graphDirState{}

	runs := 0
	noSwitch := func(context.Context) (*memorygraph.ConsolidateResult, error) {
		runs++
		return &memorygraph.ConsolidateResult{Switched: false}, nil
	}
	// The container filesystem has coarse modtime granularity, so pin each
	// segment's modtime on an explicit timeline.
	base := time.Now().UTC().Add(-time.Hour)
	roundTime := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	addSegment := func(i int) {
		t.Helper()
		id := fmt.Sprintf("seg-%d", i)
		if err := store.WriteStagingSegment(id, []byte("body")); err != nil {
			t.Fatalf("WriteStagingSegment: %v", err)
		}
		path := filepath.Join(store.Root, "staging", "segments", id+".md")
		if err := os.Chtimes(path, roundTime(i), roundTime(i)); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	// Three rounds, each with one new segment and a no-switch outcome.
	for i := 1; i <= 3; i++ {
		addSegment(i)
		ran, err := consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, noSwitch, roundTime(i).Add(30*time.Second))
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if !ran {
			t.Fatalf("round %d did not trigger", i)
		}
		if ds.ConsecutiveNoSwitch != i {
			t.Fatalf("round %d: consecutive_no_switch = %d, want %d", i, ds.ConsecutiveNoSwitch, i)
		}
	}
	if !ds.Backoff || ds.BackoffWatermark != 3 {
		t.Fatalf("state = %+v, want backoff entered with watermark 3", ds)
	}

	// Backoff active: no new staging segments -> skipped, runner not called.
	ran, err := consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, noSwitch, roundTime(3).Add(45*time.Second))
	if err != nil {
		t.Fatalf("backoff round: %v", err)
	}
	if ran || runs != 3 {
		t.Fatalf("ran=%v runs=%d, want skipped under backoff", ran, runs)
	}

	// A new staging segment beyond the watermark clears the backoff and the
	// consolidation runs again.
	addSegment(4)
	switching := func(context.Context) (*memorygraph.ConsolidateResult, error) {
		runs++
		return &memorygraph.ConsolidateResult{Switched: true}, nil
	}
	ran, err = consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, switching, roundTime(4).Add(30*time.Second))
	if err != nil {
		t.Fatalf("post-backoff round: %v", err)
	}
	if !ran || runs != 4 {
		t.Fatalf("ran=%v runs=%d, want backoff cleared and consolidation run", ran, runs)
	}
	if ds.Backoff || ds.ConsecutiveNoSwitch != 0 {
		t.Fatalf("state = %+v, want backoff cleared and counter reset after a switch", ds)
	}
}

// Consolidation errors also count toward the A3 failure backoff.
func TestGraphMemoryConsolidationErrorsCountTowardBackoff(t *testing.T) {
	dir := t.TempDir()
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg := memorygraph.DefaultConsolidateConfig()
	cfg.TriggerSegments = 1
	ds := &graphDirState{}

	boom := errors.New("backend down")
	runs := 0
	failing := func(context.Context) (*memorygraph.ConsolidateResult, error) {
		runs++
		return nil, boom
	}
	base := time.Now().UTC().Add(-time.Hour)
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("seg-%d", i)
		if err := store.WriteStagingSegment(id, []byte("body")); err != nil {
			t.Fatalf("WriteStagingSegment: %v", err)
		}
		segTime := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(store.Root, "staging", "segments", id+".md"), segTime, segTime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
		ran, err := consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, failing, segTime.Add(30*time.Second))
		if err == nil {
			t.Fatalf("round %d: want the consolidation error propagated", i)
		}
		if ran {
			t.Fatalf("round %d: ran=true on error", i)
		}
	}
	if runs != 3 || !ds.Backoff || ds.ConsecutiveNoSwitch != 3 {
		t.Fatalf("runs=%d state=%+v, want 3 error outcomes and backoff engaged", runs, ds)
	}
	// Errors do not move the last-success watermark.
	if !ds.LastSuccessAt.IsZero() {
		t.Fatalf("LastSuccessAt = %v, want zero after error-only outcomes", ds.LastSuccessAt)
	}
}

// A clean non-TTT (in-place) consolidation never switches versions by
// design (Q16) and must reset the no-switch counter instead of feeding it.
func TestGraphMemoryConsolidationNonTTTSuccessResetsBackoff(t *testing.T) {
	dir := t.TempDir()
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.WriteStagingSegment("seg-1", []byte("body")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	cfg := memorygraph.DefaultConsolidateConfig()
	cfg.TriggerSegments = 1
	cfg.TTVTrajectories = 1 // non-TTT in-place mode
	ds := &graphDirState{ConsecutiveNoSwitch: 2}

	run := func(context.Context) (*memorygraph.ConsolidateResult, error) {
		return &memorygraph.ConsolidateResult{Switched: false}, nil
	}
	ran, err := consolidateOneGraphWith(context.Background(), dir, store, cfg, ds, nil, run, time.Now().UTC())
	if err != nil {
		t.Fatalf("consolidateOneGraphWith: %v", err)
	}
	if !ran {
		t.Fatalf("non-TTT consolidation did not trigger")
	}
	if ds.ConsecutiveNoSwitch != 0 || ds.Backoff {
		t.Fatalf("state = %+v, want counter reset after a clean non-TTT run", ds)
	}
}

// The pre-A3 flat state file (last_consolidated map) migrates into per-dir
// states on load.
func TestGraphConsolidationStateMigratesLegacyFormat(t *testing.T) {
	root := t.TempDir()
	ts := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	legacy := `{"last_consolidated":{"/root/ws-1/memory_graph":"` + ts.Format(time.RFC3339Nano) + `"}}`
	if err := os.WriteFile(filepath.Join(root, graphConsolidationStateFile), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	state := loadGraphConsolidationState(root)
	ds, ok := state.Dirs["/root/ws-1/memory_graph"]
	if !ok {
		t.Fatalf("legacy state not migrated: %+v", state)
	}
	if !ds.LastConsolidated.Equal(ts) {
		t.Fatalf("LastConsolidated = %v, want %v", ds.LastConsolidated, ts)
	}
	if !ds.LastSuccessAt.IsZero() {
		t.Fatalf("LastSuccessAt = %v, want zero for migrated pre-A3 state", ds.LastSuccessAt)
	}
	// Round-trip through save keeps the per-dir format.
	if err := saveGraphConsolidationState(root, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded := loadGraphConsolidationState(root)
	if _, ok := reloaded.Dirs["/root/ws-1/memory_graph"]; !ok {
		t.Fatalf("state lost after save/load round-trip: %+v", reloaded)
	}
}

// Per-workspace memory_type resolution (design §1/A4): the profile row
// wins over the env default; the env default wins over the legacy fallback;
// a lookup failure fails open to the env default.
func TestResolveGraphMemoryMemoryType(t *testing.T) {
	ctx := context.Background()
	wsDir := "/root/3f6b1c2e-7a8d-4e5f-9a0b-1c2d3e4f5a6b/memory_graph"
	rootDir := "/root/memory_graph"

	profile := func(rt string, err error) graphMemoryTypeLookup {
		return func(context.Context, string) (string, error) { return rt, err }
	}

	cases := []struct {
		name   string
		dir    string
		env    string
		lookup graphMemoryTypeLookup
		want   string
	}{
		{"profile graph beats env legacy", wsDir, "legacy", profile("graph", nil), "graph"},
		{"profile legacy beats env graph", wsDir, "graph", profile("legacy", nil), "legacy"},
		{"missing row falls back to env", wsDir, "graph", profile("", errors.New("no rows")), "graph"},
		{"invalid profile value falls back to env", wsDir, "graph", profile("bogus", nil), "graph"},
		{"root-level dir has no workspace, env applies", rootDir, "graph", profile("legacy", nil), "graph"},
		{"no profile and empty env defaults legacy", wsDir, "", profile("", errors.New("no rows")), "legacy"},
		{"invalid env defaults legacy", rootDir, "bogus", nil, "legacy"},
		{"nil lookup resolves env only", wsDir, "graph", nil, "graph"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveGraphMemoryType(ctx, tc.dir, tc.env, tc.lookup); got != tc.want {
				t.Fatalf("resolveGraphMemoryType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGraphMemoryJobSpecValid(t *testing.T) {
	job := GraphMemoryJobs(nil, nil)
	if err := job.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if job.Name != JobNameGraphMemoryConsolidation {
		t.Fatalf("name = %q, want %q", job.Name, JobNameGraphMemoryConsolidation)
	}
}
