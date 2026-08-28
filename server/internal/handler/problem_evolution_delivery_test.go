package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func exportProblemEvolutionRun(t *testing.T, runID string) ProblemEvolutionExport {
	t.Helper()
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodGet,
		"/api/problem-evolution/runs/"+runID+"/export", nil, "runId", runID)
	testHandler.ExportProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var export ProblemEvolutionExport
	if err := json.Unmarshal(rec.Body.Bytes(), &export); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	return export
}

func TestExportProblemEvolutionRunCarriesLineageAndFrozenContract(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "parent")); err != nil {
		t.Fatalf("score parent: %v", err)
	}
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		repairEvent(t, "child", "parent")); err != nil {
		t.Fatalf("repair child: %v", err)
	}

	export := exportProblemEvolutionRun(t, uuidToString(run.ID))
	if export.Evaluator == nil || export.Evaluator.ContentHash == "" {
		t.Fatal("export omitted the frozen evaluator contract")
	}
	if export.Run.EvaluatorContentHash != export.Evaluator.ContentHash {
		t.Fatalf("run hash %q disagrees with contract hash %q",
			export.Run.EvaluatorContentHash, export.Evaluator.ContentHash)
	}
	if len(export.Candidates) < 2 {
		t.Fatalf("export has %d candidates, want at least 2", len(export.Candidates))
	}
	if len(export.Edges) == 0 {
		t.Fatal("export dropped candidate lineage")
	}
	if len(export.Evaluations) == 0 {
		t.Fatal("export dropped the evaluation history")
	}
	// Every edge must name candidates the export actually contains, otherwise a
	// reader cannot reconstruct the graph from the bundle alone.
	refs := map[string]struct{}{}
	for _, candidate := range export.Candidates {
		refs[candidate.ID] = struct{}{}
	}
	for _, edge := range export.Edges {
		if _, ok := refs[edge.ParentID]; !ok {
			t.Fatalf("edge parent %s is not in the export", edge.ParentID)
		}
		if _, ok := refs[edge.ChildID]; !ok {
			t.Fatalf("edge child %s is not in the export", edge.ChildID)
		}
	}
}

func TestExportProblemEvolutionRunNeverCarriesSecretPlaintext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withProblemEvolutionMasterKey(t)
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	const plaintext = "the-hidden-answer-42"
	createProblemEvolutionSecretForTest(t, run, plaintext)
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("score candidate: %v", err)
	}

	rec := httptest.NewRecorder()
	runID := uuidToString(run.ID)
	req := newProblemEvolutionRequest(t, http.MethodGet,
		"/api/problem-evolution/runs/"+runID+"/export", nil, "runId", runID)
	testHandler.ExportProblemEvolutionRun(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// An export is the artifact most likely to be mailed around, so the hidden
	// answer must not be reachable through it at all.
	if strings.Contains(rec.Body.String(), plaintext) {
		t.Fatal("export leaked secret plaintext")
	}
	var export ProblemEvolutionExport
	if err := json.Unmarshal(rec.Body.Bytes(), &export); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if export.SecretAudit.SecretsUsed < 0 {
		t.Fatal("secret audit summary is malformed")
	}
}

func TestExportProblemEvolutionRunReportsScopeAndReplayability(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	run := seedClaimedProblemEvolutionRun(t)
	if _, err := testHandler.persistProblemEvolutionEvent(ctx, run,
		candidateScoredEvent(t, uuid.NewString(), "c1")); err != nil {
		t.Fatalf("score candidate: %v", err)
	}

	export := exportProblemEvolutionRun(t, uuidToString(run.ID))
	// Without blind validation the bundle may not imply the result generalises.
	if export.Result.BlindValidated {
		t.Fatal("result claims blind validation that never happened")
	}
	if export.Result.ScopeClaim != "search_sample_only" {
		t.Fatalf("scope claim = %q, want search_sample_only", export.Result.ScopeClaim)
	}
	// The seeded run has no evolver version, so the bundle must say so instead
	// of implying a rerun would reproduce it.
	if export.Reproduction.Replayable {
		t.Fatal("export claims replayability without a pinned evolver version")
	}
	if len(export.Reproduction.MissingForReplay) == 0 {
		t.Fatal("export did not say what is missing for a replay")
	}
	if len(export.Reproduction.EventTypes) == 0 {
		t.Fatal("export omitted the accepted event whitelist")
	}

	current, err := testHandler.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"claim_token": uuidToString(current.ClaimToken),
		"outcome": problemevolution.BlindValidationOutcome{
			CandidateRef: "c1",
			Score:        blindScore(0.55),
			Seed:         current.BlindSeed,
		},
	})
	if err != nil {
		t.Fatalf("marshal outcome: %v", err)
	}
	rec := httptest.NewRecorder()
	blindReq := newDaemonProblemEvolutionRequest(t, http.MethodPost,
		"/api/daemon/problem-evolution/runs/"+uuidToString(run.ID)+"/blind-validation",
		body, uuidToString(run.ID))
	testHandler.ReportProblemEvolutionBlindValidation(rec, blindReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("blind validation status = %d: %s", rec.Code, rec.Body.String())
	}

	validated := exportProblemEvolutionRun(t, uuidToString(run.ID))
	if !validated.Result.BlindValidated || validated.Result.BlindScore == nil {
		t.Fatal("export did not pick up the blind result")
	}
	if validated.Result.ScopeClaim == "search_sample_only" {
		t.Fatalf("scope claim stayed %q after blind validation", validated.Result.ScopeClaim)
	}
}

func TestCompareProblemEvolutionRunsRefusesToRankAcrossContracts(t *testing.T) {
	left := db.ProblemEvolutionRun{
		ID:                   parseUUID(uuid.NewString()),
		Mode:                 problemevolution.ModeSolution,
		EvaluatorContentHash: "sha256:aaa",
		EvolverVersion:       "v1",
	}
	right := db.ProblemEvolutionRun{
		ID:                   parseUUID(uuid.NewString()),
		Mode:                 problemevolution.ModeSolution,
		EvaluatorContentHash: "sha256:bbb",
		EvolverVersion:       "v1",
	}
	comparison := compareProblemEvolutionRuns(left, right,
		ProblemEvolutionResultExport{SearchBestScore: 0.9},
		ProblemEvolutionResultExport{SearchBestScore: 0.4})
	// Scores from two different rubrics are not on one axis, so naming a winner
	// would be a fabricated result.
	if comparison.Comparable {
		t.Fatal("runs scored under different contracts were called comparable")
	}
	if comparison.PreferredRunID != "" {
		t.Fatalf("comparison picked a winner anyway: %s", comparison.PreferredRunID)
	}
	if comparison.PreferenceBasis != "not_comparable" {
		t.Fatalf("preference basis = %q, want not_comparable", comparison.PreferenceBasis)
	}
	if len(comparison.Differences) == 0 {
		t.Fatal("comparison did not name the difference")
	}
}

func TestCompareProblemEvolutionRunsPrefersBlindOverSearch(t *testing.T) {
	leftID := parseUUID(uuid.NewString())
	rightID := parseUUID(uuid.NewString())
	left := db.ProblemEvolutionRun{ID: leftID, Mode: problemevolution.ModeSolution,
		EvaluatorContentHash: "sha256:same", EvolverVersion: "v1"}
	right := db.ProblemEvolutionRun{ID: rightID, Mode: problemevolution.ModeSolution,
		EvaluatorContentHash: "sha256:same", EvolverVersion: "v1"}
	leftBlind, rightBlind := 0.4, 0.8

	comparison := compareProblemEvolutionRuns(left, right,
		ProblemEvolutionResultExport{SearchBestScore: 0.95, BlindScore: &leftBlind},
		ProblemEvolutionResultExport{SearchBestScore: 0.60, BlindScore: &rightBlind})
	// The left run looks better on the sample it searched against; the blind
	// numbers say the opposite, and those are the ones that count.
	if comparison.PreferenceBasis != "blind_validation" {
		t.Fatalf("preference basis = %q, want blind_validation", comparison.PreferenceBasis)
	}
	if comparison.PreferredRunID != uuidToString(rightID) {
		t.Fatalf("preferred %s, want the run with the better blind score",
			comparison.PreferredRunID)
	}
}

func TestCompareProblemEvolutionRunsLabelsSearchOnlyPreference(t *testing.T) {
	leftID := parseUUID(uuid.NewString())
	left := db.ProblemEvolutionRun{ID: leftID, Mode: problemevolution.ModeSolution,
		EvaluatorContentHash: "sha256:same", EvolverVersion: "v1"}
	right := db.ProblemEvolutionRun{ID: parseUUID(uuid.NewString()), Mode: problemevolution.ModeSolution,
		EvaluatorContentHash: "sha256:same", EvolverVersion: "v1"}

	comparison := compareProblemEvolutionRuns(left, right,
		ProblemEvolutionResultExport{SearchBestScore: 0.9},
		ProblemEvolutionResultExport{SearchBestScore: 0.5})
	if comparison.PreferredRunID != uuidToString(leftID) {
		t.Fatalf("preferred %s, want the higher search score", comparison.PreferredRunID)
	}
	// A preference with no blind evidence must be labelled as such.
	if comparison.PreferenceBasis != "search_score_only" {
		t.Fatalf("preference basis = %q, want search_score_only", comparison.PreferenceBasis)
	}
	if comparison.BlindDelta != nil {
		t.Fatal("comparison invented a blind delta")
	}
}

func TestCompareProblemEvolutionRunsFlagsBenchmarkModeMismatch(t *testing.T) {
	left := db.ProblemEvolutionRun{ID: parseUUID(uuid.NewString()),
		Mode:                 problemevolution.ModeTaskHarnessRewardOnly,
		EvaluatorContentHash: "sha256:same", EvolverVersion: "v1", BenchmarkMode: true}
	right := db.ProblemEvolutionRun{ID: parseUUID(uuid.NewString()),
		Mode:                 problemevolution.ModeTaskHarnessRewardOnly,
		EvaluatorContentHash: "sha256:same", EvolverVersion: "v1"}
	comparison := compareProblemEvolutionRuns(left, right,
		ProblemEvolutionResultExport{}, ProblemEvolutionResultExport{})
	// Benchmark mode disables low-score repair, so a benchmark run and a normal
	// run did not do the same amount of work per candidate.
	if comparison.Comparable {
		t.Fatal("a benchmark run was called comparable to a non-benchmark run")
	}
	found := false
	for _, difference := range comparison.Differences {
		if difference == "benchmark_mode" {
			found = true
		}
	}
	if !found {
		t.Fatalf("differences %v do not mention benchmark_mode", comparison.Differences)
	}
}

// createProblemEvolutionSecretForTest stores a sealed secret through the same
// handler path the API uses, so the export test exercises real ciphertext.
func createProblemEvolutionSecretForTest(t *testing.T, run db.ProblemEvolutionRun, plaintext string) {
	t.Helper()
	runID := uuidToString(run.ID)
	rec := httptest.NewRecorder()
	req := newProblemEvolutionRequest(t, http.MethodPost,
		"/api/problem-evolution/runs/"+runID+"/secrets", map[string]any{
			"kind":      "hidden_answer",
			"plaintext": plaintext,
		}, "runId", runID)
	testHandler.CreateProblemEvolutionSecret(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create secret: status %d: %s", rec.Code, rec.Body.String())
	}
}
