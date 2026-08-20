package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// graphMemoryQueryLogWindow is the query_log window the daemon appends its
// recall entries to (design §4.1). BacktestQueries reads across all windows,
// so the window id needs no server coordination.
//
// Design authority: docs/superpowers/specs/2026-08-14-graph-memory-reviewer-design.zh-CN.md
const graphMemoryQueryLogWindow = "daemon"

// graphMemoryStagingCheckInterval throttles the staging-area change stat in
// ensureRetriever: at most one re-stat per interval per provider (design
// §5.1 step 3: freshly staged segments must be retrievable without waiting
// for a version switch).
const graphMemoryStagingCheckInterval = 30 * time.Second

// graphMemoryMetrics is the narrow metrics surface of the graph memory
// provider. The daemon has no BusinessMetrics handle — business metrics are
// a server-side concern wired in cmd/server — so production wires
// noopGraphMemoryMetrics here; *metrics.BusinessMetrics satisfies this
// interface and serves the server-side call sites (scheduler job, judge
// service).
type graphMemoryMetrics interface {
	RecordGraphMemoryRecall(found bool, rounds int)
	ObserveGraphExploreRounds(rounds int)
}

type noopGraphMemoryMetrics struct{}

func (noopGraphMemoryMetrics) RecordGraphMemoryRecall(bool, int) {}
func (noopGraphMemoryMetrics) ObserveGraphExploreRounds(int)     {}

// graphMemoryJudgeKicker is the narrow daemon→server channel the recall path
// uses to kick the async judge + delayed-reward flow (design §5.3). The
// judge runs server-side (service.GraphMemoryJudgeService): the daemon has
// no MessageStore access for the downstream history (Q18), no BusinessMetrics
// handle, and no RL bridge configuration. *Client satisfies this interface.
type graphMemoryJudgeKicker interface {
	KickGraphMemoryJudge(ctx context.Context, runtimeID string, payload protocol.GraphMemoryJudgeKickPayload) error
}

// graphMemoryProvider wires the memorygraph subsystem into the daemon's
// execution-memory injection path (design §5.2): hybrid retrieval plus the
// explore agent replace the legacy scoped-file injection when
// memory_type=graph.
type graphMemoryProvider struct {
	store    *memorygraph.Store
	retr     *memorygraph.HybridRetriever
	explorer *memorygraph.Explorer
	recorder *memorygraph.QueryRecorder
	kicker   graphMemoryJudgeKicker // nil → judge kick skipped silently
	logger   *slog.Logger
	metrics  graphMemoryMetrics

	// retrMu guards the lazy retriever build: the indexes are built on first
	// use and rebuilt whenever the store's current-version pointer moved or
	// the staging area changed (staging signatures are re-stat'ed at most
	// once per graphMemoryStagingCheckInterval).
	retrMu           sync.Mutex
	retrBuilt        bool
	retrVersion      int
	retrStaging      stagingSignature
	lastStagingCheck time.Time
}

// stagingSignature is the cheap change-detection fingerprint of the staging
// segments dir: file count plus the maximum mtime.
type stagingSignature struct {
	count  int
	maxMod time.Time
}

// stagingSignatureOf stats the store's staging/segments dir. A missing dir
// yields the zero signature.
func stagingSignatureOf(storeRoot string) (stagingSignature, error) {
	entries, err := os.ReadDir(filepath.Join(storeRoot, "staging", "segments"))
	if err != nil {
		if os.IsNotExist(err) {
			return stagingSignature{}, nil
		}
		return stagingSignature{}, fmt.Errorf("stat staging segments: %w", err)
	}
	var sig stagingSignature
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sig.count++
		if info.ModTime().After(sig.maxMod) {
			sig.maxMod = info.ModTime()
		}
	}
	return sig, nil
}

// newGraphMemoryProvider builds the provider from daemon config. A missing
// pi CLI is a hard initialization error (the daemon then falls back to
// legacy memory injection permanently); a missing embedding endpoint only
// degrades retrieval to BM25-only (ErrEmbedNotConfigured, design §5.2).
// kicker may be nil, in which case the async judge kick is skipped.
func newGraphMemoryProvider(cfg Config, kicker graphMemoryJudgeKicker, logger *slog.Logger) (*graphMemoryProvider, error) {
	entry, ok := cfg.Agents[agentpkg.ProviderPi]
	if !ok || strings.TrimSpace(entry.Path) == "" {
		return nil, fmt.Errorf("graph memory: no pi CLI configured (MULTICA_PI_PATH)")
	}
	store := memorygraph.NewStore(cfg.GraphMemoryDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("graph memory: init store %s: %w", cfg.GraphMemoryDir, err)
	}

	var cached *memorygraph.CachedEmbedder
	emb, err := memorygraph.NewOpenAIEmbedderFromEnv()
	switch {
	case errors.Is(err, memorygraph.ErrEmbedNotConfigured):
		logger.Info("graph memory: embedding endpoint not configured; retrieval is BM25-only")
	case err != nil:
		return nil, fmt.Errorf("graph memory: %w", err)
	default:
		cached = memorygraph.NewCachedEmbedder(emb, store)
	}

	retr := memorygraph.NewHybridRetriever(store, cached, memorygraph.DefaultRetrievalConfig())
	backend, err := agentpkg.New(agentpkg.ProviderPi, agentpkg.Config{ExecutablePath: strings.TrimSpace(entry.Path), Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("graph memory: pi backend: %w", err)
	}
	explorerCfg := memorygraph.DefaultExploreConfig()
	explorerCfg.Agents = cfg.GraphExploreAgents
	explorerCfg.MaxRounds = cfg.GraphExploreMaxRounds
	explorerCfg.Model = strings.TrimSpace(entry.Model)
	return &graphMemoryProvider{
		store:    store,
		retr:     retr,
		explorer: memorygraph.NewExplorer(store, retr, backend, explorerCfg, agentpkg.ProviderPi, memorygraph.NewTraceRecorder(cfg.GraphMemoryDir)),
		recorder: memorygraph.NewQueryRecorder(store, graphMemoryQueryLogWindow),
		kicker:   kicker,
		logger:   logger,
		metrics:  noopGraphMemoryMetrics{},
	}, nil
}

// ensureRetriever builds the hybrid indexes on first use and rebuilds them
// after the store's current version pointer moved or the staging area
// changed (design §5.1 step 3: a freshly staged segment is retrievable
// without waiting for the next consolidation switch). The staging stat is
// throttled to once per graphMemoryStagingCheckInterval per provider.
func (p *graphMemoryProvider) ensureRetriever(ctx context.Context) error {
	p.retrMu.Lock()
	defer p.retrMu.Unlock()
	current, err := p.store.CurrentVersion()
	if err != nil {
		return fmt.Errorf("graph memory: current version: %w", err)
	}
	if p.retrBuilt && p.retrVersion == current {
		if time.Since(p.lastStagingCheck) < graphMemoryStagingCheckInterval {
			return nil
		}
		p.lastStagingCheck = time.Now()
		sig, err := stagingSignatureOf(p.store.Root)
		if err != nil {
			return err
		}
		if sig == p.retrStaging {
			return nil
		}
		if err := p.retr.Rebuild(ctx); err != nil {
			return err
		}
		p.retrStaging = sig
		return nil
	}
	if err := p.retr.Rebuild(ctx); err != nil {
		return err
	}
	p.retrBuilt = true
	p.retrVersion = current
	if sig, err := stagingSignatureOf(p.store.Root); err == nil {
		p.retrStaging = sig
	}
	p.lastStagingCheck = time.Now()
	return nil
}

// prepareGraphExecutionMemory recalls graph memory for query and maps the
// adopted explore trajectory into a single MemoryContextForEnv entry whose
// content keeps the exact "## Graph Memory Recall" shape, so
// renderPromotedMemorySnapshot needs no changes. A recall miss (Found=false)
// is data, not an error: it returns a nil slice (no injection).
func (p *graphMemoryProvider) prepareGraphExecutionMemory(ctx context.Context, taskID, runtimeID, query string) ([]execenv.MemoryContextForEnv, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if err := p.ensureRetriever(ctx); err != nil {
		return nil, err
	}
	recall, err := p.explorer.Explore(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("graph memory: explore: %w", err)
	}
	p.metrics.RecordGraphMemoryRecall(recall.Found, recall.Rounds)
	p.metrics.ObserveGraphExploreRounds(recall.Rounds)

	if err := p.recorder.RecordRecall(memorygraph.QueryLogEntry{
		TraceID:   recall.TraceID,
		Query:     query,
		Timestamp: time.Now().UTC(),
		Version:   recall.Version,
		NodeIDs:   recall.NodeIDs,
		Rounds:    recall.Rounds,
		AgentRuns: len(recall.AgentRuns),
		Found:     recall.Found,
	}); err != nil {
		p.logger.Warn("graph memory: query log append failed", "trace_id", recall.TraceID, "error", err)
	}

	if !recall.Found {
		return nil, nil
	}
	p.notifyAsyncJudge(taskID, runtimeID, query, recall)
	return []execenv.MemoryContextForEnv{{
		Name:    "Graph memory recall",
		Content: "## Graph Memory Recall\n" + strings.TrimSpace(recall.Summary) + "\n\n cited nodes: " + strings.Join(recall.NodeIDs, ", "),
		Scope:   "workspace",
	}}, nil
}

// notifyAsyncJudge reports a successful recall to the server, which runs the
// async judge + delayed-reward flow (design §5.3, Q28) server-side — the
// judge needs the downstream task history from the DB (Q18) and the reward
// push needs the RL bridge configuration, neither of which exists
// daemon-side (review P0-2). Fire-and-forget: the recall path never blocks
// on judging and failures are only logged.
func (p *graphMemoryProvider) notifyAsyncJudge(taskID, runtimeID, query string, recall *memorygraph.RecallResult) {
	if p.kicker == nil || taskID == "" {
		return
	}
	payload := protocol.GraphMemoryJudgeKickPayload{
		TraceID: recall.TraceID,
		TaskID:  taskID,
		Query:   query,
		Summary: recall.Summary,
		NodeIDs: recall.NodeIDs,
		Rounds:  recall.Rounds,
		Version: recall.Version,
	}
	for _, run := range recall.AgentRuns {
		payload.AgentRuns = append(payload.AgentRuns, protocol.GraphMemoryExploreRunPayload{
			RunID:  run.RunID,
			Seed:   run.Seed,
			Found:  run.Found,
			Rounds: run.Rounds,
			Error:  run.Error,
		})
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := p.kicker.KickGraphMemoryJudge(ctx, runtimeID, payload); err != nil {
			p.logger.Warn("graph memory: judge kick failed", "trace_id", recall.TraceID, "error", err)
		}
	}()
}

// graphMemory lazily initializes the graph memory provider. Initialization
// failure is permanent for the process lifetime (sync.Once) and falls back
// to legacy memory injection with a single warn log.
func (d *Daemon) graphMemory() *graphMemoryProvider {
	d.graphMemoryOnce.Do(func() {
		provider, err := newGraphMemoryProvider(d.cfg, d.client, d.logger)
		if err != nil {
			d.logger.Warn("graph memory provider initialization failed; using legacy memory injection", "error", err)
			return
		}
		d.graphMemoryProv = provider
	})
	return d.graphMemoryProv
}

// effectiveMemoryType resolves the reviewer type for one task (design
// §1/A4): a valid task-scoped override from the server payload wins over
// the process-level env default (cfg.MemoryType, already validated by
// LoadConfig); anything unrecognized falls back to the env default.
func effectiveMemoryType(configured, taskScoped string) string {
	switch strings.ToLower(strings.TrimSpace(taskScoped)) {
	case MemoryTypeLegacy:
		return MemoryTypeLegacy
	case MemoryTypeGraph:
		return MemoryTypeGraph
	}
	if configured == MemoryTypeGraph {
		return MemoryTypeGraph
	}
	return MemoryTypeLegacy
}

// graphExecutionMemories returns graph-recalled execution memory when the
// graph reviewer is active and the recall found something, or nil — the
// caller then keeps the legacy prepareTurnScopeMemory result and may merge
// extras via mergeExecutionMemories. Graph errors never break task execution:
// they are logged and fall back to legacy.
func (d *Daemon) graphExecutionMemories(ctx context.Context, task Task, log *slog.Logger) []execenv.MemoryContextForEnv {
	if effectiveMemoryType(d.cfg.MemoryType, task.MemoryType) != MemoryTypeGraph {
		return nil
	}
	provider := d.graphMemory()
	if provider == nil {
		return nil
	}
	query := graphRecallQuery(task)
	if query == "" {
		return nil
	}
	memories, err := provider.prepareGraphExecutionMemory(ctx, task.ID, task.RuntimeID, query)
	if err != nil {
		log.Warn("graph memory recall failed; falling back to legacy memory", "task_id", task.ID, "error", err)
		return nil
	}
	return memories
}

// graphRecallQuery picks the user-authored text of the task as the recall
// query.
func graphRecallQuery(task Task) string {
	for _, candidate := range []string{
		task.ChatMessage,
		task.TriggerCommentContent,
		task.QuickCreatePrompt,
		task.AgentRadarPrompt,
		task.AutopilotDescription,
	} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
