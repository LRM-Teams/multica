package memorygraph

// Tests for the backtest budget allocation (spec §5): D_q change-degree
// scoring, per-candidate independent top-B selection, and the union
// measurement set every candidate is evaluated on. Reuses the BM25-only
// retriever fixtures from explore_test.go.

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// changeFixture builds two independently addressable subgraphs in version 1
// (the baseline): "alpha ..." bodies and "beta ..." bodies.
func changeFixtureStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	now := time.Now().UTC()
	nodes := []*Node{
		{NodeID: "alpha-1", Level: 0, Body: "alpha dispatch router retries failed batch jobs"},
		{NodeID: "alpha-2", Level: 1, Body: "alpha dispatch summary with backoff details"},
		{NodeID: "beta-1", Level: 0, Body: "beta vector cache eviction policy for embeddings"},
		{NodeID: "beta-2", Level: 1, Body: "beta vector quota accounting summary"},
	}
	for _, n := range nodes {
		n.CreatedBy = CreatorIngester
		n.CreatedVersion = 1
		n.UpdatedVersion = 1
		n.ObservedAt = now
		if err := store.SaveNode(1, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	hier := []*Edge{
		{EdgeID: "h-alpha", Type: EdgeTypeSummarizes, From: "alpha-2", To: "alpha-1", CreatedBy: CreatorIngester, CreatedVersion: 1},
		{EdgeID: "h-beta", Type: EdgeTypeSummarizes, From: "beta-2", To: "beta-1", CreatedBy: CreatorIngester, CreatedVersion: 1},
	}
	if err := store.SaveEdges(1, hier, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	return store
}

// candidateVersion copies version 1 and applies mutate to the copy.
func candidateVersion(t *testing.T, store *Store, mutate func()) int {
	t.Helper()
	v, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	if mutate != nil {
		mutate()
	}
	return v
}

func changeSnapshotFor(t *testing.T, store *Store, version int) changeSnapshot {
	t.Helper()
	retr := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := retr.RebuildForVersion(context.Background(), version); err != nil {
		t.Fatalf("RebuildForVersion v%d: %v", version, err)
	}
	g, err := LoadGraph(store, version)
	if err != nil {
		t.Fatalf("LoadGraph v%d: %v", version, err)
	}
	return changeSnapshot{retr: retr, g: g}
}

func TestDefaultBacktestBudget(t *testing.T) {
	b := DefaultBacktestBudget()
	if b.B != 16 || b.Epsilon != 0.2 || b.ColdStartThreshold != 20 {
		t.Fatalf("DefaultBacktestBudget = %+v, want {16 0.2 20}", b)
	}
}

// An untouched candidate has D_q = 0 for every query.
func TestQueryChangeIdenticalGraphsIsZero(t *testing.T) {
	store := changeFixtureStore(t)
	base := changeSnapshotFor(t, store, 1)
	cand := changeSnapshotFor(t, store, 1)

	dq, err := queryChange(context.Background(), base, cand, "alpha dispatch retries", 6, 5, 0.2)
	if err != nil {
		t.Fatalf("queryChange: %v", err)
	}
	if dq != 0 {
		t.Fatalf("D_q = %.4f, want 0 for identical graphs", dq)
	}
}

// Changing the alpha subgraph moves the alpha query's D_q above the beta
// query's: D_q is query-local, not a global graph diff.
func TestQueryChangeRanksChangedSubgraphHigher(t *testing.T) {
	store := changeFixtureStore(t)
	candV := candidateVersion(t, store, func() {
		n := &Node{
			NodeID: "alpha-1", Level: 0,
			Body:             "alpha dispatch router now retries with jitter and capped backoff",
			CreatedBy:        CreatorIngester,
			CreatedVersion:   1,
			UpdatedVersion:   2,
			ObservedAt:       time.Now().UTC(),
			SegmentRefs:      []string{},
			Tags:             []string{},
			EntityRefs:       []string{},
			SourceAgentIDs:   []string{},
			SourceChannelIDs: []string{},
			SourceTaskIDs:    []string{},
		}
		if err := store.SaveNode(2, n); err != nil {
			t.Fatalf("SaveNode cand: %v", err)
		}
	})
	base := changeSnapshotFor(t, store, 1)
	cand := changeSnapshotFor(t, store, candV)

	alphaDQ, err := queryChange(context.Background(), base, cand, "alpha dispatch retries", 6, 5, 0.2)
	if err != nil {
		t.Fatalf("queryChange alpha: %v", err)
	}
	betaDQ, err := queryChange(context.Background(), base, cand, "beta vector cache eviction", 6, 5, 0.2)
	if err != nil {
		t.Fatalf("queryChange beta: %v", err)
	}
	if alphaDQ <= 0 {
		t.Fatalf("alpha D_q = %.4f, want > 0 (alpha subgraph changed)", alphaDQ)
	}
	if betaDQ != 0 {
		t.Fatalf("beta D_q = %.4f, want 0 (beta subgraph untouched)", betaDQ)
	}
}

// Each candidate independently sorts queries by D_q and takes its own top-B;
// with opposing edits and B=1 the picks disagree and the union covers both.
func TestPlanBudgetPerCandidateTopBAndUnion(t *testing.T) {
	store := changeFixtureStore(t)
	// Candidate A rewrites the alpha area; candidate B rewrites the beta area.
	candA := candidateVersion(t, store, func() {
		mustSaveBody(t, store, 2, "alpha-1", "alpha dispatch router rewrite with new retry policy")
	})
	candB := candidateVersion(t, store, func() {
		mustSaveBody(t, store, 3, "beta-1", "beta vector cache rewrite with new eviction policy")
	})

	queries := []*BacktestQuery{
		{Query: "alpha dispatch retries", RelevantNodes: []string{"alpha-1"}, BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 1},
		{Query: "beta vector cache eviction", RelevantNodes: []string{"beta-1"}, BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 1},
	}

	plan, err := PlanBudget(context.Background(), store, 1, []int{candA, candB}, queries, BacktestBudget{B: 1, Epsilon: 0.2}, DefaultRetrievalConfig(), nil, 6, 5)
	if err != nil {
		t.Fatalf("PlanBudget: %v", err)
	}

	// Union = both queries (each candidate's top-1 is a different query).
	if len(plan.Union) != 2 {
		t.Fatalf("union = %v, want both queries", plan.Union)
	}
	if len(plan.PerCandidate) != 2 {
		t.Fatalf("len(PerCandidate) = %d, want 2", len(plan.PerCandidate))
	}

	pickA := plan.PerCandidate[candA].Selected
	pickB := plan.PerCandidate[candB].Selected
	if len(pickA) != 1 || pickA[0].Query != "alpha dispatch retries" {
		t.Fatalf("candidate A top-1 = %v, want the alpha query (its edit is alpha-local)", pickA)
	}
	if len(pickB) != 1 || pickB[0].Query != "beta vector cache eviction" {
		t.Fatalf("candidate B top-1 = %v, want the beta query (its edit is beta-local)", pickB)
	}
	// D_q is recorded for every query of every candidate.
	if len(plan.PerCandidate[candA].Dq) != 2 || len(plan.PerCandidate[candB].Dq) != 2 {
		t.Fatalf("Dq maps = %d/%d entries, want 2 each", len(plan.PerCandidate[candA].Dq), len(plan.PerCandidate[candB].Dq))
	}
	if plan.PerCandidate[candA].Dq[budgetQueryIdentities(queries)[queries[0]]] <= plan.PerCandidate[candA].Dq[budgetQueryIdentities(queries)[queries[1]]] {
		t.Fatalf("candidate A D_q order = %v, want alpha > beta", plan.PerCandidate[candA].Dq)
	}
	if plan.PerCandidate[candB].Dq[budgetQueryIdentities(queries)[queries[1]]] <= plan.PerCandidate[candB].Dq[budgetQueryIdentities(queries)[queries[0]]] {
		t.Fatalf("candidate B D_q order = %v, want beta > alpha", plan.PerCandidate[candB].Dq)
	}
}

// Window query count below B degrades to the full set (spec §5.2): nothing is
// skipped and the union is the whole window.
func TestPlanBudgetBelowBDegradesToFull(t *testing.T) {
	store := changeFixtureStore(t)
	candA := candidateVersion(t, store, func() {
		mustSaveBody(t, store, 2, "alpha-1", "alpha dispatch router rewrite with new retry policy")
	})
	queries := []*BacktestQuery{
		{Query: "alpha dispatch retries", RelevantNodes: []string{"alpha-1"}, BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 1},
		{Query: "beta vector cache eviction", RelevantNodes: []string{"beta-1"}, BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 1},
	}

	plan, err := PlanBudget(context.Background(), store, 1, []int{candA}, queries, BacktestBudget{B: 5, Epsilon: 0.2}, DefaultRetrievalConfig(), nil, 6, 5)
	if err != nil {
		t.Fatalf("PlanBudget: %v", err)
	}
	if len(plan.Union) != 2 {
		t.Fatalf("union = %v, want full window (2 queries < B=5)", plan.Union)
	}
	if got := plan.PerCandidate[candA].Selected; len(got) != 2 {
		t.Fatalf("selected = %v, want both queries (no skip below B)", got)
	}
}

// Equal D_q ties break by query text lexicographically (spec §5.1: 可复现).
func TestPlanBudgetTieBreaksByQueryText(t *testing.T) {
	store := changeFixtureStore(t)
	// Identical candidate: every query has D_q = 0, so selection is pure tie.
	candA := candidateVersion(t, store, nil)
	queries := []*BacktestQuery{
		{Query: "beta vector cache eviction", RelevantNodes: []string{"beta-1"}, BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 1},
		{Query: "alpha dispatch retries", RelevantNodes: []string{"alpha-1"}, BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 1},
	}

	plan, err := PlanBudget(context.Background(), store, 1, []int{candA}, queries, BacktestBudget{B: 1, Epsilon: 0.2}, DefaultRetrievalConfig(), nil, 6, 5)
	if err != nil {
		t.Fatalf("PlanBudget: %v", err)
	}
	got := plan.PerCandidate[candA].Selected
	if len(got) != 1 || got[0].Query != "alpha dispatch retries" {
		t.Fatalf("tie pick = %v, want the lexicographically first query (alpha...)", got)
	}
}

// mustSaveBody rewrites one node body in version v, preserving the fixture's
// other fields.
func mustSaveBody(t *testing.T, store *Store, v int, nodeID, body string) {
	t.Helper()
	nodes, err := store.LoadNodes(1)
	if err != nil {
		t.Fatalf("LoadNodes: %v", err)
	}
	for _, n := range nodes {
		if n.NodeID != nodeID {
			n.UpdatedVersion = v
			if err := store.SaveNode(v, n); err != nil {
				t.Fatalf("SaveNode %s: %v", n.NodeID, err)
			}
			continue
		}
		n.Body = body
		n.UpdatedVersion = v
		if err := store.SaveNode(v, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	hier, rel, err := store.LoadEdges(1)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	if err := store.SaveEdges(v, hier, rel); err != nil {
		t.Fatalf("SaveEdges v%d: %v", v, err)
	}
}

// TestQueryChangeIncludesEmbeddingCandidates verifies that L3 observes the
// embedding-only neighbors exposed by /explore even when graph structure and
// lexical retrieval are unchanged. The fixture holds more nodes than
// MaxExpandPerRound: vectorNeighbors returns every other node up to the cap,
// so with only three nodes both snapshots would serve the full node set and
// the truncation would never shift the neighbor sets.
func TestQueryChangeIncludesEmbeddingCandidates(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	nodes := []*Node{{NodeID: "seed", Body: "seed query"}}
	for i := 1; i <= 6; i++ {
		nodes = append(nodes, &Node{
			NodeID: fmt.Sprintf("a%d", i),
			Body:   fmt.Sprintf("neighbor number %d", i),
		})
	}
	for _, n := range nodes {
		if err := store.SaveNode(1, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	candidate := candidateVersion(t, store, nil)
	base := vectorChangeSnapshot(t, store, 1, map[string][]float32{
		"seed": {1, 0},
		"a1": {1, 0}, "a2": {1, 0}, "a3": {1, 0}, "a4": {1, 0}, "a5": {1, 0},
		"a6": {0, 1},
	})
	cand := vectorChangeSnapshot(t, store, candidate, map[string][]float32{
		"seed": {1, 0},
		"a1": {0, 1},
		"a2": {1, 0}, "a3": {1, 0}, "a4": {1, 0}, "a5": {1, 0}, "a6": {1, 0},
	})

	dq, err := queryChange(context.Background(), base, cand, "seed query", 1, 5, 0.2)
	if err != nil {
		t.Fatalf("queryChange: %v", err)
	}
	if dq <= 0 {
		t.Fatalf("embedding-only D_q = %v, want > 0", dq)
	}
}

// TestQueryChangeCapsExpandCandidates verifies that L3 compares only the
// candidates /explore can return after MaxExpandPerRound truncation.
func TestQueryChangeCapsExpandCandidates(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, n := range []*Node{
		{NodeID: "seed", Body: "seed query"},
		{NodeID: "first", Body: "first neighbor"},
		{NodeID: "outside", Body: "outside candidate"},
	} {
		if err := store.SaveNode(1, n); err != nil {
			t.Fatalf("SaveNode %s: %v", n.NodeID, err)
		}
	}
	candidate := candidateVersion(t, store, nil)
	base := vectorChangeSnapshot(t, store, 1, map[string][]float32{
		"seed": {1, 0}, "first": {0.9, 0.1}, "outside": {0.8, 0.2},
	})
	cand := vectorChangeSnapshot(t, store, candidate, map[string][]float32{
		"seed": {1, 0}, "first": {0.9, 0.1}, "outside": {0, 1},
	})

	dq, err := queryChange(context.Background(), base, cand, "seed query", 1, 1, 0.2)
	if err != nil {
		t.Fatalf("queryChange: %v", err)
	}
	if dq != 0 {
		t.Fatalf("D_q beyond candidate cap = %v, want 0", dq)
	}
}

func vectorChangeSnapshot(t *testing.T, store *Store, version int, vecs map[string][]float32) changeSnapshot {
	t.Helper()
	retr := NewHybridRetriever(store, NewCachedEmbedder(vectorTestEmbeddingProvider{}, store), RetrievalConfig{TopK: 1, BM25Weight: 1})
	if err := retr.RebuildForVersion(context.Background(), version); err != nil {
		t.Fatalf("RebuildForVersion v%d: %v", version, err)
	}
	retr.mu.Lock()
	retr.vecs = vecs
	retr.mu.Unlock()
	g, err := LoadGraph(store, version)
	if err != nil {
		t.Fatalf("LoadGraph v%d: %v", version, err)
	}
	return changeSnapshot{retr: retr, g: g}
}

type vectorTestEmbeddingProvider struct{}

func (vectorTestEmbeddingProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range texts {
		vecs[i] = []float32{1, 0}
	}
	return vecs, nil
}

func (vectorTestEmbeddingProvider) Dim() int { return 2 }

// TestPlanBudgetUsesTraceIDIdentity ensures equal query text cannot merge two
// query-log entries in a top-B union or its skipped audit records.
func TestPlanBudgetUsesTraceIDIdentity(t *testing.T) {
	store := changeFixtureStore(t)
	candidate := candidateVersion(t, store, nil)
	queries := []*BacktestQuery{
		{TraceID: "trace-a", Query: "alpha dispatch retries", RelevantNodes: []string{"alpha-1"}},
		{TraceID: "trace-b", Query: "alpha dispatch retries", RelevantNodes: []string{"alpha-1"}},
	}
	plan, err := PlanBudget(context.Background(), store, 1, []int{candidate}, queries, BacktestBudget{B: 1, Epsilon: 0.2}, DefaultRetrievalConfig(), nil, 6, 5)
	if err != nil {
		t.Fatalf("PlanBudget: %v", err)
	}
	if len(plan.Union) != 1 || plan.Union[0].TraceID != "trace-a" {
		t.Fatalf("union = %+v, want only trace-a", plan.Union)
	}
	stats := appendBudgetAudit([]QueryBacktestStat{{TraceID: "trace-a", Query: queries[0].Query}}, plan.PerCandidate[candidate], plan.Union, queries)
	if len(stats) != 2 || !stats[1].Skipped || stats[1].TraceID != "trace-b" {
		t.Fatalf("budget audit = %+v, want skipped trace-b", stats)
	}
}
