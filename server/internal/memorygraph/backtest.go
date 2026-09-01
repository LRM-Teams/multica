package memorygraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
)

// FullBacktestRunner executes a full explore-agent backtest for one query
// against one candidate version (design Q13/A2: queries whose ground truth
// is covered by the retrieval-hit neighborhood run a real agent explore).
// In production this rebuilds an Explorer against the candidate version; in
// tests a fake.
type FullBacktestRunner interface {
	RunExplore(ctx context.Context, version int, query string) (rounds int, found bool, err error)
}

// ScopedFullBacktestRunner binds an external backtest runner to the same
// resolved consolidation identity as the candidate trajectory.
type ScopedFullBacktestRunner struct {
	runner FullBacktestRunner
	scope  ProviderScope
}

func newScopedFullBacktestRunner(runner FullBacktestRunner, scope ProviderScope) (*ScopedFullBacktestRunner, error) {
	if runner == nil {
		return nil, fmt.Errorf("backtest: runner is required")
	}
	if err := validateProviderScope(scope, ProviderPurposeConsolidate); err != nil {
		return nil, err
	}
	return &ScopedFullBacktestRunner{runner: runner, scope: scope}, nil
}

func providerScopeIdentityEqual(a, b ProviderScope) bool {
	return a.WorkspaceID == b.WorkspaceID &&
		a.Provider == b.Provider &&
		a.Model == b.Model &&
		a.Region == b.Region &&
		a.PolicyVersion == b.PolicyVersion
}

func (r *ScopedFullBacktestRunner) RunExplore(ctx context.Context, version int, query string) (int, bool, error) {
	if r == nil || r.runner == nil {
		return 0, false, fmt.Errorf("backtest: scoped runner is not configured")
	}
	if err := validateProviderScope(r.scope, ProviderPurposeConsolidate); err != nil {
		return 0, false, err
	}
	return r.runner.RunExplore(ctx, version, query)
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
	JudgeThreshold  float64 // retained for compatibility with recorded query metadata (default 0.6)
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
	// ColdStart disables statistical recall and rounds-regression gates while
	// the adjacent query-log window is too small to provide a trustworthy baseline.
	ColdStart bool
	// Scope is the resolved consolidation identity for this evaluation. It is
	// required when a confirmer is used without a runner.
	Scope  ProviderScope
	Runner *ScopedFullBacktestRunner
	// Confirmer semantically verifies a replacement node when deterministic
	// historical-node matching cannot satisfy an authoritative item.
	Confirmer *ScopedBacktestConfirmer
	// MaxConfirmationCandidates caps semantic checks per item (default 200).
	MaxConfirmationCandidates int
	// RequireFullBacktest rejects a candidate when no runner is configured.
	RequireFullBacktest bool
}

// BacktestConfirmer semantically confirms that node fully expresses statement.
type BacktestConfirmer interface {
	ConfirmNode(ctx context.Context, statement string, node *Node) (bool, error)
}

// ScopedBacktestConfirmer binds semantic confirmation to the resolved
// consolidation identity.
type ScopedBacktestConfirmer struct {
	confirmer BacktestConfirmer
	scope     ProviderScope
}

func NewScopedBacktestConfirmer(confirmer BacktestConfirmer, scope ProviderScope) (*ScopedBacktestConfirmer, error) {
	if confirmer == nil {
		return nil, fmt.Errorf("backtest: confirmer is required")
	}
	if err := validateProviderScope(scope, ProviderPurposeConsolidate); err != nil {
		return nil, err
	}
	return &ScopedBacktestConfirmer{confirmer: confirmer, scope: scope}, nil
}

func (c *ScopedBacktestConfirmer) ConfirmNode(ctx context.Context, statement string, node *Node) (bool, error) {
	if c == nil || c.confirmer == nil {
		return false, fmt.Errorf("backtest: scoped confirmer is not configured")
	}
	if err := validateProviderScope(c.scope, ProviderPurposeConsolidate); err != nil {
		return false, err
	}
	return c.confirmer.ConfirmNode(ctx, statement, node)
}

// normalized fills zero/negative fields with defaults.
func (c BacktestConfig) normalized() BacktestConfig {
	if c.RecallTolerance <= 0 {
		c.RecallTolerance = DefaultConsolidateConfig().RecallTolerance
	}
	if c.JudgeThreshold <= 0 {
		c.JudgeThreshold = 0.6
	}
	if c.RoundsTolerance <= 0 {
		c.RoundsTolerance = DefaultBacktestRoundsTolerance
	}
	if c.Retrieval.TopK <= 0 {
		c.Retrieval = DefaultRetrievalConfig()
	}
	if c.MaxConfirmationCandidates <= 0 {
		c.MaxConfirmationCandidates = 200
	}
	return c
}

// BacktestQuery is one judged query in the adjacent version window.
// Items are server-authoritative ground truth; RelevantNodes remains the
// legacy representation used when catalog items are unavailable.
// BacktestItem is one necessary information item. NodeIDs are equivalent
// expressions of the same fact, so one matching ID satisfies the item.
type BacktestItem struct {
	ID         string   `json:"id,omitempty"`
	Statement  string   `json:"statement"`
	NodeIDs    []string `json:"node_ids,omitempty"`
	SourceRefs []string `json:"source_refs,omitempty"`
}

type BacktestQuery struct {
	TraceID        string
	Query          string         `json:"query"`
	RelevantNodes  []string       // legacy ground truth set
	Items          []BacktestItem `json:"items,omitempty"`
	BaselineRounds int
	BaselineFound  bool
	JudgeScore     float64
	JudgeDone      bool
}

// QueryBacktestStat records the per-query outcome on one candidate.
type QueryBacktestStat struct {
	TraceID string `json:"trace_id,omitempty"`
	Query   string `json:"query"`
	// Partition is the stable fnv-1a evaluation/holdout/safety split of the
	// query's trace (spec §14.4): rewards read the evaluation partition only;
	// holdout and safety stay out of every reward and gate signal.
	Partition        string            `json:"partition,omitempty"`
	BaselineRounds   int               `json:"baseline_rounds"` // n: rounds the original query needed
	BaselineFound    bool              `json:"baseline_found"`  // baseline-side pass
	Covered          bool              `json:"covered"`         // all ground truth within the n-hop hit neighborhood
	Found            bool              `json:"found"`           // candidate-side pass (recall unit)
	Rounds           float64           `json:"rounds"`          // candidate rounds (runner result, else n estimate)
	Regressed        bool              `json:"regressed"`       // baseline pass -> candidate miss, or rounds overflow
	ItemsTotal       int               `json:"items_total"`
	ItemsSatisfied   int               `json:"items_satisfied"`
	ItemMisses       []string          `json:"item_misses,omitempty"`
	ConfirmedNodeIDs map[string]string `json:"confirmed_node_ids,omitempty"`

	// Skipped means the query fell outside every candidate's top-B union. It
	// is audited but excluded from recall and round statistics.
	Skipped    bool    `json:"skipped"`
	Dq         float64 `json:"dq"`
	SkipReason string  `json:"skip_reason,omitempty"`

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
	MeanRounds     float64             `json:"mean_rounds"`     // every successful full-backtest run, including misses
	P95Rounds      float64             `json:"p95_rounds"`      // every successful full-backtest run, including misses

	// Cost components. EmbedBytes approximates embedding-token cost by the
	// total body bytes of changed nodes: tokens are not available offline,
	// and content-hash-identical bodies reuse the shared embedding cache, so
	// only changed bodies incur embedding work (design §5.4 step 5).
	ChangedNodes int     `json:"changed_nodes"`
	EmbedBytes   int     `json:"embed_bytes"`
	EdgeChurn    int     `json:"edge_churn"`
	Cost         float64 `json:"cost"`

	// Consolidation reward (spec §14.3, Task 19): computed from the
	// candidate's OWN absolute stats over the evaluation partition — never
	// SelectWinner's batch-relative cost. Shadow-recorded on the
	// select_version op-log entry for offline calibration; no model update
	// (spec §14.4). A hard-gate failure carries the deterministic negative.
	Reward              float64          `json:"reward"`
	RewardComponents    RewardComponents `json:"reward_components,omitempty"`
	RewardPolicyVersion string           `json:"reward_policy_version,omitempty"`
}

// ConsolidationRewardInputFromStats derives the §14.3 reward input from one
// candidate's own backtest record: every aggregate reads the evaluation
// partition only (holdout/safety never influence a reward), costs stay
// absolute, and the baseline aggregates use the recorded baseline-side
// signals. With no evaluation queries (cold windows) the deltas are zero,
// leaving the absolute efficiency costs.
func ConsolidationRewardInputFromStats(c *CandidateStats) ConsolidationRewardInput {
	in := ConsolidationRewardInput{
		Passed:          c.Passed,
		GateFailures:    c.GateFailures,
		CandidateRounds: c.MeanRounds,
		EmbedBytes:      c.EmbedBytes,
		ChangedNodes:    c.ChangedNodes,
		EdgeChurn:       c.EdgeChurn,
	}
	if len(c.Queries) == 0 {
		return in
	}
	var eval, evalFound, evalBaseFound, evalCovered, evalRegressed int
	var candRounds, baseRounds float64
	for i := range c.Queries {
		q := &c.Queries[i]
		switch q.Partition {
		case "holdout":
			in.HoldoutQueries++
			continue
		case "safety":
			in.SafetyQueries++
			continue
		case "":
			// Unstated rows (older records) default to evaluation.
		}
		in.EvaluationQueries++
		if q.Skipped {
			// Budget-audit row: never measured, so it carries no signal and
			// stays out of the recall/regression denominators (mirrors
			// recallRates excluding skipped rows from statistics).
			continue
		}
		eval++
		if q.Found {
			evalFound++
		}
		if q.BaselineFound {
			evalBaseFound++
		}
		if q.Covered {
			evalCovered++
		}
		if q.Regressed {
			evalRegressed++
		}
		candRounds += q.Rounds
		baseRounds += float64(q.BaselineRounds)
	}
	in.Queries = eval
	in.Regressions = evalRegressed
	if eval > 0 {
		in.Recall = float64(evalFound) / float64(eval)
		in.BaselineRecall = float64(evalBaseFound) / float64(eval)
		in.Coverage = float64(evalCovered) / float64(eval)
		in.BaselineCoverage = in.BaselineRecall
		in.CandidateRounds = candRounds / float64(eval)
		in.BaselineRounds = baseRounds / float64(eval)
	}
	return in
}

// backtestWindowBounds returns the adjacent graph-version interval used by a
// consolidation away from fromVersion.
func backtestWindowBounds(store *Store, fromVersion int) (int, error) {
	versions, err := store.ListVersions()
	if err != nil {
		return 0, fmt.Errorf("backtest queries: list versions: %w", err)
	}
	prev := fromVersion - 1
	for _, v := range versions {
		if v < fromVersion && v > prev {
			prev = v
		}
	}
	return prev, nil
}

// BacktestQueries collects judged, authoritative entries from the adjacent
// query-log window. The permanent regression set was intentionally removed
// with protocol generation 2.
func BacktestQueries(store *Store, fromVersion int) ([]*BacktestQuery, error) {
	prev, err := backtestWindowBounds(store, fromVersion)
	if err != nil {
		return nil, err
	}

	var out []*BacktestQuery
	// Trace IDs are stable recall identities. A trace recorded in multiple
	// windows enters once; the first window occurrence wins deterministically.
	seenTraceIDs := make(map[string]bool)
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
			if e.LegacyNonAuthoritative {
				continue
			}
			if e.TraceID != "" && seenTraceIDs[e.TraceID] {
				continue
			}
			if e.TraceID != "" {
				seenTraceIDs[e.TraceID] = true
			}
			out = append(out, &BacktestQuery{
				TraceID:        e.TraceID,
				Query:          e.Query,
				RelevantNodes:  e.RelevantNodes,
				Items:          e.InfoItems,
				BaselineRounds: baselineRounds(e.Rounds),
				BaselineFound:  e.Found,
				JudgeScore:     e.JudgeScore,
				JudgeDone:      e.JudgeDone,
			})
		}
	}

	return out, nil
}

// BacktestWindowQueryCount returns every query-log entry in the adjacent
// version window, including entries whose asynchronous judge has not run.
func BacktestWindowQueryCount(store *Store, fromVersion int) (int, error) {
	prev, err := backtestWindowBounds(store, fromVersion)
	if err != nil {
		return 0, err
	}
	windows, err := store.ListQueryLogWindows()
	if err != nil {
		return 0, fmt.Errorf("backtest window query count: list windows: %w", err)
	}
	count := 0
	for _, w := range windows {
		entries, err := NewQueryRecorder(store, w).EntriesBetween(prev, fromVersion)
		if err != nil {
			return 0, err
		}
		count += len(entries)
	}
	return count, nil
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

func (b *Backtester) validateProviderBindings() error {
	var expected *ProviderScope
	if b.cfg.Scope != (ProviderScope{}) {
		if err := validateProviderScope(b.cfg.Scope, ProviderPurposeConsolidate); err != nil {
			return err
		}
		expected = &b.cfg.Scope
	}
	if b.cfg.Runner != nil {
		if err := validateProviderScope(b.cfg.Runner.scope, ProviderPurposeConsolidate); err != nil {
			return err
		}
		if expected != nil && !providerScopeIdentityEqual(*expected, b.cfg.Runner.scope) {
			return fmt.Errorf("backtest: runner scope identity does not match evaluation scope identity")
		}
		expected = &b.cfg.Runner.scope
	}
	if b.cfg.Confirmer == nil {
		return nil
	}
	if err := validateProviderScope(b.cfg.Confirmer.scope, ProviderPurposeConsolidate); err != nil {
		return err
	}
	if expected == nil {
		return fmt.Errorf("backtest: confirmer scope identity requires an evaluation scope identity")
	}
	if !providerScopeIdentityEqual(*expected, b.cfg.Confirmer.scope) {
		return fmt.Errorf("backtest: confirmer scope identity does not match evaluation scope identity")
	}
	return nil
}

// EvaluateCandidate runs the backtest and hard gates for one candidate.
func (b *Backtester) EvaluateCandidate(ctx context.Context, version, parentVersion int, queries []*BacktestQuery) CandidateStats {
	stats := CandidateStats{Version: version, Passed: true}
	fail := func(format string, args ...any) {
		stats.Passed = false
		stats.GateFailures = append(stats.GateFailures, fmt.Sprintf(format, args...))
	}
	if b.cfg.RequireFullBacktest && b.cfg.Runner == nil {
		fail("full_backtest_runner_required")
	}
	if err := b.validateProviderBindings(); err != nil {
		fail("%v", err)
		return stats
	}
	g, err := LoadGraph(b.store, version)
	if err != nil {
		fail("load graph: %v", err)
		return stats
	}
	if err := g.Validate(); err != nil {
		fail("validate: %v", err)
	}
	b.checkStagingCoverage(g, fail)
	retr := NewHybridRetriever(b.store, b.cfg.Embedder, b.cfg.Retrieval)
	if err := retr.RebuildForVersion(ctx, version); err != nil {
		fail("build candidate retriever: %v", err)
		return stats
	}

	stats.Queries = make([]QueryBacktestStat, 0, len(queries))
	for _, q := range queries {
		if len(resolveBacktestItems(q)) == 0 {
			continue
		}
		stats.Queries = append(stats.Queries, b.evalQuery(ctx, retr, g, version, q))
	}
	var ok bool
	stats.Recall, stats.BaselineRecall, ok = recallRates(stats.Queries)
	if !ok {
		if !b.cfg.ColdStart {
			fail("no_eligible_backtest_ground_truth")
		}
	} else if !b.cfg.ColdStart && stats.Recall < stats.BaselineRecall-b.cfg.RecallTolerance {
		fail("recall %.4f below baseline %.4f - tolerance %.4f", stats.Recall, stats.BaselineRecall, b.cfg.RecallTolerance)
	}
	var rounds []float64
	for _, qs := range stats.Queries {
		// Honest-skip semantics (spec §5.2): queries outside the measured
		// union carry Rounds=0 and never enter Mean/P95. Full backtests are
		// the only agent-run round measurements; coverage-only acceptance
		// (no runner) is an estimate, not an observation, and is excluded.
		if qs.Skipped || !qs.FullBacktestRan {
			continue
		}
		rounds = append(rounds, qs.Rounds)
	}
	stats.MeanRounds = mean(rounds)
	stats.P95Rounds = percentile(rounds, 95)
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

// resolvedBacktestItem is one AND-required fact after legacy conversion.
type resolvedBacktestItem struct {
	BacktestItem
	missID string
}

// resolveBacktestItems makes catalog items authoritative when present. Legacy
// nodes retain their historical AND semantics by becoming single-node items.
func resolveBacktestItems(q *BacktestQuery) []resolvedBacktestItem {
	if len(q.Items) > 0 {
		out := make([]resolvedBacktestItem, 0, len(q.Items))
		for i, item := range q.Items {
			out = append(out, resolvedBacktestItem{BacktestItem: item, missID: stableItemMissID(item, i)})
		}
		return out
	}
	out := make([]resolvedBacktestItem, 0, len(q.RelevantNodes))
	for i, id := range q.RelevantNodes {
		if id == "" {
			continue
		}
		item := BacktestItem{ID: id, NodeIDs: []string{id}}
		out = append(out, resolvedBacktestItem{BacktestItem: item, missID: stableItemMissID(item, i)})
	}
	return out
}

// stableItemMissID uses an item ID when available, otherwise the normalized
// statement SHA-256, and finally its stable query-item index for blank facts.
func stableItemMissID(item BacktestItem, index int) string {
	if item.ID != "" {
		return item.ID
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(item.Statement)), " ")
	if normalized != "" {
		sum := sha256.Sum256([]byte(normalized))
		return "statement:" + hex.EncodeToString(sum[:])
	}
	return fmt.Sprintf("item:%d", index)
}

// evalQuery evaluates AND-required items with OR-equivalent node groups.
func (b *Backtester) evalQuery(ctx context.Context, retr *HybridRetriever, g *Graph, version int, q *BacktestQuery) QueryBacktestStat {
	n := baselineRounds(q.BaselineRounds)
	items := resolveBacktestItems(q)
	stat := QueryBacktestStat{TraceID: q.TraceID, Query: q.Query, Partition: QueryPartition(q.TraceID), BaselineRounds: n, BaselineFound: q.BaselineFound, ItemsTotal: len(items)}
	hits, err := retr.Search(ctx, q.Query)
	if err != nil {
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
	candidateIDs := make([]string, 0, len(neighborhood))
	for id := range neighborhood {
		candidateIDs = append(candidateIDs, id)
	}
	sort.Strings(candidateIDs)
	if len(candidateIDs) > b.cfg.MaxConfirmationCandidates {
		candidateIDs = candidateIDs[:b.cfg.MaxConfirmationCandidates]
	}
	for _, item := range items {
		satisfied := false
		for _, id := range item.NodeIDs {
			if neighborhood[id] {
				satisfied = true
				break
			}
		}
		if !satisfied && b.cfg.Confirmer != nil {
			statement := strings.Join(strings.Fields(strings.ToLower(item.Statement)), " ")
			for _, id := range candidateIDs {
				node := g.Node(id)
				if node == nil {
					continue
				}
				confirmed, err := b.cfg.Confirmer.ConfirmNode(ctx, statement, node)
				if err != nil || !confirmed {
					continue
				}
				if stat.ConfirmedNodeIDs == nil {
					stat.ConfirmedNodeIDs = make(map[string]string)
				}
				stat.ConfirmedNodeIDs[item.missID] = id
				satisfied = true
				break
			}
		}
		if satisfied {
			stat.ItemsSatisfied++
		} else if len(stat.ItemMisses) < 20 {
			stat.ItemMisses = append(stat.ItemMisses, item.missID)
		}
	}
	stat.Covered = stat.ItemsTotal > 0 && stat.ItemsSatisfied == stat.ItemsTotal
	if !stat.Covered {
		stat.Regressed = q.BaselineFound
		return stat
	}
	if b.cfg.Runner == nil {
		stat.Found = true
		stat.Rounds = float64(n)
		stat.AcceptedWithoutExplore = true
		return stat
	}
	stat.RequiresFullBacktest = true
	rounds, found, err := b.cfg.Runner.RunExplore(ctx, version, q.Query)
	if err != nil {
		stat.Regressed = q.BaselineFound
		return stat
	}
	stat.FullBacktestRan = true
	stat.Rounds = float64(rounds)
	stat.Found = found
	if !found {
		stat.Regressed = q.BaselineFound
		return stat
	}
	if q.BaselineFound && rounds > n+b.cfg.RoundsTolerance && !b.cfg.ColdStart {
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
func recallRates(stats []QueryBacktestStat) (recall, baseline float64, ok bool) {
	if len(stats) == 0 {
		return 0, 0, false
	}
	var hits, baseHits int
	for _, qs := range stats {
		if qs.Skipped {
			continue
		}
		if qs.Found {
			hits++
		}
		if qs.BaselineFound {
			baseHits++
		}
	}
	eligible := 0
	for _, qs := range stats {
		if !qs.Skipped {
			eligible++
		}
	}
	if eligible == 0 {
		return 0, 0, false
	}
	n := float64(eligible)
	return float64(hits) / n, float64(baseHits) / n, true
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
