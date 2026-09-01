package memorygraph

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Research-graph maintenance rounds (unification spec §4.5): a no-staging
// consolidation variant whose working set is driven by recently imported
// research nodes and the three retrieval-signal sources, restricted to
// update/merge/delete/edge operations on working-set nodes with epistemic
// migrations allowed only along the supersede direction.

// seedResearchNode writes one research-sourced node (as the exporter would)
// to version v.
func seedResearchNode(t *testing.T, store *Store, v int, id, body string, observedAt time.Time, epistemic string) *Node {
	t.Helper()
	n := &Node{
		NodeID:         id,
		Body:           body,
		Epistemic:      epistemic,
		TemporalStatus: TemporalCurrent,
		Visibility:     "research",
		SourceKind:     "research_node",
		CreatedBy:      CreatorIngester,
		CreatedVersion: v,
		UpdatedVersion: v,
		ObservedAt:     observedAt,
	}
	if err := store.SaveNode(v, n); err != nil {
		t.Fatalf("SaveNode %s: %v", id, err)
	}
	return n
}

// maintenanceFixture returns a store holding one fresh research import, one
// lexically similar older research node, one unrelated node outside every
// signal, and the cited/judge nodes of one query-log entry. The builder has
// no explore/dive readers (nil signals), so the pool draws from the query
// log and research imports only.
func maintenanceFixture(t *testing.T) (*Store, *fakeConsolidateBackend, *Consolidator) {
	t.Helper()
	store := newTestStore(t)
	now := time.Now().UTC()
	v := currentTestVersion(t, store)
	seedResearchNode(t, store, v, "research_node:new1", "cache hit rate dropped after the sharding rollout", now, StatusProposed)
	seedResearchNode(t, store, v, "research_node:old1", "cache hit rate dropped after the migration", now.Add(-48*time.Hour), StatusAccepted)
	seedGraphNode(t, store, v, "outside", "unrelated cooking notes")
	seedGraphNode(t, store, v, "cited", "cited node body")
	seedGraphNode(t, store, v, "judged", "judge-relevant node body")
	if err := store.AppendQueryLog(queryLogWindowDaemon, &QueryLogEntry{
		TraceID: "t1", Query: "cache", Timestamp: now, Version: v,
		NodeIDs: []string{"cited"}, RelevantNodes: []string{"judged"}, Found: true,
	}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}

	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON()
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)
	c.SetWorkingSetBuilder(NewWorkingSetBuilder(store, RetrievalSignals{}, nil, DefaultWorkingSetConfig(), 8))
	return store, backend, c
}

func currentTestVersion(t *testing.T, store *Store) int {
	t.Helper()
	v, err := store.CurrentVersion()
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	return v
}

// The prompt carries the fresh research import, its similar older node, and
// the three-signal nodes; no staging section content; the op whitelist
// excludes add_node.
func TestMaintenanceRoundWorkingSet(t *testing.T) {
	store, backend, c := maintenanceFixture(t)
	if _, err := c.MaintenanceRound(context.Background()); err != nil {
		t.Fatalf("MaintenanceRound: %v", err)
	}
	if backend.allPrompts() == "" {
		t.Fatalf("maintenance round never called the agent")
	}
	prompt := backend.allPrompts()
	for _, want := range []string{"research_node:new1", "research_node:old1", "cited", "judged", "research-import", "cited"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "research-similar") {
		t.Fatalf("prompt missing the similar-old-node signal:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- (none)") {
		t.Fatalf("maintenance prompt should show an empty staging list:\n%s", prompt)
	}
	if strings.Contains(prompt, "\"op\":\"add_node\"") {
		t.Fatalf("maintenance prompt offers add_node:\n%s", prompt)
	}
	if strings.Contains(prompt, "outside") {
		t.Fatalf("prompt leaks a node outside every signal:\n%s", prompt)
	}

	// The import watermark rides the shared working-set cursor entry so the
	// next round is incremental (spec §4.5/§4.3 shared cursor mechanism).
	entries, err := NewOpLogger(store).Read(currentTestVersion(t, store))
	if err != nil {
		t.Fatalf("read op log: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Op == OpWorkingSetCursor && e.Detail["research_observed_through"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("op log lacks the research-import watermark: %+v", entries)
	}
}

// Consumed imports and query-log entries do not re-enter the next round's
// working set: with no new signal the second round is an idle no-op that
// never calls the agent.
func TestMaintenanceRoundCursorIncremental(t *testing.T) {
	_, backend, c := maintenanceFixture(t)
	if _, err := c.MaintenanceRound(context.Background()); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	calls := backend.calls
	if _, err := c.MaintenanceRound(context.Background()); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if backend.calls != calls {
		t.Fatalf("round 2 called the agent (%d -> %d), want idle no-op", calls, backend.calls)
	}
}

// An empty working set is an idle round: no agent call, no new version.
func TestMaintenanceRoundIdleNoAgentCall(t *testing.T) {
	store := newTestStore(t)
	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON()
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)
	c.SetWorkingSetBuilder(NewWorkingSetBuilder(store, RetrievalSignals{}, nil, DefaultWorkingSetConfig(), 8))

	res, err := c.MaintenanceRound(context.Background())
	if err != nil {
		t.Fatalf("MaintenanceRound: %v", err)
	}
	if backend.calls != 0 {
		t.Fatalf("idle round called the agent %d times", backend.calls)
	}
	if res.Switched || res.WinnerVersion != currentTestVersion(t, store) {
		t.Fatalf("idle round produced a version: %+v", res)
	}
}

// Operations may only target working-set nodes, and add_node is forbidden:
// the exporter is the only writer of new research content.
func TestMaintenanceRoundRestrictsOpsToWorkingSet(t *testing.T) {
	store, _, c := maintenanceFixture(t)
	c.backend.(*fakeConsolidateBackend).respond = func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpUpdateNode, NodeID: "research_node:new1", Node: &Node{NodeID: "research_node:new1", Body: "cache hit rate dropped after the sharding rollout, root cause confirmed", Epistemic: StatusProposed}},
			ConsolidateOp{Op: OpUpdateNode, NodeID: "outside", Node: &Node{NodeID: "outside", Body: "attempted edit outside the working set"}},
			ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "smuggled", Body: "new content must come from the exporter"}},
			ConsolidateOp{Op: OpMergeNode, InputNodeIDs: []string{"research_node:new1", "research_node:old1"}, Node: &Node{NodeID: "research_node:merged", Body: "cache hit rate dropped after the sharding rollout; same regression as the migration", Epistemic: StatusAccepted}},
			ConsolidateOp{Op: OpAddRelationEdge, Edge: &Edge{EdgeID: "r-out", Type: "supports", From: "outside", To: "cited"}},
		)
	}
	res, err := c.MaintenanceRound(context.Background())
	if err != nil {
		t.Fatalf("MaintenanceRound: %v", err)
	}
	var outsideRejected, addRejected, edgeRejected bool
	for _, r := range res.Rejected {
		if r.Target == "outside" && strings.Contains(r.Reason, "working set") {
			outsideRejected = true
		}
		if r.Op == OpAddNode {
			addRejected = true
		}
		if r.Op == OpAddRelationEdge {
			edgeRejected = true
		}
	}
	if !outsideRejected || !addRejected || !edgeRejected {
		t.Fatalf("rejections incomplete (%v/%v/%v): %+v", outsideRejected, addRejected, edgeRejected, res.Rejected)
	}
	if res.OpsApplied != 2 {
		t.Fatalf("applied %d ops, want 2 (update + merge): %+v", res.OpsApplied, res.Rejected)
	}

	g, err := LoadGraph(store, currentTestVersion(t, store))
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	if merged := g.Node("research_node:merged"); merged == nil {
		t.Fatalf("merge result node missing; nodes: %v", graphNodeIDs(g))
	}
	if outside := g.Node("outside"); outside == nil || outside.Body != "unrelated cooking notes" {
		t.Fatalf("outside node was mutated: %+v", outside)
	}
}

// Fidelity: epistemic edits may only migrate along the supersede direction
// (settling further); reviving a settled node is rejected.
func TestMaintenanceRoundEpistemicSupersedeOnly(t *testing.T) {
	store := newTestStore(t)
	v := currentTestVersion(t, store)
	seedResearchNode(t, store, v, "research_node:claim", "the regression is caused by connection pool exhaustion", time.Now().UTC(), StatusAccepted)
	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpUpdateNode, NodeID: "research_node:claim", Node: &Node{NodeID: "research_node:claim", Body: "the regression is caused by connection pool exhaustion", Epistemic: StatusProposed}},
			ConsolidateOp{Op: OpUpdateNode, NodeID: "research_node:claim", Node: &Node{NodeID: "research_node:claim", Body: "the regression is caused by connection pool exhaustion", Epistemic: StatusSuperseded}},
		)
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)
	c.SetWorkingSetBuilder(NewWorkingSetBuilder(store, RetrievalSignals{}, nil, DefaultWorkingSetConfig(), 8))

	res, err := c.MaintenanceRound(context.Background())
	if err != nil {
		t.Fatalf("MaintenanceRound: %v", err)
	}
	if len(res.Rejected) != 1 || !strings.Contains(res.Rejected[0].Reason, "supersede") {
		t.Fatalf("rejections = %+v, want one epistemic-regression rejection", res.Rejected)
	}
	g, err := LoadGraph(store, currentTestVersion(t, store))
	if err != nil {
		t.Fatalf("load graph: %v", err)
	}
	if n := g.Node("research_node:claim"); n == nil || n.Epistemic != StatusSuperseded {
		t.Fatalf("claim epistemic = %+v, want superseded (forward migration applied)", n)
	}
}

func graphNodeIDs(g *Graph) []string {
	ids := make([]string, 0, len(g.Nodes()))
	for _, n := range g.Nodes() {
		ids = append(ids, n.NodeID)
	}
	return ids
}
