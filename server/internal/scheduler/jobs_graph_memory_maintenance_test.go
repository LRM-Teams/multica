package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// Research maintenance trigger (unification spec §4.5): reuse the dual
// threshold mechanism on the research graph's own query log (staging is
// always zero there) plus a 1h minimum interval; idle rounds never invoke
// the LLM; the export switch gates the whole path.

// stubMaintenanceRunner replaces the maintenance runner for one test and
// returns the call counter.
func stubMaintenanceRunner(t *testing.T) *int32 {
	t.Helper()
	var calls int32
	orig := runResearchMaintenanceRound
	runResearchMaintenanceRound = func(context.Context, *pgxpool.Pool, string, string, *memorygraph.Store) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	t.Cleanup(func() { runResearchMaintenanceRound = orig })
	return &calls
}

// researchMaintenanceStore seeds a workspace whose profile is graph mode
// with the scoped-writer readiness flag accepted (the maintenance path
// reuses the same gate as project/channel consolidation), creates its
// research graph dir with n query-log entries, and returns the dir.
func researchMaintenanceStore(t *testing.T, pool *pgxpool.Pool, root string, entries int) string {
	t.Helper()
	ws := seedExportWorkspace(t, pool, "graph")
	if _, err := pool.Exec(context.Background(),
		`UPDATE graph_memory_profile SET scoped_writer_ready = true WHERE workspace_id = $1`, ws); err != nil {
		t.Fatalf("accept scoped writer: %v", err)
	}
	dir, err := memorygraph.EnsureScopedDir(root, util.UUIDToString(ws), memorygraph.GraphDirKindResearch, util.UUIDToString(ws))
	if err != nil {
		t.Fatalf("EnsureScopedDir: %v", err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < entries; i++ {
		if err := store.AppendQueryLog("daemon", &memorygraph.QueryLogEntry{
			TraceID: fmt.Sprintf("t%d", i), Query: "regression", Timestamp: base.Add(time.Duration(i) * time.Second),
			Version: 1, NodeIDs: []string{"research_node:x"}, Found: true,
		}); err != nil {
			t.Fatalf("AppendQueryLog %d: %v", i, err)
		}
	}
	return dir
}

func TestShouldRunResearchMaintenance(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name           string
		queries        int
		lastMaintained time.Time
		want           bool
	}{
		{"below threshold", 199, time.Time{}, false},
		{"at threshold", 200, time.Time{}, true},
		{"threshold but interval not elapsed", 500, now.Add(-30 * time.Minute), false},
		{"threshold and interval elapsed", 500, now.Add(-2 * time.Hour), true},
		{"no signal", 0, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := &graphDirState{LastMaintenance: tc.lastMaintained}
			if got := shouldRunResearchMaintenance(ds, tc.queries, now); got != tc.want {
				t.Fatalf("shouldRunResearchMaintenance(%d queries, last %v) = %v, want %v",
					tc.queries, tc.lastMaintained, got, tc.want)
			}
		})
	}
}

// Trigger fires at the query threshold, the run advances the persisted
// watermark, and the 1h minimum interval holds the very next tick back.
func TestGraphMemoryResearchMaintenanceTriggerFires(t *testing.T) {
	pool := integrationPool(t)
	root := t.TempDir()
	t.Setenv("MULTICA_MEMORY_TYPE", "")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "1")
	dir := researchMaintenanceStore(t, pool, root, 200)
	calls := stubMaintenanceRunner(t)

	res, err := GraphMemoryJobs(pool, nil).Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Result["research_maintained"] != 1 {
		t.Fatalf("research_maintained = %v, want 1", res.Result["research_maintained"])
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("maintenance ran %d times, want 1", *calls)
	}
	state := loadGraphConsolidationState(root)
	if ds := state.dir(dir); ds.LastMaintenance.IsZero() {
		t.Fatalf("maintenance watermark not persisted for %s", dir)
	}

	// Second tick within the minimum interval: no second run.
	if _, err := GraphMemoryJobs(pool, nil).Handler(context.Background(), HandlerInput{}); err != nil {
		t.Fatalf("handler 2: %v", err)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("interval guard failed: %d runs, want 1", *calls)
	}
}

// Idle research graphs (no query-log signal) never invoke the maintenance
// round — cold start stays free.
func TestGraphMemoryResearchMaintenanceIdle(t *testing.T) {
	pool := integrationPool(t)
	root := t.TempDir()
	t.Setenv("MULTICA_MEMORY_TYPE", "")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "1")
	researchMaintenanceStore(t, pool, root, 0)
	calls := stubMaintenanceRunner(t)

	res, err := GraphMemoryJobs(pool, nil).Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("idle graph ran maintenance %d times", *calls)
	}
	if res.Result["research_maintained"] != 0 {
		t.Fatalf("research_maintained = %v, want 0", res.Result["research_maintained"])
	}
}

// The export switch gates maintenance too: with
// MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED unset the round never runs, even at
// threshold.
func TestGraphMemoryResearchMaintenanceSwitchOff(t *testing.T) {
	pool := integrationPool(t)
	root := t.TempDir()
	t.Setenv("MULTICA_MEMORY_TYPE", "")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "")
	researchMaintenanceStore(t, pool, root, 300)
	calls := stubMaintenanceRunner(t)

	res, err := GraphMemoryJobs(pool, nil).Handler(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("switch-off graph ran maintenance %d times", *calls)
	}
	if res.Result["research_maintained"] != 0 {
		t.Fatalf("research_maintained = %v, want 0", res.Result["research_maintained"])
	}
}
