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

	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/service"
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

// researchMaintenanceMinInterval is the minimum spacing between two research
// maintenance rounds on one graph (spec §4.5, default 1h).
const researchMaintenanceMinInterval = time.Hour

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
	// LastMaintenance is the last successful research maintenance round
	// (unification spec §4.5): the query-log count trigger and the 1h
	// minimum interval both measure against it.
	LastMaintenance time.Time `json:"last_maintenance,omitempty"`
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
// memory_type resolution (design §1/A4) is per workspace: the
// graph_memory_profile row for the workspace a memory_graph directory
// belongs to wins; directories without a profile row (or the root-level
// default layout, which maps to no workspace) fall back to the server
// process env MULTICA_MEMORY_TYPE, then "legacy". pool may be nil (tests,
// DB-less deployments), in which case only the env fallback is available.
// bm may be nil; every BusinessMetrics method tolerates a nil receiver.
func GraphMemoryJobs(pool *pgxpool.Pool, bm *obsmetrics.BusinessMetrics, policy *service.MemoryProviderPolicyResolver) JobSpec {
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
		Handler:           makeGraphMemoryConsolidationHandler(pool, bm, policy),
	}
}

// graphMemoryWorkspaceGate is the per-workspace activation gate for graph
// jobs (spec §10/§13): memory_type from the profile (env default when no
// row) plus the scoped-writer readiness flag. Jobs stay inert until both
// are satisfied — removing the process-level registration switch must not
// activate an incomplete writer.
type graphMemoryWorkspaceGate struct {
	memoryType        string
	scopedWriterReady bool
}

type graphMemoryGateLookup func(ctx context.Context, workspaceID string) (graphMemoryWorkspaceGate, error)

func graphMemoryGateLookupForPool(pool *pgxpool.Pool) graphMemoryGateLookup {
	if pool == nil {
		return nil
	}
	q := db.New(pool)
	return func(ctx context.Context, workspaceID string) (graphMemoryWorkspaceGate, error) {
		ws, err := uuid.Parse(workspaceID)
		if err != nil {
			return graphMemoryWorkspaceGate{}, err
		}
		gate, err := q.GetGraphMemoryScopedGate(ctx, pgtype.UUID{Bytes: ws, Valid: true})
		if err != nil {
			return graphMemoryWorkspaceGate{}, err
		}
		return graphMemoryWorkspaceGate{memoryType: gate.MemoryType, scopedWriterReady: gate.ScopedWriterReady}, nil
	}
}

// resolveGraphMemoryGate maps one graph dir to its scheduling decision:
// "graph" (run), "legacy" (skip, not graph mode), or "skip_not_ready"
// (graph mode but scoped writer gates not accepted). The workspace is
// derived from the canonical dir path; lookup errors fall back to the env
// type with readiness false.
func resolveGraphMemoryGate(ctx context.Context, dir, envType string, lookup graphMemoryGateLookup) string {
	gate := graphMemoryWorkspaceGate{memoryType: envType}
	if wsID, ok := graphDirWorkspaceID(dir); ok && lookup != nil {
		if row, err := lookup(ctx, wsID); err == nil {
			gate = row
		}
	}
	if gate.memoryType != "graph" {
		return "legacy"
	}
	if !gate.scopedWriterReady {
		return "skip_not_ready"
	}
	return "graph"
}

// graphDirWorkspaceID extracts the workspace id from a canonical graph dir
// <root>/<ws>/memory_graph/<kind>s/<owner>.
func graphDirWorkspaceID(dir string) (string, bool) {
	ws := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(dir))))
	if _, err := uuid.Parse(ws); err != nil {
		return "", false
	}
	return ws, true
}

func makeGraphMemoryConsolidationHandler(pool *pgxpool.Pool, bm *obsmetrics.BusinessMetrics, policy *service.MemoryProviderPolicyResolver) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		envType := strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_MEMORY_TYPE")))
		lookup := graphMemoryGateLookupForPool(pool)
		if lookup == nil && envType != "graph" {
			// Without a DB there is no per-workspace override to resolve; the
			// env default gates the whole sweep.
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "memory_type_not_graph"}}, nil
		}
		root, err := graphMemoryWorkspacesRoot()
		if err != nil {
			return HandlerResult{}, err
		}
		dirs, err := findMemoryGraphDirs(root)
		if err != nil {
			return HandlerResult{}, err
		}
		researchDirs, err := findResearchGraphDirs(root)
		if err != nil {
			return HandlerResult{}, err
		}
		if len(dirs) == 0 && len(researchDirs) == 0 {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "no_memory_graph_dirs", "root": root}}, nil
		}
		// Task 16: drain queued channel migrations (binding copies) before
		// consolidation — the worker itself checks each workspace's
		// channel_migration gate and claims nothing while it is red, and a
		// failed sweep logs without blocking consolidation.
		channelMigrations := 0
		if pool != nil {
			reports, err := service.NewGraphMemoryChannelMigrationService(pool).RunPending(ctx, 16)
			if err != nil {
				slog.Warn("graph memory channel migration sweep failed", "error", err)
			} else {
				channelMigrations = len(reports)
			}
		}
		// Task 17: retention sweep — trace windows, archive creation and
		// due crypto-erase. Archive streams stay disabled until the
		// master key is configured; the policy/trace halves run either
		// way. A failed sweep logs without blocking consolidation.
		retentionSwept := 0
		if pool != nil {
			archiveCipher, cipherErr := service.ArchiveCipherFromEnv()
			if cipherErr != nil {
				slog.Warn("memory archive cipher unavailable", "error", cipherErr)
			}
			var archive *service.MemoryArchiveService
			if archiveCipher != nil {
				archive = service.NewMemoryArchiveService(pool, archiveCipher,
					service.NewFilesystemArchiveObjectStore(root))
			}
			if actions, err := service.NewMemoryRetentionService(pool, archive).SweepDue(ctx, 64); err != nil {
				slog.Warn("memory retention sweep failed", "error", err)
			} else {
				retentionSwept = actions
			}
		}
		// Task 21: shadow-gate sweep — prove the six named canaries per
		// workspace and auto-shutdown dependent read/training phases on a
		// red verdict. The durable DAG writes stay untouched by design; a
		// failed sweep logs without blocking consolidation.
		shadowShutdowns := 0
		if pool != nil {
			for _, wsID := range shadowGateSweepTargets(dirs) {
				wsUUID, err := uuid.Parse(wsID)
				if err != nil {
					continue
				}
				report, err := service.NewShadowGateServiceWithMetrics(pool, bm).
					Sweep(ctx, pgtype.UUID{Bytes: wsUUID, Valid: true})
				if err != nil {
					slog.Warn("shadow gate sweep failed", "workspace", wsID, "error", err)
					continue
				}
				shadowShutdowns += len(report.Shutdown)
			}
		}
		state := loadGraphConsolidationState(root)

		consolidated := 0
		for _, dir := range dirs {
			// Per-workspace activation gate (spec §10/§13): only dirs in
			// graph mode with the scoped-writer readiness flag accepted
			// consolidate; the rest (legacy mode or not-ready) are logged
			// and skipped.
			if decision := resolveGraphMemoryGate(ctx, dir, envType, lookup); decision != "graph" {
				slog.Info("graph memory consolidation skipped", "dir", dir, "decision", decision)
				continue
			}
			// Task 14: the DB-default-off atom_consolidation route gates the
			// scheduler itself — a red gate claims nothing.
			if wsID, ok := graphDirWorkspaceID(dir); ok && pool != nil {
				if wsUUID, err := uuid.Parse(wsID); err == nil {
					wsTyped := pgtype.UUID{Bytes: wsUUID, Valid: true}
					enabled, err := service.NewMemoryReadGate(db.New(pool)).
						RouteEnabled(ctx, wsTyped, service.MemoryRouteAtomConsolidation)
					if err != nil {
						slog.Warn("graph memory consolidation gate read failed", "dir", dir, "error", err)
						continue
					}
					if !enabled {
						slog.Info("graph memory consolidation skipped", "dir", dir, "decision", "atom_consolidation_disabled")
						continue
					}
				}
			}
			// Per-workspace errors are logged and the loop continues: one
			// broken graph must not starve the others.
			ok, err := consolidateOneGraphWithPool(ctx, pool, policy, dir, state.dir(dir), bm)
			if err != nil {
				slog.Warn("graph memory consolidation failed", "dir", dir, "error", err)
				continue
			}
			if ok {
				consolidated++
			}
			// Daily seal (spec §6): after local midnight + 10m grace, seal
			// the prior date's daily nodes under the same graph mutation
			// lease as updates. Runs after every successful consolidation
			// pass so quiet days still seal.
			if wsID, ok := graphDirWorkspaceID(dir); ok {
				sealGraphMemoryDaily(ctx, pool, dir, wsID)
			}
			if in.Heartbeat != nil {
				if err := in.Heartbeat(ctx); err != nil {
					return HandlerResult{}, err
				}
			}
		}
		// Research maintenance rounds (unification spec §4.5) run on the
		// research graphs after the project/channel sweep; the export
		// switch and the same per-workspace graph gate keep them inert.
		maintained := maintainResearchGraphs(ctx, pool, policy, researchDirs, state, envType, lookup)
		if err := saveGraphConsolidationState(root, state); err != nil {
			slog.Warn("graph memory consolidation state write failed", "root", root, "error", err)
		}
		return HandlerResult{
			RowsAffected: int64(consolidated),
			Result: map[string]any{
				"graph_dirs":          len(dirs),
				"consolidated":        consolidated,
				"research_dirs":       len(researchDirs),
				"research_maintained": maintained,
				"channel_migrations":  channelMigrations,
				"retention_swept":     retentionSwept,
				"shadow_shutdowns":    shadowShutdowns,
			},
		}, nil
	}
}

// shadowGateSweepTargets extracts the deduplicated, order-preserving
// workspace ids of the graph dirs the sweep must prove canaries for.
func shadowGateSweepTargets(dirs []string) []string {
	seen := make(map[string]bool, len(dirs))
	targets := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		wsID, ok := graphDirWorkspaceID(dir)
		if !ok || seen[wsID] {
			continue
		}
		seen[wsID] = true
		targets = append(targets, wsID)
	}
	return targets
}

// graphConsolidationRunner runs one consolidation+publication cycle; it is
// a function value so tests can drive the trigger/backoff state machine
// without a pi backend.
type graphConsolidationRunner func(ctx context.Context) (*service.GraphMemoryConsolidationPublishReport, error)

// consolidateOneGraphWithPool resolves every external provider only after
// the DB trigger fires. The trigger input is the scope's uncovered active
// atom count (Task 14): staging files are never replayed.
func consolidateOneGraphWithPool(ctx context.Context, pool *pgxpool.Pool, policy *service.MemoryProviderPolicyResolver, dir string, ds *graphDirState, bm *obsmetrics.BusinessMetrics) (bool, error) {
	if pool == nil {
		// The atom trigger is DB-authoritative; a DB-less sweep claims nothing.
		slog.Info("graph memory consolidation skipped", "dir", dir, "decision", "no_database")
		return false, nil
	}
	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return false, fmt.Errorf("init store: %w", err)
	}
	identity, err := memorygraph.ReadGraphIdentity(dir)
	if err != nil {
		return false, err
	}
	workspaceUUID, err := serviceWorkspaceUUID(identity.WorkspaceID)
	if err != nil {
		return false, err
	}
	ownerUUID, err := uuid.Parse(identity.OwnerID)
	if err != nil {
		// Publication scopes are UUID-owned; a non-UUID owner can never be
		// claimed by the atom flow (permanent condition, not a failure).
		slog.Info("graph memory consolidation skipped", "dir", dir, "decision", "owner_not_uuid")
		return false, nil
	}
	ownerTyped := pgtype.UUID{Bytes: ownerUUID, Valid: true}
	newAtoms, totalAtoms, err := service.ActiveGraphUncoveredAtomCounts(ctx, pool, workspaceUUID, identity.Kind, ownerTyped)
	if err != nil {
		return false, err
	}
	cfg := memorygraph.DefaultConsolidateConfig() // trigger thresholds
	run := func(ctx context.Context) (*service.GraphMemoryConsolidationPublishReport, error) {
		resolved, err := policy.Resolve(ctx, workspaceUUID, service.ProviderConsolidate)
		if err != nil {
			return nil, err
		}
		scope := graphMemoryProviderScope(identity.WorkspaceID, service.ProviderConsolidate, resolved)
		backend, err := graphMemoryPIBackend(resolved)
		if err != nil {
			return nil, err
		}
		return service.NewGraphMemoryConsolidationPublishService(pool).
			PublishScope(ctx, workspaceUUID, identity.Kind, identity.OwnerID, backend, scope)
	}
	return consolidateOneGraphWith(ctx, dir, store, cfg, ds, bm, int(newAtoms), int(totalAtoms), run, time.Now().UTC())
}

func serviceWorkspaceUUID(workspaceID string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(workspaceID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("graph memory policy workspace id: %w", err)
	}
	var out pgtype.UUID
	copy(out.Bytes[:], parsed[:])
	out.Valid = true
	return out, nil
}

func graphMemoryProviderScope(workspaceID string, purpose service.MemoryProviderPurpose, resolved service.ResolvedMemoryProvider) memorygraph.ProviderScope {
	return memorygraph.ProviderScope{
		WorkspaceID: workspaceID, Purpose: memorygraph.ProviderPurpose(purpose),
		Provider: resolved.Provider, Model: resolved.Model, Region: resolved.Region, PolicyVersion: resolved.PolicyVersion,
	}
}

// consolidateOneGraphWith implements the A3-hardened trigger (design Q10)
// over the Task 14 atom inputs: the dual threshold on uncovered active
// atoms and query-log entries, a 24h time-based fallback, and a failure
// backoff that skips dirs after graphConsolidationBackoffThreshold
// consecutive failed cycles until the scope's total active atom count
// grows past the watermark recorded at backoff entry.
func consolidateOneGraphWith(ctx context.Context, dir string, store *memorygraph.Store, cfg memorygraph.ConsolidateConfig, ds *graphDirState, bm *obsmetrics.BusinessMetrics, newAtoms, totalAtoms int, run graphConsolidationRunner, now time.Time) (bool, error) {
	queries, err := countQueryLogEntriesSince(store, ds.LastConsolidated)
	if err != nil {
		return false, err
	}

	// Failure backoff (A3): skip until the total active atom count grows
	// past the watermark recorded at backoff entry, i.e. until genuinely
	// new material arrives.
	if ds.Backoff {
		if totalAtoms > ds.BackoffWatermark {
			ds.Backoff = false
			ds.ConsecutiveNoSwitch = 0
			slog.Info("graph memory consolidation backoff cleared: new active atoms arrived",
				"dir", dir, "atoms", totalAtoms, "watermark", ds.BackoffWatermark)
		} else {
			slog.Info("graph memory consolidation skipped: failure backoff active",
				"dir", dir, "atoms", totalAtoms, "watermark", ds.BackoffWatermark,
				"consecutive_no_switch", ds.ConsecutiveNoSwitch)
			return false, nil
		}
	}

	// Dual-threshold trigger (Q10), plus the time-based fallback (A3): at
	// least one uncovered atom and no successful consolidation in over
	// graphConsolidationMaxStaleness triggers anyway. A zero LastSuccessAt
	// (never consolidated, or a pre-A3 state file) counts as maximally
	// stale.
	reason := ""
	switch {
	case memorygraph.ShouldConsolidate(newAtoms, queries, cfg):
		reason = "threshold"
	case newAtoms >= 1 && now.Sub(ds.LastSuccessAt) > graphConsolidationMaxStaleness:
		reason = "time_fallback"
	default:
		slog.Info("graph memory consolidation skipped: trigger not met",
			"dir", dir, "uncovered_atoms", newAtoms, "queries", queries,
			"last_success_at", ds.LastSuccessAt)
		return false, nil
	}
	slog.Info("graph memory consolidation triggered", "dir", dir, "reason", reason,
		"uncovered_atoms", newAtoms, "queries", queries)

	report, err := run(ctx)
	if err != nil {
		recordConsolidationFailure(ds, totalAtoms, dir, "error: "+err.Error())
		return false, fmt.Errorf("consolidate: %w", err)
	}

	switch report.Outcome {
	case service.GraphMemoryConsolidationPublishPublished:
		ds.LastConsolidated = now
		ds.LastSuccessAt = now
		ds.ConsecutiveNoSwitch = 0
		ds.Backoff = false
		if bm != nil {
			bm.RecordGraphVersionSwitch()
		}
		slog.Info("graph memory publication published", "dir", dir,
			"generation", report.Generation, "candidate_version", report.CandidateVersion,
			"atoms", report.AtomCount)
		return true, nil
	case service.GraphMemoryConsolidationPublishSkippedNoAtoms:
		// The trigger raced an emptied manifest; nothing was consumed, no
		// watermark moves, and the next sweep sees zero uncovered atoms.
		slog.Info("graph memory consolidation skipped: no active atoms", "dir", dir)
		return false, nil
	default:
		// A non-consuming abort (e.g. uncited atoms) still burned a cycle;
		// it feeds the backoff so a systematically failing agent cannot
		// burn one every sweep.
		recordConsolidationFailure(ds, totalAtoms, dir, "outcome: "+report.Outcome)
		return false, fmt.Errorf("consolidate: outcome %s", report.Outcome)
	}
}

// sealGraphMemoryDaily runs the daily seal pass for one graph directory
// (spec §6): after local midnight plus the ten-minute grace period, the
// prior date's daily nodes are sealed under the graph mutation lock. With
// a nil pool (tests, DB-less deployments) the updater's process-local
// default locker is used. Seal failures are logged, never fatal.
func sealGraphMemoryDaily(ctx context.Context, pool *pgxpool.Pool, dir, wsID string) {
	loc := graphMemoryTimezoneForWorkspace(ctx, pool, wsID)
	now := time.Now().In(loc)
	if now.Hour() == 0 && now.Minute() < 10 {
		return // grace period: the prior day may still receive in-flight events
	}
	store := memorygraph.NewStore(dir)
	updater := memorygraph.NewDailyUpdater(store, loc)
	if pool != nil {
		kind, owner := graphDirKindOwner(dir)
		coordinator := service.NewGraphMutationCoordinator(pool)
		updater.SetLocker(func(ctx context.Context, fn func() error) error {
			return coordinator.WithGraphLock(ctx, wsID, kind, owner, func(context.Context) error { return fn() })
		})
	}
	sealed, err := updater.SealPriorDay(ctx)
	if err != nil {
		slog.Warn("graph memory daily seal failed", "dir", dir, "error", err)
	} else if sealed != "" {
		slog.Info("graph memory daily node sealed", "dir", dir, "node", sealed)
	}
}

// graphDirKindOwner parses the graph kind ("project"|"channel") and owner
// id from a canonical dir <root>/<ws>/memory_graph/<kind>s/<owner>.
func graphDirKindOwner(dir string) (kind, owner string) {
	owner = filepath.Base(dir)
	sub := filepath.Base(filepath.Dir(dir)) // "projects" | "channels"
	return strings.TrimSuffix(sub, "s"), owner
}

// graphMemoryTimezoneForWorkspace resolves the workspace memory-profile
// timezone via the scoped gate row, falling back to
// memorycuration.DefaultTimezone on any error.
func graphMemoryTimezoneForWorkspace(ctx context.Context, pool *pgxpool.Pool, workspaceID string) *time.Location {
	if pool != nil {
		if id, err := uuid.Parse(workspaceID); err == nil {
			gate, err := db.New(pool).GetGraphMemoryScopedGate(ctx, pgtype.UUID{Bytes: id, Valid: true})
			if err == nil && gate.Timezone != "" {
				if loc, err := time.LoadLocation(gate.Timezone); err == nil {
					return loc
				}
			}
		}
	}
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
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
func graphMemoryPIBackend(policy service.ResolvedMemoryProvider) (memorygraph.AgentBackend, error) {
	cfg := agentpkg.Config{}
	if policy.Provider == "pi" {
		path := strings.TrimSpace(os.Getenv("MULTICA_PI_PATH"))
		if path == "" {
			path = "pi"
		}
		cfg.ExecutablePath = path
	}
	backend, err := agentpkg.New(policy.Provider, cfg)
	if err != nil {
		return nil, fmt.Errorf("graph memory backend: %w", err)
	}
	return backend, nil
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

// findMemoryGraphDirs walks the canonical layout
// <root>/<ws>/memory_graph/{projects,channels}/<owner> and returns only
// directories whose immutable identity matches their path (spec §3). The
// legacy root-level <root>/memory_graph is never returned.
func findMemoryGraphDirs(root string) ([]string, error) {
	var dirs []string
	for _, sub := range []string{"projects", "channels"} {
		kind := memorygraph.GraphDirKind(strings.TrimSuffix(sub, "s"))
		glob := filepath.Join(root, "*", "memory_graph", sub, "*")
		matches, err := filepath.Glob(glob)
		if err != nil {
			return nil, err
		}
		for _, dir := range matches {
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			owner := filepath.Base(dir)
			ws := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(dir))))
			if err := memorygraph.VerifyGraphIdentity(dir, memorygraph.GraphIdentity{
				WorkspaceID: ws, Kind: string(kind), OwnerID: owner,
			}); err != nil {
				slog.Warn("graph memory: skipping graph dir with invalid identity", "dir", dir, "error", err)
				continue
			}
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// findResearchGraphDirs walks the canonical research layout
// <root>/<ws>/memory_graph/research/<ws> and returns only directories whose
// immutable identity matches their path (spec §3; research owner is the
// workspace itself).
func findResearchGraphDirs(root string) ([]string, error) {
	var dirs []string
	matches, err := filepath.Glob(filepath.Join(root, "*", "memory_graph", "research", "*"))
	if err != nil {
		return nil, err
	}
	for _, dir := range matches {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		owner := filepath.Base(dir)
		ws := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(dir))))
		if err := memorygraph.VerifyGraphIdentity(dir, memorygraph.GraphIdentity{
			WorkspaceID: ws, Kind: string(memorygraph.GraphDirKindResearch), OwnerID: owner,
		}); err != nil {
			slog.Warn("graph memory: skipping research graph dir with invalid identity", "dir", dir, "error", err)
			continue
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// maintainResearchGraphs sweeps the research graphs of the hour (unification
// spec §4.5). Gating layers: the export switch (off → the research graph is
// never written), the per-workspace graph gate, the query-log dual-threshold
// trigger with a 1h minimum interval, and per-dir failure isolation — a
// failed round logs and leaves the watermark untouched so the next sweep
// retries. Returns the number of rounds run.
func maintainResearchGraphs(ctx context.Context, pool *pgxpool.Pool, policy *service.MemoryProviderPolicyResolver, dirs []string, state *graphConsolidationState, envType string, lookup graphMemoryGateLookup) int {
	if !researchGraphExportEnabled() || len(dirs) == 0 {
		return 0
	}
	ran := 0
	for _, dir := range dirs {
		if decision := resolveGraphMemoryGate(ctx, dir, envType, lookup); decision != "graph" {
			slog.Info("research graph maintenance skipped", "dir", dir, "decision", decision)
			continue
		}
		wsID, ok := graphDirWorkspaceID(dir)
		if !ok {
			continue
		}
		ds := state.dir(dir)
		store := memorygraph.NewStore(dir)
		if err := store.Init(); err != nil {
			slog.Warn("research graph maintenance: init store failed", "dir", dir, "error", err)
			continue
		}
		queries, err := countQueryLogEntriesSince(store, ds.LastMaintenance)
		if err != nil {
			slog.Warn("research graph maintenance: count queries failed", "dir", dir, "error", err)
			continue
		}
		now := time.Now().UTC()
		if !shouldRunResearchMaintenance(ds, queries, now) {
			continue
		}
		if err := runResearchMaintenanceRound(ctx, pool, policy, wsID, dir, store); err != nil {
			slog.Warn("research graph maintenance failed", "dir", dir, "error", err)
			continue
		}
		ds.LastMaintenance = now
		ran++
	}
	return ran
}

// shouldRunResearchMaintenance reuses the dual-threshold mechanism on the
// research graph's own query log (staging is structurally zero there) plus
// the 1h minimum interval. A zero LastMaintenance counts as long elapsed.
func shouldRunResearchMaintenance(ds *graphDirState, queries int, now time.Time) bool {
	if now.Sub(ds.LastMaintenance) < researchMaintenanceMinInterval {
		return false
	}
	return memorygraph.ShouldConsolidate(0, queries, memorygraph.DefaultConsolidateConfig())
}

// runResearchMaintenanceRound runs one LLM maintenance round against a
// research graph (spec §4.5). It is a function value so the scheduler tests
// can drive the trigger without a pi backend. The round reuses the
// Consolidator's op-log audit and working-set machinery and runs the
// in-place trajectory: TTT candidate generation stays on the project/channel
// graphs, where explore backtests are wired. Writes go through the graph
// mutation lease like every other writer.
var runResearchMaintenanceRound = func(ctx context.Context, pool *pgxpool.Pool, policy *service.MemoryProviderPolicyResolver, wsID, dir string, store *memorygraph.Store) error {
	workspaceUUID, err := serviceWorkspaceUUID(wsID)
	if err != nil {
		return err
	}
	resolved, err := policy.Resolve(ctx, workspaceUUID, service.ProviderConsolidate)
	if err != nil {
		return err
	}
	scope := graphMemoryProviderScope(wsID, service.ProviderConsolidate, resolved)
	backend, err := graphMemoryPIBackend(resolved)
	if err != nil {
		return err
	}
	cfg := memorygraph.DefaultConsolidateConfig()
	c := memorygraph.NewConsolidator(store, backend, cfg, scope, nil, memorygraph.NewTraceRecorder(dir))
	// Working-set similarity degrades to BM25 when the embed channel is not
	// configured; a missing embedder never blocks the maintenance round.
	embedder := func() *memorygraph.CachedEmbedder {
		embedResolved, embedErr := policy.Resolve(ctx, workspaceUUID, service.ProviderEmbed)
		if embedErr != nil {
			return nil
		}
		embedScope := graphMemoryProviderScope(wsID, service.ProviderEmbed, embedResolved)
		provider, embErr := memorygraph.NewOpenAIEmbedderFromEnv(embedScope)
		if embErr != nil {
			return nil
		}
		emb, embErr := memorygraph.NewCachedEmbedder(provider, store, embedScope)
		if embErr != nil {
			return nil
		}
		return emb
	}()
	c.SetWorkingSetBuilder(memorygraph.NewWorkingSetBuilder(store, memorygraph.RetrievalSignals{}, embedder, memorygraph.DefaultWorkingSetConfig(), cfg.MaxFanout))
	round := func(ctx context.Context) error {
		_, err := c.MaintenanceRound(ctx)
		return err
	}
	if pool != nil {
		coordinator := service.NewGraphMutationCoordinator(pool)
		return coordinator.WithGraphLock(ctx, wsID, "research", wsID, round)
	}
	return round(ctx)
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
