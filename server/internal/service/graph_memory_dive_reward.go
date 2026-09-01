// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/util"
)

// diveInputManifestHash pins what the judge graded (spec §14.2: dive
// model, prompt, policy, input manifest and raw dimensions belong in the
// reward ledger). The hash covers the judged job identity, the round weight
// and the exact graded/bypassed inputs: re-applying the same result replays
// the same revision idempotently, while a different judgement appends the
// next revision (spec §14.4).
func diveInputManifestHash(res *memorygraph.DiveResult, wRound float64, jobID, recallID string) string {
	manifest := struct {
		JobID      string                            `json:"job_id"`
		RecallID   string                            `json:"recall_id"`
		WRound     float64                           `json:"w_round"`
		Scores     []memorygraph.DiveTrajectoryScore `json:"scores"`
		Bypassed   []memorygraph.DiveRunInput        `json:"bypassed"`
		Incomplete bool                              `json:"incomplete"`
		Rounds     int                               `json:"rounds"`
	}{jobID, recallID, wRound, res.Scores, res.Bypassed, res.Incomplete, res.Rounds}
	blob, err := json.Marshal(manifest)
	if err != nil {
		// The manifest is plain marshalable data; a failure is a programming
		// error. Fall back to a deterministic encoding that still separates
		// re-evaluations: never silently reuse a hash.
		blob = []byte(fmt.Sprintf("%s:%s:%v:%d", jobID, recallID, wRound, res.Rounds))
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// ApplyDiveResult persists one Dive attempt's grading (spec §14.2, Task 19):
// every normal trajectory gets its dimension scores, the min-dimension
// overall, and the unclamped reward computed from the SERVER-counted explore
// rounds; bypassed runs (the explore agent's own error/budget/timeout
// violation) get a deterministic negative reward without model grading.
// Every value lands in the immutable reward ledger first (same-manifest
// replays are idempotent; a re-evaluation appends a revision), and the
// trajectory keeps a projection of the latest revision (reward_status /
// reward_revision) for training selection and offline export. It is fenced
// on the worker's live lease; completion is separate so rewards can enter
// the online-RL outbox before the job and recall terminalize. An incomplete
// result still supplies rewards but is flagged so no authoritative ground
// truth is derived from it (A9).
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

	var (
		recallID pgtype.UUID
		wsID     pgtype.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT recall_id, workspace_id FROM graph_memory_dive_job
		WHERE id = $1 AND leased_by = $2 AND status = 'running' AND lease_expires_at > now()
		FOR UPDATE
	`, jUUID, workerID).Scan(&recallID, &wsID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("graph memory dive: load leased job: %w", err)
	}
	manifestHash := diveInputManifestHash(res, wRound, jobID, util.UUIDToString(recallID))

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
		value := reward
		revision, err := RecordTrajectoryRewardTx(ctx, tx, TrajectoryRewardRecord{
			WorkspaceID: wsID, TrajectoryID: tUUID, RewardKind: "explore",
			Status: "available", Value: &value,
			Components: memorygraph.RewardComponents{
				Source: "graded", Relevance: sc.Relevance, Groundedness: sc.Groundedness,
				Completeness: sc.Completeness, Overall: overall,
				Rounds: float64(rounds), WRound: wRound,
			},
			PolicyVersion:     memorygraph.ExploreRewardPolicyVersion,
			InputManifestHash: manifestHash,
		})
		if err != nil {
			return false, fmt.Errorf("graph memory dive: reward record for %q: %w", sc.TrajectoryID, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE graph_memory_trajectory
			SET dive_status = 'graded', score_relevance = $3, score_groundedness = $4,
			    score_completeness = $5, overall_score = $6, reward = $7,
			    reward_status = 'graded', reward_revision = $8, updated_at = now()
			WHERE id = $1 AND recall_id = $2
		`, tUUID, recallID, sc.Relevance, sc.Groundedness, sc.Completeness, overall, reward, revision); err != nil {
			return false, fmt.Errorf("graph memory dive: grade trajectory %q: %w", sc.TrajectoryID, err)
		}
	}
	for _, b := range res.Bypassed {
		tUUID, err := util.ParseUUID(b.TrajectoryID)
		if err != nil {
			return false, fmt.Errorf("graph memory dive: trajectory id %q: %v", b.TrajectoryID, err)
		}
		// The explore agent's own terminal violation: a deterministic
		// negative reward (spec §14.2), never a neutral zero.
		value := memorygraph.DeterministicViolationReward(wRound, b.Rounds)
		revision, err := RecordTrajectoryRewardTx(ctx, tx, TrajectoryRewardRecord{
			WorkspaceID: wsID, TrajectoryID: tUUID, RewardKind: "explore",
			Status: "available", Value: &value,
			Components: memorygraph.RewardComponents{
				Source: "deterministic", Violation: b.Status,
				Rounds: float64(b.Rounds), WRound: wRound,
			},
			PolicyVersion:     memorygraph.ExploreRewardPolicyVersion,
			InputManifestHash: manifestHash,
		})
		if err != nil {
			return false, fmt.Errorf("graph memory dive: reward record for %q: %w", b.TrajectoryID, err)
		}
		tag, err := tx.Exec(ctx, `
			UPDATE graph_memory_trajectory
			SET dive_status = 'bypassed', reward = $3,
			    reward_status = 'deterministic', reward_revision = $4, updated_at = now()
			WHERE id = $1 AND recall_id = $2 AND status IN ('error', 'budget', 'timeout')
		`, tUUID, recallID, value, revision)
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
