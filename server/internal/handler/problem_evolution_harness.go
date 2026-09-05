package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// problemEvolutionFeedbackPolicy reads the run's frozen feedback policy,
// falling back to the low-bandwidth default rather than to an open one: a
// missing policy must not silently widen what the evolver may learn.
func (h *Handler) problemEvolutionFeedbackPolicy(
	ctx context.Context,
	run db.ProblemEvolutionRun,
) problemevolution.FeedbackPolicy {
	policy := problemevolution.DefaultFeedbackPolicy()
	if !run.EvaluatorContractID.Valid {
		return policy
	}
	contractRow, err := h.Queries.GetProblemEvolutionEvaluatorContract(ctx, db.GetProblemEvolutionEvaluatorContractParams{
		ID:          run.EvaluatorContractID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil || len(contractRow.FeedbackPolicy) == 0 {
		return policy
	}
	var stored problemevolution.FeedbackPolicy
	if err := json.Unmarshal(contractRow.FeedbackPolicy, &stored); err != nil {
		return policy
	}
	if stored.Validate() != nil {
		return policy
	}
	return stored
}

// ProblemEvolutionHarnessResponse is one proposal with its gate verdict.
type ProblemEvolutionHarnessResponse struct {
	ID           string   `json:"id"`
	HarnessRef   string   `json:"harness_ref"`
	Scope        string   `json:"scope"`
	GatePassed   bool     `json:"gate_passed"`
	GateReasons  []string `json:"gate_reasons"`
	Shortlisted  bool     `json:"shortlisted"`
	Executed     bool     `json:"executed"`
	Winner       bool     `json:"winner"`
	PriorScore   float64  `json:"prior_score"`
	Reward       *float64 `json:"reward,omitempty"`
	RepairRounds int      `json:"repair_rounds"`
}

type updateProblemEvolutionHarnessBudgetRequest struct {
	Proposals     int  `json:"harness_proposals"`
	ExecuteCount  int  `json:"harness_execute_count"`
	BenchmarkMode bool `json:"benchmark_mode"`
}

// UpdateProblemEvolutionHarnessBudget sets the generate-N / execute-K budget.
func (h *Handler) UpdateProblemEvolutionHarnessBudget(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	var req updateProblemEvolutionHarnessBudgetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	proposals := req.Proposals
	if proposals == 0 {
		proposals = problemevolution.DefaultHarnessProposals
	}
	executeCount := req.ExecuteCount
	if executeCount == 0 {
		executeCount = problemevolution.DefaultHarnessShortlist
	}
	if req.BenchmarkMode {
		executeCount = 1
	}
	if err := problemevolution.ValidateHarnessBudget(proposals, executeCount); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.Queries.SetProblemEvolutionRunHarnessBudget(r.Context(), db.SetProblemEvolutionRunHarnessBudgetParams{
		ID:                  run.ID,
		WorkspaceID:         run.WorkspaceID,
		HarnessProposals:    int32(proposals),
		HarnessExecuteCount: int32(executeCount),
		BenchmarkMode:       req.BenchmarkMode,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "harness budget can only change before the run is queued")
		return
	}
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(updated))
}

// ListProblemEvolutionHarnesses returns the proposals and their gate verdicts.
func (h *Handler) ListProblemEvolutionHarnesses(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionHarnesses(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list harnesses")
		return
	}
	resp := make([]ProblemEvolutionHarnessResponse, 0, len(rows))
	for _, row := range rows {
		var gate problemevolution.StaticGateResult
		if len(row.StaticGate) > 0 {
			_ = json.Unmarshal(row.StaticGate, &gate)
		}
		item := ProblemEvolutionHarnessResponse{
			ID:           uuidToString(row.ID),
			HarnessRef:   row.HarnessRef,
			Scope:        row.Scope,
			GatePassed:   row.GatePassed,
			GateReasons:  gate.Reasons,
			Shortlisted:  row.Shortlisted,
			Executed:     row.Executed,
			Winner:       row.Winner,
			PriorScore:   row.PriorScore,
			RepairRounds: int(row.RepairRounds),
		}
		if item.GateReasons == nil {
			item.GateReasons = []string{}
		}
		if row.Reward.Valid {
			reward := row.Reward.Float64
			item.Reward = &reward
		}
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, resp)
}

// projectProblemEvolutionHarness records a proposal and the platform's static
// gate verdict. Gating happens at ingestion, before anything is shortlisted or
// executed, because the cheapest way to stop a harness from reaching the hidden
// answer is to never run it.
func (h *Handler) projectProblemEvolutionHarness(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	candidate db.ProblemEvolutionCandidate,
	payload problemevolution.HarnessProposedPayload,
) error {
	policy := h.problemEvolutionFeedbackPolicy(ctx, run)
	gate := problemevolution.StaticGate(payload.Spec, policy)
	specJSON, err := json.Marshal(payload.Spec)
	if err != nil {
		return err
	}
	gateJSON, err := json.Marshal(gate)
	if err != nil {
		return err
	}
	_, err = h.Queries.UpsertProblemEvolutionHarness(ctx, db.UpsertProblemEvolutionHarnessParams{
		RunID:       run.ID,
		CandidateID: candidate.ID,
		WorkspaceID: run.WorkspaceID,
		HarnessRef:  candidate.ExternalRef,
		Spec:        specJSON,
		StaticGate:  gateJSON,
		GatePassed:  gate.Passed,
		PriorScore:  payload.PriorScore,
	})
	return err
}

// applyProblemEvolutionShortlist decides which proposals are executed. The
// generate-many / execute-few split is the whole point of the JIT structure:
// selection is cheap, execution is not.
func (h *Handler) applyProblemEvolutionShortlist(ctx context.Context, run db.ProblemEvolutionRun) error {
	harnesses, err := h.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
	if err != nil {
		return err
	}
	if len(harnesses) == 0 {
		return nil
	}
	proposals := make([]problemevolution.HarnessProposal, 0, len(harnesses))
	for _, harness := range harnesses {
		var gate problemevolution.StaticGateResult
		if len(harness.StaticGate) > 0 {
			_ = json.Unmarshal(harness.StaticGate, &gate)
		}
		var spec problemevolution.HarnessSpec
		if len(harness.Spec) > 0 {
			_ = json.Unmarshal(harness.Spec, &spec)
		}
		proposals = append(proposals, problemevolution.HarnessProposal{
			CandidateRef: harness.HarnessRef,
			Spec:         spec,
			Gate:         gate,
			PriorScore:   harness.PriorScore,
		})
	}
	executeCount := int(run.HarnessExecuteCount)
	// Benchmark mode reproduces the published JIT procedure, which executes a
	// single selected harness; anything else is not comparable to those numbers.
	if run.BenchmarkMode {
		executeCount = 1
	}
	shortlisted, rejected := problemevolution.ShortlistHarnesses(proposals, executeCount)
	for _, ref := range shortlisted {
		if _, err := h.Queries.SetProblemEvolutionHarnessShortlisted(ctx, db.SetProblemEvolutionHarnessShortlistedParams{
			RunID:       run.ID,
			HarnessRef:  ref,
			Shortlisted: true,
		}); err != nil {
			return err
		}
	}
	for _, ref := range rejected {
		if _, err := h.Queries.SetProblemEvolutionHarnessShortlisted(ctx, db.SetProblemEvolutionHarnessShortlistedParams{
			RunID:       run.ID,
			HarnessRef:  ref,
			Shortlisted: false,
		}); err != nil {
			return err
		}
	}
	return nil
}

// recordProblemEvolutionHarnessReward mirrors an executed harness's reward onto
// its harness row, so the JIT view does not have to reinterpret candidate rows.
func (h *Handler) recordProblemEvolutionHarnessReward(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	harnessRef string,
	reward float64,
) error {
	existing, err := h.Queries.GetProblemEvolutionHarnessByRef(ctx, db.GetProblemEvolutionHarnessByRefParams{
		RunID:      run.ID,
		HarnessRef: harnessRef,
	})
	if err != nil {
		// Not every scored candidate is a harness; a solution-mode candidate
		// simply has no harness row.
		return nil
	}
	repairRounds := existing.RepairRounds
	if existing.Executed {
		repairRounds++
	}
	_, err = h.Queries.SetProblemEvolutionHarnessReward(ctx, db.SetProblemEvolutionHarnessRewardParams{
		RunID:        run.ID,
		HarnessRef:   harnessRef,
		Reward:       pgtype.Float8{Float64: reward, Valid: true},
		RepairRounds: repairRounds,
	})
	return err
}

// selectProblemEvolutionHarnessWinner pins the best executed harness for the
// run. The winner is scoped to this run: a single run's reward is not evidence
// of a general capability gain.
func (h *Handler) selectProblemEvolutionHarnessWinner(ctx context.Context, run db.ProblemEvolutionRun) error {
	harnesses, err := h.Queries.ListProblemEvolutionHarnesses(ctx, run.ID)
	if err != nil {
		return err
	}
	bestRef := ""
	bestReward := 0.0
	found := false
	for _, harness := range harnesses {
		if !harness.Executed || !harness.GatePassed || !harness.Reward.Valid {
			continue
		}
		reward := harness.Reward.Float64
		if !found || reward > bestReward {
			found = true
			bestReward = reward
			bestRef = harness.HarnessRef
		}
	}
	if !found {
		return nil
	}
	if _, err := h.Queries.ClearProblemEvolutionHarnessWinner(ctx, run.ID); err != nil {
		return err
	}
	winner, err := h.Queries.SetProblemEvolutionHarnessWinner(ctx, db.SetProblemEvolutionHarnessWinnerParams{
		RunID:      run.ID,
		HarnessRef: bestRef,
	})
	if err != nil {
		return err
	}
	_, err = h.Queries.SetProblemEvolutionRunWinnerHarness(ctx, db.SetProblemEvolutionRunWinnerHarnessParams{
		ID:              run.ID,
		WinnerHarnessID: winner.ID,
	})
	return err
}
