package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

// seedGraphNode writes one node to version v of the store.
func seedGraphNode(t *testing.T, store *Store, v int, id, body string, segRefs ...string) *Node {
	t.Helper()
	n := &Node{
		NodeID:         id,
		Body:           body,
		SegmentRefs:    segRefs,
		Epistemic:      StatusAccepted,
		TemporalStatus: TemporalCurrent,
		CreatedBy:      CreatorIngester,
		CreatedVersion: v,
		UpdatedVersion: v,
		ObservedAt:     time.Now().UTC(),
	}
	if err := store.SaveNode(v, n); err != nil {
		t.Fatalf("SaveNode %s: %v", id, err)
	}
	return n
}

// consolidateOpsJSON encodes operations in the agent's strict-JSON final
// response format.
func consolidateOpsJSON(ops ...ConsolidateOp) string {
	b, err := json.Marshal(consolidateOutput{Operations: ops})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// fakeConsolidateBackend plays the consolidation agent: respond maps the
// prompt and 1-based call index to the final response output.
type fakeConsolidateBackend struct {
	mu      sync.Mutex
	calls   int
	prompts []string
	respond func(prompt string, callIdx int) string
}

func (f *fakeConsolidateBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	f.mu.Lock()
	f.calls++
	idx := f.calls
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	return exploreCompletedSession(f.respond(prompt, idx)), nil
}

func (f *fakeConsolidateBackend) allPrompts() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.prompts, "\n===\n")
}

// ---------------------------------------------------------------------------
// ShouldConsolidate
// ---------------------------------------------------------------------------

func TestShouldConsolidateDualThreshold(t *testing.T) {
	cfg := DefaultConsolidateConfig() // segments 50, queries 200
	cases := []struct {
		name     string
		segments int
		queries  int
		want     bool
	}{
		{"segments only", 50, 0, true},
		{"queries only", 0, 200, true},
		{"both above", 60, 250, true},
		{"neither", 49, 199, false},
		{"zero", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldConsolidate(tc.segments, tc.queries, cfg); got != tc.want {
				t.Fatalf("ShouldConsolidate(%d, %d) = %v, want %v", tc.segments, tc.queries, got, tc.want)
			}
		})
	}

	// Custom thresholds are honored.
	custom := ConsolidateConfig{TriggerSegments: 3, TriggerQueries: 7}
	if !ShouldConsolidate(3, 0, custom) || !ShouldConsolidate(0, 7, custom) || ShouldConsolidate(2, 6, custom) {
		t.Fatalf("custom thresholds not honored")
	}
}

// ---------------------------------------------------------------------------
// non-TTT consolidation
// ---------------------------------------------------------------------------

func TestConsolidateNonTTTAppliesValidOps(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha routing notes")
	if err := store.WriteStagingSegment("seg-1", []byte("gamma delta segment summary")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}

	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n2", Body: "gamma delta consolidated", SegmentRefs: []string{"seg-1"}}},
			ConsolidateOp{Op: OpAddHierarchyEdge, Edge: &Edge{EdgeID: "h1", From: "n1", To: "n2"}},
			ConsolidateOp{Op: OpUpdateNode, NodeID: "n1", Node: &Node{NodeID: "n1", Body: "alpha routing notes v2"}},
			ConsolidateOp{Op: OpDeleteNode, NodeID: "ghost"}, // invalid: unknown node
		)
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, backend, cfg, "test", nil)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if res.WinnerVersion != 1 || res.Switched {
		t.Fatalf("WinnerVersion=%d Switched=%v, want 1/false", res.WinnerVersion, res.Switched)
	}
	if res.OpsApplied != 3 {
		t.Fatalf("OpsApplied = %d, want 3", res.OpsApplied)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].Op != OpDeleteNode || res.Rejected[0].Target != "ghost" {
		t.Fatalf("Rejected = %+v, want one delete_node/ghost rejection", res.Rejected)
	}

	// Op log: exactly the 3 applied ops, actor "consolidator".
	entries, err := NewOpLogger(store).Read(1)
	if err != nil {
		t.Fatalf("read op log: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("op log entries = %d, want 3", len(entries))
	}
	wantOps := []string{OpAddNode, OpAddHierarchyEdge, OpUpdateNode}
	for i, e := range entries {
		if e.Actor != CreatorConsolidator {
			t.Fatalf("entry %d actor = %q, want %q", i, e.Actor, CreatorConsolidator)
		}
		if e.Op != wantOps[i] {
			t.Fatalf("entry %d op = %q, want %q", i, e.Op, wantOps[i])
		}
	}

	// Graph state: new node references the staging segment, update landed,
	// levels recomputed.
	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	n2 := g.Node("n2")
	if n2 == nil || len(n2.SegmentRefs) != 1 || n2.SegmentRefs[0] != "seg-1" {
		t.Fatalf("n2 = %+v, want segment_refs [seg-1]", n2)
	}
	if got := g.Node("n1").Body; got != "alpha routing notes v2" {
		t.Fatalf("n1 body = %q, want updated", got)
	}
	if g.Node("n1").Level != 1 || n2.Level != 0 {
		t.Fatalf("levels = n1:%d n2:%d, want 1/0", g.Node("n1").Level, n2.Level)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate after consolidation: %v", err)
	}

	// Manifest counts refreshed.
	m, err := store.LoadManifest(1)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.NodeCount != 2 || m.HierEdgeCount != 1 {
		t.Fatalf("manifest counts = nodes %d hier %d, want 2/1", m.NodeCount, m.HierEdgeCount)
	}

	// The prompt carried the staging summary and the operations manifest.
	prompt := backend.allPrompts()
	if !strings.Contains(prompt, "seg-1") || !strings.Contains(prompt, "gamma delta segment summary") {
		t.Fatalf("prompt missing staging summary:\n%s", prompt)
	}
	if !strings.Contains(prompt, "add_node") || !strings.Contains(prompt, "submit") {
		t.Fatalf("prompt missing operations manifest:\n%s", prompt)
	}
}

func TestConsolidateNonTTTCycleRejectedBatchContinues(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "a", "node a")
	seedGraphNode(t, store, 1, "b", "node b")
	if err := store.SaveEdges(1, []*Edge{
		{EdgeID: "h0", Type: EdgeTypeSummarizes, From: "a", To: "b", CreatedBy: CreatorIngester, CreatedVersion: 1},
	}, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}

	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpAddHierarchyEdge, Edge: &Edge{EdgeID: "h-cycle", From: "b", To: "a"}}, // cycle
			ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n3", Body: "node n3"}},                  // valid
		)
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	c := NewConsolidator(store, backend, cfg, "test", nil)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if res.OpsApplied != 1 {
		t.Fatalf("OpsApplied = %d, want 1", res.OpsApplied)
	}
	if len(res.Rejected) != 1 || !strings.Contains(res.Rejected[0].Reason, "cycle") {
		t.Fatalf("Rejected = %+v, want one cycle rejection", res.Rejected)
	}

	entries, err := NewOpLogger(store).Read(1)
	if err != nil {
		t.Fatalf("read op log: %v", err)
	}
	if len(entries) != 1 || entries[0].Op != OpAddNode {
		t.Fatalf("op log = %+v, want one add_node entry", entries)
	}

	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	if g.Node("n3") == nil {
		t.Fatalf("n3 missing: batch did not continue after rejection")
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if g.Node("a").Level != 1 || g.Node("b").Level != 0 {
		t.Fatalf("levels changed by rejected edge: a=%d b=%d", g.Node("a").Level, g.Node("b").Level)
	}
}

// ---------------------------------------------------------------------------
// TTT consolidation
// ---------------------------------------------------------------------------

func TestConsolidateTTTSelectsMinCostWinner(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta routing")
	if err := store.WriteStagingSegment("seg-1", []byte("gamma delta")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	// One judged window query against v1; the adopted path found the answer
	// in 1 round (the A2 backtest baseline, design Q13).
	if err := store.AppendQueryLog("w1", &QueryLogEntry{
		TraceID:       "t1",
		Query:         "alpha routing",
		Timestamp:     time.Now().UTC(),
		Version:       1,
		Found:         true,
		Rounds:        1,
		JudgeDone:     true,
		JudgeScore:    0.9,
		RelevantNodes: []string{"n1"},
	}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}

	// Per-trajectory behavior keyed by the sampling instruction in the prompt:
	//   trajectory 0: adds a staging-referencing node (retrieval unchanged:
	//                 the query still hits n1, ground truth covered)
	//   trajectory 1: adds a node matching the query AND rewrites n1 so the
	//                 query no longer retrieves it; a relation edge keeps n1
	//                 inside the 1-hop hit neighborhood -> covered -> full
	//                 backtest -> 5 rounds (rounds-overflow regression,
	//                 recorded but not gate-fatal for a window query)
	//   trajectory 2: only a dangling relation edge (rejected by the applier)
	//                 -> candidate fails the staging-coverage gate
	backend := &fakeConsolidateBackend{respond: func(prompt string, _ int) string {
		switch {
		case strings.Contains(prompt, "trajectory 0 of 3"):
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-t0", Body: "gamma delta consolidated", SegmentRefs: []string{"seg-1"}}},
			)
		case strings.Contains(prompt, "trajectory 1 of 3"):
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-t1", Body: "alpha routing helper", SegmentRefs: []string{"seg-1"}}},
				ConsolidateOp{Op: OpUpdateNode, NodeID: "n1", Node: &Node{NodeID: "n1", Body: "zzz qqq"}},
				ConsolidateOp{Op: OpAddRelationEdge, Edge: &Edge{EdgeID: "e-t1", Type: EdgeTypeDerivedFrom, From: "n-t1", To: "n1"}},
			)
		default: // trajectory 2 of 3
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddRelationEdge, Edge: &Edge{EdgeID: "e-bad", Type: EdgeTypeCauses, From: "n1", To: "ghost"}},
			)
		}
	}}

	// The full backtest is cheap on the unchanged candidate (1 round) and
	// expensive on trajectory 1's retrieval-regressed candidate (5 rounds).
	// Candidates are v2 (traj 0), v3 (traj 1), v4 (traj 2).
	runner := &fakeFullBacktestRunner{roundsFor: func(version int) (int, bool) {
		if version == 3 {
			return 5, true
		}
		return 1, true
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 3
	c := NewConsolidator(store, backend, cfg, "test", nil)
	c.SetRunner(runner)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}

	// Candidates were created as v2 (traj 0), v3 (traj 1), v4 (traj 2).
	if len(res.Candidates) != 3 {
		t.Fatalf("Candidates = %d, want 3", len(res.Candidates))
	}
	byActor := map[string]CandidateStats{}
	for _, cs := range res.Candidates {
		byActor[cs.Actor] = cs
	}
	t0, t1, t2 := byActor["ttt-0"], byActor["ttt-1"], byActor["ttt-2"]
	if t0.OpsApplied != 1 || t1.OpsApplied != 3 || t2.OpsApplied != 0 {
		t.Fatalf("ops applied = %d/%d/%d, want 1/3/0", t0.OpsApplied, t1.OpsApplied, t2.OpsApplied)
	}
	if t2.Passed {
		t.Fatalf("ttt-2 candidate passed gates, want staging-coverage failure")
	}
	if len(t2.GateFailures) == 0 || !strings.Contains(strings.Join(t2.GateFailures, ";"), "seg-1") {
		t.Fatalf("ttt-2 gate failures = %v, want staging seg-1 failure", t2.GateFailures)
	}
	// The dangling edge never landed: the applier rejected it (safe applier).
	if len(res.Rejected) != 1 || res.Rejected[0].Op != OpAddRelationEdge {
		t.Fatalf("Rejected = %+v, want one add_relation_edge rejection", res.Rejected)
	}

	// Cost selection: trajectory 0 (mean rounds 1) beats trajectory 1 (5).
	if !t0.Passed || !t1.Passed {
		t.Fatalf("t0/t1 should pass gates: t0=%v %v, t1=%v %v", t0.Passed, t0.GateFailures, t1.Passed, t1.GateFailures)
	}
	if t0.MeanRounds != 1 || t1.MeanRounds != 5 {
		t.Fatalf("mean rounds = %v/%v, want 1/5", t0.MeanRounds, t1.MeanRounds)
	}
	// A2: trajectory 1's candidate kept the ground truth inside the 1-hop
	// hit neighborhood, so the full backtest ran; its 5 rounds overflow the
	// recorded baseline (1 round + tolerance), a recorded regression that is
	// not gate-fatal for a window query.
	if len(t1.Queries) != 1 || !t1.Queries[0].Regressed || !t1.Queries[0].FullBacktestRan {
		t.Fatalf("t1 query stat = %+v, want regressed full backtest", t1.Queries)
	}
	if len(t0.Queries) != 1 || t0.Queries[0].Regressed || !t0.Queries[0].Found {
		t.Fatalf("t0 query stat = %+v, want found without regression", t0.Queries)
	}
	if !res.Switched || res.WinnerVersion != t0.Version {
		t.Fatalf("WinnerVersion=%d Switched=%v, want v%d/true", res.WinnerVersion, res.Switched, t0.Version)
	}
	if current, err := store.CurrentVersion(); err != nil || current != t0.Version {
		t.Fatalf("CurrentVersion = %d, %v; want %d", current, err, t0.Version)
	}

	// Losers removed; winner + parent remain.
	versions, err := store.ListVersions()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if fmt.Sprint(versions) != fmt.Sprint([]int{1, t0.Version}) {
		t.Fatalf("versions = %v, want [1 %d]", versions, t0.Version)
	}

	// Isolation: the winner carries only its own trajectory's operations.
	g, err := LoadGraph(store, t0.Version)
	if err != nil {
		t.Fatalf("LoadGraph winner: %v", err)
	}
	if g.Node("n-t0") == nil {
		t.Fatalf("winner missing its own node n-t0")
	}
	if g.Node("n-t1") != nil {
		t.Fatalf("winner contains n-t1: trajectory isolation violated")
	}
	if got := g.Node("n1").Body; got != "alpha beta routing" {
		t.Fatalf("winner n1 body = %q, want untouched original", got)
	}

	// Full backtest ran once per covered candidate (all three: even the
	// staging-gate failure is fully evaluated for auditability).
	if got := runner.callCount(); got != 3 {
		t.Fatalf("full backtest calls = %d, want 3", got)
	}

	// Audit: winner's op log has the trajectory ops plus select_version.
	entries, err := NewOpLogger(store).Read(t0.Version)
	if err != nil {
		t.Fatalf("read winner op log: %v", err)
	}
	var hasSelect, hasAdd bool
	for _, e := range entries {
		if e.Op == OpSelectVersion && e.Actor == CreatorConsolidator {
			hasSelect = true
			if e.Detail["winner"] != float64(t0.Version) {
				t.Fatalf("select_version winner detail = %v, want %d", e.Detail["winner"], t0.Version)
			}
		}
		if e.Op == OpAddNode && e.Actor == "ttt-0" {
			hasAdd = true
		}
	}
	if !hasSelect || !hasAdd {
		t.Fatalf("winner op log missing entries (select=%v add=%v): %+v", hasSelect, hasAdd, entries)
	}

	// Every trajectory prompt carried the operations manifest, the staging
	// summary and its own sampling instruction.
	for i := 0; i < 3; i++ {
		if !strings.Contains(backend.allPrompts(), fmt.Sprintf("trajectory %d of 3: use temperature seed %d", i, i)) {
			t.Fatalf("prompts missing sampling instruction for trajectory %d", i)
		}
	}
	if !strings.Contains(backend.allPrompts(), "seg-1") {
		t.Fatalf("prompts missing staging summary")
	}
}
