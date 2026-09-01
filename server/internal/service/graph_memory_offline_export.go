// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Offline export exclusion reasons for graph-memory trajectories (spec §5,
// acceptance A12, D15/D26). Complete graded rows and explicitly labeled
// incomplete=true rows are eligible; everything else is an excluded line.
const (
	GraphMemoryOfflineReasonJudgeFailed             = "judge_failed"
	GraphMemoryOfflineReasonExploreBypassed         = "explore_bypassed"
	GraphMemoryOfflineReasonNotTerminal             = "not_terminal"
	GraphMemoryOfflineReasonWrongModeOfflineCapture = "wrong_mode_offline_capture"
	// GraphMemoryOfflineReasonNotInManifest: the trajectory is not fixed in
	// the authorized training manifest (Task 18) and never serializes.
	GraphMemoryOfflineReasonNotInManifest = "not_in_manifest"
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
//
// Task 18 (spec 14.1): trainingManifestID is REQUIRED. The graph-memory
// offline export serializes only trajectories fixed in an exported training
// manifest; without one the call answers training_disabled /
// manifest_required and never selects rows directly.
func (s *GraphMemoryOfflineExportService) ListOfflineExports(ctx context.Context, workspaceID, trainingManifestID string, limit int) ([]GraphMemoryOfflineExportLine, error) {
	ws, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("graph memory offline export: workspace id: %w", err)
	}
	if limit <= 0 || limit > graphMemoryOfflineExportMaxLimit {
		limit = graphMemoryOfflineExportMaxLimit
	}
	manifestItems, err := s.requireTrainingManifest(ctx, ws, trainingManifestID)
	if err != nil {
		return nil, err
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

	lines := make([]GraphMemoryOfflineExportLine, 0)
	ownerKindByID := make(map[pgtype.UUID]string)
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
			lines = append(lines, GraphMemoryOfflineExportLine{
				TrajectoryID: util.UUIDToString(trajID),
				RecallID:     util.UUIDToString(recallID),
				Status:       offlineStatusExcluded,
				Reason:       reason,
			})
			continue
		}
		if ownerID.Valid {
			ownerKindByID[ownerID] = graphKind
		}
		lines = append(lines, GraphMemoryOfflineExportLine{
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

	// Task 18: only manifest items serialize as trajectories; everything
	// else reports not_in_manifest and carries no payload fields.
	for i := range lines {
		if lines[i].Status == offlineStatusTrajectory && !manifestItems[lines[i].TrajectoryID] {
			lines[i].Status = offlineStatusExcluded
			lines[i].Reason = GraphMemoryOfflineReasonNotInManifest
			lines[i].TraceID = ""
			lines[i].GraphKind = ""
			lines[i].GraphOwnerID = ""
			lines[i].GraphVersion = 0
			lines[i].SeedIndex = 0
			lines[i].ScoreRelevance = nil
			lines[i].ScoreGroundedness = nil
			lines[i].ScoreCompleteness = nil
			lines[i].OverallScore = nil
			lines[i].Reward = nil
			lines[i].Rounds = 0
			lines[i].ArtifactRef = ""
			lines[i].Summary = ""
		}
	}

	// Task 8A read gate: one batched fence check over the graph owners. A
	// retracted owner degrades every line of that owner to content_retracted
	// — no summary, no artifact ref, never a stored payload.
	retracted := retractedGraphOwners(ctx, s.pool, ws, ownerKindByID)
	if len(retracted) > 0 {
		for i := range lines {
			if lines[i].Status != offlineStatusTrajectory || lines[i].GraphOwnerID == "" {
				continue
			}
			if retracted[lines[i].GraphOwnerID] {
				lines[i].Status = offlineStatusExcluded
				lines[i].Reason = serviceContentRetracted
				lines[i].Summary = ""
				lines[i].ArtifactRef = ""
				lines[i].ScoreRelevance = nil
				lines[i].ScoreGroundedness = nil
				lines[i].ScoreCompleteness = nil
				lines[i].OverallScore = nil
				lines[i].Reward = nil
			}
		}
	}
	return lines, nil
}

// serviceContentRetracted is the shared, machine-readable retraction reason.
const serviceContentRetracted = "content_retracted"

// retractedGraphOwners batch-checks the retraction fence for the export's
// graph owners (channel/project scoped).
func retractedGraphOwners(ctx context.Context, pool *pgxpool.Pool, ws pgtype.UUID, ownerKindByID map[pgtype.UUID]string) map[string]bool {
	if len(ownerKindByID) == 0 {
		return nil
	}
	keys := make([]string, 0, len(ownerKindByID))
	for ownerID, kind := range ownerKindByID {
		sourceKind := serviceKindForGraphOwner(kind)
		if sourceKind == "" {
			continue
		}
		keys = append(keys, sourceKind+":"+ownerID.String())
	}
	if len(keys) == 0 {
		return nil
	}
	retractedRows, err := db.New(pool).RetractedMemorySources(ctx, db.RetractedMemorySourcesParams{
		WorkspaceID: ws, SourceKeys: keys,
	})
	if err != nil {
		// Fail closed: an unreadable fence reads as fully retracted.
		all := make(map[string]bool, len(ownerKindByID))
		for ownerID := range ownerKindByID {
			all[ownerID.String()] = true
		}
		return all
	}
	out := make(map[string]bool, len(retractedRows))
	for _, row := range retractedRows {
		out[row.SourceID] = true
	}
	return out
}

// requireTrainingManifest enforces the Task 18 export gate for graph-memory
// trajectories and returns the manifest's trajectory-id set (empty only when
// the manifest itself is empty, which is legal).
func (s *GraphMemoryOfflineExportService) requireTrainingManifest(ctx context.Context, ws pgtype.UUID, manifestID string) (map[string]bool, error) {
	q := db.New(s.pool)
	policy, err := q.GetTrainingGovernancePolicy(ctx)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return nil, &OfflineResolveError{Code: "training_disabled", Message: "training governance is not installed", Status: 503}
		}
		return nil, fmt.Errorf("graph memory offline export: policy: %w", err)
	}
	if !policy.SelectionEnabled {
		return nil, &OfflineResolveError{Code: "training_disabled", Message: "training selection is globally disabled", Status: 503}
	}
	if strings.TrimSpace(manifestID) == "" {
		return nil, &OfflineResolveError{Code: "manifest_required", Message: "a valid training manifest is required for raw export", Status: 400}
	}
	id, err := util.ParseUUID(manifestID)
	if err != nil {
		return nil, &OfflineResolveError{Code: "manifest_required", Message: "training manifest id is invalid", Status: 400}
	}
	manifest, err := q.GetTrainingManifest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &OfflineResolveError{Code: "manifest_not_found", Message: "training manifest not found", Status: 404}
	}
	if err != nil {
		return nil, fmt.Errorf("graph memory offline export: manifest: %w", err)
	}
	if manifest.WorkspaceID != ws {
		return nil, &OfflineResolveError{Code: "forbidden", Message: "training manifest belongs to another workspace", Status: 403}
	}
	switch manifest.Status {
	case "exported", "execution_started", "consumed":
	default:
		return nil, &OfflineResolveError{Code: "manifest_not_exported", Message: "training manifest is not exported", Status: 409}
	}
	items, err := q.ListTrainingManifestItems(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("graph memory offline export: manifest items: %w", err)
	}
	out := make(map[string]bool, len(items))
	for _, item := range items {
		if item.ItemKind == "graph_trajectory" {
			out[item.ItemKey] = true
		}
	}
	return out, nil
}

func serviceKindForGraphOwner(graphKind string) string {
	switch graphKind {
	case "channel":
		return "channel"
	case "project":
		return "project"
	default:
		return ""
	}
}
