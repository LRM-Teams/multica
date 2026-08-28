package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// problemEvolutionExportLimit bounds how many rows of each collection an export
// carries. An export is a delivery artifact, not a database dump.
const problemEvolutionExportLimit = 2000

// ProblemEvolutionExport is the delivery bundle for one run.
//
// It carries everything needed to defend a result — the frozen contract, the
// candidate lineage, per-evaluation history, both the search and blind numbers,
// and the pinned versions — but no hidden material: secrets appear as metadata
// only, and artifacts as references.
type ProblemEvolutionExport struct {
	SchemaVersion int                                        `json:"schema_version"`
	Run           ProblemEvolutionRunResponse                `json:"run"`
	Evaluator     *ProblemEvolutionEvaluatorContractResponse `json:"evaluator,omitempty"`
	Candidates    []ProblemEvolutionCandidateResponse        `json:"candidates"`
	Edges         []ProblemEvolutionCandidateEdgeResponse    `json:"edges"`
	Evaluations   []ProblemEvolutionEvaluationExport         `json:"evaluations"`
	Artifacts     []ProblemEvolutionArtifactExport           `json:"artifacts"`
	Harnesses     []ProblemEvolutionHarnessResponse          `json:"harnesses,omitempty"`
	Result        ProblemEvolutionResultExport               `json:"result"`
	Reproduction  ProblemEvolutionReproduction               `json:"reproduction"`
	SecretAudit   ProblemEvolutionSecretAuditSummary         `json:"secret_audit"`
}

// ProblemEvolutionEvaluationExport is one scoring event in the export.
type ProblemEvolutionEvaluationExport struct {
	CandidateRef       string          `json:"candidate_ref"`
	Phase              string          `json:"phase"`
	Attempt            int             `json:"attempt"`
	Verdict            string          `json:"verdict"`
	Score              json.RawMessage `json:"score,omitempty"`
	FeedbackProjection json.RawMessage `json:"feedback_projection,omitempty"`
	EvaluatorHash      string          `json:"evaluator_content_hash"`
	CreatedAt          string          `json:"created_at"`
}

// ProblemEvolutionArtifactExport references produced material by path and hash.
type ProblemEvolutionArtifactExport struct {
	CandidateRef string `json:"candidate_ref,omitempty"`
	Kind         string `json:"kind"`
	StorageRef   string `json:"storage_ref"`
	ContentHash  string `json:"content_hash"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
}

// ProblemEvolutionResultExport reports the search and blind numbers together.
// Reporting the search score alone would overstate what the run demonstrated.
type ProblemEvolutionResultExport struct {
	BestCandidateRef  string   `json:"best_candidate_ref,omitempty"`
	SearchBestScore   float64  `json:"search_best_score"`
	BlindScore        *float64 `json:"blind_score,omitempty"`
	OverfitGap        *float64 `json:"overfit_gap,omitempty"`
	BlindValidated    bool     `json:"blind_validated"`
	ScopeClaim        string   `json:"scope_claim"`
	WinnerHarnessRef  string   `json:"winner_harness_ref,omitempty"`
	CandidatesTotal   int      `json:"candidates_total"`
	GenerationsTotal  int      `json:"generations_total"`
	TotalCostUSD      float64  `json:"total_cost_usd"`
	StopReason        string   `json:"stop_reason"`
	FailureReason     string   `json:"failure_reason"`
	FeedbackBandwidth string   `json:"feedback_bandwidth"`
}

// ProblemEvolutionReproduction records what a rerun would need, and says plainly
// when a run cannot be reproduced exactly rather than implying it can.
type ProblemEvolutionReproduction struct {
	Replayable           bool     `json:"replayable"`
	MissingForReplay     []string `json:"missing_for_replay"`
	SchemaVersion        int      `json:"schema_version"`
	EvaluatorContentHash string   `json:"evaluator_content_hash"`
	EvolverVersion       string   `json:"evolver_version"`
	SearchSeed           int64    `json:"search_seed"`
	// The blind seed is included in the export but never in evolver input: the
	// export is read by humans after the fact, the input is read by the search.
	BlindSeed   int64           `json:"blind_seed"`
	ModelConfig json.RawMessage `json:"model_config"`
	StopConfig  json.RawMessage `json:"stop_config"`
	EventTypes  []string        `json:"allowed_event_types"`
}

// ProblemEvolutionSecretAuditSummary is the secret-boundary summary. Denials are
// listed because an attempt to reach a hidden answer from the wrong side is the
// thing a reviewer needs to see.
type ProblemEvolutionSecretAuditSummary struct {
	SecretsUsed     int            `json:"secrets_used"`
	Issued          int            `json:"capabilities_issued"`
	Redeemed        int            `json:"capabilities_redeemed"`
	Denied          int            `json:"capabilities_denied"`
	DenialsByReason map[string]int `json:"denials_by_reason,omitempty"`
}

// ExportProblemEvolutionRun assembles the delivery bundle for one run.
func (h *Handler) ExportProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	export, err := h.buildProblemEvolutionExport(r.Context(), run)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to export run")
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition",
			`attachment; filename="problem-evolution-`+uuidToString(run.ID)+`.json"`)
	}
	writeJSON(w, http.StatusOK, export)
}

func (h *Handler) buildProblemEvolutionExport(
	ctx context.Context,
	run db.ProblemEvolutionRun,
) (ProblemEvolutionExport, error) {
	export := ProblemEvolutionExport{
		SchemaVersion: problemevolution.SchemaVersion,
		Run:           problemEvolutionRunToResponse(run),
		Candidates:    []ProblemEvolutionCandidateResponse{},
		Edges:         []ProblemEvolutionCandidateEdgeResponse{},
		Evaluations:   []ProblemEvolutionEvaluationExport{},
		Artifacts:     []ProblemEvolutionArtifactExport{},
	}
	candidates, err := h.Queries.ListProblemEvolutionCandidates(ctx, run.ID)
	if err != nil {
		return ProblemEvolutionExport{}, err
	}
	refByID := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		refByID[uuidToString(candidate.ID)] = candidate.ExternalRef
		export.Candidates = append(export.Candidates, problemEvolutionCandidateToResponse(candidate))
	}
	edges, err := h.Queries.ListProblemEvolutionCandidateEdges(ctx, run.ID)
	if err != nil {
		return ProblemEvolutionExport{}, err
	}
	for _, edge := range edges {
		export.Edges = append(export.Edges, ProblemEvolutionCandidateEdgeResponse{
			ParentID:    uuidToString(edge.ParentID),
			ChildID:     uuidToString(edge.ChildID),
			Relation:    edge.Relation,
			ParentIndex: int(edge.ParentIndex),
		})
	}
	evaluations, err := h.Queries.ListProblemEvolutionEvaluations(ctx, run.ID)
	if err != nil {
		return ProblemEvolutionExport{}, err
	}
	for index, evaluation := range evaluations {
		if index >= problemEvolutionExportLimit {
			break
		}
		export.Evaluations = append(export.Evaluations, ProblemEvolutionEvaluationExport{
			CandidateRef:       refByID[uuidToString(evaluation.CandidateID)],
			Phase:              evaluation.Phase,
			Attempt:            int(evaluation.Attempt),
			Verdict:            evaluation.Verdict,
			Score:              evaluation.Score,
			FeedbackProjection: evaluation.FeedbackProjection,
			EvaluatorHash:      evaluation.EvaluatorContentHash,
			CreatedAt:          timestampToString(evaluation.CreatedAt),
		})
	}
	artifacts, err := h.Queries.ListProblemEvolutionArtifacts(ctx, run.ID)
	if err != nil {
		return ProblemEvolutionExport{}, err
	}
	for _, artifact := range artifacts {
		export.Artifacts = append(export.Artifacts, ProblemEvolutionArtifactExport{
			CandidateRef: refByID[uuidToString(artifact.CandidateID)],
			Kind:         artifact.Kind,
			StorageRef:   artifact.StorageRef,
			ContentHash:  artifact.ContentHash,
			ContentType:  artifact.ContentType,
			SizeBytes:    artifact.SizeBytes,
		})
	}
	policy := h.problemEvolutionFeedbackPolicy(ctx, run)
	if run.EvaluatorContractID.Valid {
		if contractRow, err := h.Queries.GetProblemEvolutionEvaluatorContract(ctx, db.GetProblemEvolutionEvaluatorContractParams{
			ID:          run.EvaluatorContractID,
			WorkspaceID: run.WorkspaceID,
		}); err == nil {
			contractResp := problemEvolutionContractToResponse(contractRow)
			export.Evaluator = &contractResp
		}
	}
	if run.Mode == problemevolution.ModeTaskHarnessRewardOnly {
		harnesses, err := h.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
		if err != nil {
			return ProblemEvolutionExport{}, err
		}
		for _, harness := range harnesses {
			var gate problemevolution.StaticGateResult
			if len(harness.StaticGate) > 0 {
				_ = json.Unmarshal(harness.StaticGate, &gate)
			}
			item := ProblemEvolutionHarnessResponse{
				ID:           uuidToString(harness.ID),
				HarnessRef:   harness.HarnessRef,
				Scope:        harness.Scope,
				GatePassed:   harness.GatePassed,
				GateReasons:  gate.Reasons,
				Shortlisted:  harness.Shortlisted,
				Executed:     harness.Executed,
				Winner:       harness.Winner,
				PriorScore:   harness.PriorScore,
				RepairRounds: int(harness.RepairRounds),
			}
			if item.GateReasons == nil {
				item.GateReasons = []string{}
			}
			if harness.Reward.Valid {
				reward := harness.Reward.Float64
				item.Reward = &reward
			}
			if harness.Winner {
				export.Result.WinnerHarnessRef = harness.HarnessRef
			}
			export.Harnesses = append(export.Harnesses, item)
		}
	}
	export.Result = h.buildProblemEvolutionResult(ctx, run, refByID, policy, export.Result.WinnerHarnessRef)
	export.Reproduction = problemEvolutionReproduction(run)
	export.SecretAudit, err = h.buildProblemEvolutionSecretAuditSummary(ctx, run)
	if err != nil {
		return ProblemEvolutionExport{}, err
	}
	return export, nil
}

func (h *Handler) buildProblemEvolutionResult(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	refByID map[string]string,
	policy problemevolution.FeedbackPolicy,
	winnerHarnessRef string,
) ProblemEvolutionResultExport {
	result := ProblemEvolutionResultExport{
		SearchBestScore:   run.BestScore,
		CandidatesTotal:   int(run.CandidateCount),
		GenerationsTotal:  int(run.Generation),
		TotalCostUSD:      problemEvolutionCost(run.TotalCost),
		StopReason:        run.StopReason,
		FailureReason:     run.FailureReason,
		FeedbackBandwidth: policy.Bandwidth,
		WinnerHarnessRef:  winnerHarnessRef,
	}
	if run.BestCandidateID.Valid {
		result.BestCandidateRef = refByID[uuidToString(run.BestCandidateID)]
	}
	if run.BlindScore.Valid {
		score := run.BlindScore.Float64
		result.BlindScore = &score
		result.BlindValidated = true
	}
	if run.OverfitGap.Valid {
		gap := run.OverfitGap.Float64
		result.OverfitGap = &gap
	}
	result.ScopeClaim = problemEvolutionScopeClaim(run)
	_ = ctx
	return result
}

// problemEvolutionScopeClaim states, in the export itself, how far the result
// may be generalised. A run that was never blind-validated may not be described
// as anything beyond a result on its own search sample.
func problemEvolutionScopeClaim(run db.ProblemEvolutionRun) string {
	if !run.BlindScore.Valid {
		return "search_sample_only"
	}
	if run.OverfitGap.Valid && run.OverfitGap.Float64 > 0.15 {
		return "search_sample_only_large_overfit_gap"
	}
	if run.Mode == problemevolution.ModeTaskHarnessRewardOnly {
		return "this_task_only"
	}
	return "this_problem_blind_validated"
}

func problemEvolutionReproduction(run db.ProblemEvolutionRun) ProblemEvolutionReproduction {
	reproduction := ProblemEvolutionReproduction{
		SchemaVersion:        problemevolution.SchemaVersion,
		EvaluatorContentHash: run.EvaluatorContentHash,
		EvolverVersion:       run.EvolverVersion,
		SearchSeed:           run.SearchSeed,
		BlindSeed:            run.BlindSeed,
		ModelConfig:          jsonOrEmptyObject(run.ModelConfig),
		StopConfig:           jsonOrEmptyObject(run.StopConfig),
		EventTypes:           problemevolution.AllowedEventTypes(),
		MissingForReplay:     []string{},
	}
	if run.EvolverVersion == "" {
		reproduction.MissingForReplay = append(reproduction.MissingForReplay, "evolver_version")
	}
	if run.EvaluatorContentHash == "" {
		reproduction.MissingForReplay = append(reproduction.MissingForReplay, "evaluator_content_hash")
	}
	if run.SearchSeed == 0 {
		reproduction.MissingForReplay = append(reproduction.MissingForReplay, "search_seed")
	}
	reproduction.Replayable = len(reproduction.MissingForReplay) == 0
	return reproduction
}

func (h *Handler) buildProblemEvolutionSecretAuditSummary(
	ctx context.Context,
	run db.ProblemEvolutionRun,
) (ProblemEvolutionSecretAuditSummary, error) {
	rows, err := h.Queries.ListProblemEvolutionSecretAudit(ctx, db.ListProblemEvolutionSecretAuditParams{
		RunID:       run.ID,
		ResultLimit: problemEvolutionExportLimit,
	})
	if err != nil {
		return ProblemEvolutionSecretAuditSummary{}, err
	}
	summary := ProblemEvolutionSecretAuditSummary{DenialsByReason: map[string]int{}}
	secrets := map[string]struct{}{}
	for _, row := range rows {
		if row.SecretID.Valid {
			secrets[uuidToString(row.SecretID)] = struct{}{}
		}
		switch row.Action {
		case "issued":
			summary.Issued++
		case "redeemed":
			summary.Redeemed++
		case "denied":
			summary.Denied++
			reason := row.Reason
			if reason == "" {
				reason = "unspecified"
			}
			summary.DenialsByReason[reason]++
		}
	}
	summary.SecretsUsed = len(secrets)
	if len(summary.DenialsByReason) == 0 {
		summary.DenialsByReason = nil
	}
	return summary, nil
}

// ProblemEvolutionComparison contrasts two runs and says whether they were
// actually comparable, instead of leaving a reader to assume it.
type ProblemEvolutionComparison struct {
	Left            ProblemEvolutionResultExport `json:"left"`
	Right           ProblemEvolutionResultExport `json:"right"`
	Comparable      bool                         `json:"comparable"`
	Differences     []string                     `json:"differences"`
	SearchDelta     float64                      `json:"search_delta"`
	BlindDelta      *float64                     `json:"blind_delta,omitempty"`
	PreferredRunID  string                       `json:"preferred_run_id,omitempty"`
	PreferenceBasis string                       `json:"preference_basis"`
}

// CompareProblemEvolutionRuns compares two runs in the same workspace.
func (h *Handler) CompareProblemEvolutionRuns(w http.ResponseWriter, r *http.Request) {
	left, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	otherID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "otherRunId"), "other_run_id")
	if !ok {
		return
	}
	right, err := h.Queries.GetProblemEvolutionRun(r.Context(), db.GetProblemEvolutionRunParams{
		ID:          otherID,
		WorkspaceID: left.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	leftExport, err := h.buildProblemEvolutionExport(r.Context(), left)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	rightExport, err := h.buildProblemEvolutionExport(r.Context(), right)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	writeJSON(w, http.StatusOK, compareProblemEvolutionRuns(left, right, leftExport.Result, rightExport.Result))
}

// compareProblemEvolutionRuns decides comparability from what was pinned.
//
// Two runs scored under different evaluator contracts are not comparable, and
// saying which one "won" would be meaningless — so the comparison reports the
// difference instead of a winner.
func compareProblemEvolutionRuns(
	left, right db.ProblemEvolutionRun,
	leftResult, rightResult ProblemEvolutionResultExport,
) ProblemEvolutionComparison {
	comparison := ProblemEvolutionComparison{
		Left:        leftResult,
		Right:       rightResult,
		Differences: []string{},
		SearchDelta: leftResult.SearchBestScore - rightResult.SearchBestScore,
	}
	if left.EvaluatorContentHash != right.EvaluatorContentHash {
		comparison.Differences = append(comparison.Differences, "evaluator_contract")
	}
	if left.Mode != right.Mode {
		comparison.Differences = append(comparison.Differences, "mode")
	}
	if left.EvolverVersion != right.EvolverVersion {
		comparison.Differences = append(comparison.Differences, "evolver_version")
	}
	if left.BenchmarkMode != right.BenchmarkMode {
		comparison.Differences = append(comparison.Differences, "benchmark_mode")
	}
	comparison.Comparable = len(comparison.Differences) == 0
	if leftResult.BlindScore != nil && rightResult.BlindScore != nil {
		delta := *leftResult.BlindScore - *rightResult.BlindScore
		comparison.BlindDelta = &delta
	}
	if !comparison.Comparable {
		comparison.PreferenceBasis = "not_comparable"
		return comparison
	}
	// Blind scores decide when both runs have one; search scores are only a
	// fallback, and are labelled as such so a reader knows what the preference
	// rests on.
	if comparison.BlindDelta != nil {
		comparison.PreferenceBasis = "blind_validation"
		if *comparison.BlindDelta > 0 {
			comparison.PreferredRunID = uuidToString(left.ID)
		} else if *comparison.BlindDelta < 0 {
			comparison.PreferredRunID = uuidToString(right.ID)
		}
		return comparison
	}
	comparison.PreferenceBasis = "search_score_only"
	if comparison.SearchDelta > 0 {
		comparison.PreferredRunID = uuidToString(left.ID)
	} else if comparison.SearchDelta < 0 {
		comparison.PreferredRunID = uuidToString(right.ID)
	}
	return comparison
}
