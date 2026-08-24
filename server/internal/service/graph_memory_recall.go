// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Server-authoritative recall resolution failures (spec §1/§3, A14/A23).
// Every failure is non-fatal to the business task; the sentinels classify the
// outcome for observability and the fail-closed replay matrix.
var (
	// ErrGraphMemoryRecallDisabled: memory_type is not graph for the
	// workspace, or the invoking agent's env-dispatch training mode is none.
	ErrGraphMemoryRecallDisabled = errors.New("graph memory recall: disabled for this task")
	// ErrGraphMemoryRecallIdentity: unknown task, cross-tenant task, or a
	// runtime/daemon pair that does not match the workspace.
	ErrGraphMemoryRecallIdentity = errors.New("graph memory recall: identity mismatch")
	// ErrGraphMemoryRecallNoScope: the canonical task scopes to no graph.
	ErrGraphMemoryRecallNoScope = errors.New("graph memory recall: task has no graph scope")
	// ErrGraphMemoryRecallConflict: the trace id was already committed with
	// a different canonical payload (A23: conflicting replays fail closed).
	ErrGraphMemoryRecallConflict = errors.New("graph memory recall: conflicting replay")
	// ErrGraphMemoryRecallFinalized: the recall for this trace id already
	// reached a terminal lifecycle state (A23: finalized replays fail
	// closed).
	ErrGraphMemoryRecallFinalized = errors.New("graph memory recall: finalized replay")
)

// GraphMemorySeedRetriever resolves the round-0 seed candidates for one
// pinned graph version (spec §3 step 5). Production wires the hybrid
// retriever; tests inject fakes.
type GraphMemorySeedRetriever interface {
	Seeds(ctx context.Context, dir string, version int, query string, view memorygraph.GraphView) ([]string, error)
}

// GraphMemoryRecallRequest is the daemon's recall call. Only the canonical
// identities and the query are inputs; every Caller* field is
// diagnostics-only and never consulted for resolution (spec §1, A14).
type GraphMemoryRecallRequest struct {
	WorkspaceID string
	TaskID      string
	DaemonID    string
	RuntimeID   string
	Query       string
	TraceID     string

	CallerGraphKind    string
	CallerGraphOwnerID string
	CallerGraphVersion int
	CallerTrainingMode string
	CallerK            int
}

// GraphMemoryRecallPlan is the server's authoritative, durably persisted
// recall plan: one recall row, K trajectory rows, one round-0 seed batch per
// trajectory, and a version-pin lease.
type GraphMemoryRecallPlan struct {
	RecallID     string
	WorkspaceID  string
	TaskID       string
	DaemonID     string
	RuntimeID    string
	GraphKind    string
	GraphOwnerID string
	GraphDir     string
	GraphVersion int
	GraphView    memorygraph.GraphView
	K            int
	TrainingMode string
	Tunables     GraphMemoryTunables
	TTTEnabled   bool
	Query        string
	TraceID      string
	// Replayed marks a plan adopted from the already-committed ledger row
	// (idempotent replay): no new rows were written and no provider work
	// happened for it (A23).
	Replayed bool
}

// GraphMemoryRecallOutcome is the non-fatal wrapper result (spec §1: a
// recall failure never fails the business task).
type GraphMemoryRecallOutcome struct {
	Plan   *GraphMemoryRecallPlan
	Reason string // machine-readable skip/failure reason when Plan is nil
}

// GraphMemoryRecallService owns the server-side recall resolution (spec §3):
// canonical task load, profile resolution, routing/graph identity, training
// mode, single version pin, round-0 seeds, and per-recall K.
type GraphMemoryRecallService struct {
	pool    *pgxpool.Pool
	limits  GraphMemoryLimits
	root    string // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT
	envType string // process-level memory_type default
	seeder  GraphMemorySeedRetriever
}

func NewGraphMemoryRecallService(pool *pgxpool.Pool, limits GraphMemoryLimits, root, envMemoryType string, seeder GraphMemorySeedRetriever) *GraphMemoryRecallService {
	return &GraphMemoryRecallService{pool: pool, limits: limits, root: root, envType: envMemoryType, seeder: seeder}
}

// TryBegin is the non-fatal entry point: any resolution or persistence
// failure becomes a nil-plan outcome with a machine-readable reason.
func (s *GraphMemoryRecallService) TryBegin(ctx context.Context, req GraphMemoryRecallRequest) GraphMemoryRecallOutcome {
	plan, err := s.Begin(ctx, req)
	if err == nil {
		return GraphMemoryRecallOutcome{Plan: plan}
	}
	reason := "error"
	switch {
	case errors.Is(err, ErrGraphMemoryRecallDisabled):
		reason = "disabled"
	case errors.Is(err, ErrGraphMemoryRecallNoScope):
		reason = "no_scope"
	case errors.Is(err, ErrGraphMemoryRecallIdentity):
		reason = "identity"
	case errors.Is(err, ErrGraphMemoryRecallConflict):
		reason = "conflict"
	case errors.Is(err, ErrGraphMemoryRecallFinalized):
		reason = "finalized"
	}
	slog.Warn("graph memory recall skipped", "task_id", req.TaskID, "reason", reason, "error", err)
	return GraphMemoryRecallOutcome{Reason: reason}
}

// Begin resolves the recall fully server-side and persists the ledger rows
// atomically. Replaying an already-committed trace id returns the existing
// plan without duplicating rows.
func (s *GraphMemoryRecallService) Begin(ctx context.Context, req GraphMemoryRecallRequest) (*GraphMemoryRecallPlan, error) {
	wsUUID, taskUUID, rtUUID, err := graphMemoryRecallIdentities(req)
	if err != nil {
		return nil, err
	}

	// Canonical task load: the request's workspace must own the task.
	var (
		taskWs    pgtype.UUID
		agentID   pgtype.UUID
		channelID pgtype.UUID
		issueID   pgtype.UUID
	)
	err = s.pool.QueryRow(ctx, `
		SELECT workspace_id, agent_id, channel_id, issue_id
		FROM agent_inbox_event WHERE id = $1
	`, taskUUID).Scan(&taskWs, &agentID, &channelID, &issueID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("%w: unknown task %s", ErrGraphMemoryRecallIdentity, req.TaskID)
	case err != nil:
		return nil, fmt.Errorf("graph memory recall: load task: %w", err)
	}
	if taskWs != wsUUID {
		return nil, fmt.Errorf("%w: task %s is not in workspace %s", ErrGraphMemoryRecallIdentity, req.TaskID, req.WorkspaceID)
	}

	// The reporting runtime must exist in this workspace under this daemon.
	var rtID pgtype.UUID
	err = s.pool.QueryRow(ctx, `
		SELECT id FROM agent_runtime WHERE id = $1 AND workspace_id = $2 AND daemon_id = $3
	`, rtUUID, wsUUID, req.DaemonID).Scan(&rtID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, fmt.Errorf("%w: runtime %s daemon %s", ErrGraphMemoryRecallIdentity, req.RuntimeID, req.DaemonID)
	case err != nil:
		return nil, fmt.Errorf("graph memory recall: load runtime: %w", err)
	}

	// Effective profile: the workspace row wins over the env default.
	q := db.New(s.pool)
	memoryType := s.envType
	tunables := s.limits.Defaults
	tttEnabled := false
	if profile, perr := q.GetGraphMemoryProfile(ctx, wsUUID); perr == nil {
		if profile.MemoryType == "graph" || profile.MemoryType == "legacy" {
			memoryType = profile.MemoryType
		}
		tunables = graphMemoryTunablesFromProfile(profile)
		tttEnabled = profile.TttEnabled
	}
	if memoryType != "graph" {
		return nil, fmt.Errorf("%w: memory_type is %s", ErrGraphMemoryRecallDisabled, memoryType)
	}

	// Memory-agent training behavior (spec §5): the invoking agent's active
	// env-dispatch mode governs; anything else captures offline.
	trainingMode, err := s.resolveTrainingMode(ctx, wsUUID, agentID)
	if err != nil {
		return nil, err
	}
	if trainingMode == "none" {
		return nil, fmt.Errorf("%w: env-dispatch training mode none", ErrGraphMemoryRecallDisabled)
	}

	// Graph scope from the canonical task, never from caller hints.
	graphKind, graphOwnerID, err := s.resolveGraphScope(ctx, wsUUID, channelID, issueID)
	if err != nil {
		return nil, err
	}
	ownerID := util.UUIDToString(graphOwnerID)
	root := s.root
	if root == "" {
		if root, err = graphMemoryWorkspacesRoot(); err != nil {
			return nil, err
		}
	}
	dir, err := memorygraph.DirForScope(root, req.WorkspaceID, memorygraph.GraphDirKind(graphKind), ownerID)
	if err != nil {
		return nil, fmt.Errorf("graph memory recall: %w", err)
	}
	if err := memorygraph.VerifyGraphIdentity(dir, memorygraph.GraphIdentity{
		WorkspaceID: req.WorkspaceID, Kind: graphKind, OwnerID: ownerID,
	}); err != nil {
		return nil, fmt.Errorf("graph memory recall: %w", err)
	}
	version, err := memorygraph.NewStore(dir).CurrentVersion()
	if err != nil {
		return nil, fmt.Errorf("graph memory recall: current version: %w", err)
	}

	view := memorygraph.GraphView{}
	if graphKind == string(memorygraph.GraphDirKindProject) {
		view.AllowProject = true
		if channelID.Valid {
			view.ChannelID = util.UUIDToString(channelID)
		}
	} else {
		view.ChannelID = ownerID
	}

	// K is per recall (A22: no workspace semaphore): 1 when TTT is off, else
	// the saved concurrency clamped to the server ceiling.
	k := 1
	if tttEnabled {
		k = int(tunables.TTTConcurrency)
		if k < 1 {
			k = 1
		}
		if ceil := int(s.limits.Ceilings.TTTConcurrency); ceil > 0 && k > ceil {
			k = ceil
		}
	}

	// Fail-closed replay matrix (A23): an already-committed trace id
	// resolves before any provider work (seed retrieval). An identical
	// replay returns the persisted plan; conflicting or finalized replays
	// are rejected without side effects.
	if replay, rerr := s.checkReplay(ctx, wsUUID, taskUUID, rtUUID, req); rerr != nil {
		return nil, rerr
	} else if replay != nil {
		return replay, nil
	}

	var seeds []string
	if s.seeder != nil {
		seeds, err = s.seeder.Seeds(ctx, dir, version, req.Query, view)
		if err != nil {
			return nil, fmt.Errorf("graph memory recall: seed retrieval: %w", err)
		}
	}

	plan := &GraphMemoryRecallPlan{
		WorkspaceID:  req.WorkspaceID,
		TaskID:       req.TaskID,
		DaemonID:     req.DaemonID,
		RuntimeID:    req.RuntimeID,
		GraphKind:    graphKind,
		GraphOwnerID: ownerID,
		GraphDir:     dir,
		GraphVersion: version,
		GraphView:    view,
		K:            k,
		TrainingMode: trainingMode,
		Tunables:     tunables,
		TTTEnabled:   tttEnabled,
		Query:        req.Query,
		TraceID:      req.TraceID,
	}
	if err := s.persistPlan(ctx, plan, wsUUID, taskUUID, rtUUID, graphOwnerID, seeds); err != nil {
		return nil, err
	}
	return plan, nil
}

// graphMemoryRecallIdentities validates and parses the required request
// identities. Caller hints are deliberately unread.
func graphMemoryRecallIdentities(req GraphMemoryRecallRequest) (ws, task, runtime pgtype.UUID, err error) {
	for name, v := range map[string]string{
		"workspace_id": req.WorkspaceID, "task_id": req.TaskID, "daemon_id": req.DaemonID,
		"runtime_id": req.RuntimeID, "query": req.Query, "trace_id": req.TraceID,
	} {
		if strings.TrimSpace(v) == "" {
			return ws, task, runtime, fmt.Errorf("%w: %s is required", ErrGraphMemoryRecallIdentity, name)
		}
	}
	if ws, err = util.ParseUUID(req.WorkspaceID); err != nil {
		return ws, task, runtime, fmt.Errorf("%w: workspace_id: %v", ErrGraphMemoryRecallIdentity, err)
	}
	if task, err = util.ParseUUID(req.TaskID); err != nil {
		return ws, task, runtime, fmt.Errorf("%w: task_id: %v", ErrGraphMemoryRecallIdentity, err)
	}
	if runtime, err = util.ParseUUID(req.RuntimeID); err != nil {
		return ws, task, runtime, fmt.Errorf("%w: runtime_id: %v", ErrGraphMemoryRecallIdentity, err)
	}
	return ws, task, runtime, nil
}

// resolveTrainingMode maps the invoking agent's active env-dispatch run mode
// onto the memory-agent training behavior (spec §5). Terminal runs no longer
// govern the agent.
func (s *GraphMemoryRecallService) resolveTrainingMode(ctx context.Context, wsUUID, agentID pgtype.UUID) (string, error) {
	var mode string
	err := s.pool.QueryRow(ctx, `
		SELECT ra.training_mode
		FROM env_dispatch_run_agent ra
		JOIN env_dispatch_run r ON r.run_id = ra.run_id
		WHERE ra.execution_agent_id = $1
		  AND r.workspace_id = $2
		  AND r.status NOT IN ('completed', 'failed_timeout', 'failed_preflight')
		ORDER BY ra.created_at DESC
		LIMIT 1
	`, agentID, wsUUID).Scan(&mode)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "offline_capture", nil
	case err != nil:
		return "", fmt.Errorf("graph memory recall: training mode: %w", err)
	}
	return mode, nil
}

// resolveGraphScope derives the recall's graph identity from the canonical
// task: the channel route for channel-scoped tasks (spec §4), else the
// issue's project, else no scope.
func (s *GraphMemoryRecallService) resolveGraphScope(ctx context.Context, wsUUID, channelID, issueID pgtype.UUID) (string, pgtype.UUID, error) {
	if channelID.Valid {
		route, err := ResolveChannelRoute(ctx, s.pool, util.UUIDToString(wsUUID), util.UUIDToString(channelID))
		if err != nil {
			return "", pgtype.UUID{}, fmt.Errorf("graph memory recall: %w", err)
		}
		owner, err := util.ParseUUID(route.GraphOwnerID)
		if err != nil {
			return "", pgtype.UUID{}, fmt.Errorf("graph memory recall: route owner: %w", err)
		}
		return route.GraphKind, owner, nil
	}
	if issueID.Valid {
		var projectID pgtype.UUID
		err := s.pool.QueryRow(ctx, `
			SELECT project_id FROM issue WHERE id = $1 AND workspace_id = $2
		`, issueID, wsUUID).Scan(&projectID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return "", pgtype.UUID{}, fmt.Errorf("%w: unknown issue", ErrGraphMemoryRecallNoScope)
		case err != nil:
			return "", pgtype.UUID{}, fmt.Errorf("graph memory recall: load issue: %w", err)
		}
		if projectID.Valid {
			return string(memorygraph.GraphDirKindProject), projectID, nil
		}
	}
	return "", pgtype.UUID{}, ErrGraphMemoryRecallNoScope
}

// persistPlan writes the recall row, K trajectories, one round-0 seed batch
// per trajectory, and the version-pin lease in one transaction. A concurrent
// or repeated trace id short-circuits to the already-committed recall.
func (s *GraphMemoryRecallService) persistPlan(ctx context.Context, plan *GraphMemoryRecallPlan, wsUUID, taskUUID, rtUUID, ownerUUID pgtype.UUID, seeds []string) error {
	seedJSON, err := json.Marshal(seeds)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var recallID pgtype.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO graph_memory_recall
		  (workspace_id, task_id, daemon_id, runtime_id, graph_kind, graph_owner_id, graph_version, training_mode, k, query, trace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (workspace_id, trace_id) DO NOTHING
		RETURNING id
	`, wsUUID, taskUUID, plan.DaemonID, rtUUID, plan.GraphKind, ownerUUID, plan.GraphVersion,
		plan.TrainingMode, plan.K, plan.Query, plan.TraceID).Scan(&recallID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A concurrent first writer committed this trace id first: adopt
		// the persisted row with the same fail-closed comparison as the
		// pre-seed replay path (A23), never duplicate.
		if err := tx.Rollback(ctx); err != nil {
			return err
		}
		replay, rerr := s.checkReplay(ctx, wsUUID, taskUUID, rtUUID, graphMemoryRecallCanonicalRequest(plan))
		if rerr != nil {
			return rerr
		}
		if replay == nil {
			return fmt.Errorf("graph memory recall: committed recall vanished for trace %s", plan.TraceID)
		}
		plan.RecallID = replay.RecallID
		plan.GraphVersion = replay.GraphVersion
		plan.K = replay.K
		plan.Replayed = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("graph memory recall: insert recall: %w", err)
	}

	for seed := 0; seed < plan.K; seed++ {
		var trajectoryID pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO graph_memory_trajectory (recall_id, workspace_id, seed_index)
			VALUES ($1, $2, $3)
			RETURNING id
		`, recallID, wsUUID, seed).Scan(&trajectoryID); err != nil {
			return fmt.Errorf("graph memory recall: insert trajectory %d: %w", seed, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO graph_memory_expansion_batch (trajectory_id, round, candidate_ids, request_key, view_quota)
			VALUES ($1, 0, $2, 'seed', $3)
		`, trajectoryID, seedJSON, max(int(plan.Tunables.ExploreNodesPerExpansion), 1)); err != nil {
			return fmt.Errorf("graph memory recall: insert seed batch %d: %w", seed, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_memory_version_lease
		  (workspace_id, graph_kind, graph_owner_id, graph_version, consumer_kind, consumer_id)
		VALUES ($1, $2, $3, $4, 'recall', $5)
	`, wsUUID, plan.GraphKind, ownerUUID, plan.GraphVersion, recallID); err != nil {
		return fmt.Errorf("graph memory recall: insert version lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	plan.RecallID = util.UUIDToString(recallID)
	return nil
}

// graphMemoryRecallTerminal reports whether a recall status is lifecycle
// terminal (spec §14: finalized replays fail closed).
func graphMemoryRecallTerminal(status string) bool {
	switch status {
	case "completed", "judge_failed", "failed":
		return true
	}
	return false
}

// graphMemoryRecallCanonicalRequest rebuilds the canonical request fields
// from a plan (used by the concurrent-first-writer adoption path).
func graphMemoryRecallCanonicalRequest(plan *GraphMemoryRecallPlan) GraphMemoryRecallRequest {
	return GraphMemoryRecallRequest{
		WorkspaceID: plan.WorkspaceID,
		TaskID:      plan.TaskID,
		DaemonID:    plan.DaemonID,
		RuntimeID:   plan.RuntimeID,
		Query:       plan.Query,
		TraceID:     plan.TraceID,
	}
}

// checkReplay implements the fail-closed replay matrix (A23) against the
// durable ledger, before any provider work:
//   - no row: nil plan, no error (new recall);
//   - row with a different canonical payload: conflict;
//   - row in a terminal lifecycle state: finalized;
//   - identical row: the persisted plan, marked Replayed.
func (s *GraphMemoryRecallService) checkReplay(ctx context.Context, wsUUID, taskUUID, rtUUID pgtype.UUID, req GraphMemoryRecallRequest) (*GraphMemoryRecallPlan, error) {
	var (
		recallID      pgtype.UUID
		storedTask    pgtype.UUID
		storedRuntime pgtype.UUID
		storedDaemon  string
		storedQuery   string
		status        string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, task_id, daemon_id, runtime_id, query, status
		FROM graph_memory_recall
		WHERE workspace_id = $1 AND trace_id = $2
	`, wsUUID, req.TraceID).Scan(&recallID, &storedTask, &storedDaemon, &storedRuntime, &storedQuery, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("graph memory recall: replay lookup: %w", err)
	}
	if storedTask != taskUUID || storedDaemon != req.DaemonID || storedRuntime != rtUUID || storedQuery != req.Query {
		return nil, fmt.Errorf("%w: trace %s", ErrGraphMemoryRecallConflict, req.TraceID)
	}
	if graphMemoryRecallTerminal(status) {
		return nil, fmt.Errorf("%w: trace %s is %s", ErrGraphMemoryRecallFinalized, req.TraceID, status)
	}
	replayed := &GraphMemoryRecallPlan{
		WorkspaceID: req.WorkspaceID,
		TaskID:      req.TaskID,
		DaemonID:    req.DaemonID,
		RuntimeID:   req.RuntimeID,
		Query:       req.Query,
		TraceID:     req.TraceID,
		Replayed:    true,
	}
	if err := s.loadExistingPlan(ctx, replayed, wsUUID); err != nil {
		return nil, err
	}
	return replayed, nil
}

// loadExistingPlan rebuilds the plan from the persisted recall row on an
// idempotent replay.
func (s *GraphMemoryRecallService) loadExistingPlan(ctx context.Context, plan *GraphMemoryRecallPlan, wsUUID pgtype.UUID) error {
	var (
		recallID pgtype.UUID
		ownerID  pgtype.UUID
		kind     string
		version  int32
		k        int32
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, graph_kind, graph_owner_id, graph_version, k FROM graph_memory_recall
		WHERE workspace_id = $1 AND trace_id = $2
	`, wsUUID, plan.TraceID).Scan(&recallID, &kind, &ownerID, &version, &k)
	if err != nil {
		return fmt.Errorf("graph memory recall: load replayed recall: %w", err)
	}
	root := s.root
	if root == "" {
		var rootErr error
		root, rootErr = graphMemoryWorkspacesRoot()
		if rootErr != nil {
			return rootErr
		}
	}
	owner := util.UUIDToString(ownerID)
	dir, err := memorygraph.DirForScope(root, plan.WorkspaceID, memorygraph.GraphDirKind(kind), owner)
	if err != nil {
		return fmt.Errorf("graph memory recall: replay graph dir: %w", err)
	}
	plan.RecallID = util.UUIDToString(recallID)
	plan.GraphKind = kind
	plan.GraphOwnerID = owner
	plan.GraphDir = dir
	plan.GraphVersion = int(version)
	plan.K = int(k)
	return nil
}

// graphMemoryTunablesFromProfile maps the persisted profile row onto the
// tunables struct; both carry storage-level defaults so no zero-value
// reaches the recall path.
func graphMemoryTunablesFromProfile(profile db.GraphMemoryProfile) GraphMemoryTunables {
	return GraphMemoryTunables{
		TTTConcurrency:           int(profile.ExploreAgents),
		ExploreMaxRounds:         int(profile.ExploreMaxRounds),
		ExploreNodesPerExpansion: int(profile.ExploreNodesPerExpansion),
		MaxHierarchyFanout:       int(profile.MaxHierarchyFanout),
		MaxRelationEdgesPerNode:  int(profile.MaxRelationEdgesPerNode),
		DiveMaxRounds:            int(profile.DiveMaxRounds),
		DiveMaxViewedNodes:       int(profile.DiveMaxViewedNodes),
		DiveMaxSourceFiles:       int(profile.DiveMaxSourceFiles),
		DiveTimeoutSeconds:       int(profile.DiveTimeoutSeconds),
		WRound:                   profile.WRound,
		SourceMaxFileBytes:       profile.SourceMaxFileBytes,
		SourceMaxTotalBytes:      profile.SourceMaxTotalBytes,
		SourceMaxPDFPages:        int(profile.SourceMaxPdfPages),
		SourceMaxAVSeconds:       int(profile.SourceMaxAvSeconds),
		SourceMaxImageMegapixels: int(profile.SourceMaxImageMegapixels),
	}
}
