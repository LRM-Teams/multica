package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/cloudruntime"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	problemEvolutionRunListLimit   = 50
	problemEvolutionEventPageLimit = 500
)

// ProblemEvolutionRunResponse is the API view of one run.
type ProblemEvolutionRunResponse struct {
	ID                   string          `json:"id"`
	WorkspaceID          string          `json:"workspace_id"`
	Mode                 string          `json:"mode"`
	Title                string          `json:"title"`
	ProblemSpec          json.RawMessage `json:"problem_spec"`
	ArtifactType         string          `json:"artifact_type"`
	Status               string          `json:"status"`
	Stage                string          `json:"stage"`
	RuntimeID            *string         `json:"runtime_id,omitempty"`
	ModelConfig          json.RawMessage `json:"model_config"`
	BudgetConfig         json.RawMessage `json:"budget_config"`
	StopConfig           json.RawMessage `json:"stop_config"`
	EvaluatorContractID  *string         `json:"evaluator_contract_id,omitempty"`
	EvaluatorContentHash string          `json:"evaluator_content_hash"`
	EvolverVersion       string          `json:"evolver_version"`
	BestCandidateID      *string         `json:"best_candidate_id,omitempty"`
	FinalCandidateID     *string         `json:"final_candidate_id,omitempty"`
	GraphVersion         int64           `json:"graph_version"`
	Generation           int             `json:"generation"`
	CandidateCount       int             `json:"candidate_count"`
	ModelCallCount       int             `json:"model_call_count"`
	BestScore            float64         `json:"best_score"`
	TotalCost            float64         `json:"total_cost"`
	HarnessProposals     int             `json:"harness_proposals"`
	HarnessExecuteCount  int             `json:"harness_execute_count"`
	BenchmarkMode        bool            `json:"benchmark_mode"`
	WinnerHarnessID      *string         `json:"winner_harness_id,omitempty"`
	BlindScore           *float64        `json:"blind_score,omitempty"`
	OverfitGap           *float64        `json:"overfit_gap,omitempty"`
	TaskSetID            *string         `json:"task_set_id,omitempty"`
	StopReason           string          `json:"stop_reason"`
	FailureReason        string          `json:"failure_reason"`
	StartedAt            *string         `json:"started_at,omitempty"`
	FinishedAt           *string         `json:"finished_at,omitempty"`
	CreatedAt            string          `json:"created_at"`
	UpdatedAt            string          `json:"updated_at"`
}

// ProblemEvolutionEvaluatorContractResponse is the API view of a contract.
type ProblemEvolutionEvaluatorContractResponse struct {
	ID             string          `json:"id"`
	Mode           string          `json:"mode"`
	Status         string          `json:"status"`
	Version        int32           `json:"version"`
	Contract       json.RawMessage `json:"contract"`
	FeedbackPolicy json.RawMessage `json:"feedback_policy"`
	ContentHash    string          `json:"content_hash"`
	FrozenAt       *string         `json:"frozen_at,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

// ProblemEvolutionCandidateResponse is the API view of one candidate node.
type ProblemEvolutionCandidateResponse struct {
	ID              string          `json:"id"`
	RunID           string          `json:"run_id"`
	ExternalRef     string          `json:"external_ref"`
	Generation      int32           `json:"generation"`
	Lane            string          `json:"lane"`
	Operator        string          `json:"operator"`
	Status          string          `json:"status"`
	Score           json.RawMessage `json:"score,omitempty"`
	BehaviorProfile json.RawMessage `json:"behavior_profile,omitempty"`
	Feasible        bool            `json:"feasible"`
	FeedbackRounds  int             `json:"feedback_rounds"`
	ArtifactRef     string          `json:"artifact_ref"`
	ArtifactHash    string          `json:"artifact_hash"`
	Summary         string          `json:"summary"`
	ChangeSummary   string          `json:"change_summary"`
	FailureClass    string          `json:"failure_class"`
	RuntimeSeconds  float64         `json:"runtime_seconds"`
	TokenUsage      json.RawMessage `json:"token_usage"`
	Cost            float64         `json:"cost"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// ProblemEvolutionEventResponse is the API view of one replayable event.
type ProblemEvolutionEventResponse struct {
	ID            string          `json:"id"`
	Seq           int64           `json:"seq"`
	ClientEventID string          `json:"client_event_id"`
	EventType     string          `json:"event_type"`
	CandidateID   *string         `json:"candidate_id,omitempty"`
	ActorType     string          `json:"actor_type"`
	ActorID       string          `json:"actor_id"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
}

// ProblemEvolutionSnapshotResponse is the single fetch the canvas restores
// from. GraphVersion lets the client drop a stale snapshot that arrives after
// a newer invalidate event.
type ProblemEvolutionSnapshotResponse struct {
	Run          ProblemEvolutionRunResponse                `json:"run"`
	Evaluator    *ProblemEvolutionEvaluatorContractResponse `json:"evaluator,omitempty"`
	Candidates   []ProblemEvolutionCandidateResponse        `json:"candidates"`
	Edges        []ProblemEvolutionCandidateEdgeResponse    `json:"edges"`
	GraphVersion int64                                      `json:"graph_version"`
	LatestSeq    int64                                      `json:"latest_seq"`
}

// ProblemEvolutionCandidateEdgeResponse is one lineage edge in the graph.
type ProblemEvolutionCandidateEdgeResponse struct {
	ParentID    string `json:"parent_id"`
	ChildID     string `json:"child_id"`
	Relation    string `json:"relation"`
	ParentIndex int    `json:"parent_index"`
}

type createProblemEvolutionRunRequest struct {
	Mode         string          `json:"mode"`
	Title        string          `json:"title"`
	ProblemSpec  json.RawMessage `json:"problem_spec"`
	ArtifactType string          `json:"artifact_type"`
	RuntimeID    *string         `json:"runtime_id"`
	ModelConfig  json.RawMessage `json:"model_config"`
	BudgetConfig json.RawMessage `json:"budget_config"`
	StopConfig   json.RawMessage `json:"stop_config"`
	TaskSetID    *string         `json:"task_set_id"`
}

type updateProblemEvolutionRunRequest struct {
	Title               *string         `json:"title"`
	ProblemSpec         json.RawMessage `json:"problem_spec"`
	ArtifactType        *string         `json:"artifact_type"`
	RuntimeID           *string         `json:"runtime_id"`
	ModelConfig         json.RawMessage `json:"model_config"`
	BudgetConfig        json.RawMessage `json:"budget_config"`
	StopConfig          json.RawMessage `json:"stop_config"`
	EvaluatorContractID *string         `json:"evaluator_contract_id"`
	TaskSetID           *string         `json:"task_set_id"`
}

type problemEvolutionEvaluatorRequest struct {
	Contract       problemevolution.EvaluatorContract `json:"contract"`
	FeedbackPolicy *problemevolution.FeedbackPolicy   `json:"feedback_policy"`
}

type stopProblemEvolutionRunRequest struct {
	Reason string `json:"reason"`
}

// CreateProblemEvolutionRun opens a draft run. Nothing executes until the
// evaluator contract is frozen and the run is explicitly started.
func (h *Handler) CreateProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	var req createProblemEvolutionRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Mode == "" {
		req.Mode = problemevolution.ModeSolution
	}
	switch req.Mode {
	case problemevolution.ModeSolution:
	case problemevolution.ModeTaskHarnessRewardOnly:
		// Mode B runs untrusted harnesses against hidden answers, so it stays
		// behind a flag until a deployment has decided to enable that path.
		if !problemevolution.ModeBEnabled() {
			writeError(w, http.StatusBadRequest, "task harness mode is not enabled")
			return
		}
	case problemevolution.ModeTaskHarnessPersistent:
		if req.TaskSetID == nil || strings.TrimSpace(*req.TaskSetID) == "" {
			writeError(w, http.StatusBadRequest, "persistent harness mode requires a task_set_id")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "unsupported mode")
		return
	}
	runtimeID, ok := h.optionalProblemEvolutionRuntime(w, req.RuntimeID)
	if !ok {
		return
	}
	taskSetID := pgtype.UUID{}
	if req.TaskSetID != nil && strings.TrimSpace(*req.TaskSetID) != "" {
		parsedTaskSetID, parseOK := parseUUIDOrBadRequest(w, *req.TaskSetID, "task_set_id")
		if !parseOK {
			return
		}
		taskSet, err := h.Queries.GetProblemEvolutionTaskSet(r.Context(), db.GetProblemEvolutionTaskSetParams{
			ID: parsedTaskSetID, WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "task set not found")
			return
		}
		if req.Mode == problemevolution.ModeTaskHarnessPersistent {
			var holdoutNames []string
			if json.Unmarshal(taskSet.HoldoutTaskNames, &holdoutNames) != nil || len(holdoutNames) == 0 {
				writeError(w, http.StatusBadRequest, "persistent harness mode requires holdout_task_names")
				return
			}
		}
		taskSetID = parsedTaskSetID
	}
	artifactType := req.ArtifactType
	if artifactType == "" {
		artifactType = "markdown"
	}
	row, err := h.Queries.CreateProblemEvolutionRun(r.Context(), db.CreateProblemEvolutionRunParams{
		WorkspaceID:  wsUUID,
		CreatedBy:    member.UserID,
		Mode:         req.Mode,
		Title:        req.Title,
		ProblemSpec:  jsonOrEmptyObject(req.ProblemSpec),
		ArtifactType: artifactType,
		RuntimeID:    runtimeID,
		ModelConfig:  jsonOrEmptyObject(req.ModelConfig),
		BudgetConfig: jsonOrEmptyObject(req.BudgetConfig),
		StopConfig:   jsonOrEmptyObject(req.StopConfig),
		TaskSetID:    taskSetID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create run")
		return
	}
	h.publishProblemEvolutionRunChanged(workspaceID, row)
	writeJSON(w, http.StatusCreated, problemEvolutionRunToResponse(row))
}

// ListProblemEvolutionRuns returns the most recent runs in the workspace.
func (h *Handler) ListProblemEvolutionRuns(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace_id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionRuns(r.Context(), db.ListProblemEvolutionRunsParams{
		WorkspaceID: wsUUID,
		ResultLimit: problemEvolutionRunListLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	resp := make([]ProblemEvolutionRunResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionRunToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetProblemEvolutionRun returns one run.
func (h *Handler) GetProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(run))
}

// UpdateProblemEvolutionRun edits a run that has not started yet.
func (h *Handler) UpdateProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	var req updateProblemEvolutionRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	params := db.UpdateProblemEvolutionRunDraftParams{
		ID:                  run.ID,
		WorkspaceID:         run.WorkspaceID,
		Title:               run.Title,
		ProblemSpec:         run.ProblemSpec,
		ArtifactType:        run.ArtifactType,
		RuntimeID:           run.RuntimeID,
		ModelConfig:         run.ModelConfig,
		BudgetConfig:        run.BudgetConfig,
		StopConfig:          run.StopConfig,
		TaskSetID:           run.TaskSetID,
		EvaluatorContractID: run.EvaluatorContractID,
	}
	if req.Title != nil {
		params.Title = *req.Title
	}
	if len(req.ProblemSpec) > 0 {
		params.ProblemSpec = req.ProblemSpec
	}
	if req.ArtifactType != nil {
		params.ArtifactType = *req.ArtifactType
	}
	if req.RuntimeID != nil {
		runtimeID, valid := h.optionalProblemEvolutionRuntime(w, req.RuntimeID)
		if !valid {
			return
		}
		params.RuntimeID = runtimeID
	}
	if len(req.ModelConfig) > 0 {
		params.ModelConfig = req.ModelConfig
	}
	if len(req.BudgetConfig) > 0 {
		params.BudgetConfig = req.BudgetConfig
	}
	if len(req.StopConfig) > 0 {
		params.StopConfig = req.StopConfig
	}
	if req.TaskSetID != nil {
		taskSetID, valid := parseUUIDOrBadRequest(w, *req.TaskSetID, "task_set_id")
		if !valid {
			return
		}
		if _, err := h.Queries.GetProblemEvolutionTaskSet(r.Context(), db.GetProblemEvolutionTaskSetParams{
			ID: taskSetID, WorkspaceID: run.WorkspaceID,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "task set not found")
			return
		}
		params.TaskSetID = taskSetID
	}
	if req.EvaluatorContractID != nil {
		contractID, valid := parseUUIDOrBadRequest(w, *req.EvaluatorContractID, "evaluator_contract_id")
		if !valid {
			return
		}
		if _, err := h.Queries.GetProblemEvolutionEvaluatorContract(r.Context(), db.GetProblemEvolutionEvaluatorContractParams{
			ID:          contractID,
			WorkspaceID: run.WorkspaceID,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "evaluator contract not found")
			return
		}
		params.EvaluatorContractID = contractID
	}
	updated, err := h.Queries.UpdateProblemEvolutionRunDraft(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "run has already started")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update run")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(updated.WorkspaceID), updated)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(updated))
}

// CreateProblemEvolutionEvaluator stores a draft contract for the run.
func (h *Handler) CreateProblemEvolutionEvaluator(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, uuidToString(run.WorkspaceID))
	if !ok {
		return
	}
	req, policy, ok := decodeProblemEvolutionEvaluatorRequest(w, r)
	if !ok {
		return
	}
	contractJSON, err := json.Marshal(req.Contract)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid contract")
		return
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid feedback policy")
		return
	}
	contractRow, err := h.Queries.CreateProblemEvolutionEvaluatorContract(r.Context(), db.CreateProblemEvolutionEvaluatorContractParams{
		WorkspaceID:    run.WorkspaceID,
		Mode:           run.Mode,
		Contract:       contractJSON,
		FeedbackPolicy: policyJSON,
		CreatedBy:      member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create evaluator contract")
		return
	}
	updated, err := h.Queries.UpdateProblemEvolutionRunDraft(r.Context(), db.UpdateProblemEvolutionRunDraftParams{
		ID:                  run.ID,
		WorkspaceID:         run.WorkspaceID,
		Title:               run.Title,
		ProblemSpec:         run.ProblemSpec,
		ArtifactType:        run.ArtifactType,
		RuntimeID:           run.RuntimeID,
		ModelConfig:         run.ModelConfig,
		BudgetConfig:        run.BudgetConfig,
		StopConfig:          run.StopConfig,
		EvaluatorContractID: contractRow.ID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "run has already started")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(updated.WorkspaceID), updated)
	writeJSON(w, http.StatusCreated, problemEvolutionContractToResponse(contractRow))
}

// ProposeProblemEvolutionEvaluator derives a draft contract from the run's
// problem statement and stores it as the run's contract. The proposal is a
// starting point for the user to edit: freezing is still a separate, explicit
// step, because scoring must be fixed before any candidate exists.
func (h *Handler) ProposeProblemEvolutionEvaluator(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, uuidToString(run.WorkspaceID))
	if !ok {
		return
	}
	var problem problemevolution.ProblemSpec
	if len(run.ProblemSpec) > 0 {
		if err := json.Unmarshal(run.ProblemSpec, &problem); err != nil {
			writeError(w, http.StatusBadRequest, "run problem_spec is not decodable")
			return
		}
	}
	if problem.Statement == "" {
		writeError(w, http.StatusBadRequest, "run has no problem statement")
		return
	}
	contract := problemevolution.NormalizeWeights(problemevolution.ProposeContract(problem))
	if err := contract.Validate(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	policy := problemevolution.DefaultFeedbackPolicy()
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode proposed contract")
		return
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode feedback policy")
		return
	}
	contractRow, err := h.Queries.CreateProblemEvolutionEvaluatorContract(r.Context(), db.CreateProblemEvolutionEvaluatorContractParams{
		WorkspaceID:    run.WorkspaceID,
		Mode:           run.Mode,
		Contract:       contractJSON,
		FeedbackPolicy: policyJSON,
		CreatedBy:      member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store proposed contract")
		return
	}
	updated, err := h.Queries.UpdateProblemEvolutionRunDraft(r.Context(), db.UpdateProblemEvolutionRunDraftParams{
		ID:                  run.ID,
		WorkspaceID:         run.WorkspaceID,
		Title:               run.Title,
		ProblemSpec:         run.ProblemSpec,
		ArtifactType:        run.ArtifactType,
		RuntimeID:           run.RuntimeID,
		ModelConfig:         run.ModelConfig,
		BudgetConfig:        run.BudgetConfig,
		StopConfig:          run.StopConfig,
		EvaluatorContractID: contractRow.ID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "run has already started")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(updated.WorkspaceID), updated)
	writeJSON(w, http.StatusCreated, problemEvolutionContractToResponse(contractRow))
}

// FreezeProblemEvolutionEvaluator validates and freezes the contract. After
// this the contract body is immutable for the run: the hash computed here is
// what every evaluation re-checks.
func (h *Handler) FreezeProblemEvolutionEvaluator(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	if !run.EvaluatorContractID.Valid {
		writeError(w, http.StatusBadRequest, "run has no evaluator contract")
		return
	}
	contractRow, err := h.Queries.GetProblemEvolutionEvaluatorContract(r.Context(), db.GetProblemEvolutionEvaluatorContractParams{
		ID:          run.EvaluatorContractID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "evaluator contract not found")
		return
	}
	if contractRow.Status == "frozen" {
		writeJSON(w, http.StatusOK, problemEvolutionContractToResponse(contractRow))
		return
	}
	var contract problemevolution.EvaluatorContract
	if err := json.Unmarshal(contractRow.Contract, &contract); err != nil {
		writeError(w, http.StatusBadRequest, "stored contract is not decodable")
		return
	}
	policy := problemevolution.DefaultFeedbackPolicy()
	if len(contractRow.FeedbackPolicy) > 0 {
		if err := json.Unmarshal(contractRow.FeedbackPolicy, &policy); err != nil {
			writeError(w, http.StatusBadRequest, "stored feedback policy is not decodable")
			return
		}
	}
	if err := contract.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := policy.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := problemevolution.ContentHash(contract, policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash contract")
		return
	}
	frozen, err := h.Queries.FreezeProblemEvolutionEvaluatorContract(r.Context(), db.FreezeProblemEvolutionEvaluatorContractParams{
		ID:          contractRow.ID,
		WorkspaceID: run.WorkspaceID,
		ContentHash: hash,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "contract is not freezable")
		return
	}
	writeJSON(w, http.StatusOK, problemEvolutionContractToResponse(frozen))
}

// StartProblemEvolutionRun queues the run for a daemon to claim. It refuses
// unless the evaluator contract is frozen, and pins the contract hash.
func (h *Handler) StartProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	if err := h.preflightProblemEvolutionBilling(r.Context(), r, run); err != nil {
		if errors.Is(err, errProblemEvolutionBillingUnavailable) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
		} else {
			writeError(w, http.StatusPaymentRequired, err.Error())
		}
		return
	}
	queued, err := h.Queries.QueueProblemEvolutionRun(r.Context(), db.QueueProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "run needs a frozen evaluator contract and a startable status")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to start run")
		return
	}
	// Seeds are pinned at start so the same run always splits search from blind
	// validation the same way, which is what makes a rerun comparable.
	seeds := problemevolution.DeriveSeeds(uuidToString(queued.ID))
	if seeded, err := h.Queries.SetProblemEvolutionRunSeeds(r.Context(), db.SetProblemEvolutionRunSeedsParams{
		ID:         queued.ID,
		SearchSeed: seeds.Search,
		BlindSeed:  seeds.Blind,
	}); err == nil {
		queued = seeded
	}
	h.publishProblemEvolutionRunChanged(uuidToString(queued.WorkspaceID), queued)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(queued))
}

var errProblemEvolutionBillingUnavailable = errors.New("problem evolution billing unavailable")

// preflightProblemEvolutionBilling uses the existing cloud billing service when
// configured. A zero run cost ceiling means "no run-level ceiling", not
// "bypass account billing": a configured wallet must still have a positive
// balance before execution starts. Unknown self-hosted billing responses are
// left to local run budgets rather than blocking deployments that do not expose
// the cloud wallet.
func (h *Handler) preflightProblemEvolutionBilling(ctx context.Context, r *http.Request, run db.ProblemEvolutionRun) error {
	if h.CloudRuntime == nil || !h.CloudRuntime.Enabled() {
		return nil
	}
	userID := requestUserID(r)
	resp, err := h.CloudRuntime.Do(ctx, cloudruntime.Request{
		Method: http.MethodGet, Path: "/api/v1/billing/balance", UserID: userID,
		RequestID: cloudRuntimeRequestID(r), Op: "billing_balance",
	})
	if err != nil {
		return fmt.Errorf("%w: billing balance could not be checked", errProblemEvolutionBillingUnavailable)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: billing balance returned HTTP %d", errProblemEvolutionBillingUnavailable, resp.StatusCode)
	}
	balance, found := billingBalanceFromJSON(resp.Body)
	if !found {
		return nil
	}
	config := problemEvolutionStopConfig(run)
	if balance <= 0 {
		return errors.New("billing balance is insufficient to start problem evolution")
	}
	if config.MaxCostUSD > 0 && balance < config.MaxCostUSD {
		return fmt.Errorf("billing balance %.6f is below the configured run ceiling %.6f", balance, config.MaxCostUSD)
	}
	return nil
}

func billingBalanceFromJSON(raw []byte) (float64, bool) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	var walk func(any) (float64, bool)
	walk = func(node any) (float64, bool) {
		switch v := node.(type) {
		case map[string]any:
			for _, key := range []string{"available_balance", "balance", "credit_balance", "credits"} {
				if number, ok := v[key].(float64); ok {
					return number, true
				}
			}
			for _, key := range []string{"data", "wallet", "account"} {
				if nested, ok := v[key]; ok {
					if number, found := walk(nested); found {
						return number, true
					}
				}
			}
		}
		return 0, false
	}
	return walk(value)
}

// StopProblemEvolutionRun asks the daemon to stop producing candidates. The
// run reaches `cancelled` through the stopping path, never directly.
func (h *Handler) StopProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	var req stopProblemEvolutionRunRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	reason := req.Reason
	if reason == "" {
		reason = "user_stopped"
	}
	stopping, err := h.Queries.RequestStopProblemEvolutionRun(r.Context(), db.RequestStopProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
		StopReason:  reason,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "run is not stoppable")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to stop run")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(stopping.WorkspaceID), stopping)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(stopping))
}

// GetProblemEvolutionSnapshot returns the full canvas state plus the graph
// version the client must compare against incoming events.
func (h *Handler) GetProblemEvolutionSnapshot(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	candidates, err := h.Queries.ListProblemEvolutionCandidates(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load candidates")
		return
	}
	resp := ProblemEvolutionSnapshotResponse{
		Run:          problemEvolutionRunToResponse(run),
		Candidates:   make([]ProblemEvolutionCandidateResponse, 0, len(candidates)),
		Edges:        []ProblemEvolutionCandidateEdgeResponse{},
		GraphVersion: run.GraphVersion,
	}
	for _, candidate := range candidates {
		resp.Candidates = append(resp.Candidates, problemEvolutionCandidateToResponse(candidate))
	}
	if run.EvaluatorContractID.Valid {
		if contractRow, contractErr := h.Queries.GetProblemEvolutionEvaluatorContract(r.Context(), db.GetProblemEvolutionEvaluatorContractParams{
			ID:          run.EvaluatorContractID,
			WorkspaceID: run.WorkspaceID,
		}); contractErr == nil {
			contractResp := problemEvolutionContractToResponse(contractRow)
			resp.Evaluator = &contractResp
		}
	}
	if edges, edgeErr := h.Queries.ListProblemEvolutionCandidateEdges(r.Context(), run.ID); edgeErr == nil {
		resp.Edges = make([]ProblemEvolutionCandidateEdgeResponse, 0, len(edges))
		for _, edge := range edges {
			resp.Edges = append(resp.Edges, ProblemEvolutionCandidateEdgeResponse{
				ParentID:    uuidToString(edge.ParentID),
				ChildID:     uuidToString(edge.ChildID),
				Relation:    edge.Relation,
				ParentIndex: int(edge.ParentIndex),
			})
		}
	}
	if latestSeq, seqErr := h.Queries.GetProblemEvolutionLatestEventSeq(r.Context(), run.ID); seqErr == nil {
		resp.LatestSeq = latestSeq
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListProblemEvolutionEvents replays events after a sequence number so a
// reconnecting client can catch up without refetching the whole snapshot.
func (h *Handler) ListProblemEvolutionEvents(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	afterSeq := int64(0)
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid after_seq")
			return
		}
		afterSeq = parsed
	}
	rows, err := h.Queries.ListProblemEvolutionEventsAfterSeq(r.Context(), db.ListProblemEvolutionEventsAfterSeqParams{
		RunID:       run.ID,
		AfterSeq:    afterSeq,
		ResultLimit: problemEvolutionEventPageLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load events")
		return
	}
	resp := make([]ProblemEvolutionEventResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionEventToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetProblemEvolutionCandidate returns one candidate's detail card.
func (h *Handler) GetProblemEvolutionCandidate(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	candidateID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "candidateId"), "candidate_id")
	if !ok {
		return
	}
	candidate, err := h.Queries.GetProblemEvolutionCandidate(r.Context(), db.GetProblemEvolutionCandidateParams{
		ID:          candidateID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "candidate not found")
		return
	}
	if uuidToString(candidate.RunID) != uuidToString(run.ID) {
		writeError(w, http.StatusNotFound, "candidate not found")
		return
	}
	writeJSON(w, http.StatusOK, problemEvolutionCandidateToResponse(candidate))
}

func (h *Handler) loadProblemEvolutionRun(w http.ResponseWriter, r *http.Request) (db.ProblemEvolutionRun, bool) {
	wsUUID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace_id")
	if !ok {
		return db.ProblemEvolutionRun{}, false
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run_id")
	if !ok {
		return db.ProblemEvolutionRun{}, false
	}
	run, err := h.Queries.GetProblemEvolutionRun(r.Context(), db.GetProblemEvolutionRunParams{
		ID:          runID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "run not found")
			return db.ProblemEvolutionRun{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return db.ProblemEvolutionRun{}, false
	}
	return run, true
}

func (h *Handler) optionalProblemEvolutionRuntime(w http.ResponseWriter, raw *string) (pgtype.UUID, bool) {
	if raw == nil || *raw == "" {
		return pgtype.UUID{}, true
	}
	runtimeID, ok := parseUUIDOrBadRequest(w, *raw, "runtime_id")
	if !ok {
		return pgtype.UUID{}, false
	}
	return runtimeID, true
}

func decodeProblemEvolutionEvaluatorRequest(w http.ResponseWriter, r *http.Request) (problemEvolutionEvaluatorRequest, problemevolution.FeedbackPolicy, bool) {
	var req problemEvolutionEvaluatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return req, problemevolution.FeedbackPolicy{}, false
	}
	if req.Contract.SchemaVersion == 0 {
		req.Contract.SchemaVersion = problemevolution.SchemaVersion
	}
	if err := req.Contract.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return req, problemevolution.FeedbackPolicy{}, false
	}
	policy := problemevolution.DefaultFeedbackPolicy()
	if req.FeedbackPolicy != nil {
		policy = *req.FeedbackPolicy
		if policy.SchemaVersion == 0 {
			policy.SchemaVersion = problemevolution.SchemaVersion
		}
		if err := policy.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return req, problemevolution.FeedbackPolicy{}, false
		}
	}
	return req, policy, true
}

func (h *Handler) publishProblemEvolutionRunChanged(workspaceID string, run db.ProblemEvolutionRun) {
	h.publish("problem_evolution_run:changed", workspaceID, "system", "", map[string]any{
		"workspace_id":  workspaceID,
		"run_id":        uuidToString(run.ID),
		"graph_version": run.GraphVersion,
		"status":        run.Status,
	})
}

func (h *Handler) publishProblemEvolutionRunCompleted(workspaceID string, run db.ProblemEvolutionRun) {
	h.publish("problem_evolution_run:completed", workspaceID, "system", "", map[string]any{
		"workspace_id":  workspaceID,
		"run_id":        uuidToString(run.ID),
		"graph_version": run.GraphVersion,
	})
}

func problemEvolutionCost(value pgtype.Numeric) float64 {
	if !value.Valid {
		return 0
	}
	converted, err := value.Float64Value()
	if err != nil || !converted.Valid {
		return 0
	}
	return converted.Float64
}

func jsonOrEmptyObject(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}

func problemEvolutionRunToResponse(row db.ProblemEvolutionRun) ProblemEvolutionRunResponse {
	return ProblemEvolutionRunResponse{
		ID:                   uuidToString(row.ID),
		WorkspaceID:          uuidToString(row.WorkspaceID),
		Mode:                 row.Mode,
		Title:                row.Title,
		ProblemSpec:          row.ProblemSpec,
		ArtifactType:         row.ArtifactType,
		Status:               row.Status,
		Stage:                row.Stage,
		RuntimeID:            uuidToPtr(row.RuntimeID),
		ModelConfig:          row.ModelConfig,
		BudgetConfig:         row.BudgetConfig,
		StopConfig:           row.StopConfig,
		TaskSetID:            uuidToPtr(row.TaskSetID),
		EvaluatorContractID:  uuidToPtr(row.EvaluatorContractID),
		EvaluatorContentHash: row.EvaluatorContentHash,
		EvolverVersion:       row.EvolverVersion,
		BestCandidateID:      uuidToPtr(row.BestCandidateID),
		FinalCandidateID:     uuidToPtr(row.FinalCandidateID),
		GraphVersion:         row.GraphVersion,
		Generation:           int(row.Generation),
		CandidateCount:       int(row.CandidateCount),
		ModelCallCount:       int(row.ModelCallCount),
		BestScore:            row.BestScore,
		TotalCost:            problemEvolutionCost(row.TotalCost),
		HarnessProposals:     int(row.HarnessProposals),
		HarnessExecuteCount:  int(row.HarnessExecuteCount),
		BenchmarkMode:        row.BenchmarkMode,
		WinnerHarnessID:      uuidToPtr(row.WinnerHarnessID),
		BlindScore:           float8ToPtr(row.BlindScore),
		OverfitGap:           float8ToPtr(row.OverfitGap),
		StopReason:           row.StopReason,
		FailureReason:        row.FailureReason,
		StartedAt:            timestampToPtr(row.StartedAt),
		FinishedAt:           timestampToPtr(row.FinishedAt),
		CreatedAt:            timestampToString(row.CreatedAt),
		UpdatedAt:            timestampToString(row.UpdatedAt),
	}
}

func problemEvolutionContractToResponse(row db.ProblemEvolutionEvaluatorContract) ProblemEvolutionEvaluatorContractResponse {
	return ProblemEvolutionEvaluatorContractResponse{
		ID:             uuidToString(row.ID),
		Mode:           row.Mode,
		Status:         row.Status,
		Version:        row.Version,
		Contract:       row.Contract,
		FeedbackPolicy: row.FeedbackPolicy,
		ContentHash:    row.ContentHash,
		FrozenAt:       timestampToPtr(row.FrozenAt),
		CreatedAt:      timestampToString(row.CreatedAt),
		UpdatedAt:      timestampToString(row.UpdatedAt),
	}
}

func problemEvolutionCandidateToResponse(row db.ProblemEvolutionCandidate) ProblemEvolutionCandidateResponse {
	return ProblemEvolutionCandidateResponse{
		ID:              uuidToString(row.ID),
		RunID:           uuidToString(row.RunID),
		ExternalRef:     row.ExternalRef,
		Generation:      row.Generation,
		Lane:            row.Lane,
		Operator:        row.Operator,
		Status:          row.Status,
		Score:           row.Score,
		BehaviorProfile: row.BehaviorProfile,
		Feasible:        row.Feasible,
		FeedbackRounds:  int(row.FeedbackRounds),
		ArtifactRef:     row.ArtifactRef,
		ArtifactHash:    row.ArtifactHash,
		Summary:         row.Summary,
		ChangeSummary:   row.ChangeSummary,
		FailureClass:    row.FailureClass,
		RuntimeSeconds:  row.RuntimeSeconds,
		TokenUsage:      row.TokenUsage,
		Cost:            problemEvolutionCost(row.Cost),
		CreatedAt:       timestampToString(row.CreatedAt),
		UpdatedAt:       timestampToString(row.UpdatedAt),
	}
}

func problemEvolutionEventToResponse(row db.ProblemEvolutionEvent) ProblemEvolutionEventResponse {
	return ProblemEvolutionEventResponse{
		ID:            uuidToString(row.ID),
		Seq:           row.Seq,
		ClientEventID: row.ClientEventID,
		EventType:     row.EventType,
		CandidateID:   uuidToPtr(row.CandidateID),
		ActorType:     row.ActorType,
		ActorID:       row.ActorID,
		Payload:       row.Payload,
		CreatedAt:     timestampToString(row.CreatedAt),
	}
}
