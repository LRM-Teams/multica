package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const JobNameGraphMemoryConsolidation = "graph_memory_consolidation"

// graphConsolidationStateFile is the small watermark file under the
// workspaces root recording per-directory consolidation state, so the
// dual-threshold trigger (design Q10), the time-based fallback and the
// failure backoff (design Q10/A3) survive restarts.
const graphConsolidationStateFile = ".consolidation_state.json"

// graphConsolidationMaxStaleness is the age of the last successful
// consolidation beyond which a single new staging segment suffices to
// trigger (design Q10/A3 time-based fallback).
const graphConsolidationMaxStaleness = 24 * time.Hour

// graphConsolidationBackoffThreshold is the number of consecutive failed or
// no-switch consolidations after which the dir is skipped until new staging
// segments arrive beyond the watermark recorded at backoff entry (A3).
const graphConsolidationBackoffThreshold = 3

// graphDirState is the per-memory_graph-directory consolidation state.
type graphDirState struct {
	// LastConsolidated is the watermark the dual-threshold trigger counts
	// "new" segments/queries against; updated on every consolidation that
	// ran without error.
	LastConsolidated time.Time `json:"last_consolidated"`
	// LastSuccessAt is the last time Consolidate completed without error
	// (A3 time-based fallback). Zero when the dir has never succeeded (or
	// the state predates A3); a zero value is treated as maximally stale.
	LastSuccessAt time.Time `json:"last_success_at,omitempty"`
	// ConsecutiveNoSwitch counts consecutive consolidations that errored or
	// (in TTT mode) completed without a version switch.
	ConsecutiveNoSwitch int `json:"consecutive_no_switch,omitempty"`
	// Backoff is true while the dir is in failure backoff: consolidation is
	// skipped until the total staging-segment count exceeds
	// BackoffWatermark.
	Backoff bool `json:"backoff,omitempty"`
	// BackoffWatermark is the total staging-segment count observed when
	// backoff was entered.
	BackoffWatermark int `json:"backoff_watermark,omitempty"`
}

// graphConsolidationState is <workspaces root>/.consolidation_state.json.
// LastConsolidated is the pre-A3 flat format, migrated into Dirs on load.
type graphConsolidationState struct {
	Dirs             map[string]*graphDirState `json:"dirs"`
	LastConsolidated map[string]time.Time      `json:"last_consolidated,omitempty"` // legacy, read-only
}

// dir returns the state for one memory_graph directory, creating it on
// first observation.
func (s *graphConsolidationState) dir(path string) *graphDirState {
	ds, ok := s.Dirs[path]
	if !ok {
		ds = &graphDirState{}
		s.Dirs[path] = ds
	}
	return ds
}

// GraphMemoryJobs returns the graph memory consolidation job (design §5.4).
//
// reviewer.type resolution (design §1/A4) is per workspace: the
// graph_memory_profile row for the workspace a memory_graph directory
// belongs to wins; directories without a profile row (or the root-level
// default layout, which maps to no workspace) fall back to the server
// process env MULTICA_REVIEWER_TYPE, then "legacy". pool may be nil (tests,
// DB-less deployments), in which case only the env fallback is available.
// bm may be nil; every BusinessMetrics method tolerates a nil receiver.
func GraphMemoryJobs(pool *pgxpool.Pool, bm *obsmetrics.BusinessMetrics) JobSpec {
	return JobSpec{
		Name:              JobNameGraphMemoryConsolidation,
		Cadence:           time.Hour,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		MaxPlansPerTick:   1,
		RunTimeout:        30 * time.Minute,
		StaleTimeout:      45 * time.Minute,
		HeartbeatInterval: time.Minute,
		AllowStaleReentry: true,
		MaxAttempts:       2,
		RetryBackoff:      []time.Duration{10 * time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeGraphMemoryConsolidationHandler(pool, bm),
	}
}

// graphReviewerTypeLookup resolves the per-workspace reviewer type from
// graph_memory_profile; it must return an error (e.g. pgx.ErrNoRows) when
// no profile row exists.
type graphReviewerTypeLookup func(ctx context.Context, workspaceID string) (string, error)

// graphMemoryProfileLookup builds the lookup over the sqlc queries; nil
// pool yields a nil lookup (env-only resolution).
func graphMemoryProfileLookup(pool *pgxpool.Pool) graphReviewerTypeLookup {
	if pool == nil {
		return nil
	}
	q := db.New(pool)
	return func(ctx context.Context, workspaceID string) (string, error) {
		profile, err := q.GetGraphMemoryProfile(ctx, pgtype.UUID{Bytes: uuid.MustParse(workspaceID), Valid: true})
		if err != nil {
			return "", err
		}
		return profile.ReviewerType, nil
	}
}

// resolveGraphMemoryReviewerType resolves reviewer.type for one
// memory_graph directory (design §1/A4): the per-workspace profile row wins
// when the directory maps to a workspace (<root>/<workspace>/memory_graph);
// otherwise the MULTICA_REVIEWER_TYPE env default applies, then "legacy".
// Lookup errors (including a missing row) fail open to the env default so a
// transient DB hiccup never flips a workspace's memory pipeline.
func resolveGraphMemoryReviewerType(ctx context.Context, dir, envType string, lookup graphReviewerTypeLookup) string {
	if lookup != nil {
		if wsID, ok := graphDirWorkspaceID(dir); ok {
			if rt, err := lookup(ctx, wsID); err == nil && (rt == "legacy" || rt == "graph") {
				return rt
			}
		}
	}
	if envType == "graph" || envType == "legacy" {
		return envType
	}
	return "legacy"
}

// graphDirWorkspaceID extracts the workspace UUID from a per-workspace
// <root>/<workspace>/memory_graph directory; the root-level default layout
// maps to no workspace.
func graphDirWorkspaceID(dir string) (string, bool) {
	parent := filepath.Base(filepath.Dir(dir))
	if _, err := uuid.Parse(parent); err != nil {
		return "", false
	}
	return parent, true
}

func makeGraphMemoryConsolidationHandler(pool *pgxpool.Pool, bm *obsmetrics.BusinessMetrics) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		envType := strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_REVIEWER_TYPE")))
		lookup := graphMemoryProfileLookup(pool)
		if lookup == nil && envType != "graph" {
			// Without a DB there is no per-workspace override to resolve; the
			// env default gates the whole sweep.
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "reviewer_not_graph"}}, nil
		}
		root, err := graphMemoryWorkspacesRoot()
		if err != nil {
			return HandlerResult{}, err
		}
		dirs, err := findMemoryGraphDirs(root)
		if err != nil {
			return HandlerResult{}, err
		}
		if len(dirs) == 0 {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "no_memory_graph_dirs", "root": root}}, nil
		}
		state := loadGraphConsolidationState(root)

		consolidated := 0
		for _, dir := range dirs {
			// Per-workspace reviewer.type (A4): only graph-enabled dirs
			// consolidate; the rest are logged and skipped.
			if rt := resolveGraphMemoryReviewerType(ctx, dir, envType, lookup); rt != "graph" {
				slog.Info("graph memory consolidation skipped: reviewer not graph", "dir", dir, "reviewer_type", rt)
				continue
			}
			// Per-workspace errors are logged and the loop continues: one
			// broken graph must not starve the others.
			ok, err := consolidateOneGraph(ctx, dir, state.dir(dir), bm)
			if err != nil {
				slog.Warn("graph memory consolidation failed", "dir", dir, "error", err)
				continue
			}
			if ok {
				consolidated++
			}
			if in.Heartbeat != nil {
				if err := in.Heartbeat(ctx); err != nil {
					return HandlerResult{}, err
				}
			}
		}
		if err := saveGraphConsolidationState(root, state); err != nil {
			slog.Warn("graph memory consolidation state write failed", "root", root, "error", err)
		}
		return HandlerResult{
			RowsAffected: int64(consolidated),
			Result:       map[string]any{"graph_dirs": len(dirs), "consolidated": consolidated},
		}, nil
	}
}

// graphConsolidationRunner runs one consolidation against a store; it is a
// function value so tests can drive the trigger/backoff state machine
// without a pi backend.
type graphConsolidationRunner func(ctx context.Context) (*memorygraph.ConsolidateResult, error)

// consolidateOneGraph runs one gated consolidation cycle against the
// memory_graph store at dir, updating ds in place. It reports whether a
// consolidation ran.
func consolidateOneGraph(ctx context.Context, dir string, ds *graphDirState, bm *obsmetrics.BusinessMetrics) (bool, error) {
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return false, fmt.Errorf("init store: %w", err)
	}
	cfg := memorygraph.DefaultConsolidateConfig()
	cfg.Model = strings.TrimSpace(os.Getenv("MULTICA_PI_MODEL"))
	run := func(ctx context.Context) (*memorygraph.ConsolidateResult, error) {
		backend, err := graphMemoryPIBackend()
		if err != nil {
			return nil, err
		}
		consolidator := memorygraph.NewConsolidator(store, backend, cfg, "pi", nil, memorygraph.NewTraceRecorder(dir))
		if cfg.TTVTrajectories > 1 {
			// R2: wire the production full-backtest runner so a candidate
			// whose retrieval coverage rises runs a real explore backtest
			// (rebuilt against the candidate version) instead of taking the
			// conservative pass. The embedder (when configured) keeps the
			// backtest retrieval identical to production (A2).
			emb := graphMemoryEmbedder(store)
			exploreCfg := memorygraph.DefaultExploreConfig()
			exploreCfg.Model = cfg.Model
			consolidator.SetRunner(memorygraph.NewExploreBacktestRunner(store, emb, backend, memorygraph.DefaultRetrievalConfig(), exploreCfg, "pi"))
			consolidator.SetEmbedder(emb)
		}
		return consolidator.Consolidate(ctx)
	}
	return consolidateOneGraphWith(ctx, dir, store, cfg, ds, bm, run, time.Now().UTC())
}

// graphMemoryEmbedder builds the shared embedding cache for a store from the
// MULTICA_GRAPH_EMBED_* env contract; it returns nil when no endpoint is
// configured (BM25-only retrieval, same as the daemon).
func graphMemoryEmbedder(store *memorygraph.Store) *memorygraph.CachedEmbedder {
	emb, err := memorygraph.NewOpenAIEmbedderFromEnv()
	if err != nil {
		return nil
	}
	return memorygraph.NewCachedEmbedder(emb, store)
}

// consolidateOneGraphWith implements the A3-hardened trigger (design Q10):
// the dual threshold, a 24h time-based fallback, and a failure backoff that
// skips dirs after graphConsolidationBackoffThreshold consecutive errored
// or no-switch consolidations until new staging segments arrive.
func consolidateOneGraphWith(ctx context.Context, dir string, store *memorygraph.Store, cfg memorygraph.ConsolidateConfig, ds *graphDirState, bm *obsmetrics.BusinessMetrics, run graphConsolidationRunner, now time.Time) (bool, error) {
	newSegments, totalStaging, err := countStagingSegmentsSince(store, ds.LastConsolidated)
	if err != nil {
		return false, err
	}
	queries, err := countQueryLogEntriesSince(store, ds.LastConsolidated)
	if err != nil {
		return false, err
	}

	// Failure backoff (A3): skip until the total staging count grows past
	// the watermark recorded at backoff entry, i.e. until genuinely new
	// material arrives.
	if ds.Backoff {
		if totalStaging > ds.BackoffWatermark {
			ds.Backoff = false
			ds.ConsecutiveNoSwitch = 0
			slog.Info("graph memory consolidation backoff cleared: new staging segments arrived",
				"dir", dir, "staging", totalStaging, "watermark", ds.BackoffWatermark)
		} else {
			slog.Info("graph memory consolidation skipped: failure backoff active",
				"dir", dir, "staging", totalStaging, "watermark", ds.BackoffWatermark,
				"consecutive_no_switch", ds.ConsecutiveNoSwitch)
			return false, nil
		}
	}

	// Dual-threshold trigger (Q10), plus the time-based fallback (A3): at
	// least one new staging segment and no successful consolidation in over
	// graphConsolidationMaxStaleness triggers anyway. A zero LastSuccessAt
	// (never consolidated, or a pre-A3 state file) counts as maximally
	// stale.
	reason := ""
	switch {
	case memorygraph.ShouldConsolidate(newSegments, queries, cfg):
		reason = "threshold"
	case newSegments >= 1 && now.Sub(ds.LastSuccessAt) > graphConsolidationMaxStaleness:
		reason = "time_fallback"
	default:
		slog.Info("graph memory consolidation skipped: trigger not met",
			"dir", dir, "new_segments", newSegments, "queries", queries,
			"last_success_at", ds.LastSuccessAt)
		return false, nil
	}
	slog.Info("graph memory consolidation triggered", "dir", dir, "reason", reason,
		"new_segments", newSegments, "queries", queries)

	res, err := run(ctx)
	if err != nil {
		recordConsolidationFailure(ds, totalStaging, dir, "error: "+err.Error())
		return false, fmt.Errorf("consolidate: %w", err)
	}

	ds.LastConsolidated = now
	ds.LastSuccessAt = now
	// A TTT consolidation that completes without a version switch counts
	// toward the backoff (A3/R2: a systematically-failing TTT must not burn
	// T trajectories every hour). Non-TTT in-place consolidations never
	// switch by design (Q16), so a clean non-TTT run resets the counter.
	switch {
	case res.Switched:
		ds.ConsecutiveNoSwitch = 0
		ds.Backoff = false
		bm.RecordGraphVersionSwitch()
	case cfg.TTVTrajectories > 1:
		recordConsolidationFailure(ds, totalStaging, dir, "no version switch")
	default:
		ds.ConsecutiveNoSwitch = 0
		ds.Backoff = false
	}
	if ratio, ok := backtestBypassRatio(res); ok {
		bm.RecordGraphBacktestBypass(ratio)
	}
	return true, nil
}

// recordConsolidationFailure increments the consecutive failure/no-switch
// counter and enters backoff once the threshold is reached, watermarking
// the current total staging count (A3).
func recordConsolidationFailure(ds *graphDirState, totalStaging int, dir, cause string) {
	ds.ConsecutiveNoSwitch++
	if ds.ConsecutiveNoSwitch >= graphConsolidationBackoffThreshold && !ds.Backoff {
		ds.Backoff = true
		ds.BackoffWatermark = totalStaging
		slog.Info("graph memory consolidation entering failure backoff",
			"dir", dir, "consecutive_no_switch", ds.ConsecutiveNoSwitch,
			"staging", totalStaging, "cause", cause)
	}
}

// graphMemoryPIBackend builds the pi agent backend for the consolidation
// trajectory, reading the same env contract the daemon uses
// (MULTICA_PI_PATH / MULTICA_PI_MODEL).
func graphMemoryPIBackend() (memorygraph.AgentBackend, error) {
	path := strings.TrimSpace(os.Getenv("MULTICA_PI_PATH"))
	if path == "" {
		path = "pi"
	}
	backend, err := agentpkg.New("pi", agentpkg.Config{ExecutablePath: path})
	if err != nil {
		return nil, fmt.Errorf("pi backend: %w", err)
	}
	return backend, nil
}

// countStagingSegmentsSince counts staging segment files modified after
// since and also returns the total staging count (staging segments are
// immutable and kept for provenance after consolidation (design §5.1), so
// the modtime watermark is the "new" predicate and the total is monotonic,
// which the A3 failure backoff watermarks against).
func countStagingSegmentsSince(store *memorygraph.Store, since time.Time) (newCount, total int, err error) {
	ids, err := store.ListStagingSegments()
	if err != nil {
		return 0, 0, fmt.Errorf("list staging segments: %w", err)
	}
	for _, id := range ids {
		info, err := os.Stat(filepath.Join(store.Root, "staging", "segments", id+".md"))
		if err != nil {
			return 0, 0, fmt.Errorf("stat staging segment %s: %w", id, err)
		}
		if info.ModTime().After(since) {
			newCount++
		}
	}
	return newCount, len(ids), nil
}

// countQueryLogEntriesSince counts query-log entries recorded after since
// across all windows (design §5.2).
func countQueryLogEntriesSince(store *memorygraph.Store, since time.Time) (int, error) {
	windows, err := store.ListQueryLogWindows()
	if err != nil {
		return 0, fmt.Errorf("list query log windows: %w", err)
	}
	count := 0
	for _, window := range windows {
		entries, err := store.ReadQueryLog(window)
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			if e.Timestamp.After(since) {
				count++
			}
		}
	}
	return count, nil
}

// backtestBypassRatio computes the backtest bypass rate (回测免跑率, design
// §7): the fraction of backtested queries accepted on graph distance alone
// without a full agent explore run. ok is false when no query was
// backtested.
func backtestBypassRatio(res *memorygraph.ConsolidateResult) (float64, bool) {
	total, bypassed := 0, 0
	for _, cand := range res.Candidates {
		for _, q := range cand.Queries {
			total++
			if q.AcceptedWithoutExplore {
				bypassed++
			}
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(bypassed) / float64(total), true
}

// graphMemoryWorkspacesRoot resolves the workspaces root on the server from
// MULTICA_WORKSPACES_ROOT (same contract as the daemon), defaulting to
// ~/.multica/workspaces.
func graphMemoryWorkspacesRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("MULTICA_WORKSPACES_ROOT"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w (set MULTICA_WORKSPACES_ROOT to override)", err)
		}
		root = filepath.Join(home, ".multica", "workspaces")
	}
	return filepath.Abs(root)
}

// findMemoryGraphDirs locates memory_graph directories under the workspaces
// root: the root-level default layout (<root>/memory_graph, matching the
// daemon's default GraphMemoryDir) plus any per-workspace
// <root>/<workspace>/memory_graph.
func findMemoryGraphDirs(root string) ([]string, error) {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if seen[dir] {
			return
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	add(filepath.Join(root, "memory_graph"))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspaces root %s: %w", root, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			add(filepath.Join(root, entry.Name(), "memory_graph"))
		}
	}
	return dirs, nil
}

func loadGraphConsolidationState(root string) *graphConsolidationState {
	state := &graphConsolidationState{Dirs: map[string]*graphDirState{}}
	b, err := os.ReadFile(filepath.Join(root, graphConsolidationStateFile))
	if err != nil {
		return state // missing or unreadable state restarts the watermark at zero
	}
	if err := json.Unmarshal(b, state); err != nil {
		slog.Warn("graph memory consolidation state unreadable; resetting watermark", "root", root, "error", err)
		return &graphConsolidationState{Dirs: map[string]*graphDirState{}}
	}
	if state.Dirs == nil {
		state.Dirs = map[string]*graphDirState{}
	}
	// Migrate the pre-A3 flat format: last_consolidated map entries become
	// dir states (LastSuccessAt stays zero — unknown pre-A3 success times
	// count as maximally stale for the time-based fallback).
	for dir, ts := range state.LastConsolidated {
		if _, ok := state.Dirs[dir]; !ok {
			state.Dirs[dir] = &graphDirState{LastConsolidated: ts}
		}
	}
	return state
}

func saveGraphConsolidationState(root string, state *graphConsolidationState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal consolidation state: %w", err)
	}
	path := filepath.Join(root, graphConsolidationStateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write consolidation state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write consolidation state: %w", err)
	}
	return nil
}
