package memorygraph

import (
	"context"
	"fmt"
	"sort"
)

// BacktestBudget caps Explore backtest effort per consolidation (spec §5) and
// carries the cold-start gate threshold (spec §7).
type BacktestBudget struct {
	// B is the per-candidate top-B cap: each candidate independently measures
	// its B most-changed queries. The effective budget degrades to the full
	// window when the window holds <= B queries (spec §5.2).
	B int
	// Epsilon weights L3 (expand-candidate change) in D_q. Small because L3
	// is indirect and the most expensive layer (spec §5.1).
	Epsilon float64
	// ColdStartThreshold is the query-log entry count below which the recall
	// and rounds gates are skipped (spec §7).
	ColdStartThreshold int
}

// DefaultBacktestBudget returns the 2026-08-21 finalized defaults (spec §9):
// B = min(window, 16), ε = 0.2, cold-start threshold = 20.
func DefaultBacktestBudget() BacktestBudget {
	return BacktestBudget{B: 16, Epsilon: 0.2, ColdStartThreshold: 20}
}

// normalized fills non-positive fields with the defaults.
func (b BacktestBudget) normalized() BacktestBudget {
	d := DefaultBacktestBudget()
	if b.B <= 0 {
		b.B = d.B
	}
	if b.Epsilon <= 0 {
		b.Epsilon = d.Epsilon
	}
	if b.ColdStartThreshold <= 0 {
		b.ColdStartThreshold = d.ColdStartThreshold
	}
	return b
}

// changeSnapshot bundles one graph version's retrieval and graph state for
// D_q scoring.
type changeSnapshot struct {
	retr *HybridRetriever
	g    *Graph
}

// buildChangeSnapshot loads one version's graph and builds its production
// retrieval pipeline (same two-channel merge as the backtest retriever).
func buildChangeSnapshot(ctx context.Context, store *Store, version int, retrieval RetrievalConfig, emb *CachedEmbedder) (changeSnapshot, error) {
	g, err := LoadGraph(store, version)
	if err != nil {
		return changeSnapshot{}, fmt.Errorf("change snapshot v%d: load graph: %w", version, err)
	}
	retr := NewHybridRetriever(store, emb, retrieval)
	if err := retr.RebuildForVersion(ctx, version); err != nil {
		return changeSnapshot{}, fmt.Errorf("change snapshot v%d: build retriever: %w", version, err)
	}
	return changeSnapshot{retr: retr, g: g}, nil
}

// queryChange scores D_q = L1 + L2 + ε·L3 for one query between the baseline
// and candidate snapshots (spec §5.1):
//
//	L1 — Jaccard distance of the retrieval-hit node-id sets (seed change),
//	L2 — body-hash diff + edge churn within the closureRounds closure around
//	     the hits (structural change),
//	L3 — Jaccard distance of the neighbor sets offered for expansion.
//
// Identical snapshots score exactly 0.
func queryChange(ctx context.Context, base, cand changeSnapshot, query string, closureRounds, maxExpandPerRound int, epsilon float64) (float64, error) {
	baseHits, err := graphHitIDs(ctx, base.retr, query)
	if err != nil {
		return 0, fmt.Errorf("baseline retrieval: %w", err)
	}
	candHits, err := graphHitIDs(ctx, cand.retr, query)
	if err != nil {
		return 0, fmt.Errorf("candidate retrieval: %w", err)
	}

	l1 := jaccardDistance(baseHits, candHits)
	l2 := closureChange(base.g, cand.g, unionKeys(baseHits, candHits), closureRounds)
	l3 := jaccardDistance(
		expandNeighborSet(base.g, base.retr, baseHits, maxExpandPerRound),
		expandNeighborSet(cand.g, cand.retr, candHits, maxExpandPerRound),
	)
	return l1 + l2 + epsilon*l3, nil
}

// graphHitIDs runs one retrieval and returns the non-staging hit id set.
func graphHitIDs(ctx context.Context, retr *HybridRetriever, query string) (map[string]bool, error) {
	hits, err := retr.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(hits))
	for _, h := range hits {
		if !IsStagingID(h.ID) {
			out[h.ID] = true
		}
	}
	return out, nil
}

// closureChange measures the structural change between two graphs inside the
// closureRounds neighborhood of seeds (spec §5.1 L2): the fraction of closure
// nodes whose body hash differs (added/deleted nodes count as changed) and
// the edge churn within the closure, averaged into [0,1].
func closureChange(baseG, candG *Graph, seeds map[string]bool, closureRounds int) float64 {
	if len(seeds) == 0 {
		return 0
	}
	baseClosure := baseG.KHopNeighborhood(keys(seeds), closureRounds)
	candClosure := candG.KHopNeighborhood(keys(seeds), closureRounds)
	all := unionKeys(baseClosure, candClosure)
	if len(all) == 0 {
		return 0
	}

	changed := 0
	for id := range all {
		baseHash, candHash := "", ""
		if baseClosure[id] {
			if n := baseG.Node(id); n != nil {
				baseHash = ComputeContentHash(n.Body)
			}
		}
		if candClosure[id] {
			if n := candG.Node(id); n != nil {
				candHash = ComputeContentHash(n.Body)
			}
		}
		if baseHash != candHash {
			changed++
		}
	}
	bodyDiff := float64(changed) / float64(len(all))

	baseEdges := closureEdgeIDs(baseG, baseClosure)
	candEdges := closureEdgeIDs(candG, candClosure)
	edgeChurn := symmetricDiffRatio(baseEdges, candEdges)
	return (bodyDiff + edgeChurn) / 2
}

// closureEdgeIDs returns the ids of edges whose endpoints both lie in the
// closure. Edge-ref targets (edge:<id>) are compared as-is.
func closureEdgeIDs(g *Graph, closure map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for _, e := range g.HierarchyEdges() {
		if closure[e.From] && closure[e.To] {
			out[e.EdgeID] = true
		}
	}
	for _, e := range g.RelationEdges() {
		if closure[e.From] && closure[e.To] {
			out[e.EdgeID] = true
		}
	}
	return out
}

// expandNeighborSet collects the capped neighbor ids /explore would offer for
// every seed. It deliberately reuses expandCandidateRefs so L3 observes the
// same priority, embedding channel and MaxExpandPerRound truncation as the
// protocol surface.
func expandNeighborSet(g *Graph, retr *HybridRetriever, seeds map[string]bool, maxExpandPerRound int) map[string]bool {
	out := make(map[string]bool)
	var allow func(*Node) bool
	if retr != nil && retr.viewActive() {
		allow = retr.cfg.View.Allows
	}
	for id := range seeds {
		node := g.Node(id)
		if node == nil {
			continue
		}
		for _, candidate := range expandCandidateRefs(g, node, retr, maxExpandPerRound, "", allow) {
			out[candidate.NodeID] = true
		}
	}
	return out
}

// jaccardDistance returns 1 - |a∩b|/|a∪b|; two empty sets are identical (0).
func jaccardDistance(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for id := range a {
		if b[id] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return 1 - float64(inter)/float64(union)
}

// symmetricDiffRatio returns |a△b|/|a∪b|; two empty sets yield 0.
func symmetricDiffRatio(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for id := range a {
		if b[id] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(union-inter) / float64(union)
}

// unionKeys returns the union of the key sets.
func unionKeys(sets ...map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for _, s := range sets {
		for k := range s {
			out[k] = true
		}
	}
	return out
}

// keys returns the key set of m (identity for a single set; kept for
// readability at call sites).
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// candidateBudget is one candidate's D_q ranking and top-B selection.
type candidateBudget struct {
	// Dq maps budget identity -> D_q for every window query. TraceID is the
	// identity when present; an empty TraceID uses the stable window ordinal.
	Dq map[string]float64
	// Selected is the candidate's top-B (or the full window below B),
	// ordered by D_q descending with query-text tie-break.
	Selected []*BacktestQuery
}

// BudgetPlan is the per-consolidation budget allocation (spec §5.2): each
// candidate's independent D_q ranking and the union measurement set all
// candidates are evaluated on.
type BudgetPlan struct {
	// Union is the measurement set: the union of all candidates' top-B
	// selections (or the full window when it holds <= B queries). Ordered by
	// max cross-candidate D_q descending, query text ascending.
	Union []*BacktestQuery
	// PerCandidate maps candidate version -> its ranking and selection.
	PerCandidate map[int]candidateBudget
	// Full reports that the window degraded to full measurement (no skip).
	Full bool
}

// PlanBudget computes the per-candidate D_q rankings and the union
// measurement set (spec §5.2). baselineVersion is the version the queries
// were recorded against; every candidate is scored against it independently.
// closureRounds is the ExploreConfig.MaxRounds closure radius for L2.
func PlanBudget(ctx context.Context, store *Store, baselineVersion int, candidateVersions []int, queries []*BacktestQuery, budget BacktestBudget, retrieval RetrievalConfig, emb *CachedEmbedder, closureRounds, maxExpandPerRound int) (*BudgetPlan, error) {
	budget = budget.normalized()
	base, err := buildChangeSnapshot(ctx, store, baselineVersion, retrieval, emb)
	if err != nil {
		return nil, err
	}

	plan := &BudgetPlan{PerCandidate: make(map[int]candidateBudget, len(candidateVersions))}
	if len(queries) == 0 {
		return plan, nil
	}
	// Window <= B: top-B is the full window for every candidate (spec §5.2).
	plan.Full = len(queries) <= budget.B

	identities := budgetQueryIdentities(queries)
	inUnion := make(map[string]bool)
	for _, v := range candidateVersions {
		cand, err := buildChangeSnapshot(ctx, store, v, retrieval, emb)
		if err != nil {
			return nil, err
		}
		cb := candidateBudget{Dq: make(map[string]float64, len(queries))}
		for _, q := range queries {
			dq, err := queryChange(ctx, base, cand, q.Query, closureRounds, maxExpandPerRound, budget.Epsilon)
			if err != nil {
				return nil, fmt.Errorf("candidate v%d query %q: %w", v, q.Query, err)
			}
			cb.Dq[identities[q]] = dq
		}
		cb.Selected = selectTopB(queries, cb.Dq, identities, budget.B)
		plan.PerCandidate[v] = cb
		for _, q := range cb.Selected {
			inUnion[identities[q]] = true
		}
	}

	// Union ordered by max cross-candidate D_q descending, query text ascending.
	maxDq := make(map[string]float64, len(queries))
	for _, q := range queries {
		id := identities[q]
		for _, cb := range plan.PerCandidate {
			if cb.Dq[id] > maxDq[id] {
				maxDq[id] = cb.Dq[id]
			}
		}
	}
	for _, q := range queries {
		if inUnion[identities[q]] {
			plan.Union = append(plan.Union, q)
		}
	}
	sort.SliceStable(plan.Union, func(i, j int) bool {
		di, dj := maxDq[identities[plan.Union[i]]], maxDq[identities[plan.Union[j]]]
		if di != dj {
			return di > dj
		}
		return plan.Union[i].Query < plan.Union[j].Query
	})
	return plan, nil
}

// budgetQueryIdentities returns the stable budget identity for each query in
// one window. Empty TraceIDs fall back to the window ordinal, which is stable
// and unique within that window; query text is never an identity.
func budgetQueryIdentities(queries []*BacktestQuery) map[*BacktestQuery]string {
	identities := make(map[*BacktestQuery]string, len(queries))
	for i, q := range queries {
		if q.TraceID != "" {
			identities[q] = "trace:" + q.TraceID
			continue
		}
		identities[q] = fmt.Sprintf("window:%d", i)
	}
	return identities
}

// selectTopB returns the budget-highest queries by D_q (ties by query text
// ascending), capped at B; when the window holds <= B queries it returns the
// full window in the same deterministic order.
func selectTopB(queries []*BacktestQuery, dq map[string]float64, identities map[*BacktestQuery]string, b int) []*BacktestQuery {
	sorted := make([]*BacktestQuery, len(queries))
	copy(sorted, queries)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := dq[identities[sorted[i]]], dq[identities[sorted[j]]]
		if di != dj {
			return di > dj
		}
		return sorted[i].Query < sorted[j].Query
	})
	if len(sorted) > b {
		sorted = sorted[:b]
	}
	return sorted
}
