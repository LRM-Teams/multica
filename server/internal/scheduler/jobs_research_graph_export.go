package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// JobNameResearchGraphExport is the research→memory graph export job
// (unification spec §4.2): polls the research ledgers and mirrors admitted
// nodes/insights/results into the workspace's research memory graph.
const JobNameResearchGraphExport = "research_graph_export"

// researchGraphExportSwitchEnv is the process-level activation switch. The
// job registers unconditionally and stays inert (handler no-ops) until an
// operator opts a deployment in; per-workspace Graph gating is layered on
// top inside the exporter, which fails closed for non-graph workspaces.
const researchGraphExportSwitchEnv = "MULTICA_RESEARCH_GRAPH_EXPORT_ENABLED"

// researchGraphExportEnabled reads the switch, defaulting to OFF.
func researchGraphExportEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(researchGraphExportSwitchEnv)))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

// researchExportRunner runs one export poll for one workspace; it is a
// function value so tests can drive gating and retry behaviour without the
// ledger.
type researchExportRunner func(ctx context.Context, workspaceID pgtype.UUID) (*service.ResearchGraphExportResult, error)

// ResearchGraphExportJob returns the export job: a 1-minute watermark poll
// driven by research_graph_export_state, so missed ticks recover on the next
// run without per-tick replay (CatchUpLatestOnly).
func ResearchGraphExportJob(pool *pgxpool.Pool) JobSpec {
	return JobSpec{
		Name:              JobNameResearchGraphExport,
		Cadence:           time.Minute,
		ScheduleDelay:     0,
		CatchUpMode:       CatchUpLatestOnly,
		CatchUpWindow:     5 * time.Minute,
		MaxPlansPerTick:   1,
		RunTimeout:        5 * time.Minute,
		StaleTimeout:      6 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		MaxAttempts:       3,
		RetryBackoff:      []time.Duration{30 * time.Second, time.Minute},
		Scopes:            StaticScopes(ScopeGlobal),
		Handler:           makeResearchGraphExportHandler(pool, researchGraphExportRunnerForPool(pool)),
	}
}

// researchGraphExportRunnerForPool wires the production exporter with the
// deterministic hash-cosine import-time dedup scorer. A nil pool (tests,
// DB-less deployments) yields a nil runner and the handler stays inert.
func researchGraphExportRunnerForPool(pool *pgxpool.Pool) researchExportRunner {
	if pool == nil {
		return nil
	}
	exporter := service.NewResearchGraphExporter(pool, db.New(pool), service.ResearchGraphExporterConfig{})
	exporter.SetSimilarity(service.NewResearchHashSimilarity())
	return exporter.ExportWorkspace
}

// makeResearchGraphExportHandler sweeps every graph-mode workspace. A failing
// workspace is logged and skipped so one broken ledger cannot starve the
// rest, but the tick reports failure so the scheduler retries — replays are
// idempotent (watermark + export-key dedup) and the failed workspace is
// retried, never skipped.
func makeResearchGraphExportHandler(pool *pgxpool.Pool, run researchExportRunner) Handler {
	return func(ctx context.Context, in HandlerInput) (HandlerResult, error) {
		if !researchGraphExportEnabled() {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "export_disabled"}}, nil
		}
		if pool == nil || run == nil {
			return HandlerResult{Result: map[string]any{"skipped": true, "reason": "pool_unavailable"}}, nil
		}
		workspaces, err := listResearchExportWorkspaces(ctx, pool)
		if err != nil {
			return HandlerResult{}, fmt.Errorf("research graph export: list workspaces: %w", err)
		}
		exported, failed, nodes, edges := 0, 0, 0, 0
		var firstErr error
		for _, id := range workspaces {
			res, err := run(ctx, id)
			if err != nil {
				failed++
				slog.Warn("research graph export failed", "workspace", util.UUIDToString(id), "error", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			exported++
			nodes += res.NodesWritten
			edges += res.EdgesWritten
			if in.Heartbeat != nil {
				if err := in.Heartbeat(ctx); err != nil {
					return HandlerResult{}, err
				}
			}
		}
		result := HandlerResult{
			RowsAffected: int64(exported),
			Result: map[string]any{
				"workspaces":    len(workspaces),
				"exported":      exported,
				"failed":        failed,
				"nodes_written": nodes,
				"edges_written": edges,
			},
		}
		if failed > 0 {
			return result, fmt.Errorf("research graph export: %d/%d workspaces failed (first: %w)", failed, len(workspaces), firstErr)
		}
		return result, nil
	}
}

// listResearchExportWorkspaces returns the workspaces eligible for research
// export: every graph_memory_profile row in graph mode, plus — only when the
// process env default is graph — workspaces without a profile row (they
// inherit the env default). Profiled legacy workspaces stay excluded no
// matter the env, mirroring resolveGraphMemoryType's precedence. Direct pgx:
// no sqlc query covers the enumeration (same deviation as the exporter).
func listResearchExportWorkspaces(ctx context.Context, pool *pgxpool.Pool) ([]pgtype.UUID, error) {
	envGraph := strings.ToLower(strings.TrimSpace(os.Getenv("MULTICA_MEMORY_TYPE"))) == "graph"
	rows, err := pool.Query(ctx, `
		SELECT workspace_id FROM graph_memory_profile WHERE memory_type = 'graph'
		UNION
		SELECT w.id FROM workspace w
		WHERE $1 AND NOT EXISTS (
			SELECT 1 FROM graph_memory_profile p WHERE p.workspace_id = w.id
		)
		ORDER BY 1`, envGraph)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
