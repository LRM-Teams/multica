package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// fakeResearchExporter records export calls keyed by workspace id and can be
// scripted to fail selected workspaces (partial-failure retry test).
type fakeResearchExporter struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error
}

func (f *fakeResearchExporter) run(_ context.Context, id pgtype.UUID) (*service.ResearchGraphExportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := util.UUIDToString(id)
	f.calls = append(f.calls, key)
	if err, ok := f.fail[key]; ok {
		return nil, err
	}
	return &service.ResearchGraphExportResult{}, nil
}

func (f *fakeResearchExporter) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func TestResearchGraphExportSwitchParsing(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"off", false},
		{"no", false},
		{"junk", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"on", true},
		{"yes", true},
	} {
		t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", tc.value)
		if got := researchGraphExportEnabled(); got != tc.want {
			t.Fatalf("switch %q = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// The switch defaults to OFF: with the env unset the handler must not touch
// the database or run a single export, and with the switch on but no pool
// the job stays inert instead of panicking.
func TestResearchGraphExportJobDisabledByDefault(t *testing.T) {
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "")
	fake := &fakeResearchExporter{}
	res, err := makeResearchGraphExportHandler(nil, fake.run)(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Result["skipped"] != true || res.Result["reason"] != "export_disabled" {
		t.Fatalf("result = %v, want skipped/export_disabled", res.Result)
	}
	if calls := fake.called(); len(calls) != 0 {
		t.Fatalf("disabled job exported workspaces: %v", calls)
	}

	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "1")
	res, err = makeResearchGraphExportHandler(nil, fake.run)(context.Background(), HandlerInput{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Result["skipped"] != true || res.Result["reason"] != "pool_unavailable" {
		t.Fatalf("result = %v, want skipped/pool_unavailable", res.Result)
	}
	if calls := fake.called(); len(calls) != 0 {
		t.Fatalf("pool-less job exported workspaces: %v", calls)
	}
}

// seedExportWorkspace creates a workspace with a graph_memory_profile row of
// the given memory_type. It returns the workspace id.
func seedExportWorkspace(t *testing.T, pool *pgxpool.Pool, memoryType string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8] + time.Now().Format("150405.000000000")
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"export job "+suffix, "export-job-"+suffix+"@multica.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1::uuid`, userID) })
	var ws pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"export job ws "+suffix, "export-job-"+suffix).Scan(&ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws) })
	if memoryType != "" {
		if _, err := pool.Exec(ctx,
			`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, $2)`, ws, memoryType); err != nil {
			t.Fatalf("seed profile %s: %v", memoryType, err)
		}
	}
	return ws
}

// Legacy gating: only graph-mode workspaces run. A profile row wins over the
// env default, and an unprofiled workspace inherits the env default (unset →
// legacy → skipped; graph → exported). Assertions are presence-based: the
// shared dev database already carries other graph-mode workspaces.
func TestResearchGraphExportJobGatesLegacyWorkspaces(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "1")
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())

	graphWS := seedExportWorkspace(t, pool, "graph")
	legacyWS := seedExportWorkspace(t, pool, "legacy")
	bareWS := seedExportWorkspace(t, pool, "")

	t.Run("env legacy excludes unprofiled", func(t *testing.T) {
		t.Setenv("MULTICA_MEMORY_TYPE", "")
		fake := &fakeResearchExporter{}
		if _, err := makeResearchGraphExportHandler(pool, fake.run)(ctx, HandlerInput{}); err != nil {
			t.Fatalf("handler: %v", err)
		}
		calls := fake.called()
		if !containsString(calls, util.UUIDToString(graphWS)) {
			t.Fatalf("graph workspace not exported: %v", calls)
		}
		for _, excluded := range []pgtype.UUID{legacyWS, bareWS} {
			if containsString(calls, util.UUIDToString(excluded)) {
				t.Fatalf("workspace %v exported despite legacy gating", excluded)
			}
		}
	})
	t.Run("env graph inherits to unprofiled", func(t *testing.T) {
		t.Setenv("MULTICA_MEMORY_TYPE", "graph")
		fake := &fakeResearchExporter{}
		if _, err := makeResearchGraphExportHandler(pool, fake.run)(ctx, HandlerInput{}); err != nil {
			t.Fatalf("handler: %v", err)
		}
		calls := fake.called()
		for _, included := range []pgtype.UUID{graphWS, bareWS} {
			if !containsString(calls, util.UUIDToString(included)) {
				t.Fatalf("workspace %v not exported", included)
			}
		}
		if containsString(calls, util.UUIDToString(legacyWS)) {
			t.Fatalf("legacy-profile workspace exported despite profile gating: %v", legacyWS)
		}
	})
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Cursor persistence: run 1 exports the seeded node, run 2 (after a second
// node lands) exports only the new one — the watermark row survives between
// runs. The runner is the production exporter wiring (similarity included)
// driven directly so other workspaces' pending data cannot mask the result.
func TestResearchGraphExportJobCursorAdvancesAcrossRuns(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "1")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	t.Setenv("MULTICA_MEMORY_TYPE", "")

	ws, session, agent := seedResearchExportGraph(t, pool)
	if _, err := pool.Exec(ctx,
		`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'graph')`, ws); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	first := insertResearchExportNode(t, pool, ws, session, agent, "finding", "first finding")
	run := researchGraphExportRunnerForPool(pool)

	res1, err := run(ctx, ws)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if res1.NodesWritten != 1 {
		t.Fatalf("run 1 nodes = %d, want 1", res1.NodesWritten)
	}
	store := memorygraph.NewStore(filepath.Join(root, util.UUIDToString(ws), "memory_graph", "research", util.UUIDToString(ws)))
	g, err := memorygraph.LoadGraph(store, storeCurrentVersion(t, store))
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	if n := countResearchNodes(g); n != 1 {
		t.Fatalf("after run 1 graph has %d research nodes, want 1", n)
	}

	second := insertResearchExportNode(t, pool, ws, session, agent, "finding", "second finding")
	res2, err := run(ctx, ws)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res2.NodesWritten != 1 {
		t.Fatalf("run 2 nodes = %d, want 1 (cursor advanced past the first export)", res2.NodesWritten)
	}
	g, err = memorygraph.LoadGraph(store, storeCurrentVersion(t, store))
	if err != nil {
		t.Fatalf("reload graph: %v", err)
	}
	if n := countResearchNodes(g); n != 2 {
		t.Fatalf("after run 2 graph has %d research nodes, want 2 (no replay, no skip)", n)
	}
	for _, id := range []pgtype.UUID{first, second} {
		if g.Node("research_node:"+util.UUIDToString(id)) == nil {
			t.Fatalf("node %v missing from research graph", id)
		}
	}
}

func countResearchNodes(g *memorygraph.Graph) int {
	n := 0
	for _, node := range g.Nodes() {
		if strings.HasPrefix(node.NodeID, "research_node:") {
			n++
		}
	}
	return n
}

// Partial failure: one broken workspace must not starve the others in the
// same tick, and the failed workspace must be retried (not skipped) on the
// next tick.
func TestResearchGraphExportJobPartialFailureRetries(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "1")
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	t.Setenv("MULTICA_MEMORY_TYPE", "")

	ws1 := seedExportWorkspace(t, pool, "graph")
	ws2 := seedExportWorkspace(t, pool, "graph")
	fake := &fakeResearchExporter{fail: map[string]error{
		util.UUIDToString(ws1): errors.New("simulated exporter failure"),
	}}

	_, err := makeResearchGraphExportHandler(pool, fake.run)(ctx, HandlerInput{})
	if err == nil {
		t.Fatalf("run 1: want error when a workspace fails")
	}
	calls1 := fake.called()
	if !containsString(calls1, util.UUIDToString(ws1)) || !containsString(calls1, util.UUIDToString(ws2)) {
		t.Fatalf("run 1 calls missing workspaces: %v", calls1)
	}

	fake.fail = nil
	res, err := makeResearchGraphExportHandler(pool, fake.run)(ctx, HandlerInput{})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res.Result["failed"] != 0 {
		t.Fatalf("run 2 failed = %v, want 0", res.Result["failed"])
	}
	if countString(fake.called(), util.UUIDToString(ws1)) != 2 {
		t.Fatalf("failed workspace not retried on run 2: %v", fake.called())
	}
}

func countString(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

// With the switch off the job must write nothing: no watermark row and no
// research graph directory, even for a graph-mode workspace with research
// data waiting.
func TestResearchGraphExportJobDisabledWritesNothing(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	root := t.TempDir()
	t.Setenv("MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED", "")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")

	ws, session, agent := seedResearchExportGraph(t, pool)
	insertResearchExportNode(t, pool, ws, session, agent, "finding", "disabled finding")

	if _, err := ResearchGraphExportJob(pool).Handler(ctx, HandlerInput{}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM research_graph_export_state WHERE workspace_id=$1`, ws).Scan(&n); err != nil {
		t.Fatalf("count state: %v", err)
	}
	if n != 0 {
		t.Fatalf("disabled job wrote export state rows: %d", n)
	}
	if _, err := os.Stat(filepath.Join(root, util.UUIDToString(ws), "memory_graph", "research")); !os.IsNotExist(err) {
		t.Fatalf("disabled job wrote a research graph dir: %v", err)
	}
}

// seedResearchExportGraph mirrors the production creation chain (research
// fleet, session passport in the same transaction) used by the service-level
// exporter tests.
func seedResearchExportGraph(t *testing.T, pool *pgxpool.Pool) (ws, session, agent pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8] + time.Now().Format("150405.000000000")
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"export e2e "+suffix, "export-e2e-"+suffix+"@multica.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1::uuid`, userID) })
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"export e2e ws "+suffix, "export-e2e-"+suffix).Scan(&ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws) })

	var runtime pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider) VALUES ($1, 'export runtime', 'local', 'test') RETURNING id`,
		ws).Scan(&runtime); err != nil {
		t.Fatalf("seed agent runtime: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, model, max_concurrent_tasks) VALUES ($1, 'researcher', 'local', $2, 'glm-5.3', 1) RETURNING id`,
		ws, runtime).Scan(&agent); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	var fleet pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO research_fleet (workspace_id, lead_agent_id) VALUES ($1, $2) RETURNING id`,
		ws, agent).Scan(&fleet); err != nil {
		t.Fatalf("seed fleet: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_session (
			workspace_id, fleet_id, created_by, title, goal, status, current_stage,
			depth_tier, product_round, product_round_budget
		) VALUES ($1, $2, $3::uuid, $4, $5, 'running', 's1_plan', 'standard', 1, 5) RETURNING id`,
		ws, fleet, userID, "export session "+suffix, "export goal").Scan(&session); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT research_ensure_run_session_passport($1, $2)`, ws, session); err != nil {
		t.Fatalf("ensure run session passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit session: %v", err)
	}
	return ws, session, agent
}

func insertResearchExportNode(t *testing.T, pool *pgxpool.Pool, ws, session, agent pgtype.UUID, nodeType, title string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin node tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_graph_node (workspace_id, session_id, node_type, title, summary, status, actor_agent_id)
		VALUES ($1, $2, $3, $4, $5, 'active', $6) RETURNING id`,
		ws, session, nodeType, title, title+" summary", agent).Scan(&id); err != nil {
		t.Fatalf("insert research node %s: %v", nodeType, err)
	}
	if err := researchrun.RegisterProductionGraphNodeTx(ctx, tx, util.UUIDToString(ws), util.UUIDToString(session), util.UUIDToString(id)); err != nil {
		t.Fatalf("register node passport: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit node: %v", err)
	}
	return id
}

func storeCurrentVersion(t *testing.T, store *memorygraph.Store) int {
	t.Helper()
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatalf("current version: %v", err)
	}
	return v
}
