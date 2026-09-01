// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §5, acceptance A12, decisions D15/D26: offline export eligibility is
// complete graded trajectories plus explicitly labeled incomplete=true ones;
// judge_failed is audit-only; Explore error/timeout/budget never export;
// online_rl and offline_capture are the wrong mode.

func TestGraphMemoryOfflineExportClassifier(t *testing.T) {
	cases := []struct {
		name           string
		trainingMode   string
		diveStatus     string
		recallTerminal bool
		jobCompleted   bool
		jobIncomplete  bool
		eligible       bool
		reason         string
	}{
		{
			name:           "offline_rl graded complete job",
			trainingMode:   "offline_rl",
			diveStatus:     "graded",
			recallTerminal: true,
			jobCompleted:   true,
			eligible:       true,
		},
		{
			name:           "offline_rl graded incomplete label",
			trainingMode:   "offline_rl",
			diveStatus:     "graded",
			recallTerminal: true,
			jobCompleted:   true,
			jobIncomplete:  true,
			eligible:       true,
		},
		{
			name:           "offline_rl judge_failed",
			trainingMode:   "offline_rl",
			diveStatus:     "judge_failed",
			recallTerminal: true,
			reason:         "judge_failed",
		},
		{
			name:           "offline_rl bypassed any job",
			trainingMode:   "offline_rl",
			diveStatus:     "bypassed",
			recallTerminal: true,
			jobCompleted:   true,
			reason:         "explore_bypassed",
		},
		{
			name:         "offline_rl bypassed no job",
			trainingMode: "offline_rl",
			diveStatus:   "bypassed",
			reason:       "explore_bypassed",
		},
		{
			name:         "offline_rl not yet graded",
			trainingMode: "offline_rl",
			diveStatus:   "",
			reason:       "not_terminal",
		},
		{
			name:           "offline_rl graded but job not complete",
			trainingMode:   "offline_rl",
			diveStatus:     "graded",
			recallTerminal: true,
			reason:         "not_terminal",
		},
		{
			name:           "online_rl graded still wrong mode",
			trainingMode:   "online_rl",
			diveStatus:     "graded",
			recallTerminal: true,
			jobCompleted:   true,
			reason:         "wrong_mode_online_rl",
		},
		{
			name:         "online_rl empty status",
			trainingMode: "online_rl",
			reason:       "wrong_mode_online_rl",
		},
		{
			name:           "offline_capture graded still wrong mode",
			trainingMode:   "offline_capture",
			diveStatus:     "graded",
			recallTerminal: true,
			jobCompleted:   true,
			reason:         "wrong_mode_offline_capture",
		},
		{
			name:         "offline_capture bypassed",
			trainingMode: "offline_capture",
			diveStatus:   "bypassed",
			reason:       "wrong_mode_offline_capture",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eligible, reason := service.ClassifyOfflineExportEligibility(
				tc.trainingMode, tc.diveStatus, tc.recallTerminal, tc.jobCompleted, tc.jobIncomplete)
			if eligible != tc.eligible || reason != tc.reason {
				t.Fatalf("ClassifyOfflineExportEligibility(%q, %q, terminal=%v, job=%v, incomplete=%v) = (%v, %q), want (%v, %q)",
					tc.trainingMode, tc.diveStatus, tc.recallTerminal, tc.jobCompleted, tc.jobIncomplete,
					eligible, reason, tc.eligible, tc.reason)
			}
		})
	}
}

func TestGraphMemoryOfflineExportGradedComplete(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-complete-"+uuid.NewString()[:8], 2)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 3)
	mustTerminalTrajectoryWithRounds(t, recallID, 1, "miss", 5)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	t1 := trajectoryIDBySeed(t, recallID, 1)
	mustSetTrajectoryExportMeta(t, t0, "found the node", "s3://gm/complete-0")
	mustSetTrajectoryExportMeta(t, t1, "missed the node", "s3://gm/complete-1")
	mustGradeOfflineRecall(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.9, Groundedness: 0.4, Completeness: 0.7},
		{TrajectoryID: t1, Relevance: 0.2, Groundedness: 0.2, Completeness: 0.2},
	}, nil, false)

	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if lines[0].TrajectoryID != t0 || lines[1].TrajectoryID != t1 {
		t.Fatalf("order = [%s %s], want seed_index order [%s %s]", lines[0].TrajectoryID, lines[1].TrajectoryID, t0, t1)
	}
	assertEligibleExportLine(t, lines[0], eligibleExportWant{
		trajectoryID: t0, recallID: util.UUIDToString(recallID), traceID: recallTraceID(t, recallID),
		graphKind: "project", graphOwnerID: util.UUIDToString(fx.projectID), graphVersion: 1,
		seedIndex: 0, relevance: 0.9, groundedness: 0.4, completeness: 0.7, overall: 0.4, reward: 0.1,
		rounds: 3, incomplete: false, artifactRef: "s3://gm/complete-0", summary: "found the node",
	})
	assertEligibleExportLine(t, lines[1], eligibleExportWant{
		trajectoryID: t1, recallID: util.UUIDToString(recallID), traceID: recallTraceID(t, recallID),
		graphKind: "project", graphOwnerID: util.UUIDToString(fx.projectID), graphVersion: 1,
		seedIndex: 1, relevance: 0.2, groundedness: 0.2, completeness: 0.2, overall: 0.2, reward: -0.3,
		rounds: 5, incomplete: false, artifactRef: "s3://gm/complete-1", summary: "missed the node",
	})
}

func TestGraphMemoryOfflineExportGradedIncomplete(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-inc-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	mustSetTrajectoryExportMeta(t, t0, "partial dive", "s3://gm/inc-0")
	mustGradeOfflineRecall(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.6, Groundedness: 0.6, Completeness: 0.6},
	}, nil, true)

	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	assertEligibleExportLine(t, lines[0], eligibleExportWant{
		trajectoryID: t0, recallID: util.UUIDToString(recallID), traceID: recallTraceID(t, recallID),
		graphKind: "project", graphOwnerID: util.UUIDToString(fx.projectID), graphVersion: 1,
		seedIndex: 0, relevance: 0.6, groundedness: 0.6, completeness: 0.6, overall: 0.6, reward: 0.5,
		rounds: 1, incomplete: true, artifactRef: "s3://gm/inc-0", summary: "partial dive",
	})
}

func TestGraphMemoryOfflineExportJudgeFailed(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-jf-"+uuid.NewString()[:8], 2)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 2)
	mustTerminalTrajectoryWithRounds(t, recallID, 1, "miss", 2)
	ctx := context.Background()
	dive := service.NewGraphMemoryDiveService(testPool)
	if _, err := dive.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_dive_job SET max_attempts = 1 WHERE recall_id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	job, err := dive.Lease(ctx, "export-worker", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}
	terminal, err := dive.Fail(ctx, job.ID, "export-worker", "infra", "model endpoint 503", true)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if !terminal {
		t.Fatal("max_attempts=1 must terminalize on first failure")
	}

	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for _, line := range lines {
		assertExcludedExportLine(t, line, util.UUIDToString(recallID), "judge_failed")
	}
}

func TestGraphMemoryOfflineExportExploreBypassed(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-bypass-"+uuid.NewString()[:8], 3)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "error", 0)
	mustTerminalTrajectoryWithRounds(t, recallID, 1, "budget", 0)
	mustTerminalTrajectoryWithRounds(t, recallID, 2, "timeout", 0)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_trajectory SET dive_status = 'bypassed', reward = 0 WHERE recall_id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}

	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	for _, line := range lines {
		assertExcludedExportLine(t, line, util.UUIDToString(recallID), "explore_bypassed")
	}
}

func TestGraphMemoryOfflineExportNotTerminal(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-open-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 2)

	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	assertExcludedExportLine(t, lines[0], util.UUIDToString(recallID), "not_terminal")
}

func TestGraphMemoryOfflineExportWrongModeOnlineRL(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "export-online-"+uuid.NewString()[:8], 2)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 1, "miss", 1)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	t1 := trajectoryIDBySeed(t, recallID, 1)
	mustGradeOfflineRecall(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.8, Groundedness: 0.8, Completeness: 0.8},
		{TrajectoryID: t1, Relevance: 0.7, Groundedness: 0.7, Completeness: 0.7},
	}, nil, false)

	var ws, owner pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT workspace_id, graph_owner_id FROM graph_memory_recall WHERE id = $1
	`, recallID).Scan(&ws, &owner); err != nil {
		t.Fatal(err)
	}
	lines := mustListOfflineExports(t, ws, owner, recallID)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for _, line := range lines {
		assertExcludedExportLine(t, line, util.UUIDToString(recallID), "wrong_mode_online_rl")
	}
}

func TestGraphMemoryOfflineExportWrongModeOfflineCapture(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustGraphMemoryDiveFixture(t, "export-capture-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	mustGradeOfflineRecall(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, nil, false)

	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	assertExcludedExportLine(t, lines[0], util.UUIDToString(recallID), "wrong_mode_offline_capture")
}

func TestGraphMemoryOfflineExportDoesNotTouchProviderCallLedger(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-d15-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	mustSetTrajectoryExportMeta(t, t0, "d15 graded", "s3://gm/d15")
	mustGradeOfflineRecall(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5},
	}, nil, false)

	const sessionCanary = "CANARY_AREAL_SESSION_d15"
	const proxyCanary = "sk-canary-proxy-key-d15"
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO graph_memory_rl_session (workspace_id, trajectory_id, recall_id, status, session_id, proxy_key)
		VALUES ($1, $2, $3, 'open', $4, $5)
	`, fx.workspaceID, t0, recallID, sessionCanary, proxyCanary); err != nil {
		t.Fatal(err)
	}

	var before int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM pi_provider_call`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	lines := mustListOfflineExports(t, fx.workspaceID, fx.projectID, recallID)
	var after int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM pi_provider_call`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("pi_provider_call count changed %d → %d; D15 forbids ledger writes", before, after)
	}
	if len(lines) != 1 || lines[0].Status != "trajectory" {
		t.Fatalf("expected one eligible trajectory line, got %+v", lines)
	}
	raw, err := json.Marshal(lines)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(raw)
	if strings.Contains(serialized, sessionCanary) || strings.Contains(serialized, proxyCanary) {
		t.Fatalf("serialized export leaked session/proxy canary: %s", serialized)
	}
}

func mustOfflineRLExportFixture(t *testing.T, traceID string, k int) (recallLedgerFixture, pgtype.UUID) {
	t.Helper()
	fx, recallID := mustGraphMemoryDiveFixture(t, traceID, k)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_recall SET training_mode = 'offline_rl' WHERE id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	return fx, recallID
}

func mustSetTrajectoryExportMeta(t *testing.T, trajectoryID, summary, artifactRef string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_trajectory SET summary = $2, artifact_ref = $3 WHERE id = $1
	`, trajectoryID, summary, artifactRef); err != nil {
		t.Fatal(err)
	}
}

func mustGradeOfflineRecall(t *testing.T, recallID pgtype.UUID, scores []memorygraph.DiveTrajectoryScore, bypassed []memorygraph.DiveRunInput, incomplete bool) {
	t.Helper()
	ctx := context.Background()
	dive := service.NewGraphMemoryDiveService(testPool)
	if _, err := dive.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	job, err := dive.Lease(ctx, "export-worker", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}
	ok, err := dive.ApplyDiveResult(ctx, job.ID, "export-worker", &memorygraph.DiveResult{
		Scores: scores, Bypassed: bypassed, Incomplete: incomplete,
	}, 0.1)
	if err != nil || !ok {
		t.Fatalf("ApplyDiveResult: ok=%v err=%v", ok, err)
	}
	if ok, err := dive.Complete(ctx, job.ID, "export-worker", incomplete, []byte(`{}`)); err != nil || !ok {
		t.Fatalf("Complete: ok=%v err=%v", ok, err)
	}
}

// mustSeedManifestBackfillTrajectory seeds one minimal graded offline_rl
// trajectory in the workspace so the training-manifest selection always has
// at least one item, even when every fixture trajectory of the recall under
// test is itself excluded (Task 18: an empty selection is not a manifest).
// It lives under its own scratch recall and is filtered out of the returned
// lines.
func mustSeedManifestBackfillTrajectory(t *testing.T, ws, owner pgtype.UUID) {
	t.Helper()
	// The faithful schema's recall identity trigger requires a task from the
	// same workspace and daemon_id is NOT NULL; reuse both from the
	// workspace's existing fixture recalls.
	var taskID, runtimeID pgtype.UUID
	var daemonID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT task_id, daemon_id, runtime_id FROM graph_memory_recall
		WHERE workspace_id = $1 AND task_id IS NOT NULL LIMIT 1`, ws).Scan(&taskID, &daemonID, &runtimeID); err != nil {
		t.Fatalf("resolve workspace recall task: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		WITH r AS (
			INSERT INTO graph_memory_recall (
			  workspace_id, task_id, daemon_id, runtime_id, training_mode, trace_id,
			  graph_kind, graph_owner_id, graph_version, k, query, terminal_at
			) VALUES ($1, $2, $3, $4, 'offline_rl', 'manifest-backfill', 'project', $5, 1, 4, 'q', now())
			RETURNING id
		), tr AS (
			INSERT INTO graph_memory_trajectory (
			  workspace_id, recall_id, seed_index, dive_status, reward, rounds
			)
			SELECT $1, r.id, 0, 'graded', 0.5, 1 FROM r
			RETURNING id, recall_id
		)
		INSERT INTO graph_memory_dive_job (
		  recall_id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, status, incomplete
		)
		SELECT recall_id, $1, 'manifest-backfill', 'project', $5, 1, 'completed', false FROM tr
	`, ws, taskID, daemonID, runtimeID, owner); err != nil {
		t.Fatalf("seed manifest backfill trajectory: %v", err)
	}
}

// mustListOfflineExports drives the Task 18 governance path for the export
// tests: selection switch on, workspace grant acknowledged, a graph
// trajectory manifest selected and exported, then the raw NDJSON listing
// scoped to that manifest. Only lines of the recall under test are returned.
func mustListOfflineExports(t *testing.T, ws, owner, recallID pgtype.UUID) []service.GraphMemoryOfflineExportLine {
	t.Helper()
	ctx := context.Background()
	restoreTrainingPolicy(t)
	gov := service.NewTrainingGovernanceService(testPool, nil)
	on := true
	if _, err := gov.SetTrainingPolicy(ctx, service.TrainingPolicyPatch{SelectionEnabled: &on}, "export-test"); err != nil {
		t.Fatalf("enable training selection: %v", err)
	}
	mustAckTrainingGrant(t, gov, ctx, util.UUIDToString(ws))
	mustSeedManifestBackfillTrajectory(t, ws, owner)
	manifest, err := gov.SelectGraphTrainingManifest(ctx, service.TrainingSelectionRequest{
		WorkspaceID: util.UUIDToString(ws), Purpose: "tenant", Actor: "export-test",
	})
	if err != nil {
		t.Fatalf("select graph training manifest: %v", err)
	}
	if _, err := gov.ExportTrainingManifest(ctx, util.UUIDToString(ws), manifest.ManifestID); err != nil {
		t.Fatalf("export graph training manifest: %v", err)
	}
	svc := service.NewGraphMemoryOfflineExportService(testPool)
	lines, err := svc.ListOfflineExports(ctx, util.UUIDToString(ws), manifest.ManifestID, 100)
	if err != nil {
		t.Fatalf("ListOfflineExports: %v", err)
	}
	filtered := make([]service.GraphMemoryOfflineExportLine, 0, len(lines))
	for _, line := range lines {
		if line.RecallID == util.UUIDToString(recallID) || (line.Status == "excluded" && line.RecallID == util.UUIDToString(recallID)) {
			filtered = append(filtered, line)
		}
	}
	return filtered
}

func recallTraceID(t *testing.T, recallID pgtype.UUID) string {
	t.Helper()
	var traceID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT trace_id FROM graph_memory_recall WHERE id = $1
	`, recallID).Scan(&traceID); err != nil {
		t.Fatal(err)
	}
	return traceID
}

type eligibleExportWant struct {
	trajectoryID, recallID, traceID string
	graphKind, graphOwnerID         string
	graphVersion, seedIndex, rounds int
	relevance, groundedness         float64
	completeness, overall, reward   float64
	incomplete                      bool
	artifactRef, summary            string
}

func assertEligibleExportLine(t *testing.T, line service.GraphMemoryOfflineExportLine, want eligibleExportWant) {
	t.Helper()
	if line.Status != "trajectory" || line.Reason != "" {
		t.Fatalf("status/reason = (%q, %q), want (trajectory, empty)", line.Status, line.Reason)
	}
	if line.TrajectoryID != want.trajectoryID || line.RecallID != want.recallID {
		t.Fatalf("ids = (%s, %s), want (%s, %s)", line.TrajectoryID, line.RecallID, want.trajectoryID, want.recallID)
	}
	if line.TraceID != want.traceID || line.GraphKind != want.graphKind ||
		line.GraphOwnerID != want.graphOwnerID || line.GraphVersion != want.graphVersion {
		t.Fatalf("graph identity = (%s %s %s v%d), want (%s %s %s v%d)",
			line.TraceID, line.GraphKind, line.GraphOwnerID, line.GraphVersion,
			want.traceID, want.graphKind, want.graphOwnerID, want.graphVersion)
	}
	if line.SeedIndex != want.seedIndex || line.Rounds != want.rounds || line.Incomplete != want.incomplete {
		t.Fatalf("seed/rounds/incomplete = (%d, %d, %v), want (%d, %d, %v)",
			line.SeedIndex, line.Rounds, line.Incomplete, want.seedIndex, want.rounds, want.incomplete)
	}
	if !almostEqualPtr(line.ScoreRelevance, want.relevance) ||
		!almostEqualPtr(line.ScoreGroundedness, want.groundedness) ||
		!almostEqualPtr(line.ScoreCompleteness, want.completeness) ||
		!almostEqualPtr(line.OverallScore, want.overall) ||
		!almostEqualPtr(line.Reward, want.reward) {
		t.Fatalf("scores = (rel=%v ground=%v comp=%v overall=%v reward=%v), want (%v %v %v %v %v)",
			fmtFloat(line.ScoreRelevance), fmtFloat(line.ScoreGroundedness), fmtFloat(line.ScoreCompleteness),
			fmtFloat(line.OverallScore), fmtFloat(line.Reward),
			want.relevance, want.groundedness, want.completeness, want.overall, want.reward)
	}
	if line.ArtifactRef != want.artifactRef || line.Summary != want.summary {
		t.Fatalf("artifact/summary = (%q, %q), want (%q, %q)", line.ArtifactRef, line.Summary, want.artifactRef, want.summary)
	}
}

func assertExcludedExportLine(t *testing.T, line service.GraphMemoryOfflineExportLine, recallID, reason string) {
	t.Helper()
	if line.Status != "excluded" || line.Reason != reason {
		t.Fatalf("excluded line = (%q, %q), want (excluded, %q)", line.Status, line.Reason, reason)
	}
	if line.RecallID != recallID || line.TrajectoryID == "" {
		t.Fatalf("excluded ids = recall %q traj %q, want recall %q and a trajectory id", line.RecallID, line.TrajectoryID, recallID)
	}
	if line.TraceID != "" || line.Summary != "" || line.ArtifactRef != "" ||
		line.ScoreRelevance != nil || line.Reward != nil {
		t.Fatalf("excluded line must carry only ids + status + reason, got %+v", line)
	}
}

func almostEqualPtr(p *float64, want float64) bool {
	if p == nil {
		return false
	}
	d := *p - want
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func fmtFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// Task 18: the raw graph-memory NDJSON export is closed without governance —
// the global switch off answers training_disabled, a missing manifest
// answers manifest_required, and no payload line serializes either way.
func TestGraphMemoryOfflineExportRequiresTrainingManifest(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fx, recallID := mustOfflineRLExportFixture(t, "export-gate-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	t0 := trajectoryIDBySeed(t, recallID, 0)
	mustGradeOfflineRecall(t, recallID, []memorygraph.DiveTrajectoryScore{
		{TrajectoryID: t0, Relevance: 0.9, Groundedness: 0.9, Completeness: 0.9},
	}, nil, false)

	ctx := context.Background()
	restoreTrainingPolicy(t)
	svc := service.NewGraphMemoryOfflineExportService(testPool)
	gov := service.NewTrainingGovernanceService(testPool, nil)

	// Switch off -> training_disabled even when a manifest id is supplied.
	off := false
	if _, err := gov.SetTrainingPolicy(ctx, service.TrainingPolicyPatch{SelectionEnabled: &off}, "export-test"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ListOfflineExports(ctx, util.UUIDToString(fx.workspaceID), uuid.NewString(), 100)
	if resolveErr := (*service.OfflineResolveError)(nil); errors.As(err, &resolveErr) && resolveErr.Code != "training_disabled" {
		t.Fatalf("disabled export error = %v, want training_disabled", err)
	} else if !errors.As(err, &resolveErr) {
		t.Fatalf("disabled export error = %v, want OfflineResolveError", err)
	}

	// Switch on but no manifest -> manifest_required.
	on := true
	if _, err := gov.SetTrainingPolicy(ctx, service.TrainingPolicyPatch{SelectionEnabled: &on}, "export-test"); err != nil {
		t.Fatal(err)
	}
	_, err = svc.ListOfflineExports(ctx, util.UUIDToString(fx.workspaceID), "", 100)
	var gateErr *service.OfflineResolveError
	if !errors.As(err, &gateErr) || gateErr.Code != "manifest_required" {
		t.Fatalf("manifestless export error = %v, want manifest_required", err)
	}

	// An un-exported (only selected) manifest authorizes nothing.
	mustAckTrainingGrant(t, gov, ctx, util.UUIDToString(fx.workspaceID))
	manifest, err := gov.SelectGraphTrainingManifest(ctx, service.TrainingSelectionRequest{
		WorkspaceID: util.UUIDToString(fx.workspaceID), Purpose: "tenant", Actor: "export-test",
	})
	if err != nil {
		t.Fatalf("select graph training manifest: %v", err)
	}
	_, err = svc.ListOfflineExports(ctx, util.UUIDToString(fx.workspaceID), manifest.ManifestID, 100)
	if !errors.As(err, &gateErr) || gateErr.Code != "manifest_not_exported" {
		t.Fatalf("unexported manifest error = %v, want manifest_not_exported", err)
	}
}
