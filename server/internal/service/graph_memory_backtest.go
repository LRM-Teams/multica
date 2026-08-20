// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

const graphMemoryBacktestRecallPageSize = 500

// AttachBacktestGroundTruth enriches collected file-backed backtest queries
// with authoritative catalog items and server-counted recall baselines. The
// watermark makes the keyset walk a stable ledger snapshot. When a linked
// recall query differs from a local log query, the ledger text is trusted.
func (s *GraphMemoryInfoCatalogService) AttachBacktestGroundTruth(ctx context.Context, graphKind, graphOwnerID string, queries []*memorygraph.BacktestQuery) error {
	owner, err := util.ParseUUID(graphOwnerID)
	if err != nil {
		return fmt.Errorf("graph memory backtest ground truth: graph owner id: %w", err)
	}
	var watermark *time.Time
	if err := s.pool.QueryRow(ctx, `
		SELECT max(created_at) FROM graph_memory_recall
		WHERE graph_kind = $1 AND graph_owner_id = $2
	`, graphKind, owner).Scan(&watermark); err != nil {
		return fmt.Errorf("graph memory backtest ground truth: snapshot watermark: %w", err)
	}
	if watermark == nil {
		return nil
	}
	byTrace := make(map[string][]*memorygraph.BacktestQuery, len(queries))
	for _, q := range queries {
		if q != nil && q.TraceID != "" {
			byTrace[q.TraceID] = append(byTrace[q.TraceID], q)
		}
	}
	if len(byTrace) == 0 {
		return nil
	}

	var (
		lastCreated time.Time
		lastID      = pgtype.UUID{Valid: true}
		seen        = make(map[string]bool)
	)
	for {
		rows, err := s.pool.Query(ctx, `
			SELECT r.id, r.trace_id, r.query,
			       COALESCE(
			         (SELECT t.rounds FROM graph_memory_trajectory t
			          WHERE t.recall_id = r.id AND t.status = 'found'
			          ORDER BY t.seed_index ASC LIMIT 1),
			         (SELECT t.rounds FROM graph_memory_trajectory t
			          WHERE t.recall_id = r.id AND t.status IN ('found', 'miss', 'error', 'budget', 'timeout')
			          ORDER BY t.rounds DESC, t.seed_index ASC LIMIT 1),
			         0
			       ) AS adopted_rounds,
			       EXISTS(SELECT 1 FROM graph_memory_trajectory t WHERE t.recall_id = r.id AND t.status = 'found') AS baseline_found,
			       r.created_at
			FROM graph_memory_recall r
			WHERE r.graph_kind = $1 AND r.graph_owner_id = $2 AND r.created_at <= $3
			  AND (r.created_at, r.id) > ($4, $5)
			ORDER BY r.created_at, r.id
			LIMIT $6
		`, graphKind, owner, *watermark, lastCreated, lastID, graphMemoryBacktestRecallPageSize)
		if err != nil {
			return fmt.Errorf("graph memory backtest ground truth: list recalls: %w", err)
		}
		pageCount := 0
		for rows.Next() {
			var (
				id             pgtype.UUID
				traceID, query string
				rounds         int
				found          bool
				created        time.Time
			)
			if err := rows.Scan(&id, &traceID, &query, &rounds, &found, &created); err != nil {
				rows.Close()
				return fmt.Errorf("graph memory backtest ground truth: scan recall: %w", err)
			}
			pageCount++
			lastCreated, lastID = created, id
			recallID := util.UUIDToString(id)
			if seen[recallID] {
				continue
			}
			seen[recallID] = true
			targets := byTrace[traceID]
			if len(targets) == 0 {
				continue
			}
			items, err := s.ItemsForRecall(ctx, recallID)
			if err != nil {
				rows.Close()
				return err
			}
			backtestItems := make([]memorygraph.BacktestItem, 0, len(items))
			for _, item := range items {
				if item.Status != "authoritative" {
					continue
				}
				backtestItems = append(backtestItems, memorygraph.BacktestItem{
					ID: item.ID, Statement: NormalizeInfoStatement(item.Statement),
					NodeIDs: append([]string(nil), item.NodeIDs...), SourceRefs: append([]string(nil), item.SourceRefs...),
				})
			}
			if len(backtestItems) == 0 {
				continue
			}
			for _, q := range targets {
				q.Items = append([]memorygraph.BacktestItem(nil), backtestItems...)
				q.BaselineRounds = backtestBaselineRounds(rounds)
				q.BaselineFound = found
				q.Query = query
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("graph memory backtest ground truth: iterate recalls: %w", err)
		}
		rows.Close()
		if pageCount < graphMemoryBacktestRecallPageSize {
			return nil
		}
	}
}

// backtestBaselineRounds mirrors memorygraph's legacy fallback for server
// ledger rows whose adopted trajectory has zero recorded rounds.
func backtestBaselineRounds(rounds int) int {
	if rounds <= 0 {
		return memorygraph.DefaultBacktestBaselineRounds
	}
	return rounds
}
