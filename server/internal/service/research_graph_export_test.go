// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/researchrun"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Pure mapping tests (no database)
// ---------------------------------------------------------------------------

// The node-type whitelist admits knowledge nodes and excludes execution
// bookkeeping (unification spec §4.2 decision 3).
func TestResearchExportNodeTypeWhitelist(t *testing.T) {
	admitted := map[string]bool{
		"goal": true, "subquestion": true, "probe": true, "finding": true,
		"conflict": true, "dead_end": true, "refuted": true, "pivot": true,
		"conclusion": true, "insight": true,
	}
	for kind, want := range admitted {
		if got := researchNodeTypeAdmitted(kind); got != want {
			t.Fatalf("researchNodeTypeAdmitted(%q) = %v, want %v", kind, got, want)
		}
	}
	for _, kind := range []string{"agent_activity", "stage_gate", "roster_change", "product_round_gate", "", "unknown"} {
		if researchNodeTypeAdmitted(kind) {
			t.Fatalf("researchNodeTypeAdmitted(%q) must exclude execution bookkeeping", kind)
		}
	}
}

// The epistemic mapping table, asserted row by row (spec §4.2): terminal
// research states land directly in the matching memory epistemic status.
func TestResearchExportEpistemicMapping(t *testing.T) {
	cases := []struct {
		nodeType, status, want string
	}{
		{"conclusion", "active", memorygraph.StatusAccepted},
		{"insight", "active", memorygraph.StatusSupported},
		{"finding", "active", memorygraph.StatusProposed},
		{"goal", "active", memorygraph.StatusProposed},
		{"subquestion", "active", memorygraph.StatusProposed},
		{"probe", "active", memorygraph.StatusProposed},
		{"pivot", "active", memorygraph.StatusProposed},
		{"conflict", "active", memorygraph.StatusContested},
		{"dead_end", "active", memorygraph.StatusRejected},
		{"refuted", "active", memorygraph.StatusRejected},
		// Status overrides the type mapping.
		{"finding", "superseded", memorygraph.StatusSuperseded},
		{"conclusion", "superseded", memorygraph.StatusSuperseded},
		{"finding", "invalidated", memorygraph.StatusRejected},
		{"finding", "deprecated", memorygraph.StatusRejected},
		{"finding", "abandoned", memorygraph.StatusRejected},
		{"finding", "restarted", memorygraph.StatusSuperseded},
	}
	for _, c := range cases {
		if got := researchEpistemic(c.nodeType, c.status); got != c.want {
			t.Fatalf("researchEpistemic(%q,%q) = %q, want %q", c.nodeType, c.status, got, c.want)
		}
	}
}

// V6 conclusion/integration states map onto the same epistemic ladder.
func TestResearchExportV6StateMapping(t *testing.T) {
	insight := map[string]string{
		"proposed": memorygraph.StatusProposed, "accepted": memorygraph.StatusAccepted,
		"stale": memorygraph.StatusContested, "superseded": memorygraph.StatusSuperseded,
		"obsolete": memorygraph.StatusSuperseded, "unknown": memorygraph.StatusProposed,
	}
	for in, want := range insight {
		if got := researchInsightEpistemic(in); got != want {
			t.Fatalf("researchInsightEpistemic(%q) = %q, want %q", in, got, want)
		}
	}
	result := map[string]string{
		"proposed": memorygraph.StatusProposed, "accepted": memorygraph.StatusAccepted,
		"challenged": memorygraph.StatusContested, "refuted": memorygraph.StatusRejected,
		"invalid": memorygraph.StatusRejected, "unknown": memorygraph.StatusProposed,
	}
	for in, want := range result {
		if got := researchResultEpistemic(in); got != want {
			t.Fatalf("researchResultEpistemic(%q) = %q, want %q", in, got, want)
		}
	}
}

// The agent supernode dissolves into provenance: the memory node carries the
// actor agent id in SourceAgentIDs, the research source kind, and the source
// session; no agent node kind ever enters the graph.
func TestResearchExportSupernodeDissolves(t *testing.T) {
	ws := util.MustParseUUID("00000000-0000-0000-0000-0000000000a1")
	session := util.MustParseUUID("00000000-0000-0000-0000-0000000000b2")
	agent := util.MustParseUUID("00000000-0000-0000-0000-0000000000c3")
	row := researchNodeRow{
		ID:           util.MustParseUUID("11111111-1111-1111-1111-111111111111"),
		WorkspaceID:  ws,
		SessionID:    session,
		NodeType:     "finding",
		Title:        "routing regression",
		Summary:      "latency doubled after the switch",
		Status:       "active",
		ActorAgentID: agent,
		CreatedAt:    time.Unix(1725000000, 0).UTC(),
	}
	n := mapResearchNodeToMemory(row)
	if n.SourceKind != ResearchSourceKindNode {
		t.Fatalf("SourceKind = %q, want %q", n.SourceKind, ResearchSourceKindNode)
	}
	if len(n.SourceAgentIDs) != 1 || n.SourceAgentIDs[0] != util.UUIDToString(agent) {
		t.Fatalf("SourceAgentIDs = %v, want [%s]", n.SourceAgentIDs, util.UUIDToString(agent))
	}
	if n.SourceSessionID != util.UUIDToString(session) {
		t.Fatalf("SourceSessionID = %q, want %q", n.SourceSessionID, util.UUIDToString(session))
	}
	if n.Visibility != "research" || n.ChannelID != "" {
		t.Fatalf("scope = %q/%q, want research/empty", n.Visibility, n.ChannelID)
	}
	if n.NodeID != "research_node:"+util.UUIDToString(row.ID) {
		t.Fatalf("NodeID = %q, want deterministic research_node:<uuid>", n.NodeID)
	}
	if n.Epistemic != memorygraph.StatusProposed || n.CreatedBy != memorygraph.CreatorIngester {
		t.Fatalf("epistemic/creator = %q/%q", n.Epistemic, n.CreatedBy)
	}
}

// Only same-name edge types cross over, and never as hierarchy edges.
func TestResearchExportEdgeMapping(t *testing.T) {
	same := map[string]bool{
		"supports": true, "contradicts": true, "supersedes": true,
		"derived_from": true, "merged_from": true,
	}
	for et := range same {
		if !researchEdgeTypeAdmitted(et) {
			t.Fatalf("edge type %q must be admitted", et)
		}
	}
	for _, et := range []string{"leads_to", "abandons", "deepens", "restart_of", "superseded_by", "invalidated_by", "summarizes", ""} {
		if researchEdgeTypeAdmitted(et) {
			t.Fatalf("edge type %q must not be admitted", et)
		}
	}
}

// ---------------------------------------------------------------------------
// End-to-end export against a real Postgres (skipped without DATABASE_URL)
// ---------------------------------------------------------------------------

func researchExportTestPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// seedResearchGraph mirrors the production creation chain: a research
// fleet, a session whose passport registers in the same transaction
// (deferred domain guard), and graph nodes registered through
// researchrun's production passport API.
func seedResearchGraph(t *testing.T, pool *pgxpool.Pool) (ws, session, agent pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	suffix := util.UUIDToString(util.MustParseUUID("33333333-3333-3333-3333-333333333301"))[0:8] + time.Now().Format("150405.000000000")
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`,
		"research export "+suffix, "research-export-"+suffix+"@multica.test").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id=$1::uuid`, userID) })

	if err := pool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id`,
		"research export ws "+suffix, "research-export-"+suffix).Scan(&ws); err != nil {
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

func insertResearchNode(t *testing.T, pool *pgxpool.Pool, ws, session, agent pgtype.UUID, nodeType, title, status string) pgtype.UUID {
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
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		ws, session, nodeType, title, title+" summary", status, agent).Scan(&id); err != nil {
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

func insertResearchEdge(t *testing.T, pool *pgxpool.Pool, ws, session, from, to pgtype.UUID, edgeType string) error {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO research_graph_edge (workspace_id, session_id, from_node_id, to_node_id, edge_type)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, ws, session, from, to, edgeType).Scan(&id); err != nil {
		return err
	}
	if err := researchrun.RegisterProductionGraphEdgeTx(ctx, tx, util.UUIDToString(ws), util.UUIDToString(session), util.UUIDToString(id)); err != nil {
		return fmt.Errorf("register edge passport: %w", err)
	}
	return tx.Commit(ctx)
}

func newTestExporter(t *testing.T, pool *pgxpool.Pool) (*ResearchGraphExporter, pgtype.UUID) {
	t.Helper()
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	ws, _, _ := seedResearchGraph(t, pool)
	return NewResearchGraphExporter(pool, db.New(pool), ResearchGraphExporterConfig{}), ws
}

// End-to-end: whitelist filtering, epistemic mapping, supernode dissolution,
// same-name edges with no hierarchy edges, idempotent replay, and supersede
// state sync (unification spec §4.2).
func TestResearchGraphExportEndToEnd(t *testing.T) {
	pool := researchExportTestPool(t)
	ctx := context.Background()
	exporter, ws := newTestExporter(t, pool)
	session, agent := seedResearchIDs(t, pool, ws)

	goal := insertResearchNode(t, pool, ws, session, agent, "goal", "map the regression", "done")
	finding := insertResearchNode(t, pool, ws, session, agent, "finding", "latency doubled", "active")
	conclusion := insertResearchNode(t, pool, ws, session, agent, "conclusion", "switch caused it", "done")
	activity := insertResearchNode(t, pool, ws, session, agent, "agent_activity", "agent ran probe", "done")
	insertResearchNode(t, pool, ws, session, agent, "stage_gate", "stage 2 passed", "done")
	for _, e := range []struct {
		from, to pgtype.UUID
		edgeType string
	}{
		{finding, conclusion, "supports"},
		{goal, conclusion, "leads_to"}, // edge type not admitted
		{activity, goal, "supports"},   // endpoint filtered out
	} {
		if err := insertResearchEdge(t, pool, ws, session, e.from, e.to, e.edgeType); err != nil {
			t.Fatalf("insert edge %s: %v", e.edgeType, err)
		}
	}

	res1, err := exporter.ExportWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("ExportWorkspace: %v", err)
	}
	if res1.NodesWritten != 3 || res1.EdgesWritten != 1 {
		t.Fatalf("res1 = %+v, want 3 nodes / 1 edge", res1)
	}

	g := loadResearchGraphForTest(t, pool, ws)
	findingNode := g.Node("research_node:" + util.UUIDToString(finding))
	if findingNode == nil {
		t.Fatalf("finding node missing; nodes: %v", researchGraphNodeIDs(g))
	}
	if findingNode.Epistemic != memorygraph.StatusProposed {
		t.Fatalf("finding epistemic = %q", findingNode.Epistemic)
	}
	if n := g.Node("research_node:" + util.UUIDToString(conclusion)); n == nil || n.Epistemic != memorygraph.StatusAccepted {
		t.Fatalf("conclusion node = %+v", g.Node("research_node:"+util.UUIDToString(conclusion)))
	}
	if len(findingNode.SourceAgentIDs) != 1 || findingNode.SourceAgentIDs[0] != util.UUIDToString(agent) {
		t.Fatalf("supernode did not dissolve into provenance: %+v", findingNode)
	}
	if findingNode.SourceSessionID != util.UUIDToString(session) || findingNode.Visibility != "research" {
		t.Fatalf("scope/provenance wrong: %+v", findingNode)
	}
	for _, n := range g.Nodes() {
		if n.SourceKind != "" && n.SourceKind != ResearchSourceKindNode {
			t.Fatalf("unexpected source kind %q", n.SourceKind)
		}
	}
	_, rel := g.HierarchyEdges(), g.RelationEdges()
	if len(rel) != 1 || rel[0].Type != "supports" {
		t.Fatalf("relation edges = %+v, want one supports edge", rel)
	}

	// Idempotent replay: nothing changed, nothing written.
	res2, err := exporter.ExportWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("ExportWorkspace replay: %v", err)
	}
	if res2.NodesWritten != 0 || res2.EdgesWritten != 0 {
		t.Fatalf("res2 = %+v, want a no-op replay", res2)
	}

	// Supersede sync: the finding is superseded by the conclusion.
	if _, err := pool.Exec(ctx, `
		UPDATE research_graph_node SET status='superseded', superseded_by=$2, updated_at=now()+interval '1 second'
		WHERE id=$1`, finding, conclusion); err != nil {
		t.Fatalf("supersede finding: %v", err)
	}
	if _, err := exporter.ExportWorkspace(ctx, ws); err != nil {
		t.Fatalf("ExportWorkspace supersede: %v", err)
	}
	g = loadResearchGraphForTest(t, pool, ws)
	if n := g.Node("research_node:" + util.UUIDToString(finding)); n == nil || n.Epistemic != memorygraph.StatusSuperseded {
		t.Fatalf("superseded finding = %+v", g.Node("research_node:"+util.UUIDToString(finding)))
	}
	found := false
	for _, e := range g.RelationEdges() {
		if e.Type == memorygraph.EdgeTypeSupersedes && e.From == "research_node:"+util.UUIDToString(conclusion) && e.To == "research_node:"+util.UUIDToString(finding) {
			found = true
		}
	}
	if !found {
		t.Fatalf("supersedes edge conclusion→finding missing: %+v", g.RelationEdges())
	}
}

// The exporter-created research graph carries its immutable identity marker
// so identity-verified readers (federated recall, Director background recall)
// never degrade on exporter-created graphs (unification spec §3, §4.4, §5).
func TestResearchGraphExportStampsGraphIdentity(t *testing.T) {
	pool := researchExportTestPool(t)
	ctx := context.Background()
	exporter, ws := newTestExporter(t, pool)
	session, agent := seedResearchIDs(t, pool, ws)
	insertResearchNode(t, pool, ws, session, agent, "conclusion", "identity stamped", "done")
	if _, err := exporter.ExportWorkspace(ctx, ws); err != nil {
		t.Fatalf("ExportWorkspace: %v", err)
	}
	wsStr := util.UUIDToString(ws)
	dir, err := researchGraphDir(ctx, db.New(pool), ws)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := memorygraph.ReadGraphIdentity(dir)
	if err != nil {
		t.Fatalf("exporter-created research graph lacks identity: %v", err)
	}
	want := memorygraph.GraphIdentity{WorkspaceID: wsStr, Kind: string(memorygraph.GraphDirKindResearch), OwnerID: wsStr}
	if identity != want {
		t.Fatalf("identity=%+v, want %+v", identity, want)
	}
}

// Import-time deterministic dedup (spec §4.2): a ≥0.95 similarity hit folds
// the research node into the existing memory node with traceable lineage;
// below the threshold the import proceeds as a plain add.
func TestResearchGraphExportDedupMerge(t *testing.T) {
	pool := researchExportTestPool(t)
	ctx := context.Background()
	exporter, ws := newTestExporter(t, pool)
	session, agent := seedResearchIDs(t, pool, ws)

	first := insertResearchNode(t, pool, ws, session, agent, "finding", "latency doubled after the migration", "active")
	if _, err := exporter.ExportWorkspace(ctx, ws); err != nil {
		t.Fatalf("ExportWorkspace 1: %v", err)
	}
	existingID := "research_node:" + util.UUIDToString(first)

	exporter.similarity = func(context.Context, string, string, string) (string, float64, error) {
		return existingID, 0.97, nil
	}
	second := insertResearchNode(t, pool, ws, session, agent, "finding", "latency doubled after the migration again", "active")
	if _, err := exporter.ExportWorkspace(ctx, ws); err != nil {
		t.Fatalf("ExportWorkspace 2: %v", err)
	}

	g := loadResearchGraphForTest(t, pool, ws)
	dup := g.Node("research_node:" + util.UUIDToString(second))
	if dup == nil || dup.Epistemic != memorygraph.StatusSuperseded {
		t.Fatalf("duplicate must be imported then superseded: %+v", dup)
	}
	merged := false
	for _, e := range g.RelationEdges() {
		if e.Type == memorygraph.EdgeTypeMergedFrom && e.To == existingID {
			merged = true
		}
	}
	if !merged {
		t.Fatalf("merged_from edge into the survivor missing: %+v", g.RelationEdges())
	}
	if survivor := g.Node(existingID); survivor == nil || len(survivor.SourceAgentIDs) < 1 {
		t.Fatalf("survivor must keep provenance: %+v", g.Node(existingID))
	}
}

// Legacy workspaces fail closed: no research graph without graph memory.
func TestResearchGraphExportLegacyFailsClosed(t *testing.T) {
	pool := researchExportTestPool(t)
	t.Setenv("MULTICA_MEMORY_TYPE", "legacy")
	t.Setenv("MULTICA_WORKSPACES_ROOT", t.TempDir())
	exporter, ws := seedResearchOnly(t, pool)
	if _, err := exporter.ExportWorkspace(context.Background(), ws); err == nil {
		t.Fatal("legacy workspace must fail closed")
	}
}

// --- shared helpers below ---

// seedResearchIDs returns the session/agent of the workspace seeded by
// newTestExporter (the seed helper only returned ids before insert).
func seedResearchIDs(t *testing.T, pool *pgxpool.Pool, ws pgtype.UUID) (session, agent pgtype.UUID) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT id, (SELECT id FROM agent WHERE workspace_id=$1 LIMIT 1) FROM research_session WHERE workspace_id=$1 LIMIT 1`,
		ws).Scan(&session, &agent); err != nil {
		t.Fatalf("seedResearchIDs: %v", err)
	}
	return session, agent
}

func seedResearchOnly(t *testing.T, pool *pgxpool.Pool) (*ResearchGraphExporter, pgtype.UUID) {
	t.Helper()
	_, ws, _ := seedResearchGraph(t, pool)
	return NewResearchGraphExporter(pool, db.New(pool), ResearchGraphExporterConfig{}), ws
}

func loadResearchGraphForTest(t *testing.T, pool *pgxpool.Pool, ws pgtype.UUID) *memorygraph.Graph {
	t.Helper()
	dir, err := researchGraphDir(context.Background(), db.New(pool), ws)
	if err != nil {
		t.Fatalf("researchGraphDir: %v", err)
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatalf("current version: %v", err)
	}
	g, err := memorygraph.LoadGraph(store, v)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	return g
}

func researchGraphNodeIDs(g *memorygraph.Graph) []string {
	ids := make([]string, 0, len(g.Nodes()))
	for _, n := range g.Nodes() {
		ids = append(ids, n.NodeID)
	}
	return ids
}

// graphFingerprint captures the reproducible identity of a research graph:
// per-node content hash + epistemic and the relation-edge multiset. Two
// builds from the same ledger rows share the fingerprint regardless of
// version numbering.
type researchGraphFingerprint struct {
	nodes map[string]string // node_id -> content_hash + "|" + epistemic
	edges map[string]bool   // "type|from->to"
}

func fingerprintResearchGraph(g *memorygraph.Graph) *researchGraphFingerprint {
	fp := &researchGraphFingerprint{nodes: map[string]string{}, edges: map[string]bool{}}
	for _, n := range g.Nodes() {
		fp.nodes[n.NodeID] = n.ContentHash + "|" + n.Epistemic
	}
	for _, e := range g.RelationEdges() {
		fp.edges[e.Type+"|"+e.From+"->"+e.To] = true
	}
	return fp
}

// Plan guarantee (line 18, unification plan): the research graph directory
// can be wiped entirely and rebuilt by replay — the export state's keys must
// not skip re-materializing nodes the graph no longer contains. The rebuild
// is byte-consistent (same deterministic ids, hashes, epistemic, edges) and
// the next run is again a no-op.
func TestResearchGraphExportWipeAndRebuild(t *testing.T) {
	pool := researchExportTestPool(t)
	ctx := context.Background()
	exporter, ws := newTestExporter(t, pool)
	session, agent := seedResearchIDs(t, pool, ws)

	insertResearchNode(t, pool, ws, session, agent, "goal", "map the regression", "done")
	insertResearchNode(t, pool, ws, session, agent, "conclusion", "switch caused it", "done")
	if _, err := exporter.ExportWorkspace(ctx, ws); err != nil {
		t.Fatalf("ExportWorkspace: %v", err)
	}
	before := fingerprintResearchGraph(loadResearchGraphForTest(t, pool, ws))
	if len(before.nodes) != 2 {
		t.Fatalf("initial graph nodes = %v, want 2", before.nodes)
	}

	// Wipe the whole research graph directory; the durable export state
	// (cursor + keys) stays, as an operator deleting the on-disk graph
	// would leave it.
	dir, err := researchGraphDir(ctx, db.New(pool), ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := exporter.ExportWorkspace(ctx, ws); err != nil {
		t.Fatalf("ExportWorkspace rebuild: %v", err)
	}
	after := fingerprintResearchGraph(loadResearchGraphForTest(t, pool, ws))
	if len(after.nodes) != len(before.nodes) {
		t.Fatalf("rebuilt graph has %d nodes, want %d (wipe must rebuild, not skip): %v",
			len(after.nodes), len(before.nodes), after.nodes)
	}
	for id, want := range before.nodes {
		if got, ok := after.nodes[id]; !ok || got != want {
			t.Fatalf("rebuilt node %s = %q ok=%v, want %q", id, got, ok, want)
		}
	}
	if len(after.edges) != len(before.edges) {
		t.Fatalf("rebuilt edges = %v, want %v", after.edges, before.edges)
	}

	// Post-rebuild replay is again a no-op.
	res, err := exporter.ExportWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("ExportWorkspace post-rebuild: %v", err)
	}
	if res.NodesWritten != 0 || res.EdgesWritten != 0 {
		t.Fatalf("post-rebuild replay = %+v, want no-op", res)
	}
}
