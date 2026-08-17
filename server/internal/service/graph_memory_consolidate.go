package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
	agentpkg "github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// GraphMemoryConsolidationService runs manual consolidations (spec §10).
// Every graph mutation serializes through the GraphMutationCoordinator and
// the run is recorded in graph_memory_consolidation_run for status/retry.
type GraphMemoryConsolidationService struct {
	queries *db.Queries
	pool    *pgxpool.Pool
	root    string
}

func NewGraphMemoryConsolidationService(queries *db.Queries, pool *pgxpool.Pool, root string) *GraphMemoryConsolidationService {
	return &GraphMemoryConsolidationService{queries: queries, pool: pool, root: root}
}

// ErrGraphNotReady is the stable refusal for non-graph or not-ready
// workspaces; the handler maps it to 409 graph_not_ready.
var ErrGraphNotReady = fmt.Errorf("graph_profile_invalid: workspace is not a ready graph workspace")

// Run validates the workspace gate, records a run row, and executes the
// consolidation asynchronously. Retry semantics = call Run again.
func (s *GraphMemoryConsolidationService) Run(ctx context.Context, workspaceID, triggerKind string) (string, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return "", err
	}
	gate, err := s.queries.GetGraphMemoryScopedGate(ctx, ws)
	if err != nil || gate.MemoryType != "graph" || !gate.ScopedWriterReady {
		return "", ErrGraphNotReady
	}
	run, err := s.queries.InsertGraphMemoryConsolidationRun(ctx, db.InsertGraphMemoryConsolidationRunParams{
		WorkspaceID: ws, TriggerKind: triggerKind,
	})
	if err != nil {
		return "", err
	}
	runID := util.UUIDToString(run.ID)
	go s.execute(context.Background(), run.ID, workspaceID)
	return runID, nil
}

func (s *GraphMemoryConsolidationService) execute(ctx context.Context, runID pgtype.UUID, workspaceID string) {
	details := map[string]any{}
	finish := func(status, errText string) {
		body, _ := json.Marshal(details)
		if err := s.queries.FinishGraphMemoryConsolidationRun(ctx, db.FinishGraphMemoryConsolidationRunParams{
			ID: runID, Status: status, Error: errText, Details: body,
		}); err != nil {
			slog.Error("graph memory consolidation: finish run record failed", "run_id", runID, "error", err)
		}
	}
	root := s.root
	if root == "" {
		var err error
		root, err = graphMemoryWorkspacesRoot()
		if err != nil {
			finish("failed", err.Error())
			return
		}
	}
	coordinator := NewGraphMutationCoordinator(s.pool)
	failed := false
	forEachWorkspaceGraph(root, workspaceID, func(kind memorygraph.GraphDirKind, ownerID, dir string) {
		err := coordinator.WithGraphLock(ctx, workspaceID, string(kind), ownerID, func(ctx context.Context) error {
			store := memorygraph.NewStore(dir)
			if err := store.Init(); err != nil {
				return err
			}
			backend, err := graphMemoryPIBackend()
			if err != nil {
				return err
			}
			c := memorygraph.NewConsolidator(store, backend, memorygraph.DefaultConsolidateConfig(), "pi",
				memorygraph.NewOpLogger(store), memorygraph.NewTraceRecorder(dir))
			res, err := c.Consolidate(ctx)
			if err != nil {
				return err
			}
			details[string(kind)+":"+ownerID] = fmt.Sprintf("version %d", res.WinnerVersion)
			return nil
		})
		if err != nil {
			slog.Warn("graph memory manual consolidation failed", "dir", dir, "error", err)
			details[string(kind)+":"+ownerID] = "error: " + err.Error()
			failed = true
		}
	})
	if failed {
		finish("failed", "one or more graphs failed; see details")
		return
	}
	finish("succeeded", "")
}

// graphMemoryPIBackend mirrors the scheduler job's pi backend construction
// (MULTICA_PI_PATH / MULTICA_PI_MODEL env contract).
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
