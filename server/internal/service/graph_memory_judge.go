// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/util"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// graphMemoryJudgeQueryWindow is the query_log window the daemon appends
// recall entries to; judge write-back targets the same window.
const graphMemoryJudgeQueryWindow = "daemon"

// GraphMemoryJudgeRequest is the daemon's recall report: one found graph
// memory recall handed to the downstream agent, awaiting its async judge.
type GraphMemoryJudgeRequest struct {
	TraceID   string
	TaskID    string // agent_run_id of the downstream task the recall served
	Query     string
	Summary   string
	NodeIDs   []string
	Rounds    int
	Version   int
	AgentRuns []memorygraph.ExploreRun
}

// GraphMemoryJudgeService runs the async judge + delayed-reward flow
// server-side (design §5.3, Q18/Q28; review P0-2/R3).
//
// Why server-side: kickAsyncJudge lives at the recall site in the daemon,
// but the daemon has no MessageStore access for the judge's downstream
// history (Q18: the judge sees the same history as the downstream agent),
// no BusinessMetrics handle, and no RL bridge configuration. The daemon
// therefore reports each recall over a narrow daemon-auth HTTP endpoint and
// this service performs the judge, the query-log write-back (with the R10
// baseline coverage signal), the judge metric, and the reward composition
// here, where all three dependencies exist.
type GraphMemoryJudgeService struct {
	queries  *db.Queries
	root     string // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT per call
	bm       *obsmetrics.BusinessMetrics
	model    string
	judgeCfg memorygraph.JudgeConfig

	// rewards composes delayed explore rewards; sink resolves trace ids to
	// session proxy keys. Both are nil when the RL bridge is not configured
	// (training disabled): judging and ground-truth write-back still run,
	// only the reward push is skipped.
	rewards *memorygraph.RewardComposer
	sink    *arealrl.RewardSink

	backendOnce sync.Once
	backend     memorygraph.AgentBackend
	backendErr  error
}

// NewGraphMemoryJudgeService returns the server-side judge service. rl may
// be nil (RL bridge unconfigured), in which case the delayed-reward flow is
// disabled and only judging/write-back runs. rewardTimeout <= 0 falls back
// to memorygraph.DefaultRewardTimeout.
func NewGraphMemoryJudgeService(queries *db.Queries, root string, bm *obsmetrics.BusinessMetrics, rl *arealrl.Client, rewardTimeout time.Duration) *GraphMemoryJudgeService {
	s := &GraphMemoryJudgeService{
		queries:  queries,
		root:     root,
		bm:       bm,
		model:    strings.TrimSpace(os.Getenv("MULTICA_PI_MODEL")),
		judgeCfg: memorygraph.DefaultJudgeConfig(),
	}
	s.judgeCfg.Model = s.model
	if rl != nil {
		s.sink = arealrl.NewRewardSink(rl)
		s.rewards = memorygraph.NewRewardComposer(s.sink, memorygraph.DefaultRewardParams(), rewardTimeout)
	}
	return s
}

// JudgeTimeout returns the configured wall-clock timeout of one judge run;
// handlers use it to bound the detached judge goroutine.
func (s *GraphMemoryJudgeService) JudgeTimeout() time.Duration { return s.judgeCfg.Timeout }

// JudgeRecall judges one reported recall: it loads the downstream history,
// runs the judge agent, records the outcome metric, writes the result (with
// the judge-time baseline coverage signal, R10) back to the query log, and
// composes the delayed reward. Best-effort end to end: individual step
// failures are logged and abort the flow without failing the caller's task.
func (s *GraphMemoryJudgeService) JudgeRecall(ctx context.Context, req GraphMemoryJudgeRequest) error {
	if s.queries == nil {
		return errors.New("graph memory judge: queries not configured")
	}
	if req.TraceID == "" || req.TaskID == "" {
		return errors.New("graph memory judge: trace_id and task_id are required")
	}

	taskUUID, err := util.ParseUUID(req.TaskID)
	if err != nil {
		return fmt.Errorf("graph memory judge: parse task_id %q: %w", req.TaskID, err)
	}
	task, err := s.queries.GetAgentTask(ctx, taskUUID)
	if err != nil {
		return fmt.Errorf("graph memory judge: load task %s: %w", req.TaskID, err)
	}

	root, err := s.workspacesRoot()
	if err != nil {
		return err
	}
	dir, ok := graphMemoryDirForWorkspace(root, util.UUIDToString(task.WorkspaceID))
	if !ok {
		// No memory_graph dir for this workspace: the recall was recorded
		// nowhere the server can see; skip silently.
		return nil
	}
	store := memorygraph.NewStore(dir)

	recall := &memorygraph.RecallResult{
		TraceID:   req.TraceID,
		Summary:   req.Summary,
		NodeIDs:   req.NodeIDs,
		Rounds:    req.Rounds,
		Version:   req.Version,
		AgentRuns: req.AgentRuns,
		Found:     true,
	}

	// Buffer the trajectory for delayed reward composition (Q28). The trace
	// is registered with the session proxy key of the owning task (task
	// context areal_proxy, see maybeOpenTrainingSession); a non-training
	// task has no proxy key and its reward is skipped silently by the sink.
	if s.rewards != nil {
		if cfg, ok := extractArealProxyConfig(task.Context); ok && cfg.APIKey != "" {
			s.sink.RegisterTrace(req.TraceID, cfg.APIKey)
		}
		if err := s.rewards.Submit(ctx, req.TraceID, recall); err != nil {
			slog.Warn("graph memory judge: reward submit failed", "trace_id", req.TraceID, "error", err)
		}
	}

	history, err := NewGraphMemoryHistoryProvider(s.queries, req.TaskID).DownstreamHistory(ctx, req.TraceID)
	if err != nil {
		return fmt.Errorf("graph memory judge: history lookup: %w", err)
	}

	backend, err := s.agentBackend()
	if err != nil {
		return err
	}
	judge := memorygraph.NewJudge(backend, s.judgeCfg, "pi")
	res, err := judge.Judge(ctx, req.Query, recall, history)
	if err != nil {
		return fmt.Errorf("graph memory judge: %w", err)
	}
	s.bm.RecordGraphJudge(res.Score >= s.judgeCfg.RelevanceThreshold)

	// R10: the baseline coverage signal is computed on the CURRENT version
	// at judge time (not hard-coded): hybrid top-k hits plus the n-hop
	// coverage check, n = the adopted path's explore rounds.
	baseline := s.computeBaseline(ctx, store, req.Query, res.RelevantNodes, req.Rounds)
	found, err := memorygraph.NewQueryRecorder(store, graphMemoryJudgeQueryWindow).ApplyJudge(req.TraceID, res, baseline)
	if err != nil {
		slog.Warn("graph memory judge: write-back failed", "trace_id", req.TraceID, "error", err)
	} else if !found {
		slog.Warn("graph memory judge: no query log entry for trace", "trace_id", req.TraceID, "dir", dir)
	}

	if s.rewards != nil {
		if err := s.rewards.OnJudgeResult(ctx, req.TraceID, res); err != nil {
			slog.Warn("graph memory judge: reward push failed", "trace_id", req.TraceID, "error", err)
		}
	}
	return nil
}

// computeBaseline builds a retriever + graph over the store's current
// version and computes the A2 coverage signal. A failure (e.g. unreadable
// current version) yields the zero signal — the write-back still records the
// judge score and ground truth.
func (s *GraphMemoryJudgeService) computeBaseline(ctx context.Context, store *memorygraph.Store, query string, groundTruth []string, adoptedRounds int) memorygraph.BaselineSignal {
	current, err := store.CurrentVersion()
	if err != nil {
		slog.Warn("graph memory judge: baseline current version", "error", err)
		return memorygraph.BaselineSignal{}
	}
	g, err := memorygraph.LoadGraph(store, current)
	if err != nil {
		slog.Warn("graph memory judge: baseline load graph", "version", current, "error", err)
		return memorygraph.BaselineSignal{}
	}
	retr := memorygraph.NewHybridRetriever(store, graphMemoryCachedEmbedder(store), memorygraph.DefaultRetrievalConfig())
	if err := retr.RebuildForVersion(ctx, current); err != nil {
		slog.Warn("graph memory judge: baseline retriever rebuild", "version", current, "error", err)
		return memorygraph.BaselineSignal{}
	}
	return memorygraph.ComputeBaselineCoverage(ctx, retr, g, query, groundTruth, adoptedRounds)
}

// RunRewardSweep runs RewardComposer.SweepTimeouts on a ticker until ctx is
// cancelled (design Q28, review R15): pending traces whose judge result
// never arrived are pushed the miss penalty. It is a no-op when the reward
// flow is disabled (RL bridge unconfigured).
func (s *GraphMemoryJudgeService) RunRewardSweep(ctx context.Context, interval time.Duration) {
	if s.rewards == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			swept, err := s.rewards.SweepTimeouts(ctx)
			if err != nil {
				slog.Warn("graph memory reward sweep failed", "error", err)
			} else if swept > 0 {
				slog.Info("graph memory reward sweep", "swept", swept)
			}
		}
	}
}

// workspacesRoot returns the configured root or resolves the env default.
func (s *GraphMemoryJudgeService) workspacesRoot() (string, error) {
	if s.root != "" {
		return s.root, nil
	}
	return graphMemoryWorkspacesRoot()
}

// agentBackend lazily builds the pi agent backend for the judge (same env
// contract as the scheduler's graphMemoryPIBackend: MULTICA_PI_PATH).
func (s *GraphMemoryJudgeService) agentBackend() (memorygraph.AgentBackend, error) {
	s.backendOnce.Do(func() {
		path := strings.TrimSpace(os.Getenv("MULTICA_PI_PATH"))
		if path == "" {
			path = "pi"
		}
		backend, err := agentpkg.New("pi", agentpkg.Config{ExecutablePath: path})
		if err != nil {
			s.backendErr = fmt.Errorf("graph memory judge: pi backend: %w", err)
			return
		}
		s.backend = backend
	})
	return s.backend, s.backendErr
}

// graphMemoryCachedEmbedder builds the shared embedding cache for a store
// from the embed endpoint env contract (MULTICA_GRAPH_EMBED_*); it returns
// nil when no endpoint is configured, degrading retrieval to BM25-only.
func graphMemoryCachedEmbedder(store *memorygraph.Store) *memorygraph.CachedEmbedder {
	emb, err := memorygraph.NewOpenAIEmbedderFromEnv()
	if err != nil {
		return nil // ErrEmbedNotConfigured: BM25-only; other errors degrade the same way
	}
	return memorygraph.NewCachedEmbedder(emb, store)
}
