// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// ApplyDiveResult persists one Dive attempt's grading (spec §7): every
// normal trajectory gets its dimension scores, the min-dimension overall,
// and the unclamped reward computed from the SERVER-counted explore rounds;
// bypassed runs get reward 0 without grading. It is fenced on the worker's
// live lease; completion is separate so rewards can enter the online-RL
// outbox before the job and recall terminalize. An incomplete result still
// supplies rewards but is flagged so no authoritative ground truth is derived
// from it (A9).
func (s *GraphMemoryDiveService) ApplyDiveResult(ctx context.Context, jobID, workerID string, res *memorygraph.DiveResult, wRound float64) (bool, error) {
	if res == nil {
		return false, fmt.Errorf("graph memory dive: nil result")
	}
	jUUID, err := util.ParseUUID(jobID)
	if err != nil {
		return false, fmt.Errorf("graph memory dive: job id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var recallID pgtype.UUID
	err = tx.QueryRow(ctx, `
		SELECT recall_id FROM graph_memory_dive_job
		WHERE id = $1 AND leased_by = $2 AND status = 'running' AND lease_expires_at > now()
		FOR UPDATE
	`, jUUID, workerID).Scan(&recallID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph memory dive: load leased job: %w", err)
	}

	for _, sc := range res.Scores {
		tUUID, err := util.ParseUUID(sc.TrajectoryID)
		if err != nil {
			return false, fmt.Errorf("graph memory dive: trajectory id %q: %v", sc.TrajectoryID, err)
		}
		var rounds int
		err = tx.QueryRow(ctx, `
			SELECT rounds FROM graph_memory_trajectory
			WHERE id = $1 AND recall_id = $2 AND status IN ('found', 'miss')
			FOR UPDATE
		`, tUUID, recallID).Scan(&rounds)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("graph memory dive: score for foreign or non-normal trajectory %q", sc.TrajectoryID)
		}
		if err != nil {
			return false, fmt.Errorf("graph memory dive: load trajectory: %w", err)
		}
		overall := sc.Overall()
		reward := memorygraph.ExploreReward(overall, wRound, rounds)
		if _, err := tx.Exec(ctx, `
			UPDATE graph_memory_trajectory
			SET dive_status = 'graded', score_relevance = $3, score_groundedness = $4,
			    score_completeness = $5, overall_score = $6, reward = $7, updated_at = now()
			WHERE id = $1 AND recall_id = $2
		`, tUUID, recallID, sc.Relevance, sc.Groundedness, sc.Completeness, overall, reward); err != nil {
			return false, fmt.Errorf("graph memory dive: grade trajectory %q: %w", sc.TrajectoryID, err)
		}
	}
	for _, b := range res.Bypassed {
		tUUID, err := util.ParseUUID(b.TrajectoryID)
		if err != nil {
			return false, fmt.Errorf("graph memory dive: trajectory id %q: %v", b.TrajectoryID, err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE graph_memory_trajectory
			SET dive_status = 'bypassed', reward = 0, updated_at = now()
			WHERE id = $1 AND recall_id = $2 AND status IN ('error', 'budget', 'timeout')
		`, tUUID, recallID)
		if err != nil {
			return false, fmt.Errorf("graph memory dive: bypass trajectory %q: %w", b.TrajectoryID, err)
		}
		if tag.RowsAffected() != 1 {
			return false, fmt.Errorf("graph memory dive: bypass for foreign or normal trajectory %q", b.TrajectoryID)
		}
	}

	// Persist grading and catalog evidence while the lease is live. Completion
	// is deliberately separate so online-RL reward outboxing can succeed before
	// the durable job and recall terminalize.
	catalog := NewGraphMemoryInfoCatalogService(s.pool)
	if _, err := catalog.UpsertDiveInformationItems(ctx, tx, util.UUIDToString(recallID), res.NecessaryInformation, !res.Incomplete); err != nil {
		return false, fmt.Errorf("graph memory dive: catalog upsert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
