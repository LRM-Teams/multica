package service

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/memorygraph"
)

// GraphMemoryAuditService backs the query/judge/backtest audit API
// (spec §10): 24h recall volume and quality and judge write-back coverage,
// aggregated across the workspace's physical graphs.
type GraphMemoryAuditService struct {
	root string // workspaces root; empty resolves MULTICA_WORKSPACES_ROOT
}

func NewGraphMemoryAuditService(root string) *GraphMemoryAuditService {
	return &GraphMemoryAuditService{root: root}
}

// GraphMemoryAuditSummary is the workspace-level audit view (spec §10).
type GraphMemoryAuditSummary struct {
	WorkspaceID         string  `json:"workspace_id"`
	Queries24h          int     `json:"queries_24h"`
	RecallHits24h       int     `json:"recall_hits_24h"`
	RecallHitRate24h    float64 `json:"recall_hit_rate_24h"`
	AvgExploreRounds24h float64 `json:"avg_explore_rounds_24h"`
	JudgedQueries24h    int     `json:"judged_queries_24h"`
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
	sum := &GraphMemoryAuditSummary{WorkspaceID: workspaceID}
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
	})
	if sum.Queries24h > 0 {
		sum.RecallHitRate24h = float64(sum.RecallHits24h) / float64(sum.Queries24h)
		sum.AvgExploreRounds24h = float64(rounds) / float64(sum.Queries24h)
	}
	return sum, nil
}
