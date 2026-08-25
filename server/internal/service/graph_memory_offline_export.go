// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
)

// Offline export exclusion reasons for graph-memory trajectories (spec §5,
// acceptance A12, D15/D26). Complete graded rows and explicitly labeled
// incomplete=true rows are eligible; everything else is an excluded line.
const (
	GraphMemoryOfflineReasonJudgeFailed             = "judge_failed"
	GraphMemoryOfflineReasonExploreBypassed         = "explore_bypassed"
	GraphMemoryOfflineReasonNotTerminal             = "not_terminal"
	GraphMemoryOfflineReasonWrongModeOfflineCapture = "wrong_mode_offline_capture"
)

const graphMemoryOfflineExportMaxLimit = 1000

// GraphMemoryOfflineExportLine is one NDJSON-style export candidate: either a
// trainable "trajectory" or an "excluded" row with a machine-readable reason.
// AReaL session ids and proxy keys never appear on this type (A29/D15).
type GraphMemoryOfflineExportLine struct {
	TrajectoryID      string   `json:"trajectory_id"`
	RecallID          string   `json:"recall_id"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	TraceID           string   `json:"trace_id,omitempty"`
	GraphKind         string   `json:"graph_kind,omitempty"`
	GraphOwnerID      string   `json:"graph_owner_id,omitempty"`
	GraphVersion      int      `json:"graph_version,omitempty"`
	SeedIndex         int      `json:"seed_index,omitempty"`
	ScoreRelevance    *float64 `json:"score_relevance,omitempty"`
	ScoreGroundedness *float64 `json:"score_groundedness,omitempty"`
	ScoreCompleteness *float64 `json:"score_completeness,omitempty"`
	OverallScore      *float64 `json:"overall_score,omitempty"`
	Reward            *float64 `json:"reward,omitempty"`
	Rounds            int      `json:"rounds,omitempty"`
	Incomplete        bool     `json:"incomplete,omitempty"`
	ArtifactRef       string   `json:"artifact_ref,omitempty"`
	Summary           string   `json:"summary,omitempty"`
}

// GraphMemoryOfflineExportService lists graph-memory trajectories for offline
// training export. It is self-contained over graph_memory_* tables and never
// reads or writes pi_provider_call or the roster-agent ledger (D15).
type GraphMemoryOfflineExportService struct {
	pool *pgxpool.Pool
}

func NewGraphMemoryOfflineExportService(pool *pgxpool.Pool) *GraphMemoryOfflineExportService {
	return &GraphMemoryOfflineExportService{pool: pool}
}

// ClassifyOfflineExportEligibility is the pure eligibility matrix (spec §5).
// jobIncomplete does not gate eligibility: labeled incomplete=true rows still
// export. recallTerminal is accepted for callers that already know the recall
// lifecycle; ungraded / unfinished jobs are "not_terminal".
func ClassifyOfflineExportEligibility(trainingMode, trajectoryDiveStatus string, recallTerminal, jobCompleted, jobIncomplete bool) (eligible bool, reason string) {
	switch trainingMode {
	case "online_rl":
		return false, OfflineReasonWrongModeOnlineRL
	case "offline_capture":
		return false, GraphMemoryOfflineReasonWrongModeOfflineCapture
	case "offline_rl":
		// continue into dive-status checks
	default:
		return false, GraphMemoryOfflineReasonNotTerminal
	}
	switch trajectoryDiveStatus {
	case "bypassed":
		return false, GraphMemoryOfflineReasonExploreBypassed
	case "judge_failed":
		return false, GraphMemoryOfflineReasonJudgeFailed
	case "graded":
		if jobCompleted {
			return true, ""
		}
		return false, GraphMemoryOfflineReasonNotTerminal
	default:
		return false, GraphMemoryOfflineReasonNotTerminal
	}
}

// ListOfflineExports returns one line per trajectory in the workspace,
// including excluded candidates with their reason. Results are ordered by
// recall created_at then seed_index. limit <= 0 or above the cap is clamped
// to graphMemoryOfflineExportMaxLimit.
func (s *GraphMemoryOfflineExportService) ListOfflineExports(ctx context.Context, workspaceID string, limit int) ([]GraphMemoryOfflineExportLine, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("graph memory offline export: workspace id: %w", err)
	}
	if limit <= 0 || limit > graphMemoryOfflineExportMaxLimit {
		limit = graphMemoryOfflineExportMaxLimit
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
		  t.id, t.recall_id, t.seed_index, t.summary, t.rounds, t.artifact_ref,
		  t.dive_status, t.score_relevance, t.score_groundedness, t.score_completeness,
		  t.overall_score, t.reward,
		  r.training_mode, r.trace_id, r.graph_kind, r.graph_owner_id, r.graph_version,
		  r.terminal_at, j.status, j.incomplete
		FROM graph_memory_trajectory t
		JOIN graph_memory_recall r ON r.id = t.recall_id
		LEFT JOIN graph_memory_dive_job j ON j.recall_id = r.id
		WHERE t.workspace_id = $1
		ORDER BY r.created_at ASC, t.seed_index ASC
		LIMIT $2
	`, ws, limit)
	if err != nil {
		return nil, fmt.Errorf("graph memory offline export: list: %w", err)
	}
	defer rows.Close()

	out := make([]GraphMemoryOfflineExportLine, 0)
	for rows.Next() {
		var (
			trajID, recallID, ownerID  pgtype.UUID
			seedIndex, rounds, version int
			summary, artifactRef       string
			diveStatus, trainingMode   string
			traceID, graphKind         string
			rel, grounded, complete    *float64
			overall, reward            *float64
			terminalAt                 *time.Time
			jobStatus                  *string
			jobIncomplete              *bool
		)
		if err := rows.Scan(
			&trajID, &recallID, &seedIndex, &summary, &rounds, &artifactRef,
			&diveStatus, &rel, &grounded, &complete, &overall, &reward,
			&trainingMode, &traceID, &graphKind, &ownerID, &version,
			&terminalAt, &jobStatus, &jobIncomplete,
		); err != nil {
			return nil, fmt.Errorf("graph memory offline export: scan: %w", err)
		}
		jobCompleted := jobStatus != nil && *jobStatus == "completed"
		jobIncompleteFlag := jobIncomplete != nil && *jobIncomplete
		incomplete := jobCompleted && jobIncompleteFlag
		recallTerminal := terminalAt != nil
		eligible, reason := ClassifyOfflineExportEligibility(
			trainingMode, diveStatus, recallTerminal, jobCompleted, jobIncompleteFlag)
		if !eligible {
			out = append(out, GraphMemoryOfflineExportLine{
				TrajectoryID: util.UUIDToString(trajID),
				RecallID:     util.UUIDToString(recallID),
				Status:       offlineStatusExcluded,
				Reason:       reason,
			})
			continue
		}
		out = append(out, GraphMemoryOfflineExportLine{
			TrajectoryID:      util.UUIDToString(trajID),
			RecallID:          util.UUIDToString(recallID),
			Status:            offlineStatusTrajectory,
			TraceID:           traceID,
			GraphKind:         graphKind,
			GraphOwnerID:      util.UUIDToString(ownerID),
			GraphVersion:      version,
			SeedIndex:         seedIndex,
			ScoreRelevance:    rel,
			ScoreGroundedness: grounded,
			ScoreCompleteness: complete,
			OverallScore:      overall,
			Reward:            reward,
			Rounds:            rounds,
			Incomplete:        incomplete,
			ArtifactRef:       artifactRef,
			Summary:           summary,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph memory offline export: rows: %w", err)
	}
	return out, nil
}
