// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Phase 2 acceptance tests (unification spec §8, items 8-10): the Director
// cycle context carries a research-graph background block with epistemic and
// date annotation (8), project-bound sessions read the project graph while
// unbound sessions read research only (9), and a second research run's
// Director planning cites the first run's exported conclusions through the
// closed loop exporter → research graph → brief (10).

type backgroundAcceptanceFixture struct {
	pool      *pgxpool.Pool
	store     *researchrun.PostgresStore
	engine    researchrun.ResearchRunDirectorControl
	exporter  *ResearchGraphExporter
	ws        pgtype.UUID
	workspace string
	root      string
	userID    string
	agent     pgtype.UUID
	fleet     pgtype.UUID
}

func seedBackgroundAcceptance(t *testing.T) *backgroundAcceptanceFixture {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()

	root := t.TempDir()
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)

	suffix := uuid.NewString()[:8]
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"bg-accept-"+suffix, "bg-accept-"+suffix+"@multica.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var ws pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"bg acceptance "+suffix, "bg-accept-"+suffix).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1`, ws)
		_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1::uuid`, userID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_memory_profile(workspace_id,memory_type,graph_memory_mode,explore_max_rounds,explore_nodes_per_expansion)
		VALUES($1,'graph','agent',6,4)`, ws); err != nil {
		t.Fatal(err)
	}
	var runtime pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider) VALUES ($1, 'bg runtime', 'local', 'test') RETURNING id`, ws).Scan(&runtime); err != nil {
		t.Fatal(err)
	}
	var agent pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, model) VALUES ($1, 'director', 'local', $2, 'glm-5.3') RETURNING id`, ws, runtime).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	var fleet pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO research_fleet (workspace_id, lead_agent_id) VALUES ($1, $2) RETURNING id`, ws, agent).Scan(&fleet); err != nil {
		t.Fatal(err)
	}

	store := researchrun.NewPostgresStore(pool)
	store.SetBackgroundKnowledgeProvider(NewResearchBackgroundKnowledgeService(pool, root))
	return &backgroundAcceptanceFixture{
		pool: pool, store: store,
		engine:   researchrun.NewEngine(store, nil, nil).(researchrun.ResearchRunDirectorControl),
		exporter: NewResearchGraphExporter(pool, db.New(pool), ResearchGraphExporterConfig{}),
		ws:       ws, workspace: util.UUIDToString(ws), root: root, userID: userID, agent: agent, fleet: fleet,
	}
}

// newV6Session inserts an orchestrator-v6 research session; projectBinding
// may be nil (unbound) or a project UUID.
func (f *backgroundAcceptanceFixture) newV6Session(t *testing.T, goal string, projectBinding any) pgtype.UUID {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx())
	var session pgtype.UUID
	if err := tx.QueryRow(f.ctx(), `
		INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, depth_tier)
		VALUES ($1, $2, $3::uuid, $4, $5, 'running', 'standard') RETURNING id`,
		f.ws, f.fleet, f.userID, "session "+goal, goal).Scan(&session); err != nil {
		t.Fatal(err)
	}
	// The session passport guard is deferred: the registration must land in
	// the same transaction as the insert, and the V6 orchestrator switch
	// rides along.
	if _, err := tx.Exec(f.ctx(), `
		SELECT research_artifact_backfill_registered($1, $2, $2, 'run_session', now(), NULL, NULL)`,
		f.ws, session); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx(), `
		UPDATE research_session SET orchestrator_version='research-run-v6', project_id=$2 WHERE id=$1`,
		session, projectBinding); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(f.ctx()); err != nil {
		t.Fatal(err)
	}
	return session
}

func (f *backgroundAcceptanceFixture) ctx() context.Context { return context.Background() }

func (f *backgroundAcceptanceFixture) seedGraph(t *testing.T, kind memorygraph.GraphDirKind, owner string, nodes ...*memorygraph.Node) {
	t.Helper()
	dir, err := memorygraph.EnsureScopedDir(f.root, f.workspace, kind, owner)
	if err != nil {
		t.Fatal(err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if err := store.SaveNode(1, node); err != nil {
			t.Fatal(err)
		}
	}
}

// directorCyclePage runs one Director cycle for the session and returns the
// parsed first brief page.
func (f *backgroundAcceptanceFixture) directorCyclePage(t *testing.T, session pgtype.UUID) map[string]any {
	t.Helper()
	ctx := f.ctx()
	if _, err := f.store.AssignV6Director(ctx, researchrun.AssignV6DirectorInput{
		WorkspaceID: f.workspace, RunID: util.UUIDToString(session),
		AgentID: util.UUIDToString(f.agent), UserID: f.userID,
		Reason: "acceptance cycle", ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatalf("AssignV6Director: %v", err)
	}
	var stateVersion, through int64
	if err := f.pool.QueryRow(ctx, `
		SELECT state_version, COALESCE((SELECT max(sequence) FROM research_run_event e WHERE e.session_id=s.id),0)
		FROM research_session s WHERE s.id=$1`, session).Scan(&stateVersion, &through); err != nil {
		t.Fatal(err)
	}
	cycle, err := f.engine.StartV6DirectorCycle(ctx, researchrun.StartV6DirectorCycleInput{
		WorkspaceID: f.workspace, RunID: util.UUIDToString(session),
		TriggerKey: "acceptance-" + uuid.NewString()[:8], FromSequence: through, ThroughSequence: through,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("StartV6DirectorCycle: %v", err)
	}
	var content []byte
	if err := f.pool.QueryRow(ctx, `
		SELECT content_bytes FROM research_director_brief_page
		WHERE director_cycle_id=$1::uuid AND ordinal=0`, cycle.ID).Scan(&content); err != nil {
		t.Fatalf("persisted page: %v", err)
	}
	var page map[string]any
	if err := json.Unmarshal(content, &page); err != nil {
		t.Fatalf("page json: %v", err)
	}
	return page
}

func briefBackgroundEntries(t *testing.T, page map[string]any) []map[string]any {
	t.Helper()
	block, ok := page["background_knowledge"].(map[string]any)
	if !ok {
		t.Fatalf("page lacks background_knowledge: %v", page)
	}
	raw, _ := block["entries"].([]any)
	entries := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, _ := item.(map[string]any)
		entries = append(entries, entry)
	}
	return entries
}

func backgroundEntryIDs(entries []map[string]any, graph string) []string {
	ids := []string{}
	for _, entry := range entries {
		if entry["graph"] == graph {
			ids = append(ids, entry["node_id"].(string))
		}
	}
	return ids
}

// Acceptance 8: the Director cycle context contains a research-graph
// background block whose entries carry epistemic status and observation date.
func TestAcceptance8_DirectorBriefContainsResearchBackground(t *testing.T) {
	f := seedBackgroundAcceptance(t)
	acceptedAt := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)
	proposedAt := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	f.seedGraph(t, memorygraph.GraphDirKindResearch, f.workspace,
		&memorygraph.Node{NodeID: "res-accepted", Body: "cache pools exhaust under sustained load", Epistemic: memorygraph.StatusAccepted, Visibility: "research", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: acceptedAt},
		&memorygraph.Node{NodeID: "res-proposed", Body: "cache pools exhaust under sustained load retries", Epistemic: memorygraph.StatusProposed, Visibility: "research", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: proposedAt},
	)
	session := f.newV6Session(t, "Investigate why cache pools exhaust under sustained load", nil)

	page := f.directorCyclePage(t, session)
	entries := briefBackgroundEntries(t, page)
	ids := backgroundEntryIDs(entries, "research")
	if len(ids) == 0 {
		t.Fatalf("entries=%v, want research background", entries)
	}
	byID := map[string]map[string]any{}
	for _, entry := range entries {
		byID[entry["node_id"].(string)] = entry
	}
	if accepted := byID["res-accepted"]; accepted == nil || accepted["epistemic"] != "accepted" || accepted["observed_at_date"] != "2026-08-15" {
		t.Fatalf("accepted entry=%v, want epistemic and 2026-08-15 annotation", accepted)
	}
	if proposed := byID["res-proposed"]; proposed == nil || proposed["epistemic"] != "proposed" || proposed["observed_at_date"] != "2026-08-22" {
		t.Fatalf("proposed entry=%v, want epistemic and 2026-08-22 annotation", proposed)
	}
	block := page["background_knowledge"].(map[string]any)
	if guidance, _ := block["guidance"].(string); !strings.Contains(guidance, "不作为证据") {
		t.Fatalf("guidance=%q, want the not-evidence wording", guidance)
	}
}

// Acceptance 9: a project-bound session's brief cites both the project graph
// and the research graph; an unbound session cites research only.
func TestAcceptance9_BoundSessionReadsProjectGraph(t *testing.T) {
	f := seedBackgroundAcceptance(t)
	observed := time.Now().UTC()
	f.seedGraph(t, memorygraph.GraphDirKindResearch, f.workspace,
		&memorygraph.Node{NodeID: "res-1", Body: "router queue depth grows during research runs", Epistemic: memorygraph.StatusAccepted, Visibility: "research", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: observed})
	var project pgtype.UUID
	if err := f.pool.QueryRow(f.ctx(), `INSERT INTO project (workspace_id, title) VALUES ($1, 'acceptance project') RETURNING id`, f.ws).Scan(&project); err != nil {
		t.Fatal(err)
	}
	f.seedGraph(t, memorygraph.GraphDirKindProject, util.UUIDToString(project),
		&memorygraph.Node{NodeID: "prj-1", Body: "router queue depth grows during research runs", Epistemic: memorygraph.StatusSupported, Visibility: "project", CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: observed})

	bound := f.directorCyclePage(t, f.newV6Session(t, "router queue depth grows", project))
	entries := briefBackgroundEntries(t, bound)
	if ids := backgroundEntryIDs(entries, "project"); len(ids) == 0 || ids[0] != "prj-1" {
		t.Fatalf("bound entries=%v, want the project graph node cited", entries)
	}
	if ids := backgroundEntryIDs(entries, "research"); len(ids) == 0 || ids[0] != "res-1" {
		t.Fatalf("bound entries=%v, want the research graph node cited", entries)
	}

	unbound := f.directorCyclePage(t, f.newV6Session(t, "router queue depth grows", nil))
	entries = briefBackgroundEntries(t, unbound)
	if ids := backgroundEntryIDs(entries, "research"); len(ids) == 0 {
		t.Fatalf("unbound entries=%v, want research background", entries)
	}
	if ids := backgroundEntryIDs(entries, "project"); len(ids) != 0 {
		t.Fatalf("unbound entries=%v, project graph must stay unread", entries)
	}
}

// Acceptance 10: the progressive closed loop — session A's conclusion is
// exported into the workspace research graph by the Phase 1 exporter, and
// session B's Director cycle cites that conclusion in its background block.
// A superseded conclusion stays filtered inside the same loop.
func TestAcceptance10_ProgressiveLoopAcrossSessions(t *testing.T) {
	f := seedBackgroundAcceptance(t)
	ctx := f.ctx()

	// Session A (legacy orchestrator) with one durable conclusion and one
	// superseded finding. The passport registration shares the insert
	// transaction (deferred guard).
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var sessionA pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_session (workspace_id, fleet_id, created_by, title, goal, status, depth_tier)
		VALUES ($1, $2, $3::uuid, 'session A', 'first pass', 'running', 'standard') RETURNING id`,
		f.ws, f.fleet, f.userID).Scan(&sessionA); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT research_artifact_backfill_registered($1, $2, $2, 'run_session', now(), NULL, NULL)`, f.ws, sessionA); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	conclusion := insertResearchNode(t, f.pool, f.ws, sessionA, f.agent, "conclusion", "cache pools exhaust under sustained load", "done")
	superseded := insertResearchNode(t, f.pool, f.ws, sessionA, f.agent, "finding", "cache pools exhaust under sustained load gc pauses", "active")
	if _, err := f.pool.Exec(ctx, `UPDATE research_graph_node SET status='superseded', superseded_by=$2 WHERE id=$1`, superseded, conclusion); err != nil {
		t.Fatal(err)
	}
	if _, err := f.exporter.ExportWorkspace(ctx, f.ws); err != nil {
		t.Fatalf("ExportWorkspace: %v", err)
	}

	// Session B plans with a goal that overlaps the first run's conclusion.
	sessionB := f.newV6Session(t, "Investigate why cache pools exhaust under sustained load", nil)
	page := f.directorCyclePage(t, sessionB)

	conclusionID := "research_node:" + util.UUIDToString(conclusion)
	supersededID := "research_node:" + util.UUIDToString(superseded)
	entries := briefBackgroundEntries(t, page)
	var cited map[string]any
	for _, entry := range entries {
		if entry["node_id"] == conclusionID {
			cited = entry
		}
		if entry["node_id"] == supersededID {
			t.Fatalf("superseded conclusion %s leaked into the second run's background", supersededID)
		}
	}
	if cited == nil {
		t.Fatalf("entries=%v, want the first run's conclusion %s cited", entries, conclusionID)
	}
	if cited["graph"] != "research" || cited["epistemic"] != "accepted" {
		t.Fatalf("cited entry=%v, want the exported research conclusion with accepted status", cited)
	}
	if date, _ := cited["observed_at_date"].(string); len(date) != 10 {
		t.Fatalf("cited entry date=%v, want YYYY-MM-DD", cited["observed_at_date"])
	}
}
