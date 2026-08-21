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
// prompt and 1-based call index to the final response output. msgs, when
// set, are emitted on the session's message channel before the result.
type fakeConsolidateBackend struct {
	mu      sync.Mutex
	calls   int
	prompts []string
	respond func(prompt string, callIdx int) string
	msgs    []agent.Message
}

func (f *fakeConsolidateBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	f.mu.Lock()
	f.calls++
	idx := f.calls
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	return completedSessionWithMessages(f.respond(prompt, idx), f.msgs...), nil
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
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)

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
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)

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
	// Warm mode: the rounds-overflow regression assertion below needs the
	// statistical gates active despite the single-query window.
	cfg.Budget = BacktestBudget{B: 16, Epsilon: 0.2, ColdStartThreshold: 1}
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)
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

// TestConsolidateTTTBudgetUnionMeasurement covers the spec §5.2 wiring: with
// a runner wired and a window larger than B, each candidate independently
// picks its top-B most-changed queries and every candidate is measured on
// the union — queries no candidate picked are never evaluated.
func TestConsolidateTTTBudgetUnionMeasurement(t *testing.T) {
	store := newTestStore(t)
	// Three topically isolated nodes: each query only ever retrieves its own.
	seedGraphNode(t, store, 1, "n-alpha", "alpha routing rules for dispatch")
	seedGraphNode(t, store, 1, "n-beta", "beta scheduling queue behavior")
	seedGraphNode(t, store, 1, "n-gamma", "gamma retention policy notes")
	if err := store.WriteStagingSegment("seg-1", []byte("delta epsilon")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	for i, tc := range []struct{ trace, query, node string }{
		{"t-alpha", "alpha routing rules", "n-alpha"},
		{"t-beta", "beta scheduling queue", "n-beta"},
		{"t-gamma", "gamma retention policy", "n-gamma"},
	} {
		if err := store.AppendQueryLog("w1", &QueryLogEntry{
			TraceID:       tc.trace,
			Query:         tc.query,
			Timestamp:     time.Now().UTC().Add(time.Duration(i) * time.Second),
			Version:       1,
			Found:         true,
			Rounds:        1,
			JudgeDone:     true,
			JudgeScore:    0.9,
			RelevantNodes: []string{tc.node},
		}); err != nil {
			t.Fatalf("AppendQueryLog %s: %v", tc.trace, err)
		}
	}

	// Trajectory 0 changes only the alpha subgraph, trajectory 1 only beta;
	// both reference the staging segment so the coverage gate passes.
	backend := &fakeConsolidateBackend{respond: func(prompt string, _ int) string {
		switch {
		case strings.Contains(prompt, "trajectory 0 of 2"):
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-a2", Body: "alpha routing rules deepened", SegmentRefs: []string{"seg-1"}}},
				ConsolidateOp{Op: OpAddHierarchyEdge, Edge: &Edge{EdgeID: "h-a", From: "n-a2", To: "n-alpha"}},
			)
		default: // trajectory 1 of 2
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-b2", Body: "beta scheduling queue deepened", SegmentRefs: []string{"seg-1"}}},
				ConsolidateOp{Op: OpAddHierarchyEdge, Edge: &Edge{EdgeID: "h-b", From: "n-b2", To: "n-beta"}},
			)
		}
	}}
	runner := &fakeFullBacktestRunner{rounds: 1, found: true}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 2
	cfg.Budget = BacktestBudget{B: 1, Epsilon: 0.2, ColdStartThreshold: 20}
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)
	c.SetRunner(runner)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("Candidates = %d, want 2", len(res.Candidates))
	}

	// Union = {alpha, beta}: each candidate's top-1 is the query its own
	// trajectory changed. Both candidates are measured on exactly the union;
	// gamma never gets measured and lands in every candidate's stats as an
	// honest skip (Skipped, Rounds=0, D_q and reason recorded).
	byActor := map[string]map[string]QueryBacktestStat{}
	for _, cs := range res.Candidates {
		byQuery := map[string]QueryBacktestStat{}
		for _, qs := range cs.Queries {
			byQuery[qs.Query] = qs
		}
		byActor[cs.Actor] = byQuery
		if len(byQuery) != 3 {
			t.Fatalf("candidate v%d stats = %+v, want all 3 window queries (2 measured + 1 skipped)", cs.Version, byQuery)
		}
		for _, measured := range []string{"alpha routing rules", "beta scheduling queue"} {
			qs := byQuery[measured]
			if qs.Skipped {
				t.Fatalf("candidate v%d skipped %q, want it measured (in the union)", cs.Version, measured)
			}
			if !qs.FullBacktestRan {
				t.Fatalf("candidate v%d did not run the full backtest on %q: %+v", cs.Version, measured, qs)
			}
		}
		sk := byQuery["gamma retention policy"]
		if !sk.Skipped {
			t.Fatalf("candidate v%d measured gamma, want it skipped (outside every top-B)", cs.Version)
		}
		if sk.Rounds != 0 || sk.Found || sk.FullBacktestRan {
			t.Fatalf("skipped stat = %+v, want Rounds=0 / not found / no explore", sk)
		}
		if sk.SkipReason == "" || sk.Dq != 0 {
			t.Fatalf("skipped stat = %+v, want a reason and D_q=0 (gamma unchanged by either trajectory)", sk)
		}
		if !sk.BaselineFound || sk.BaselineRounds != 1 {
			t.Fatalf("skipped stat = %+v, want baseline facts preserved", sk)
		}
	}
	// Measured entries carry their D_q too: the subgraph each trajectory
	// changed scores > 0 on its own candidate.
	if byActor["ttt-0"]["alpha routing rules"].Dq <= 0 {
		t.Fatalf("ttt-0 alpha Dq = %v, want > 0 (its trajectory changed the alpha subgraph)", byActor["ttt-0"]["alpha routing rules"].Dq)
	}
	if byActor["ttt-1"]["beta scheduling queue"].Dq <= 0 {
		t.Fatalf("ttt-1 beta Dq = %v, want > 0 (its trajectory changed the beta subgraph)", byActor["ttt-1"]["beta scheduling queue"].Dq)
	}

	// Every union query ran a full explore on every candidate (both keep the
	// ground truth within the 1-hop hit neighborhood): 2 candidates x 2 queries.
	if got := runner.callCount(); got != 4 {
		t.Fatalf("full backtest calls = %d, want 4 (2 candidates x union of 2)", got)
	}

	// Recall aggregates only the measured union: skipped gamma never dilutes
	// the rates.
	for _, cs := range res.Candidates {
		if cs.Recall != 1.0 || cs.BaselineRecall != 1.0 {
			t.Fatalf("candidate v%d recall = %v baseline = %v, want 1/1 over the measured union", cs.Version, cs.Recall, cs.BaselineRecall)
		}
	}
}

// TestConsolidateTTTColdStartSkipsRecallGate covers spec §7: with fewer
// window query-log entries than ColdStartThreshold the statistical gates
// degrade — a recall drop that would reject every candidate in warm mode is
// tolerated, the switch happens, and the audit entry records cold_start.
func TestConsolidateTTTColdStartSkipsRecallGate(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n-target", "alpha beta routing notes")
	seedGraphNode(t, store, 1, "n-gone", "omega cache eviction policy")

	appendJudged := func(traceID, query string, nodes []string) {
		t.Helper()
		if err := store.AppendQueryLog("w1", &QueryLogEntry{
			TraceID:       traceID,
			Query:         query,
			Timestamp:     time.Now().UTC(),
			Version:       1,
			Found:         true,
			Rounds:        1,
			JudgeDone:     true,
			JudgeScore:    0.9,
			RelevantNodes: nodes,
		}); err != nil {
			t.Fatalf("AppendQueryLog %s: %v", traceID, err)
		}
	}
	// Two queries < ColdStartThreshold (20): cold start by construction.
	appendJudged("t-ok", "alpha routing", []string{"n-target"})
	appendJudged("t-bad", "omega cache eviction", []string{"n-gone"})

	// Both trajectories delete n-gone: the omega query's ground truth leaves
	// the graph, so recall drops 1.0 -> 0.5 on every candidate — a hard
	// gate-3 failure in warm mode (tolerance 0.02).
	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpDeleteNode, NodeID: "n-gone"},
			ConsolidateOp{Op: OpSubmit},
		)
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 2
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if !res.Switched {
		t.Fatalf("Switched = false, want cold start to tolerate the recall drop (candidates: %+v)", res.Candidates)
	}
	for _, cs := range res.Candidates {
		if !cs.Passed {
			t.Fatalf("candidate v%d failed gates under cold start: %v", cs.Version, cs.GateFailures)
		}
		if cs.Recall != 0.5 {
			t.Fatalf("candidate v%d recall = %v, want the honest 0.5 (reported, not gated)", cs.Version, cs.Recall)
		}
	}

	// Audit: the select_version entry records the cold-start degradation.
	entries, err := NewOpLogger(store).Read(res.WinnerVersion)
	if err != nil {
		t.Fatalf("read winner op log: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Op == OpSelectVersion {
			found = true
			if e.Detail["cold_start"] != true {
				t.Fatalf("select_version cold_start = %v, want true", e.Detail["cold_start"])
			}
		}
	}
	if !found {
		t.Fatalf("winner op log has no select_version entry")
	}
}

// TestConsolidateTTTWindowCountEnablesRecallGate verifies that cold-start
// eligibility counts every window log entry, while measurement remains
// limited to the two judged entries.
func TestConsolidateTTTWindowCountEnablesRecallGate(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n-target", "alpha beta routing notes")
	seedGraphNode(t, store, 1, "n-gone", "omega cache eviction policy")
	appendEntry := func(traceID, query string, judged bool, nodes []string) {
		t.Helper()
		entry := &QueryLogEntry{
			TraceID: traceID, Query: query, Timestamp: time.Now().UTC(), Version: 1,
			Found: true, Rounds: 1, JudgeDone: judged, RelevantNodes: nodes,
		}
		if judged {
			entry.JudgeScore = 0.9
		}
		if err := store.AppendQueryLog("w1", entry); err != nil {
			t.Fatalf("AppendQueryLog %s: %v", traceID, err)
		}
	}
	appendEntry("t-ok", "alpha routing", true, []string{"n-target"})
	appendEntry("t-bad", "omega cache eviction", true, []string{"n-gone"})
	for i := 0; i < 18; i++ {
		appendEntry(fmt.Sprintf("t-unjudged-%d", i), "pending query", false, nil)
	}

	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpDeleteNode, NodeID: "n-gone"},
			ConsolidateOp{Op: OpSubmit},
		)
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 2
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if res.Switched {
		t.Fatalf("Switched = true, want recall gate to reject the warm window")
	}
	for _, cs := range res.Candidates {
		if cs.Passed || cs.Recall != 0.5 {
			t.Fatalf("candidate v%d = %+v, want recall-gate failure at 0.5", cs.Version, cs)
		}
	}
	entries, err := NewOpLogger(store).Read(res.WinnerVersion)
	if err != nil {
		t.Fatalf("read winner op log: %v", err)
	}
	for _, e := range entries {
		if e.Op == OpSelectVersion {
			if e.Detail["cold_start"] != false || e.Detail["window_queries"] != float64(20) || e.Detail["judged_queries"] != float64(2) {
				t.Fatalf("select_version detail = %v, want warm 20-window/2-judged counts", e.Detail)
			}
			return
		}
	}
	t.Fatalf("winner op log has no select_version entry")
}
