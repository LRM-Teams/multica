package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

func TestGraphMemoryConsolidationSkipsWhenReviewerNotGraph(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "")
	res, err := GraphMemoryJobs(nil, nil, nil).Handler(context.Background(), HandlerInput{})
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

	ws, pid := uuid.NewString(), uuid.NewString()
	dir, err := memorygraph.EnsureScopedDir(root, ws, memorygraph.GraphDirKindProject, pid)
	if err != nil {
		t.Fatalf("EnsureScopedDir: %v", err)
	}
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

	res, err := GraphMemoryJobs(nil, nil, nil).Handler(context.Background(), HandlerInput{})
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

// Per-workspace activation gate (spec §10/§13): the profile row wins over
// the env default; the env default wins over the legacy fallback; a lookup
// failure falls back to the env type with readiness false, and graph mode
// additionally requires the scoped-writer readiness flag.
func TestResolveGraphMemoryGate(t *testing.T) {
	ctx := context.Background()
	wsDir := "/root/3f6b1c2e-7a8d-4e5f-9a0b-1c2d3e4f5a6b/memory_graph/projects/1f2e3d4c-5b6a-4978-8c7d-6e5f4a3b2c1d"
	rootDir := "/root/memory_graph"

	gate := func(memoryType string, ready bool, err error) graphMemoryGateLookup {
		return func(context.Context, string) (graphMemoryWorkspaceGate, error) {
			return graphMemoryWorkspaceGate{memoryType: memoryType, scopedWriterReady: ready}, err
		}
	}

	cases := []struct {
		name   string
		dir    string
		env    string
		lookup graphMemoryGateLookup
		want   string
	}{
		{"ready graph profile beats env legacy", wsDir, "legacy", gate("graph", true, nil), "graph"},
		{"not-ready graph profile skips", wsDir, "legacy", gate("graph", false, nil), "skip_not_ready"},
		{"legacy profile beats env graph", wsDir, "graph", gate("legacy", false, nil), "legacy"},
		{"missing row falls back to env with readiness false", wsDir, "graph", gate("", false, errors.New("no rows")), "skip_not_ready"},
		{"missing row with legacy env stays legacy", wsDir, "legacy", gate("", false, errors.New("no rows")), "legacy"},
		{"invalid profile value is not graph", wsDir, "graph", gate("bogus", true, nil), "legacy"},
		{"root-level dir has no workspace, env applies", rootDir, "graph", gate("graph", true, nil), "skip_not_ready"},
		{"no profile and empty env defaults legacy", wsDir, "", gate("", false, errors.New("no rows")), "legacy"},
		{"invalid env defaults legacy", rootDir, "bogus", nil, "legacy"},
		{"nil lookup resolves env only, readiness false", wsDir, "graph", nil, "skip_not_ready"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveGraphMemoryGate(ctx, tc.dir, tc.env, tc.lookup); got != tc.want {
				t.Fatalf("resolveGraphMemoryGate = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGraphMemoryConsolidationConfigs(t *testing.T) {
	cases := []struct {
		name       string
		rounds     int
		tttEnabled bool
		wantRounds int
		wantTTV    int
	}{
		{name: "zero keeps default rounds", rounds: 0, tttEnabled: true, wantRounds: 6, wantTTV: 4},
		{name: "profile rounds override default", rounds: 10, tttEnabled: true, wantRounds: 10, wantTTV: 4},
		{name: "negative keeps default rounds", rounds: -1, tttEnabled: true, wantRounds: 6, wantTTV: 4},
		{name: "ttt off forces non-TTT in-place", rounds: 10, tttEnabled: false, wantRounds: 10, wantTTV: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, exploreCfg := graphMemoryConsolidationConfigs("model", tc.rounds, tc.tttEnabled)
			if exploreCfg.MaxRounds != tc.wantRounds {
				t.Fatalf("exploreCfg.MaxRounds = %d, want %d", exploreCfg.MaxRounds, tc.wantRounds)
			}
			if cfg.ExploreMaxRounds != tc.wantRounds {
				t.Fatalf("cfg.ExploreMaxRounds = %d, want %d", cfg.ExploreMaxRounds, tc.wantRounds)
			}
			if cfg.TTVTrajectories != tc.wantTTV {
				t.Fatalf("cfg.TTVTrajectories = %d, want %d", cfg.TTVTrajectories, tc.wantTTV)
			}
			if exploreCfg.Model != "model" {
				t.Fatalf("exploreCfg.Model = %q, want model", exploreCfg.Model)
			}
		})
	}
}

func TestResolveGraphMemoryProfile(t *testing.T) {
	ctx := context.Background()
	wsID := "3f6b1c2e-7a8d-4e5f-9a0b-1c2d3e4f5a6b"
	wsDir := "/root/" + wsID + "/memory_graph/projects/1f2e3d4c-5b6a-4978-8c7d-6e5f4a3b2c1d"

	var gotWorkspaceID string
	lookup := func(_ context.Context, workspaceID string) (int, bool) {
		gotWorkspaceID = workspaceID
		return 10, true
	}
	if rounds, ttt := resolveGraphMemoryProfile(ctx, wsDir, lookup); rounds != 10 || !ttt {
		t.Fatalf("resolveGraphMemoryProfile = (%d, %v), want (10, true)", rounds, ttt)
	}
	if gotWorkspaceID != wsID {
		t.Fatalf("lookup workspace ID = %q, want %q", gotWorkspaceID, wsID)
	}
	if rounds, ttt := resolveGraphMemoryProfile(ctx, "/root/memory_graph", lookup); rounds != 0 || ttt {
		t.Fatalf("root-level profile = (%d, %v), want (0, false)", rounds, ttt)
	}
	if rounds, ttt := resolveGraphMemoryProfile(ctx, wsDir, nil); rounds != 0 || ttt {
		t.Fatalf("nil lookup profile = (%d, %v), want (0, false)", rounds, ttt)
	}
}

func TestGraphMemoryJobSpecValid(t *testing.T) {
	job := GraphMemoryJobs(nil, nil, nil)
	if err := job.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if job.Name != JobNameGraphMemoryConsolidation {
		t.Fatalf("name = %q, want %q", job.Name, JobNameGraphMemoryConsolidation)
	}
}

// Spec §13 P0/P1: graph jobs stay inert until the per-workspace
// scoped-writer readiness flag is accepted; no profile row falls back to
// the env type with readiness false.
func TestGraphMemoryJobInertWithoutScopedWriterReady(t *testing.T) {
	// Canonical workspace dir so the gate lookup is consulted.
	wsDir := "/root/3f6b1c2e-7a8d-4e5f-9a0b-1c2d3e4f5a6b/memory_graph/projects/1f2e3d4c-5b6a-4978-8c7d-6e5f4a3b2c1d"
	lookup := func(ctx context.Context, workspaceID string) (graphMemoryWorkspaceGate, error) {
		return graphMemoryWorkspaceGate{memoryType: "graph", scopedWriterReady: false}, nil
	}
	if got := resolveGraphMemoryGate(context.Background(), wsDir, "graph", lookup); got != "skip_not_ready" {
		t.Fatalf("gate = %q, want skip_not_ready (spec §13 P0/P1: jobs inert until writer gates pass)", got)
	}
	lookup = func(ctx context.Context, workspaceID string) (graphMemoryWorkspaceGate, error) {
		return graphMemoryWorkspaceGate{memoryType: "graph", scopedWriterReady: true}, nil
	}
	if got := resolveGraphMemoryGate(context.Background(), wsDir, "graph", lookup); got != "graph" {
		t.Fatalf("gate = %q, want graph", got)
	}
	lookup = func(ctx context.Context, workspaceID string) (graphMemoryWorkspaceGate, error) {
		return graphMemoryWorkspaceGate{}, errors.New("no profile row")
	}
	// No profile row: the env default decides type, and readiness is false.
	if got := resolveGraphMemoryGate(context.Background(), "any-dir", "graph", lookup); got != "skip_not_ready" {
		t.Fatalf("env-graph without readiness = %q, want skip_not_ready", got)
	}
	if got := resolveGraphMemoryGate(context.Background(), "any-dir", "legacy", lookup); got != "legacy" {
		t.Fatalf("legacy env = %q, want legacy", got)
	}
}

// Spec §10: the graph memory job registers unconditionally; the only
// activation control is the per-workspace scoped-writer readiness gate.
func TestGraphMemoryJobsSpecRequiresNoEnvGate(t *testing.T) {
	t.Setenv("MULTICA_GRAPH_CONSOLIDATION_ENABLED", "")
	spec := GraphMemoryJobs(nil, nil, nil)
	if spec.Name != JobNameGraphMemoryConsolidation || spec.Handler == nil {
		t.Fatalf("graph memory job must register without any env switch: %+v", spec)
	}
}
