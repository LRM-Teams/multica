// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

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
	queries *db.Queries
	root    string // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT per call
	bm      *obsmetrics.BusinessMetrics
	model   string

	backendOnce sync.Once
	backend     memorygraph.AgentBackend
	backendErr  error
}

// NewGraphMemoryIngestHook returns the production ingest hook. queries must
// be non-nil (the hook resolves the segment's workspace from the task row);
// bm may be nil. root may be empty, in which case MULTICA_WORKSPACES_ROOT is
// resolved per ingest.
func NewGraphMemoryIngestHook(queries *db.Queries, root string, bm *obsmetrics.BusinessMetrics) *GraphMemoryIngestHook {
	return &GraphMemoryIngestHook{
		queries: queries,
		root:    root,
		bm:      bm,
		model:   strings.TrimSpace(os.Getenv("MULTICA_PI_MODEL")),
	}
}

// graphScopeForTask resolves the canonical graph scope one task writes to:
// the owning project of the task's issue when present, else the task's
// channel, else no scope (unscoped tasks never touch the graph, spec §2).
func (h *GraphMemoryIngestHook) graphScopeForTask(ctx context.Context, task db.AgentInboxEvent) (memorygraph.GraphDirKind, string, error) {
	if task.IssueID.Valid {
		issue, err := h.queries.GetIssue(ctx, task.IssueID)
		if err != nil {
			return "", "", fmt.Errorf("graph memory ingest: load issue %s: %w", util.UUIDToString(task.IssueID), err)
		}
		if issue.ProjectID.Valid {
			return memorygraph.GraphDirKindProject, util.UUIDToString(issue.ProjectID), nil
		}
	}
	if task.ChannelID.Valid {
		return memorygraph.GraphDirKindChannel, util.UUIDToString(task.ChannelID), nil
	}
	return "", "", nil
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

	// Per-workspace memory_type gate (design §1/A4): legacy workspaces
	// never grow staging segments.
	if rt := resolveGraphMemoryType(ctx, h.queries, task.WorkspaceID, graphMemoryEnvMemoryType()); rt != "graph" {
		return nil
	}

	root, err := h.workspacesRoot()
	if err != nil {
		return err
	}
	// Interim scoped resolution (refined to the route registry in the scoped
	// writer task): issue-bound tasks write the owning project's graph (the
	// task row carries issue_id, not project_id); channel-only tasks write
	// the channel graph; unscoped tasks never create graphs (spec §2).
	wsID := util.UUIDToString(task.WorkspaceID)
	kind, ownerID, err := h.graphScopeForTask(ctx, task)
	if err != nil {
		return err
	}
	if ownerID == "" {
		return nil
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
	return ingester.Ingest(ctx, seg)
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
