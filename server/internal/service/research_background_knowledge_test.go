// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

// Phase 2 slice P2.1 (unification spec §5): the Director background-knowledge
// provider retrieves bounded, epistemic-filtered entries from the workspace
// research graph plus (for project-bound sessions) the bound project graph,
// and every failure degrades to empty entries rather than an error.

type backgroundFixture struct {
	pool        *pgxpool.Pool
	svc         *ResearchBackgroundKnowledgeService
	workspaceID string
	projectID   string
	root        string
}

func seedBackgroundWorkspace(t *testing.T, memoryType string) *backgroundFixture {
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

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Background Knowledge Test', 'bg-know-'||$1, '', 'BGK')
		RETURNING id::text`, uuid.NewString()[:8]).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM workspace WHERE id=$1::uuid`, workspaceID) })

	if _, err := pool.Exec(ctx, `
		INSERT INTO graph_memory_profile(workspace_id,memory_type,graph_memory_mode,explore_max_rounds,explore_nodes_per_expansion)
		VALUES($1::uuid,$2,'agent',6,4)`, workspaceID, memoryType); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Setenv("MULTICA_WORKSPACES_ROOT", root)

	return &backgroundFixture{
		pool: pool, svc: NewResearchBackgroundKnowledgeService(pool, root),
		workspaceID: workspaceID, projectID: uuid.NewString(), root: root,
	}
}

// seedBackgroundGraph creates the scoped graph for kind/owner and saves nodes
// at version 1.
func (f *backgroundFixture) seedBackgroundGraph(t *testing.T, kind memorygraph.GraphDirKind, ownerID string, nodes ...*memorygraph.Node) *memorygraph.Store {
	t.Helper()
	dir, err := memorygraph.EnsureScopedDir(f.root, f.workspaceID, kind, ownerID)
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
	return store
}

func backgroundNode(id, body, epistemic string, visibility string, observed time.Time) *memorygraph.Node {
	return &memorygraph.Node{
		NodeID: id, Body: body, Epistemic: epistemic, Visibility: visibility,
		CreatedBy: memorygraph.CreatorIngester, CreatedVersion: 1, UpdatedVersion: 1, ObservedAt: observed,
	}
}

func entryIDs(entries []researchrun.V6BackgroundKnowledgeEntry, graph string) []string {
	ids := []string{}
	for i := range entries {
		if entries[i].Graph == graph {
			ids = append(ids, entries[i].NodeID)
		}
	}
	return ids
}

func hasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// overwriteGraphIdentity rewrites a graph directory's identity marker with a
// foreign owner so VerifyGraphIdentity fails closed.
func overwriteGraphIdentity(root, workspaceID string, kind memorygraph.GraphDirKind, ownerID, foreignOwner string) error {
	dir, err := memorygraph.DirForScope(root, workspaceID, kind, ownerID)
	if err != nil {
		return err
	}
	body, err := json.Marshal(memorygraph.GraphIdentity{WorkspaceID: workspaceID, Kind: string(kind), OwnerID: foreignOwner})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".graph_identity.json"), body, 0o644)
}

// The research graph supplies background entries with epistemic annotation:
// rejected and superseded nodes are filtered by default, every other
// epistemic state passes, and entries carry the node id, graph tag, epistemic
// status and observation date.
func TestResearchBackgroundKnowledgeResearchGraph(t *testing.T) {
	f := seedBackgroundWorkspace(t, "graph")
	observed := time.Date(2026, 8, 15, 9, 30, 0, 0, time.UTC)
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID,
		backgroundNode("acc-1", "cache pool exhaustion recurs under sustained load", memorygraph.StatusAccepted, "research", observed),
		backgroundNode("prop-1", "cache pool exhaustion may correlate with retries", memorygraph.StatusProposed, "research", observed),
		backgroundNode("sup-1", "cache pool exhaustion was blamed on gc pauses", memorygraph.StatusSuperseded, "research", observed),
		backgroundNode("rej-1", "cache pool exhaustion is caused by cosmic rays", memorygraph.StatusRejected, "research", observed),
	)

	entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "cache pool exhaustion", "")
	if err != nil {
		t.Fatalf("BackgroundKnowledge: %v", err)
	}
	// Content determinism: the same graph version and the same goal render the
	// same entries (BM25-only retrieval, dates rendered verbatim), so a brief
	// compiled twice within one cycle carries identical background.
	replayed, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "cache pool exhaustion", "")
	if err != nil {
		t.Fatalf("BackgroundKnowledge replay: %v", err)
	}
	if len(replayed) != len(entries) {
		t.Fatalf("replay entries=%d, want %d", len(replayed), len(entries))
	}
	for i := range entries {
		if entries[i] != replayed[i] {
			t.Fatalf("replay entry %d = %+v, want %+v", i, replayed[i], entries[i])
		}
	}
	ids := entryIDs(entries, "research")
	if !hasID(ids, "acc-1") || !hasID(ids, "prop-1") {
		t.Fatalf("entries=%v, want accepted and proposed research nodes", ids)
	}
	if hasID(ids, "sup-1") || hasID(ids, "rej-1") {
		t.Fatalf("entries=%v, want superseded and rejected filtered by default", ids)
	}
	for _, entry := range entries {
		if entry.Graph != "research" {
			t.Fatalf("entry %s graph=%q, want research", entry.NodeID, entry.Graph)
		}
	}
	var accepted *researchrun.V6BackgroundKnowledgeEntry
	for i := range entries {
		if entries[i].NodeID == "acc-1" {
			accepted = &entries[i]
		}
	}
	if accepted == nil {
		t.Fatalf("entries=%v, want acc-1", ids)
	}
	if accepted.Epistemic != memorygraph.StatusAccepted {
		t.Fatalf("acc-1 epistemic=%q, want accepted", accepted.Epistemic)
	}
	if !accepted.ObservedAt.Equal(observed) {
		t.Fatalf("acc-1 observed_at=%v, want %v", accepted.ObservedAt, observed)
	}
	if !strings.Contains(accepted.Summary, "cache pool exhaustion recurs") {
		t.Fatalf("acc-1 summary=%q, want the node body", accepted.Summary)
	}
}

// A project-bound session reads both graphs: the workspace research graph and
// the bound project's project graph, tagged by the graph field. A
// research-visibility node inside the project graph never leaks into the
// project view.
func TestResearchBackgroundKnowledgeBoundProject(t *testing.T) {
	f := seedBackgroundWorkspace(t, "graph")
	observed := time.Now().UTC()
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID,
		backgroundNode("res-1", "router queue depth grows during research runs", memorygraph.StatusAccepted, "research", observed))
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindProject, f.projectID,
		backgroundNode("prj-1", "router queue depth grows during research runs", memorygraph.StatusSupported, "project", observed),
		backgroundNode("prj-intruder", "router queue depth grows during research runs", memorygraph.StatusAccepted, "research", observed))

	entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "router queue depth", f.projectID)
	if err != nil {
		t.Fatalf("BackgroundKnowledge: %v", err)
	}
	researchIDs := entryIDs(entries, "research")
	projectIDs := entryIDs(entries, "project")
	if !hasID(researchIDs, "res-1") {
		t.Fatalf("entries=%v, want the research graph node for a bound session", researchIDs)
	}
	if !hasID(projectIDs, "prj-1") {
		t.Fatalf("entries=%v, want the bound project graph node", projectIDs)
	}
	if hasID(projectIDs, "prj-intruder") || hasID(researchIDs, "prj-intruder") {
		t.Fatalf("entries=%v/%v, research-visibility node leaked from the project graph", projectIDs, researchIDs)
	}
}

// An unbound session reads the research graph only: the project graph exists
// on disk but is never consulted without a session binding.
func TestResearchBackgroundKnowledgeUnboundSessionOnlyResearch(t *testing.T) {
	f := seedBackgroundWorkspace(t, "graph")
	observed := time.Now().UTC()
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID,
		backgroundNode("res-1", "merge conflicts cluster around schema files", memorygraph.StatusAccepted, "research", observed))
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindProject, f.projectID,
		backgroundNode("prj-1", "merge conflicts cluster around schema files", memorygraph.StatusAccepted, "project", observed))

	entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "merge conflicts", "")
	if err != nil {
		t.Fatalf("BackgroundKnowledge: %v", err)
	}
	if !hasID(entryIDs(entries, "research"), "res-1") {
		t.Fatalf("entries=%v, want the research node for an unbound session", entryIDs(entries, "research"))
	}
	if hasID(entryIDs(entries, "project"), "prj-1") {
		t.Fatalf("entries=%v, unbound session must not read the project graph", entryIDs(entries, "project"))
	}
}

// Every retrieval failure degrades to empty entries without error: a missing
// research graph, an identity mismatch, and an empty query all yield no
// background rather than blocking the Director cycle.
func TestResearchBackgroundKnowledgeFailsDegraded(t *testing.T) {
	f := seedBackgroundWorkspace(t, "graph")

	if entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "anything", ""); err != nil || len(entries) != 0 {
		t.Fatalf("missing research graph: entries=%v err=%v, want empty with no error", entries, err)
	}

	observed := time.Now().UTC()
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID,
		backgroundNode("res-1", "index rebuild latency doubles at scale", memorygraph.StatusAccepted, "research", observed))
	if entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "   ", ""); err != nil || len(entries) != 0 {
		t.Fatalf("blank query: entries=%v err=%v, want empty with no error", entries, err)
	}

	// Corrupt the identity marker: verification fails closed and the provider
	// degrades to empty rather than reading a foreign graph.
	if err := overwriteGraphIdentity(f.root, f.workspaceID, memorygraph.GraphDirKindResearch, f.workspaceID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "index rebuild latency", ""); err != nil || len(entries) != 0 {
		t.Fatalf("identity mismatch: entries=%v err=%v, want empty with no error", entries, err)
	}
}

// Output stays bounded regardless of graph size: summaries truncate at the
// rune cap and each graph contributes at most its top-K entries.
func TestResearchBackgroundKnowledgeBounded(t *testing.T) {
	f := seedBackgroundWorkspace(t, "graph")
	observed := time.Now().UTC()
	long := strings.Repeat("长", 3000)
	nodes := []*memorygraph.Node{backgroundNode("long-1", "deployment throughput "+long, memorygraph.StatusAccepted, "research", observed)}
	for i := 0; i < 8; i++ {
		nodes = append(nodes, backgroundNode(
			fmt.Sprintf("bounded-%02d", i),
			"deployment throughput varies with fleet size "+strings.Repeat("x", 40+i), memorygraph.StatusAccepted, "research", observed))
	}
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID, nodes...)
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindProject, f.projectID,
		backgroundNode("prj-long", "deployment throughput "+long, memorygraph.StatusAccepted, "project", observed))

	entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "deployment throughput", f.projectID)
	if err != nil {
		t.Fatalf("BackgroundKnowledge: %v", err)
	}
	if n := len(entryIDs(entries, "research")); n > 5 {
		t.Fatalf("research entries=%d, want <= 5 per graph", n)
	}
	if n := len(entryIDs(entries, "project")); n > 5 {
		t.Fatalf("project entries=%d, want <= 5 per graph", n)
	}
	for _, entry := range entries {
		if runes := len([]rune(entry.Summary)); runes > 400 {
			t.Fatalf("entry %s summary runes=%d, want <= 400", entry.NodeID, runes)
		}
	}
}

// A legacy-memory workspace gets zero behavior: the provider returns empty
// entries even when a research graph exists on disk.
func TestResearchBackgroundKnowledgeLegacyWorkspace(t *testing.T) {
	f := seedBackgroundWorkspace(t, "legacy")
	observed := time.Now().UTC()
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID,
		backgroundNode("res-1", "legacy workspaces never read the graph", memorygraph.StatusAccepted, "research", observed))

	entries, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "legacy workspaces", "")
	if err != nil {
		t.Fatalf("BackgroundKnowledge: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries=%v, legacy workspace must get zero background", entries)
	}
}

// Director recalls feed the graph's own query log (spec §4.5): the
// maintenance trigger counts query-log entries across windows, so background
// recall counts as a usage signal for both the research and the project
// graph. Legacy/degraded paths write nothing.
func TestResearchBackgroundKnowledgeRecordsQueryLog(t *testing.T) {
	f := seedBackgroundWorkspace(t, "graph")
	observed := time.Now().UTC()
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, f.workspaceID,
		backgroundNode("res-1", "schema drift accumulates during research", memorygraph.StatusAccepted, "research", observed))
	f.seedBackgroundGraph(t, memorygraph.GraphDirKindProject, f.projectID,
		backgroundNode("prj-1", "schema drift accumulates during research", memorygraph.StatusAccepted, "project", observed))

	if _, err := f.svc.BackgroundKnowledge(context.Background(), f.workspaceID, "run-1", "schema drift", f.projectID); err != nil {
		t.Fatalf("BackgroundKnowledge: %v", err)
	}
	assertDirectorQueryLogRecall(t, f.root, f.workspaceID, memorygraph.GraphDirKindResearch, f.workspaceID, "res-1")
	assertDirectorQueryLogRecall(t, f.root, f.workspaceID, memorygraph.GraphDirKindProject, f.projectID, "prj-1")

	// A miss writes nothing: only adopted recalls are usage signals.
	fresh := seedBackgroundWorkspace(t, "graph")
	fresh.seedBackgroundGraph(t, memorygraph.GraphDirKindResearch, fresh.workspaceID,
		backgroundNode("res-9", "unrelated topic", memorygraph.StatusAccepted, "research", observed))
	if _, err := fresh.svc.BackgroundKnowledge(context.Background(), fresh.workspaceID, "run-1", "schema drift", ""); err != nil {
		t.Fatalf("BackgroundKnowledge miss: %v", err)
	}
	if recalls := directorQueryLogRecalls(t, fresh.root, fresh.workspaceID, memorygraph.GraphDirKindResearch, fresh.workspaceID); len(recalls) != 0 {
		t.Fatalf("miss recorded recalls=%v, want none", recalls)
	}
}

func directorQueryLogRecalls(t *testing.T, root, ws string, kind memorygraph.GraphDirKind, owner string) []string {
	t.Helper()
	dir, err := memorygraph.DirForScope(root, ws, kind, owner)
	if err != nil {
		t.Fatal(err)
	}
	store := memorygraph.NewStore(dir)
	windows, err := store.ListQueryLogWindows()
	if err != nil {
		t.Fatal(err)
	}
	recalls := []string{}
	for _, window := range windows {
		entries, err := store.ReadQueryLog(window)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Found {
				recalls = append(recalls, entry.NodeIDs...)
			}
		}
	}
	return recalls
}

func assertDirectorQueryLogRecall(t *testing.T, root, ws string, kind memorygraph.GraphDirKind, owner, wantNode string) {
	t.Helper()
	for _, id := range directorQueryLogRecalls(t, root, ws, kind, owner) {
		if id == wantNode {
			return
		}
	}
	t.Fatalf("no adopted query-log recall of %s in the %s graph", wantNode, string(kind))
}
