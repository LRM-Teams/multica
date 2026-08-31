package memorygraph

import (
	"context"
	"fmt"
)

// exploreBacktestRunner is the production FullBacktestRunner (design Q13/A2,
// review R2): for one candidate version it rebuilds a HybridRetriever via
// RebuildForVersion and an Explorer pinned to that version, then runs a real
// explore. The candidate version is never the current pointer during a TTT
// backtest, so the Explorer must run with PinVersion(version) — its tool
// server then reads the candidate's version dir for the whole call.
type exploreBacktestRunner struct {
	store    *Store
	emb      *CachedEmbedder // nil → BM25-only retrieval, as in production without an embedder
	backend  AgentBackend
	retrCfg  RetrievalConfig
	expCfg   ExploreConfig
	provider string
}

// NewExploreBacktestRunner returns a FullBacktestRunner that evaluates
// candidate versions with the same retrieval + explore pipeline production
// recalls use. retrCfg/expCfg zero values fall back to the design §6
// defaults.
func NewExploreBacktestRunner(store *Store, emb *CachedEmbedder, backend AgentBackend, retrCfg RetrievalConfig, expCfg ExploreConfig, scope ProviderScope) (*ScopedFullBacktestRunner, error) {
	if store == nil || backend == nil {
		return nil, fmt.Errorf("backtest runner: store and agent backend are required")
	}
	if err := validateProviderScope(scope, ProviderPurposeConsolidate); err != nil {
		return nil, err
	}
	if emb != nil {
		if err := validateProviderScope(emb.scope, ProviderPurposeEmbed); err != nil {
			return nil, err
		}
		if !providerScopeIdentityEqual(emb.scope, scope) {
			return nil, fmt.Errorf("backtest runner: embedder scope identity does not match runner scope identity")
		}
	}
	if retrCfg.TopK <= 0 {
		retrCfg = DefaultRetrievalConfig()
	}
	runner := &exploreBacktestRunner{
		store:    store,
		emb:      emb,
		backend:  backend,
		retrCfg:  retrCfg,
		expCfg:   expCfg.normalized(),
		provider: scope.Provider,
	}
	return newScopedFullBacktestRunner(runner, scope)
}

// RunExplore runs one explore against the candidate version and returns the
// adopted trajectory's rounds and found flag.
func (r *exploreBacktestRunner) RunExplore(ctx context.Context, version int, query string) (rounds int, found bool, err error) {
	if r.backend == nil {
		return 0, false, fmt.Errorf("backtest runner: agent backend not configured")
	}
	retr := NewHybridRetriever(r.store, r.emb, r.retrCfg)
	if err := retr.RebuildForVersion(ctx, version); err != nil {
		return 0, false, fmt.Errorf("backtest runner: rebuild retriever for v%d: %w", version, err)
	}
	// Backtest trajectories are evaluation-only: they have no query-log entry
	// and no reward join, so they are not persisted (nil recorder).
	explorer := NewExplorer(r.store, retr, r.backend, r.expCfg, r.provider, nil)
	explorer.PinVersion(version)
	recall, err := explorer.Explore(ctx, query)
	if err != nil {
		return 0, false, fmt.Errorf("backtest runner: explore v%d: %w", version, err)
	}
	return recall.Rounds, recall.Found, nil
}

var _ FullBacktestRunner = (*exploreBacktestRunner)(nil)
