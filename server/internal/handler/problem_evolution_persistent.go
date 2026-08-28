package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const problemEvolutionTaskSetListLimit = 100

type ProblemEvolutionTaskSetResponse struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	Source           string          `json:"source"`
	DatasetRef       string          `json:"dataset_ref"`
	DatasetRevision  string          `json:"dataset_revision"`
	TaskNames        json.RawMessage `json:"task_names"`
	HoldoutTaskNames json.RawMessage `json:"holdout_task_names"`
	RolloutsPerTask  int             `json:"rollouts_per_task"`
	MaxParallel      int             `json:"max_parallel"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
}

type createProblemEvolutionTaskSetRequest struct {
	Source           string   `json:"source"`
	DatasetRef       string   `json:"dataset_ref"`
	DatasetRevision  string   `json:"dataset_revision"`
	TaskNames        []string `json:"task_names"`
	HoldoutTaskNames []string `json:"holdout_task_names"`
	RolloutsPerTask  int      `json:"rollouts_per_task"`
	MaxParallel      int      `json:"max_parallel"`
}

type ProblemEvolutionHarnessVersionResponse struct {
	ID              string          `json:"id"`
	RunID           string          `json:"run_id"`
	Iteration       int             `json:"iteration"`
	ParentVersionID *string         `json:"parent_version_id,omitempty"`
	Components      json.RawMessage `json:"components"`
	ContentHash     string          `json:"content_hash"`
	RolledBack      bool            `json:"rolled_back"`
	PromotedScope   string          `json:"promoted_scope"`
	CreatedAt       string          `json:"created_at"`
}

type ProblemEvolutionIterationResponse struct {
	ID              string  `json:"id"`
	RunID           string  `json:"run_id"`
	Iteration       int     `json:"iteration"`
	InputVersionID  *string `json:"input_version_id,omitempty"`
	EvolveVersionID *string `json:"evolve_version_id,omitempty"`
	Stage           string  `json:"stage"`
	PassRate        float64 `json:"pass_rate"`
	HoldoutPassRate float64 `json:"holdout_pass_rate"`
	Cost            float64 `json:"cost"`
	Tokens          int64   `json:"tokens"`
}

type ProblemEvolutionTaskResultResponse struct {
	TaskName       string  `json:"task_name"`
	RolloutIndex   int     `json:"rollout_index"`
	Split          string  `json:"split"`
	Reward         float64 `json:"reward"`
	Verdict        string  `json:"verdict"`
	TraceRef       string  `json:"trace_ref,omitempty"`
	TraceDigestRef string  `json:"trace_digest_ref,omitempty"`
	Tokens         int64   `json:"tokens"`
	Cost           float64 `json:"cost"`
}

type ProblemEvolutionChangeRecordResponse struct {
	ID                     string          `json:"id"`
	IterationID            string          `json:"iteration_id"`
	HarnessVersionID       *string         `json:"harness_version_id,omitempty"`
	Component              string          `json:"component"`
	FailureEvidenceRef     string          `json:"failure_evidence_ref"`
	RootCause              string          `json:"root_cause"`
	FixSummary             string          `json:"fix_summary"`
	PredictedPassTaskNames json.RawMessage `json:"predicted_pass_task_names"`
	PredictedRiskTaskNames json.RawMessage `json:"predicted_risk_task_names"`
	ObservedFlips          json.RawMessage `json:"observed_flips"`
	Verdict                string          `json:"verdict"`
	Action                 string          `json:"action"`
}

// CreateProblemEvolutionTaskSet stores only an external dataset reference.
// Tasks, prompts, and verifiers remain on the sandbox side.
func (h *Handler) CreateProblemEvolutionTaskSet(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	var req createProblemEvolutionTaskSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateProblemEvolutionTaskSet(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	taskNames, _ := json.Marshal(req.TaskNames)
	holdoutNames, _ := json.Marshal(req.HoldoutTaskNames)
	row, err := h.Queries.CreateProblemEvolutionTaskSet(r.Context(), db.CreateProblemEvolutionTaskSetParams{
		WorkspaceID: wsUUID, Source: strings.TrimSpace(req.Source),
		DatasetRef: strings.TrimSpace(req.DatasetRef), DatasetRevision: strings.TrimSpace(req.DatasetRevision),
		TaskNames: taskNames, HoldoutTaskNames: holdoutNames,
		RolloutsPerTask: int32(req.RolloutsPerTask), MaxParallel: int32(req.MaxParallel),
		CreatedBy: member.UserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create task set")
		return
	}
	writeJSON(w, http.StatusCreated, problemEvolutionTaskSetToResponse(row))
}

func (h *Handler) ListProblemEvolutionTaskSets(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionTaskSets(r.Context(), db.ListProblemEvolutionTaskSetsParams{
		WorkspaceID: wsUUID, ResultLimit: problemEvolutionTaskSetListLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task sets")
		return
	}
	resp := make([]ProblemEvolutionTaskSetResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionTaskSetToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetProblemEvolutionWorkspaceHarness(w http.ResponseWriter, r *http.Request) {
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	row, err := h.Queries.GetProblemEvolutionWorkspaceHarnessVersion(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace harness is not promoted")
		return
	}
	writeJSON(w, http.StatusOK, problemEvolutionHarnessVersionToResponse(row))
}

func (h *Handler) ListProblemEvolutionHarnessVersions(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionHarnessVersions(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list harness versions")
		return
	}
	resp := make([]ProblemEvolutionHarnessVersionResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionHarnessVersionToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListProblemEvolutionIterations(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionIterations(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list iterations")
		return
	}
	resp := make([]ProblemEvolutionIterationResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionIterationToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListProblemEvolutionTaskResults(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionTaskResults(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task results")
		return
	}
	resp := make([]ProblemEvolutionTaskResultResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionTaskResultToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListProblemEvolutionChangeRecords(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	rows, err := h.Queries.ListProblemEvolutionChangeRecords(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list change records")
		return
	}
	resp := make([]ProblemEvolutionChangeRecordResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, problemEvolutionChangeRecordToResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

// PromoteProblemEvolutionHarnessVersion is the only path that can widen a
// persistent harness from run scope to workspace scope. The gate is server
// owned: it requires held-out evidence, a bounded search/holdout gap, and at
// least one change that was confirmed by the next iteration.
func (h *Handler) PromoteProblemEvolutionHarnessVersion(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	if run.Mode != problemevolution.ModeTaskHarnessPersistent {
		writeError(w, http.StatusBadRequest, "run is not a persistent harness run")
		return
	}
	versionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "versionId"), "version_id")
	if !ok {
		return
	}
	version, err := h.Queries.GetProblemEvolutionHarnessVersion(r.Context(), db.GetProblemEvolutionHarnessVersionParams{
		ID: versionID, RunID: run.ID, WorkspaceID: run.WorkspaceID,
	})
	if err != nil || version.RolledBack {
		writeError(w, http.StatusConflict, "harness version is not promotable")
		return
	}
	iterations, err := h.Queries.ListProblemEvolutionIterations(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load iterations")
		return
	}
	if len(iterations) == 0 {
		writeError(w, http.StatusConflict, "held-out validation is required")
		return
	}
	latest := iterations[len(iterations)-1]
	baseline := iterations[0].HoldoutPassRate
	if latest.HoldoutPassRate < baseline || latest.PassRate-latest.HoldoutPassRate > 0.15 {
		writeError(w, http.StatusConflict, "held-out promotion gate failed")
		return
	}
	changes, err := h.Queries.ListProblemEvolutionChangeRecords(r.Context(), run.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load change records")
		return
	}
	confirmed := false
	for _, change := range changes {
		if change.Verdict == "confirmed" {
			confirmed = true
			break
		}
	}
	if !confirmed {
		writeError(w, http.StatusConflict, "no confirmed harness improvement")
		return
	}
	promoted, err := h.Queries.PromoteProblemEvolutionHarnessVersion(r.Context(), db.PromoteProblemEvolutionHarnessVersionParams{
		ID: version.ID, WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "harness version is not promotable")
		return
	}
	if _, err := h.Queries.UpsertProblemEvolutionHarnessRegistry(r.Context(), db.UpsertProblemEvolutionHarnessRegistryParams{
		WorkspaceID: run.WorkspaceID, HarnessVersionID: promoted.ID, ContentHash: promoted.ContentHash,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update workspace harness registry")
		return
	}
	writeJSON(w, http.StatusOK, problemEvolutionHarnessVersionToResponse(promoted))
}

func (h *Handler) projectProblemEvolutionPersistentEvent(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	event problemevolution.EvolverEvent,
) error {
	switch event.EventType {
	case problemevolution.EventIterationStarted:
		var p problemevolution.IterationStartedPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		inputID := pgtype.UUID{}
		if version, err := h.Queries.GetProblemEvolutionHarnessVersionByHash(ctx, db.GetProblemEvolutionHarnessVersionByHashParams{
			RunID: run.ID, ContentHash: p.InputVersionHash,
		}); err == nil {
			inputID = version.ID
		}
		_, err := h.Queries.UpsertProblemEvolutionIteration(ctx, db.UpsertProblemEvolutionIterationParams{
			RunID: run.ID, Iteration: int32(p.Iteration), InputVersionID: inputID,
			Stage: "evaluating",
		})
		return err
	case problemevolution.EventTaskResult:
		var p problemevolution.TaskResultPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		iteration, err := h.Queries.GetProblemEvolutionIteration(ctx, db.GetProblemEvolutionIterationParams{
			RunID: run.ID, Iteration: int32(p.Iteration),
		})
		if err != nil {
			return fmt.Errorf("%w: task_result references unknown iteration", problemevolution.ErrEventRejected)
		}
		if run.TaskSetID.Valid {
			taskSet, err := h.Queries.GetProblemEvolutionTaskSet(ctx, db.GetProblemEvolutionTaskSetParams{
				ID: run.TaskSetID, WorkspaceID: run.WorkspaceID,
			})
			if err != nil {
				return fmt.Errorf("%w: task_result task set is unavailable", problemevolution.ErrEventRejected)
			}
			var searchNames, holdoutNames []string
			_ = json.Unmarshal(taskSet.TaskNames, &searchNames)
			_ = json.Unmarshal(taskSet.HoldoutTaskNames, &holdoutNames)
			allowed := problemEvolutionContainsString(searchNames, p.TaskName)
			if p.Split == "holdout" {
				allowed = problemEvolutionContainsString(holdoutNames, p.TaskName)
				if p.Iteration+1 < problemEvolutionStopConfig(run).MaxIterations {
					return fmt.Errorf("%w: holdout results are only allowed in the final iteration", problemevolution.ErrEventRejected)
				}
			}
			if !allowed {
				return fmt.Errorf("%w: task_result task is not in its declared split", problemevolution.ErrEventRejected)
			}
		}
		_, err = h.Queries.UpsertProblemEvolutionTaskResult(ctx, db.UpsertProblemEvolutionTaskResultParams{
			RunID: run.ID, IterationID: iteration.ID, TaskName: problemevolution.TruncateFreeText(p.TaskName),
			RolloutIndex: int32(p.RolloutIndex), Split: p.Split, Reward: p.Reward, Verdict: p.Verdict,
			TraceRef:       problemevolution.TruncateFreeText(p.TraceRef),
			TraceDigestRef: problemevolution.TruncateFreeText(p.TraceDigestRef),
			Tokens:         p.Tokens, Cost: numericFromFloat(p.Cost),
		})
		return err
	case problemevolution.EventAnalysisReady:
		var p problemevolution.AnalysisReadyPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		iteration, err := h.Queries.GetProblemEvolutionIteration(ctx, db.GetProblemEvolutionIterationParams{
			RunID: run.ID, Iteration: int32(p.Iteration),
		})
		if err != nil {
			return fmt.Errorf("%w: analysis references unknown iteration", problemevolution.ErrEventRejected)
		}
		iteration.Stage = "analyzing"
		_, err = h.Queries.UpsertProblemEvolutionIteration(ctx, iterationUpsertParams(iteration))
		return err
	case problemevolution.EventChangeProposed:
		var p problemevolution.ChangeProposedPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		iteration, err := h.Queries.GetProblemEvolutionIteration(ctx, db.GetProblemEvolutionIterationParams{
			RunID: run.ID, Iteration: int32(p.Iteration),
		})
		if err != nil {
			return fmt.Errorf("%w: change references unknown iteration", problemevolution.ErrEventRejected)
		}
		predictedPass, _ := json.Marshal(p.PredictedPassTaskNames)
		predictedRisk, _ := json.Marshal(p.PredictedRiskTaskNames)
		_, err = h.Queries.InsertProblemEvolutionChangeRecord(ctx, db.InsertProblemEvolutionChangeRecordParams{
			RunID: run.ID, IterationID: iteration.ID, Component: problemevolution.TruncateFreeText(p.Component),
			FailureEvidenceRef:     problemevolution.TruncateFreeText(p.FailureEvidenceRef),
			RootCause:              problemevolution.TruncateFreeText(p.RootCause),
			FixSummary:             problemevolution.TruncateFreeText(p.FixSummary),
			PredictedPassTaskNames: predictedPass, PredictedRiskTaskNames: predictedRisk,
		})
		return err
	case problemevolution.EventHarnessVersionReady:
		var p problemevolution.HarnessVersionReadyPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		parentID := pgtype.UUID{}
		if p.ParentVersionHash != "" {
			parent, err := h.Queries.GetProblemEvolutionHarnessVersionByHash(ctx, db.GetProblemEvolutionHarnessVersionByHashParams{
				RunID: run.ID, ContentHash: p.ParentVersionHash,
			})
			if err != nil {
				return fmt.Errorf("%w: parent harness version not found", problemevolution.ErrEventRejected)
			}
			parentID = parent.ID
		}
		version, err := h.Queries.UpsertProblemEvolutionHarnessVersion(ctx, db.UpsertProblemEvolutionHarnessVersionParams{
			RunID: run.ID, WorkspaceID: run.WorkspaceID, Iteration: int32(p.Iteration),
			ParentVersionID: parentID, Components: p.Components, ContentHash: problemevolution.TruncateFreeText(p.ContentHash),
		})
		if err != nil {
			return err
		}
		iteration, err := h.Queries.GetProblemEvolutionIteration(ctx, db.GetProblemEvolutionIterationParams{
			RunID: run.ID, Iteration: int32(p.Iteration),
		})
		if err != nil {
			_, err = h.Queries.UpsertProblemEvolutionIteration(ctx, db.UpsertProblemEvolutionIterationParams{
				RunID: run.ID, Iteration: int32(p.Iteration), EvolveVersionID: version.ID, Stage: "improving",
			})
			return err
		}
		iteration.EvolveVersionID = version.ID
		iteration.Stage = "improving"
		_, err = h.Queries.UpsertProblemEvolutionIteration(ctx, iterationUpsertParams(iteration))
		return err
	case problemevolution.EventIterationFinished:
		var p problemevolution.IterationFinishedPayload
		if err := json.Unmarshal(event.Payload, &p); err != nil {
			return err
		}
		iteration, err := h.Queries.GetProblemEvolutionIteration(ctx, db.GetProblemEvolutionIterationParams{
			RunID: run.ID, Iteration: int32(p.Iteration),
		})
		if err != nil {
			return fmt.Errorf("%w: iteration_finished references unknown iteration", problemevolution.ErrEventRejected)
		}
		iteration.Stage, iteration.PassRate, iteration.HoldoutPassRate = "settled", p.PassRate, p.HoldoutPassRate
		iteration.Cost, iteration.Tokens = numericFromFloat(p.Cost), p.Tokens
		if _, err := h.Queries.UpsertProblemEvolutionIteration(ctx, iterationUpsertParams(iteration)); err != nil {
			return err
		}
		return h.settleProblemEvolutionChanges(ctx, run.ID, iteration.ID)
	default:
		return nil
	}
}

func iterationUpsertParams(row db.ProblemEvolutionIteration) db.UpsertProblemEvolutionIterationParams {
	return db.UpsertProblemEvolutionIterationParams{
		RunID: row.RunID, Iteration: row.Iteration, InputVersionID: row.InputVersionID,
		EvolveVersionID: row.EvolveVersionID, Stage: row.Stage, PassRate: row.PassRate,
		HoldoutPassRate: row.HoldoutPassRate, Cost: row.Cost, Tokens: row.Tokens,
	}
}

func (h *Handler) settleProblemEvolutionChanges(ctx context.Context, runID, iterationID pgtype.UUID) error {
	changes, err := h.Queries.ListProblemEvolutionChangeRecords(ctx, runID)
	if err != nil {
		return err
	}
	results, err := h.Queries.ListProblemEvolutionTaskResultsByIteration(ctx, iterationID)
	if err != nil {
		return err
	}
	statusByTask := make(map[string]string, len(results))
	for _, result := range results {
		statusByTask[result.TaskName] = result.Verdict
	}
	for _, change := range changes {
		if change.IterationID == iterationID {
			// A proposal is falsified by the next iteration, never by the
			// rollout that motivated it.
			continue
		}
		if change.Verdict != "pending" {
			continue
		}
		var predictedPass, predictedRisk []string
		_ = json.Unmarshal(change.PredictedPassTaskNames, &predictedPass)
		_ = json.Unmarshal(change.PredictedRiskTaskNames, &predictedRisk)
		observed := make([]map[string]string, 0)
		confirmed := false
		for _, task := range predictedPass {
			if verdict := statusByTask[task]; verdict != "" {
				observed = append(observed, map[string]string{"task": task, "predicted": "pass", "observed": verdict})
				if verdict == "pass" {
					confirmed = true
				}
			}
		}
		for _, task := range predictedRisk {
			if verdict := statusByTask[task]; verdict != "" {
				observed = append(observed, map[string]string{"task": task, "predicted": "risk", "observed": verdict})
			}
		}
		if len(observed) == 0 {
			continue
		}
		raw, _ := json.Marshal(observed)
		verdict, action := "refuted", "reverted"
		if confirmed {
			verdict, action = "confirmed", "kept"
		}
		if _, err := h.Queries.SetProblemEvolutionChangeVerdict(ctx, db.SetProblemEvolutionChangeVerdictParams{
			ID: change.ID, RunID: runID, ObservedFlips: raw, Verdict: verdict, Action: action,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) enforceProblemEvolutionPersistentStopConditions(ctx context.Context, run db.ProblemEvolutionRun) error {
	current, err := h.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID: run.ID, WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return err
	}
	if current.Status != "running" && current.Status != "synthesizing" {
		return nil
	}
	iterations, err := h.Queries.ListProblemEvolutionIterations(ctx, run.ID)
	if err != nil || len(iterations) == 0 {
		return err
	}
	latest := iterations[len(iterations)-1]
	config := problemEvolutionStopConfig(current)
	stop, reason := problemevolution.ShouldStop(config, problemevolution.RunProgress{
		Iterations:        int(latest.Iteration) + 1,
		PassRate:          latest.PassRate,
		ModelCalls:        int(current.ModelCallCount),
		CostUSD:           problemEvolutionCost(current.TotalCost),
		RoundsWithoutGain: int(current.RoundsWithoutGain),
	})
	if !stop {
		return nil
	}
	stopped, err := h.Queries.RequestStopProblemEvolutionRun(ctx, db.RequestStopProblemEvolutionRunParams{
		ID: current.ID, WorkspaceID: current.WorkspaceID, StopReason: reason,
	})
	if err != nil {
		return err
	}
	h.publishProblemEvolutionRunChanged(uuidToString(stopped.WorkspaceID), stopped)
	return nil
}

func numericFromFloat(value float64) pgtype.Numeric {
	if value <= 0 {
		return pgtype.Numeric{Int: big.NewInt(0), Valid: true}
	}
	return pgtype.Numeric{Int: big.NewInt(int64(math.Round(value * 1_000_000))), Exp: -6, Valid: true}
}

func problemEvolutionContainsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func problemEvolutionHarnessVersionToResponse(row db.ProblemEvolutionHarnessVersion) ProblemEvolutionHarnessVersionResponse {
	return ProblemEvolutionHarnessVersionResponse{
		ID: uuidToString(row.ID), RunID: uuidToString(row.RunID), Iteration: int(row.Iteration),
		ParentVersionID: uuidToPtr(row.ParentVersionID), Components: row.Components,
		ContentHash: row.ContentHash, RolledBack: row.RolledBack, PromotedScope: row.PromotedScope,
		CreatedAt: timestampToString(row.CreatedAt),
	}
}

func problemEvolutionIterationToResponse(row db.ProblemEvolutionIteration) ProblemEvolutionIterationResponse {
	return ProblemEvolutionIterationResponse{
		ID: uuidToString(row.ID), RunID: uuidToString(row.RunID), Iteration: int(row.Iteration),
		InputVersionID: uuidToPtr(row.InputVersionID), EvolveVersionID: uuidToPtr(row.EvolveVersionID),
		Stage: row.Stage, PassRate: row.PassRate, HoldoutPassRate: row.HoldoutPassRate,
		Cost: problemEvolutionCost(row.Cost), Tokens: row.Tokens,
	}
}

func problemEvolutionTaskResultToResponse(row db.ProblemEvolutionTaskResult) ProblemEvolutionTaskResultResponse {
	return ProblemEvolutionTaskResultResponse{
		TaskName: row.TaskName, RolloutIndex: int(row.RolloutIndex), Split: row.Split,
		Reward: row.Reward, Verdict: row.Verdict, TraceRef: row.TraceRef,
		TraceDigestRef: row.TraceDigestRef, Tokens: row.Tokens, Cost: problemEvolutionCost(row.Cost),
	}
}

func problemEvolutionChangeRecordToResponse(row db.ProblemEvolutionChangeRecord) ProblemEvolutionChangeRecordResponse {
	return ProblemEvolutionChangeRecordResponse{
		ID: uuidToString(row.ID), IterationID: uuidToString(row.IterationID),
		HarnessVersionID: uuidToPtr(row.HarnessVersionID), Component: row.Component,
		FailureEvidenceRef: row.FailureEvidenceRef, RootCause: row.RootCause, FixSummary: row.FixSummary,
		PredictedPassTaskNames: row.PredictedPassTaskNames, PredictedRiskTaskNames: row.PredictedRiskTaskNames,
		ObservedFlips: row.ObservedFlips, Verdict: row.Verdict, Action: row.Action,
	}
}

func validateProblemEvolutionTaskSet(req createProblemEvolutionTaskSetRequest) error {
	if strings.TrimSpace(req.DatasetRef) == "" {
		return fmt.Errorf("dataset_ref is required")
	}
	if len(req.TaskNames) == 0 {
		return fmt.Errorf("task_names must not be empty")
	}
	if req.RolloutsPerTask <= 0 || req.MaxParallel <= 0 {
		return fmt.Errorf("rollouts_per_task and max_parallel must be positive")
	}
	seen := make(map[string]string, len(req.TaskNames)+len(req.HoldoutTaskNames))
	validateNames := func(names []string, split string) error {
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("task names must not be empty")
			}
			if previous, exists := seen[name]; exists {
				if previous == split {
					return fmt.Errorf("task %q appears more than once", name)
				}
				return fmt.Errorf("task %q appears in both %s and %s", name, previous, split)
			}
			seen[name] = split
		}
		return nil
	}
	if err := validateNames(req.TaskNames, "search"); err != nil {
		return err
	}
	if err := validateNames(req.HoldoutTaskNames, "holdout"); err != nil {
		return err
	}
	return nil
}

func problemEvolutionTaskSetToResponse(row db.ProblemEvolutionTaskSet) ProblemEvolutionTaskSetResponse {
	return ProblemEvolutionTaskSetResponse{
		ID: uuidToString(row.ID), WorkspaceID: uuidToString(row.WorkspaceID),
		Source: row.Source, DatasetRef: row.DatasetRef, DatasetRevision: row.DatasetRevision,
		TaskNames: row.TaskNames, HoldoutTaskNames: row.HoldoutTaskNames,
		RolloutsPerTask: int(row.RolloutsPerTask), MaxParallel: int(row.MaxParallel),
		CreatedAt: timestampToString(row.CreatedAt), UpdatedAt: timestampToString(row.UpdatedAt),
	}
}
