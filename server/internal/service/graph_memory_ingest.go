// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorycuration"
	"github.com/multica-ai/multica/server/internal/memorygraph"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/util"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GraphMemoryIngestHook is the production SegmentIngestHook (design §5.1,
// review P0-1): the interaction-dag seams fire it on segment close and it
// routes the segment into the memorygraph.Ingester of the workspace's
// memory_graph store. The store location is the canonical per-scope graph
// directory (spec §3: <root>/<ws>/memory_graph/{projects,channels}/<owner>)
// — the same layout the consolidation scheduler discovers.
//
// Nil-safe by design: a task whose workspace has no memory_graph directory,
// or whose memory_type is not "graph", is skipped silently. The pi agent
// backend for summarization is built lazily on first use (same env contract
// as the scheduler job: MULTICA_PI_PATH / MULTICA_PI_MODEL); a backend
// construction failure degrades to the ingester's extractive fallback rather
// than failing the hook.
type GraphMemoryIngestHook struct {
	queries   *db.Queries
	pool      *pgxpool.Pool // channel route resolution (spec §4); nil only in tests
	root      string        // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT per call
	bm        *obsmetrics.BusinessMetrics
	model     string
	mutations *GraphMutationCoordinator

	backendOnce sync.Once
	backend     memorygraph.AgentBackend
	backendErr  error
}

// NewGraphMemoryIngestHook returns the production ingest hook. queries must
// be non-nil (the hook resolves the segment's workspace from the task row);
// pool must be non-nil in production (channel-origin tasks resolve their
// write target through the route registry); bm may be nil. root may be
// empty, in which case MULTICA_WORKSPACES_ROOT is resolved per ingest.
func NewGraphMemoryIngestHook(queries *db.Queries, pool *pgxpool.Pool, root string, bm *obsmetrics.BusinessMetrics) *GraphMemoryIngestHook {
	return &GraphMemoryIngestHook{
		queries:   queries,
		pool:      pool,
		root:      root,
		bm:        bm,
		model:     strings.TrimSpace(os.Getenv("MULTICA_PI_MODEL")),
		mutations: NewGraphMutationCoordinator(pool),
	}
}

// projectForTask resolves the owning project of the task's issue when
// present (the task row carries issue_id, not project_id); "" otherwise.
func (h *GraphMemoryIngestHook) projectForTask(ctx context.Context, task db.AgentInboxEvent) (string, error) {
	if task.IssueID.Valid {
		issue, err := h.queries.GetIssue(ctx, task.IssueID)
		if err != nil {
			return "", fmt.Errorf("graph memory ingest: load issue %s: %w", util.UUIDToString(task.IssueID), err)
		}
		return util.UUIDToString(issue.ProjectID), nil
	}
	return "", nil
}

// graphMemoryDerivativeAgent prevents the managed Memory Agent's synthesized
// output from recursively becoming source evidence for the same graph.
func graphMemoryDerivativeAgent(agent db.Agent) bool {
	return agent.ManagedRole.Valid && strings.EqualFold(strings.TrimSpace(agent.ManagedRole.String), "graph_memory_channel")
}

// ingestScopeForTask derives the write target and segment scope metadata
// from the canonical task scope and the server-resolved route (spec §4/§5).
// The route argument is only consulted when the task has a channel; it must
// come from ResolveChannelRoute (server binding is authoritative — a stale
// task.ProjectID never selects the target).
func ingestScopeForTask(workspaceID, projectID, channelID string, route GraphRouteResolution, agentID, taskID string) (memorygraph.SegmentMeta, memorygraph.GraphDirKind, string, bool) {
	meta := memorygraph.SegmentMeta{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		AgentID:     agentID,
		TaskID:      taskID,
	}
	switch {
	case channelID != "":
		if route.GraphOwnerID == "" {
			return memorygraph.SegmentMeta{}, "", "", false
		}
		meta.Visibility = "channel" // channel-origin defaults to channel-only (§5)
		meta.ChannelID = channelID
		meta.LineageGeneration = route.Generation
		return meta, memorygraph.GraphDirKind(route.GraphKind), route.GraphOwnerID, true
	case projectID != "":
		meta.Visibility = "project"
		return meta, memorygraph.GraphDirKindProject, projectID, true
	default:
		return memorygraph.SegmentMeta{}, "", "", false
	}
}

// Ingest implements SegmentIngestHook.
func (h *GraphMemoryIngestHook) Ingest(ctx context.Context, seg memorygraph.SegmentExport) error {
	if h.queries == nil {
		return fmt.Errorf("graph memory ingest: queries not configured")
	}
	if seg.AgentRunID == "" {
		return fmt.Errorf("graph memory ingest: agent_run_id required")
	}

	// Resolve the owning workspace through the task row (agent_run_id =
	// task.ID, D8).
	taskUUID, err := util.ParseUUID(seg.AgentRunID)
	if err != nil {
		return fmt.Errorf("graph memory ingest: parse agent_run_id %q: %w", seg.AgentRunID, err)
	}
	task, err := h.queries.GetAgentTask(ctx, taskUUID)
	if err != nil {
		return fmt.Errorf("graph memory ingest: load task %s: %w", seg.AgentRunID, err)
	}
	producer, err := h.queries.GetAgent(ctx, task.AgentID)
	if err != nil {
		return fmt.Errorf("graph memory ingest: load producer agent for task %s: %w", seg.AgentRunID, err)
	}
	if graphMemoryDerivativeAgent(producer) {
		return nil
	}

	// Per-workspace memory_type gate (design §1/A4): legacy workspaces
	// never grow staging segments.
	if rt := resolveGraphMemoryType(ctx, h.queries, task.WorkspaceID, graphMemoryEnvMemoryType()); rt != "graph" {
		return nil
	}

	root, err := h.workspacesRoot()
	if err != nil {
		return err
	}
	// Scoped write target (spec §4/§5): channel-origin tasks resolve their
	// target through the route registry (the server binding is
	// authoritative); project-only tasks write the owning project's graph;
	// unscoped tasks never create graphs (spec §2).
	wsID := util.UUIDToString(task.WorkspaceID)
	projectID, err := h.projectForTask(ctx, task)
	if err != nil {
		return err
	}
	channelID := util.UUIDToString(task.ChannelID)
	var route GraphRouteResolution
	if channelID != "" {
		if h.pool == nil {
			return fmt.Errorf("graph memory ingest: pool not configured for channel route resolution")
		}
		route, err = ResolveChannelRoute(ctx, h.pool, wsID, channelID)
		if err != nil {
			return err
		}
	}
	meta, kind, ownerID, ok := ingestScopeForTask(wsID, projectID, channelID, route,
		util.UUIDToString(task.AgentID), util.UUIDToString(task.ID))
	if !ok {
		return nil // unscoped task: no graph (spec §2)
	}
	dir, err := memorygraph.EnsureScopedDir(root, wsID, kind, ownerID)
	if err != nil {
		return err
	}

	store := memorygraph.NewStore(dir)
	if err := store.Init(); err != nil {
		return fmt.Errorf("graph memory ingest: init store %s: %w", dir, err)
	}
	// The ingester is constructed per segment: stores are per-workspace, so
	// no cross-workspace state is shared between ingests.
	ingester := memorygraph.NewIngester(store, h.agentBackend(), "pi", h.model, 0)
	ingester.SetMetrics(h.bm)
	if err := ingester.Ingest(ctx, seg); err != nil {
		return err
	}
	if err := store.WriteStagingSegmentMeta(seg.SegmentID, &meta); err != nil {
		return fmt.Errorf("graph memory ingest: segment meta: %w", err)
	}

	// Daily writer (spec §6): merge a concise outcome into the open daily
	// node of the same graph, serialized by the graph mutation lock. A
	// daily failure never fails the ingest.
	if h.mutations != nil {
		updater := memorygraph.NewDailyUpdater(store, h.timezoneFor(ctx, task.WorkspaceID))
		updater.SetLocker(func(ctx context.Context, fn func() error) error {
			return h.mutations.WithGraphLock(ctx, wsID, string(kind), ownerID, func(context.Context) error { return fn() })
		})
		if err := updater.Record(ctx, memorygraph.DailyEvent{
			AgentID: meta.AgentID, ProjectID: meta.ProjectID, ChannelID: meta.ChannelID,
			TaskID: meta.TaskID, Text: seg.ClosingEvent, OccurredAt: time.Now(),
		}); err != nil {
			slog.Warn("graph memory daily update failed", "error", err)
		}
	}
	return nil
}

// timezoneFor resolves the workspace memory-profile timezone (spec §6);
// empty/invalid falls back to memorycuration.DefaultTimezone.
func (h *GraphMemoryIngestHook) timezoneFor(ctx context.Context, workspaceID pgtype.UUID) *time.Location {
	if gate, err := h.queries.GetGraphMemoryScopedGate(ctx, workspaceID); err == nil && gate.Timezone != "" {
		if loc, err := time.LoadLocation(gate.Timezone); err == nil {
			return loc
		}
	}
	loc, err := time.LoadLocation(memorycuration.DefaultTimezone)
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// workspacesRoot returns the configured root or resolves the env default.
func (h *GraphMemoryIngestHook) workspacesRoot() (string, error) {
	if h.root != "" {
		return h.root, nil
	}
	return graphMemoryWorkspacesRoot()
}

// agentBackend lazily builds the pi agent backend for summarization (same
// env contract as the scheduler's graphMemoryPIBackend). A construction
// failure is logged once and degrades the ingester to its extractive
// fallback.
func (h *GraphMemoryIngestHook) agentBackend() memorygraph.AgentBackend {
	h.backendOnce.Do(func() {
		path := strings.TrimSpace(os.Getenv("MULTICA_PI_PATH"))
		if path == "" {
			path = "pi"
		}
		backend, err := agentpkg.New("pi", agentpkg.Config{ExecutablePath: path})
		if err != nil {
			h.backendErr = err
			slog.Warn("graph memory ingest: pi backend unavailable; summaries use the extractive fallback", "error", err)
			return
		}
		h.backend = backend
	})
	return h.backend
}

var _ SegmentIngestHook = (*GraphMemoryIngestHook)(nil)
