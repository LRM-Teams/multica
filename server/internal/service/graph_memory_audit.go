package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// GraphMemoryAuditService backs the query/judge/backtest audit API and, when
// configured with a database pool, the server-authoritative recall ledger.
type GraphMemoryAuditService struct {
	root string // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT
	pool *pgxpool.Pool
}

func NewGraphMemoryAuditService(root string) *GraphMemoryAuditService {
	return &GraphMemoryAuditService{root: root}
}

// NewGraphMemoryAuditServiceWithPool adds authoritative PostgreSQL ledger
// observability without changing the legacy file-query-log constructor.
func NewGraphMemoryAuditServiceWithPool(pool *pgxpool.Pool, root string) *GraphMemoryAuditService {
	return &GraphMemoryAuditService{root: root, pool: pool}
}

// GraphMemoryAuditSummary is the workspace-level audit view.
type GraphMemoryAuditSummary struct {
	WorkspaceID         string                 `json:"workspace_id"`
	Queries24h          int                    `json:"queries_24h"`
	RecallHits24h       int                    `json:"recall_hits_24h"`
	RecallHitRate24h    float64                `json:"recall_hit_rate_24h"`
	AvgExploreRounds24h float64                `json:"avg_explore_rounds_24h"`
	JudgedQueries24h    int                    `json:"judged_queries_24h"`
	RegressionsTotal    int                    `json:"regressions_total"`
	Ledger              GraphMemoryAuditLedger `json:"ledger"`
}

// GraphMemoryAuditFailure is the most recent Dive failure retained for audit.
type GraphMemoryAuditFailure struct {
	Kind    string `json:"kind,omitempty"`
	Message string `json:"message,omitempty"`
}

// GraphMemoryAuditLedger is a bounded aggregate over the authoritative
// server-side graph-memory ledger. It intentionally contains no prompts,
// provider responses, model text, or credentials.
type GraphMemoryAuditLedger struct {
	RecallsByStatus            map[string]int          `json:"recalls_by_status"`
	RecallsByErrorKind         map[string]int          `json:"recalls_by_error_kind"`
	TrajectoriesByStatus       map[string]int          `json:"trajectories_by_status"`
	TrajectoriesByDiveStatus   map[string]int          `json:"trajectories_by_dive_status"`
	AvgRounds                  float64                 `json:"avg_rounds"`
	P95Rounds                  float64                 `json:"p95_rounds"`
	GradedTrajectories         int                     `json:"graded_trajectories"`
	OverallRewardMin           float64                 `json:"overall_reward_min"`
	OverallRewardAvg           float64                 `json:"overall_reward_avg"`
	DiveJobsByStatus           map[string]int          `json:"dive_jobs_by_status"`
	DiveJobAttempts            int                     `json:"dive_job_attempts"`
	LastFailure                GraphMemoryAuditFailure `json:"last_failure"`
	RewardOutboxByStatus       map[string]int          `json:"reward_outbox_by_status"`
	OldestPendingAgeSeconds    float64                 `json:"oldest_pending_age_seconds"`
	OfflineExportEligible      int                     `json:"offline_export_eligible"`
	CatalogItems               int                     `json:"catalog_items"`
	DiveGroundTruthItems       int                     `json:"dive_ground_truth_items"`
	AuditOnlyItems             int                     `json:"audit_only_items"`
	ManagementRejections       int                     `json:"management_rejections"`
	ManagementRejectionReasons map[string]int          `json:"management_rejection_reasons"`
}

func newGraphMemoryAuditLedger() GraphMemoryAuditLedger {
	return GraphMemoryAuditLedger{
		RecallsByStatus: map[string]int{}, RecallsByErrorKind: map[string]int{},
		TrajectoriesByStatus: map[string]int{}, TrajectoriesByDiveStatus: map[string]int{},
		DiveJobsByStatus: map[string]int{}, RewardOutboxByStatus: map[string]int{},
		ManagementRejectionReasons: map[string]int{},
	}
}

func (s *GraphMemoryAuditService) Summary(ctx context.Context, workspaceID string) (*GraphMemoryAuditSummary, error) {
	root := s.root
	if root == "" {
		var err error
		root, err = graphMemoryWorkspacesRoot()
		if err != nil {
			return nil, err
		}
	}
	sum := &GraphMemoryAuditSummary{WorkspaceID: workspaceID, Ledger: newGraphMemoryAuditLedger()}
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	rounds := 0
	forEachWorkspaceGraph(root, workspaceID, func(_ memorygraph.GraphDirKind, _, dir string) {
		store := memorygraph.NewStore(dir)
		if err := store.Init(); err != nil {
			return
		}
		windows, err := store.ListQueryLogWindows()
		if err != nil {
			return
		}
		for _, w := range windows {
			entries, err := store.ReadQueryLog(w)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.Timestamp.Before(cutoff) {
					continue
				}
				sum.Queries24h++
				rounds += e.Rounds
				if e.Found {
					sum.RecallHits24h++
				}
				if e.JudgeDone {
					sum.JudgedQueries24h++
				}
			}
		}
		if regressions, err := store.ReadRegression(); err == nil {
			sum.RegressionsTotal += len(regressions)
		}
	})
	if sum.Queries24h > 0 {
		sum.RecallHitRate24h = float64(sum.RecallHits24h) / float64(sum.Queries24h)
		sum.AvgExploreRounds24h = float64(rounds) / float64(sum.Queries24h)
	}
	if s.pool != nil {
		if err := s.populateLedger(ctx, workspaceID, cutoff, &sum.Ledger); err != nil {
			return nil, err
		}
	}
	s.populateManagementRejections(root, workspaceID, &sum.Ledger)
	return sum, nil
}

func (s *GraphMemoryAuditService) populateManagementRejections(root, workspaceID string, ledger *GraphMemoryAuditLedger) {
	forEachWorkspaceGraph(root, workspaceID, func(_ memorygraph.GraphDirKind, _, dir string) {
		store := memorygraph.NewStore(dir)
		if err := store.Init(); err != nil {
			return
		}
		versions, err := store.ListVersions()
		if err != nil {
			return
		}
		log := memorygraph.NewOpLogger(store)
		for _, version := range versions {
			entries, err := log.Read(version)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.Op != memorygraph.OpRejectedManagement {
					continue
				}
				ledger.ManagementRejections++
				if reason, ok := entry.Detail["reason"].(string); ok && reason != "" {
					ledger.ManagementRejectionReasons[reason]++
				}
			}
		}
	})
}

func (s *GraphMemoryAuditService) populateLedger(ctx context.Context, workspaceID string, cutoff time.Time, ledger *GraphMemoryAuditLedger) error {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return fmt.Errorf("graph memory audit: workspace id: %w", err)
	}
	if err := s.countBy(ctx, `SELECT status, count(*) FROM graph_memory_recall WHERE workspace_id = $1 AND created_at >= $2 GROUP BY status`, ws, cutoff, ledger.RecallsByStatus); err != nil {
		return err
	}
	// Recall errors are recorded on trajectories by persistRuns/persistFailure;
	// count distinct recalls so retries cannot inflate the recall distribution.
	if err := s.countBy(ctx, `SELECT error_kind, count(DISTINCT recall_id) FROM graph_memory_trajectory WHERE workspace_id = $1 AND created_at >= $2 AND error_kind <> '' GROUP BY error_kind`, ws, cutoff, ledger.RecallsByErrorKind); err != nil {
		return err
	}
	if err := s.countBy(ctx, `SELECT status, count(*) FROM graph_memory_trajectory WHERE workspace_id = $1 AND created_at >= $2 GROUP BY status`, ws, cutoff, ledger.TrajectoriesByStatus); err != nil {
		return err
	}
	if err := s.countBy(ctx, `SELECT dive_status, count(*) FROM graph_memory_trajectory WHERE workspace_id = $1 AND created_at >= $2 AND dive_status <> '' GROUP BY dive_status`, ws, cutoff, ledger.TrajectoriesByDiveStatus); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(avg(rounds), 0), COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY rounds), 0)
		FROM graph_memory_trajectory WHERE workspace_id = $1 AND created_at >= $2 AND status IN ('found', 'miss')
	`, ws, cutoff).Scan(&ledger.AvgRounds, &ledger.P95Rounds); err != nil {
		return fmt.Errorf("graph memory audit: rounds: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(min(reward), 0), COALESCE(avg(reward), 0)
		FROM graph_memory_trajectory
		WHERE workspace_id = $1 AND created_at >= $2 AND dive_status = 'graded' AND reward IS NOT NULL
	`, ws, cutoff).Scan(&ledger.GradedTrajectories, &ledger.OverallRewardMin, &ledger.OverallRewardAvg); err != nil {
		return fmt.Errorf("graph memory audit: scores: %w", err)
	}
	if err := s.countBy(ctx, `SELECT status, count(*) FROM graph_memory_dive_job WHERE workspace_id = $1 AND created_at >= $2 GROUP BY status`, ws, cutoff, ledger.DiveJobsByStatus); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(sum(attempts), 0) FROM graph_memory_dive_job WHERE workspace_id = $1 AND created_at >= $2`, ws, cutoff).Scan(&ledger.DiveJobAttempts); err != nil {
		return fmt.Errorf("graph memory audit: dive attempts: %w", err)
	}
	var failureAt *time.Time
	if err := s.pool.QueryRow(ctx, `
		SELECT error_kind, last_error, updated_at FROM graph_memory_dive_job
		WHERE workspace_id = $1 AND last_error <> '' ORDER BY updated_at DESC LIMIT 1
	`, ws).Scan(&ledger.LastFailure.Kind, &ledger.LastFailure.Message, &failureAt); err != nil && err.Error() != "no rows in result set" {
		return fmt.Errorf("graph memory audit: last failure: %w", err)
	}
	ledger.LastFailure.Message = RedactGraphMemoryObservability(ledger.LastFailure.Message)
	if err := s.countBy(ctx, `SELECT status, count(*) FROM graph_memory_reward_outbox WHERE workspace_id = $1 AND created_at >= $2 GROUP BY status`, ws, cutoff, ledger.RewardOutboxByStatus); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0) FROM graph_memory_reward_outbox WHERE workspace_id = $1 AND status = 'pending'`, ws).Scan(&ledger.OldestPendingAgeSeconds); err != nil {
		return fmt.Errorf("graph memory audit: pending age: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_trajectory t
		JOIN graph_memory_recall r ON r.id = t.recall_id
		LEFT JOIN graph_memory_dive_job j ON j.recall_id = r.id
		WHERE t.workspace_id = $1 AND r.created_at >= $2 AND r.training_mode = 'offline_rl'
		  AND t.dive_status = 'graded' AND j.status = 'completed'
	`, ws, cutoff).Scan(&ledger.OfflineExportEligible); err != nil {
		return fmt.Errorf("graph memory audit: offline eligibility: %w", err)
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE EXISTS (
			SELECT 1 FROM graph_memory_recall_info_item ri
			JOIN graph_memory_dive_job j ON j.recall_id = ri.recall_id
			WHERE ri.item_id = i.id AND j.status = 'completed' AND NOT j.incomplete
		))
		FROM graph_memory_info_item i WHERE i.workspace_id = $1
	`, ws).Scan(&ledger.CatalogItems, &ledger.DiveGroundTruthItems); err != nil {
		return fmt.Errorf("graph memory audit: catalog authority: %w", err)
	}
	ledger.AuditOnlyItems = ledger.CatalogItems - ledger.DiveGroundTruthItems
	return nil
}

func (s *GraphMemoryAuditService) countBy(ctx context.Context, query string, ws pgtype.UUID, cutoff time.Time, destination map[string]int) error {
	rows, err := s.pool.Query(ctx, query, ws, cutoff)
	if err != nil {
		return fmt.Errorf("graph memory audit: counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return fmt.Errorf("graph memory audit: count scan: %w", err)
		}
		destination[key] = count
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("graph memory audit: count rows: %w", err)
	}
	return nil
}
