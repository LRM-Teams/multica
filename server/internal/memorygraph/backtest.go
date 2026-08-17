package memorygraph

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// FullBacktestRunner executes a full explore-agent backtest for one query
// against one candidate version (design Q13/A2: queries whose ground truth
// is covered by the retrieval-hit neighborhood run a real agent explore).
// In production this rebuilds an Explorer against the candidate version; in
// tests a fake.
type FullBacktestRunner interface {
	RunExplore(ctx context.Context, version int, query string) (rounds int, found bool, err error)
}

// DefaultBacktestBaselineRounds is the n of the n-hop coverage check when a
// backtest query carries no recorded baseline rounds (legacy regression
// entries predate the baseline_rounds field, design Q13/A2).
const DefaultBacktestBaselineRounds = 2

// DefaultBacktestRoundsTolerance is the allowed candidate-rounds overflow
// over the recorded baseline before a covered query counts as a regression
// (design Q13/A2).
const DefaultBacktestRoundsTolerance = 1

// BacktestConfig configures candidate evaluation (design §5.4 steps 3-4).
type BacktestConfig struct {
	RecallTolerance float64 // allowed recall-rate drop vs baseline (default 0.02)
	JudgeThreshold  float64 // τ: only judge-passed queries feed mean/p95 rounds (default 0.6)
	// RoundsTolerance is the rounds-overflow tolerance for the regression
	// gate (default DefaultBacktestRoundsTolerance).
	RoundsTolerance int
	// Retrieval mirrors the production retrieval configuration (top_k,
	// bm25_weight); zero values fall back to DefaultRetrievalConfig.
	Retrieval RetrievalConfig
	// Embedder enables the vector channel of the per-candidate hybrid
	// retrieval. nil is allowed: backtests then run BM25-only through the
	// same two-channel merge as production (retriever.go hybridSearch).
	Embedder *CachedEmbedder
	Runner   FullBacktestRunner
}

// normalized fills zero/negative fields with defaults.
func (c BacktestConfig) normalized() BacktestConfig {
	if c.RecallTolerance <= 0 {
		c.RecallTolerance = DefaultConsolidateConfig().RecallTolerance
	}
	if c.JudgeThreshold <= 0 {
		c.JudgeThreshold = DefaultJudgeConfig().RelevanceThreshold
	}
	if c.RoundsTolerance <= 0 {
		c.RoundsTolerance = DefaultBacktestRoundsTolerance
	}
	if c.Retrieval.TopK <= 0 {
		c.Retrieval = DefaultRetrievalConfig()
	}
	return c
}

// BacktestQuery is one backtest input: a judged window query (design Q26)
// or a permanent regression-set entry. BaselineRounds is the number of
// explore rounds the original query needed (QueryLogEntry.Rounds of the
// adopted path for window queries; RegressionEntry.BaselineRounds for
// regression entries, defaulting to DefaultBacktestBaselineRounds when
// absent). BaselineFound records whether the query passed baseline-side:
// QueryLogEntry.Found for window queries, true for regression entries
// (the regression set holds critical queries expected to pass).
type BacktestQuery struct {
	TraceID        string   // empty for regression entries
	Query          string   `json:"query"`
	RelevantNodes  []string // ground truth set
	BaselineRounds int
	BaselineFound  bool
	JudgeScore     float64
	JudgeDone      bool
	Regression     bool
}

// QueryBacktestStat records the per-query outcome on one candidate.
type QueryBacktestStat struct {
	TraceID        string  `json:"trace_id,omitempty"`
	Query          string  `json:"query"`
	Regression     bool    `json:"regression"`
	BaselineRounds int     `json:"baseline_rounds"` // n: rounds the original query needed
	BaselineFound  bool    `json:"baseline_found"`  // baseline-side pass
	Covered        bool    `json:"covered"`         // all ground truth within the n-hop hit neighborhood
	Found          bool    `json:"found"`           // candidate-side pass (recall unit)
	Rounds         float64 `json:"rounds"`          // candidate rounds (runner result, else n estimate)
	Regressed      bool    `json:"regressed"`       // baseline pass -> candidate miss, or rounds overflow

	RequiresFullBacktest   bool `json:"requires_full_backtest"`
	FullBacktestRan        bool `json:"full_backtest_ran"`
	AcceptedWithoutExplore bool `json:"accepted_without_explore"` // runner absent: coverage stands as the pass signal
}

// CandidateStats is the per-candidate evaluation and selection record
// (design §5.4 steps 3-5). It is returned in ConsolidateResult and mirrored
// into the select_version op-log entry for auditability.
type CandidateStats struct {
	Version    int    `json:"version"`
	Actor      string `json:"actor"`
	OpsApplied int    `json:"ops_applied"`
	Error      string `json:"error,omitempty"` // trajectory-level failure

	Passed       bool     `json:"passed"`
	GateFailures []string `json:"gate_failures,omitempty"`

	Queries        []QueryBacktestStat `json:"queries,omitempty"`
	Recall         float64             `json:"recall"`          // fraction of queries with finite distance
	BaselineRecall float64             `json:"baseline_recall"` // fraction of queries with finite baseline
	MeanRounds     float64             `json:"mean_rounds"`     // judge-passed queries only
	P95Rounds      float64             `json:"p95_rounds"`      // judge-passed queries only

	// Cost components. EmbedBytes approximates embedding-token cost by the
	// total body bytes of changed nodes: tokens are not available offline,
	// and content-hash-identical bodies reuse the shared embedding cache, so
	// only changed bodies incur embedding work (design §5.4 step 5).
	ChangedNodes int     `json:"changed_nodes"`
	EmbedBytes   int     `json:"embed_bytes"`
	EdgeChurn    int     `json:"edge_churn"`
	Cost         float64 `json:"cost"`
}

// BacktestQueries collects the backtest input for a consolidation away from
// fromVersion (design Q26): every judged query-log entry recorded against
// fromVersion across all windows (the adjacent-version window) plus the
// permanent regression set. Window baselines come from the recorded entry
// (Rounds of the adopted path, Found); regression baselines come from the
// entry's baseline_rounds (defaulting to DefaultBacktestBaselineRounds for
// pre-A2 files) and are treated as baseline-passing by construction.
func BacktestQueries(store *Store, fromVersion int) ([]*BacktestQuery, error) {
	versions, err := store.ListVersions()
	if err != nil {
		return nil, fmt.Errorf("backtest queries: list versions: %w", err)
	}
	prev := fromVersion - 1
	for _, v := range versions {
		if v < fromVersion && v > prev {
			prev = v
		}
	}

	var out []*BacktestQuery
	windows, err := store.ListQueryLogWindows()
	if err != nil {
		return nil, fmt.Errorf("backtest queries: list windows: %w", err)
	}
	for _, w := range windows {
		entries, err := NewQueryRecorder(store, w).QueriesBetween(prev, fromVersion)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			out = append(out, &BacktestQuery{
				TraceID:        e.TraceID,
				Query:          e.Query,
				RelevantNodes:  e.RelevantNodes,
				BaselineRounds: baselineRounds(e.Rounds),
				BaselineFound:  e.Found,
				JudgeScore:     e.JudgeScore,
				JudgeDone:      e.JudgeDone,
			})
		}
	}

	regression, err := store.ReadRegression()
	if err != nil {
		return nil, err
	}
	for _, re := range regression {
		if len(re.RelevantNodes) == 0 {
			continue
		}
		out = append(out, &BacktestQuery{
			Query:          re.Query,
			RelevantNodes:  re.RelevantNodes,
			BaselineRounds: baselineRounds(re.BaselineRounds),
			BaselineFound:  true,
			Regression:     true,
		})
	}
	return out, nil
}

// baselineRounds normalizes a recorded baseline-rounds value: absent/zero
// (legacy files, or a recorded 0-round entry) falls back to
// DefaultBacktestBaselineRounds.
func baselineRounds(rounds int) int {
	if rounds <= 0 {
		return DefaultBacktestBaselineRounds
	}
	return rounds
}

// ComputeBaselineCoverage computes the judge-time baseline coverage signal
// (design Q13/A2, review R10) for one query against the given retriever and
// graph — both resolved on the CURRENT version at judge time by the caller:
// hybrid top-k hits, then the check whether the ground truth set lies within
// their n-hop undirected neighborhood, where n is the number of explore
// rounds the query's adopted path needed (normalized via baselineRounds).
// A retrieval error yields the zero signal (not covered, no hits), matching
// evalQuery's conservative-miss treatment.
func ComputeBaselineCoverage(ctx context.Context, retr *HybridRetriever, g *Graph, query string, groundTruth []string, adoptedRounds int) BaselineSignal {
	hits, err := retr.Search(ctx, query)
	if err != nil {
		return BaselineSignal{}
	}
	var hitIDs []string
	for _, h := range hits {
		if !IsStagingID(h.ID) {
			hitIDs = append(hitIDs, h.ID)
		}
	}
	gt := nodeSet(groundTruth)
	neighborhood := g.KHopNeighborhood(hitIDs, baselineRounds(adoptedRounds))
	return BaselineSignal{
		Covered: len(gt) > 0 && allInSet(gt, neighborhood),
		TopK:    hitIDs,
	}
}

// Backtester evaluates candidate versions against the backtest query set
// (design §5.4 steps 3-4).
type Backtester struct {
	store *Store
	cfg   BacktestConfig
}

// NewBacktester returns a Backtester over store; cfg zero values fall back
// to the design §6 defaults.
func NewBacktester(store *Store, cfg BacktestConfig) *Backtester {
	return &Backtester{store: store, cfg: cfg.normalized()}
}

// EvaluateCandidate runs the backtest and the hard gates for one candidate
// version and returns its stats. parentVersion is the version the candidate
// was copied from, used for the cost diffs. A candidate that fails any hard
// gate has Passed=false and the failures listed in GateFailures; evaluation
// still completes so the stats remain auditable.
//
// Retrieval note (design Q13/A2): each candidate is evaluated with a
// per-candidate HybridRetriever built over the candidate's version — the
// same two-channel merge (retriever.go hybridSearch), TopK and BM25Weight
// as production, BM25-only when no embedder is configured. The per-query
// gate is neighborhood coverage: the ground truth set must lie within the
// n-hop graph neighborhood of the hybrid top-k hits, where n is the number
// of explore rounds the original query needed. Uncovered queries fail
// outright (recall miss, and regression when the query passed
// baseline-side); covered queries run the full explore backtest when a
// FullBacktestRunner is wired, and conservatively pass on coverage alone
// otherwise.
func (b *Backtester) EvaluateCandidate(ctx context.Context, version, parentVersion int, queries []*BacktestQuery) CandidateStats {
	stats := CandidateStats{Version: version, Passed: true}
	fail := func(format string, args ...any) {
		stats.Passed = false
		stats.GateFailures = append(stats.GateFailures, fmt.Sprintf(format, args...))
	}

	g, err := LoadGraph(b.store, version)
	if err != nil {
		fail("load graph: %v", err)
		return stats
	}

	// Hard gate 1: schema/graph validation.
	if err := g.Validate(); err != nil {
		fail("validate: %v", err)
	}

	// Hard gate 2: every staging segment is referenced by >= 1 node
	// (新 segment 已处理).
	b.checkStagingCoverage(g, fail)

	// Per-candidate hybrid retriever identical to production (A2).
	retr := NewHybridRetriever(b.store, b.cfg.Embedder, b.cfg.Retrieval)
	if err := retr.RebuildForVersion(ctx, version); err != nil {
		fail("build candidate retriever: %v", err)
		return stats
	}

	// Per-query backtest.
	stats.Queries = make([]QueryBacktestStat, 0, len(queries))
	for _, q := range queries {
		stats.Queries = append(stats.Queries, b.evalQuery(ctx, retr, g, version, q))
	}

	// Hard gate 3: recall rate >= baseline - tolerance. Recall is the
	// fraction of backtest queries passing candidate-side (neighborhood
	// coverage, plus explore found=true when a runner is wired); the
	// baseline is the fraction passing baseline-side.
	stats.Recall, stats.BaselineRecall = recallRates(stats.Queries)
	if stats.Recall < stats.BaselineRecall-b.cfg.RecallTolerance {
		fail("recall %.4f below baseline %.4f - tolerance %.4f", stats.Recall, stats.BaselineRecall, b.cfg.RecallTolerance)
	}

	// Hard gate 4: no regression-set query may regress (关键 query 不退化):
	// a regression entry that passed baseline-side but misses
	// candidate-side, or whose full-backtest rounds exceed the recorded
	// baseline rounds plus RoundsTolerance, fails the gate.
	for _, qs := range stats.Queries {
		if qs.Regression && qs.Regressed {
			fail("regression query %q regressed (covered=%v found=%v rounds=%.0f baseline_rounds=%d)",
				qs.Query, qs.Covered, qs.Found, qs.Rounds, qs.BaselineRounds)
		}
	}

	// Rounds stats over judge-passed queries only (design §5.4 step 6).
	var rounds []float64
	for i, q := range queries {
		qs := stats.Queries[i]
		if q.Regression || !q.JudgeDone || q.JudgeScore < b.cfg.JudgeThreshold || !qs.Found {
			continue
		}
		rounds = append(rounds, qs.Rounds)
	}
	stats.MeanRounds = mean(rounds)
	stats.P95Rounds = percentile(rounds, 95)

	// Cost diffs against the parent version.
	changed, embedBytes, churn, err := diffVersions(b.store, parentVersion, g)
	if err != nil {
		fail("cost diff: %v", err)
		return stats
	}
	stats.ChangedNodes = changed
	stats.EmbedBytes = embedBytes
	stats.EdgeChurn = churn
	return stats
}

// checkStagingCoverage fails the gate for every staging segment not
// referenced by any candidate node.
func (b *Backtester) checkStagingCoverage(g *Graph, fail func(string, ...any)) {
	segIDs, err := b.store.ListStagingSegments()
	if err != nil {
		fail("list staging segments: %v", err)
		return
	}
	if len(segIDs) == 0 {
		return
	}
	referenced := make(map[string]bool)
	for _, n := range g.Nodes() {
		for _, ref := range n.SegmentRefs {
			referenced[ref] = true
		}
	}
	for _, id := range segIDs {
		if !referenced[id] {
			fail("staging segment %s is not referenced by any node", id)
		}
	}
}

// evalQuery runs the A2 neighborhood-coverage backtest for one query
// (design Q13):
//  1. hybrid retrieval against the candidate version (same pipeline as
//     production),
//  2. the ground truth set must lie within the n-hop neighborhood of the
//     top-k hit node ids, where n = the rounds the original query needed —
//     otherwise the query fails outright (no agent run),
//  3. covered queries run the full explore backtest via the configured
//     FullBacktestRunner; without a runner, coverage alone stands as the
//     pass signal (documented conservative default: the coverage check is
//     the signal, the agent run only refines the rounds stats).
//
// A query regresses when it passed baseline-side but misses candidate-side,
// or when its full-backtest rounds exceed baseline rounds + RoundsTolerance.
func (b *Backtester) evalQuery(ctx context.Context, retr *HybridRetriever, g *Graph, version int, q *BacktestQuery) QueryBacktestStat {
	n := baselineRounds(q.BaselineRounds)
	stat := QueryBacktestStat{
		TraceID:        q.TraceID,
		Query:          q.Query,
		Regression:     q.Regression,
		BaselineRounds: n,
		BaselineFound:  q.BaselineFound,
	}
	gt := nodeSet(q.RelevantNodes)

	hits, err := retr.Search(ctx, q.Query)
	if err != nil {
		// A retrieval failure is a conservative miss, not a gate error.
		stat.Regressed = q.BaselineFound
		return stat
	}
	var hitIDs []string
	for _, h := range hits {
		if !IsStagingID(h.ID) {
			hitIDs = append(hitIDs, h.ID)
		}
	}
	neighborhood := g.KHopNeighborhood(hitIDs, n)
	stat.Covered = len(gt) > 0 && allInSet(gt, neighborhood)
	if !stat.Covered {
		stat.Regressed = q.BaselineFound
		return stat
	}

	if b.cfg.Runner == nil {
		// Runner-absent conservative default: coverage stands as the pass
		// signal; the rounds estimate is the n the original query needed.
		stat.Found = true
		stat.Rounds = float64(n)
		stat.AcceptedWithoutExplore = true
		return stat
	}

	stat.RequiresFullBacktest = true
	rounds, found, err := b.cfg.Runner.RunExplore(ctx, version, q.Query)
	stat.FullBacktestRan = true
	if err != nil || !found {
		stat.Regressed = q.BaselineFound
		return stat
	}
	stat.Found = true
	stat.Rounds = float64(rounds)
	if q.BaselineFound && rounds > n+b.cfg.RoundsTolerance {
		stat.Regressed = true
	}
	return stat
}

// allInSet reports whether every key of set is present in container.
func allInSet(set, container map[string]bool) bool {
	for id := range set {
		if !container[id] {
			return false
		}
	}
	return true
}

// recallRates computes candidate and baseline recall over the evaluated
// queries: the fraction passing candidate-side (Found) resp. baseline-side
// (BaselineFound).
func recallRates(stats []QueryBacktestStat) (recall, baseline float64) {
	if len(stats) == 0 {
		return 1, 1
	}
	var hits, baseHits int
	for _, qs := range stats {
		if qs.Found {
			hits++
		}
		if qs.BaselineFound {
			baseHits++
		}
	}
	n := float64(len(stats))
	return float64(hits) / n, float64(baseHits) / n
}

// nodeSet builds a set from node ids.
func nodeSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// diffVersions computes the cost diffs of candidate graph g against the
// stored parent version: changed nodes (added, body-changed via content
// hash, or deleted), the total body bytes of added/changed nodes (the
// embedding-token approximation — deleted nodes only remove an index entry
// and do not embed, design §5.4 step 5), and edge churn (edge ids added or
// deleted across hierarchy and relation edges).
func diffVersions(store *Store, parentV int, g *Graph) (changedNodes, embedBytes, edgeChurn int, err error) {
	parentNodes, err := store.LoadNodes(parentV)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("diff vs v%d: load parent nodes: %w", parentV, err)
	}
	parentHash := make(map[string]string, len(parentNodes))
	for _, n := range parentNodes {
		parentHash[n.NodeID] = ComputeContentHash(n.Body)
	}
	seen := make(map[string]bool)
	for _, n := range g.Nodes() {
		seen[n.NodeID] = true
		hash, ok := parentHash[n.NodeID]
		if !ok || hash != ComputeContentHash(n.Body) {
			changedNodes++
			embedBytes += len(n.Body)
		}
	}
	for id := range parentHash {
		if !seen[id] {
			changedNodes++ // deleted node: index removal only, no embed cost
		}
	}

	parentHier, parentRel, err := store.LoadEdges(parentV)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("diff vs v%d: load parent edges: %w", parentV, err)
	}
	parentEdgeIDs := make(map[string]bool, len(parentHier)+len(parentRel))
	for _, e := range parentHier {
		parentEdgeIDs[e.EdgeID] = true
	}
	for _, e := range parentRel {
		parentEdgeIDs[e.EdgeID] = true
	}
	candEdgeIDs := make(map[string]bool)
	for _, e := range g.HierarchyEdges() {
		candEdgeIDs[e.EdgeID] = true
	}
	for _, e := range g.RelationEdges() {
		candEdgeIDs[e.EdgeID] = true
	}
	for id := range candEdgeIDs {
		if !parentEdgeIDs[id] {
			edgeChurn++
		}
	}
	for id := range parentEdgeIDs {
		if !candEdgeIDs[id] {
			edgeChurn++
		}
	}
	return changedNodes, embedBytes, edgeChurn, nil
}

// SelectWinner computes the design §5.4 step-5 cost over the gate survivors
// and returns the index of the minimum-cost candidate, or -1 when no
// candidate passed the gates:
//
//	cost = w_round*meanRounds + w_tail*p95Rounds + w_embed*norm(EmbedBytes)
//	     + w_node*norm(ChangedNodes) + w_graph*norm(EdgeChurn)
//
// norm is min-max normalization across the surviving candidates; a single
// survivor (or zero spread) yields a zero norm vector, so its cost is the
// raw rounds terms only. Costs are written back onto the candidates for
// auditability. Ties break toward the lowest version.
func SelectWinner(cands []CandidateStats, w CostWeights) int {
	var survivors []int
	for i := range cands {
		if cands[i].Passed {
			survivors = append(survivors, i)
		}
	}
	if len(survivors) == 0 {
		return -1
	}
	norm := func(value func(int) float64) map[int]float64 {
		out := make(map[int]float64, len(survivors))
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, i := range survivors {
			v := value(i)
			lo = min(lo, v)
			hi = max(hi, v)
		}
		for _, i := range survivors {
			if hi > lo {
				out[i] = (value(i) - lo) / (hi - lo)
			}
		}
		return out
	}
	embedNorm := norm(func(i int) float64 { return float64(cands[i].EmbedBytes) })
	nodeNorm := norm(func(i int) float64 { return float64(cands[i].ChangedNodes) })
	graphNorm := norm(func(i int) float64 { return float64(cands[i].EdgeChurn) })

	winner := -1
	for _, i := range survivors {
		cs := &cands[i]
		cs.Cost = w.Round*cs.MeanRounds + w.Tail*cs.P95Rounds +
			w.Embed*embedNorm[i] + w.Node*nodeNorm[i] + w.Graph*graphNorm[i]
		if winner < 0 || cs.Cost < cands[winner].Cost ||
			(cs.Cost == cands[winner].Cost && cs.Version < cands[winner].Version) {
			winner = i
		}
	}
	return winner
}

// mean returns the arithmetic mean of xs (0 for an empty slice).
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	total := 0.0
	for _, x := range xs {
		total += x
	}
	return total / float64(len(xs))
}

// percentile returns the p-th percentile (0 <= p <= 100) of xs using linear
// interpolation between closest ranks (the numpy "linear" method): with n
// sorted values, rank = p/100*(n-1), and the result is
// xs[floor(rank)] + frac*(xs[ceil(rank)]-xs[floor(rank)]). A single value
// returns itself; an empty slice returns 0. The input is not mutated.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := make([]float64, len(xs))
	copy(sorted, xs)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	p = min(max(p, 0), 100)
	rank := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := min(lo+1, len(sorted)-1)
	frac := rank - float64(lo)
	return sorted[lo] + frac*(sorted[hi]-sorted[lo])
}
