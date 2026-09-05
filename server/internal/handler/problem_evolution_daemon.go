package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/problemevolution"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ProblemEvolutionClaimResponse is the payload a daemon receives when it wins a
// queued run. It carries the frozen evaluator's non-secret projection only:
// hidden answers never travel on this path.
type ProblemEvolutionClaimResponse struct {
	Run        ProblemEvolutionRunResponse     `json:"run"`
	ClaimToken string                          `json:"claim_token"`
	Input      problemevolution.EvolverInput   `json:"input"`
	Policy     problemevolution.FeedbackPolicy `json:"feedback_policy"`
}

type problemEvolutionClaimRequest struct {
	RuntimeID string `json:"runtime_id"`
}

type problemEvolutionEventsRequest struct {
	ClaimToken string                          `json:"claim_token"`
	Events     []problemevolution.EvolverEvent `json:"events"`
}

// ProblemEvolutionEventsResponse tells the daemon what the server did with the
// batch. Rejected events are counted, not echoed, so a misbehaving evolver
// cannot use the response as a channel.
type ProblemEvolutionEventsResponse struct {
	Accepted      int   `json:"accepted"`
	Duplicates    int   `json:"duplicates"`
	Rejected      int   `json:"rejected"`
	LatestSeq     int64 `json:"latest_seq"`
	GraphVersion  int64 `json:"graph_version"`
	StopRequested bool  `json:"stop_requested"`
}

type problemEvolutionClaimTokenRequest struct {
	ClaimToken string `json:"claim_token"`
}

type problemEvolutionCompleteRequest struct {
	ClaimToken       string `json:"claim_token"`
	BestCandidateRef string `json:"best_candidate_ref"`
	EvolverVersion   string `json:"evolver_version"`
}

type problemEvolutionFailRequest struct {
	ClaimToken    string `json:"claim_token"`
	FailureReason string `json:"failure_reason"`
}

// ClaimProblemEvolutionRun hands one queued run to the calling runtime.
func (h *Handler) ClaimProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	var req problemEvolutionClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, req.RuntimeID)
	if !ok {
		return
	}
	run, err := h.Queries.ClaimProblemEvolutionRun(r.Context(), db.ClaimProblemEvolutionRunParams{
		ClaimedRuntimeID: runtime.ID,
		WorkspaceID:      runtime.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, map[string]any{"run": nil})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to claim run")
		return
	}
	input, policy, err := h.buildProblemEvolutionInput(r.Context(), run)
	if err != nil {
		// The run cannot be executed with the contract it pinned; fail it
		// rather than leaving a claimed run with no valid input.
		failed, failErr := h.Queries.FailProblemEvolutionRun(r.Context(), db.FailProblemEvolutionRunParams{
			ID:            run.ID,
			WorkspaceID:   run.WorkspaceID,
			ClaimToken:    run.ClaimToken,
			FailureReason: "evolver_input_rejected",
		})
		if failErr == nil {
			h.publishProblemEvolutionRunChanged(uuidToString(failed.WorkspaceID), failed)
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(run.WorkspaceID), run)
	writeJSON(w, http.StatusOK, ProblemEvolutionClaimResponse{
		Run:        problemEvolutionRunToResponse(run),
		ClaimToken: uuidToString(run.ClaimToken),
		Input:      input,
		Policy:     policy,
	})
}

// ReportProblemEvolutionEvents ingests a batch of evolver events. Sequence
// numbers are allocated here, inside the insert, so parallel candidates cannot
// mint conflicting sequences on the daemon side.
func (h *Handler) ReportProblemEvolutionEvents(w http.ResponseWriter, r *http.Request) {
	run, body, ok := h.loadClaimedProblemEvolutionRun(w, r, func(decoder *json.Decoder) (string, []problemevolution.EvolverEvent, bool) {
		var req problemEvolutionEventsRequest
		if err := decoder.Decode(&req); err != nil {
			return "", nil, false
		}
		return req.ClaimToken, req.Events, true
	})
	if !ok {
		return
	}
	events := body.events
	resp := ProblemEvolutionEventsResponse{}
	scoredInBatch := false
	harnessProposedInBatch := false
	progressInBatch := false
	iterationFinishedInBatch := false
	for _, event := range events {
		if err := event.Validate(); err != nil {
			resp.Rejected++
			continue
		}
		if err := event.ValidatePayload(); err != nil {
			resp.Rejected++
			continue
		}
		outcome, err := h.persistProblemEvolutionEvent(r.Context(), run, event)
		if err != nil {
			// A policy rejection is about this one event; the rest of the batch
			// is still valid and must not be lost to a 500.
			if errors.Is(err, problemevolution.ErrEventRejected) {
				resp.Rejected++
				continue
			}
			writeError(w, http.StatusInternalServerError, "failed to persist event")
			return
		}
		switch outcome.disposition {
		case problemEvolutionEventInserted:
			resp.Accepted++
			resp.LatestSeq = outcome.seq
			switch event.EventType {
			case problemevolution.EventCandidateScored:
				scoredInBatch = true
			case problemevolution.EventHarnessProposed:
				harnessProposedInBatch = true
			case problemevolution.EventProgress:
				progressInBatch = true
			case problemevolution.EventIterationFinished:
				iterationFinishedInBatch = true
			}
		case problemEvolutionEventDuplicate:
			resp.Duplicates++
			if outcome.seq > resp.LatestSeq {
				resp.LatestSeq = outcome.seq
			}
		}
	}
	// Selection and stop enforcement run after the whole batch, not per event:
	// electing an elite from a half-ingested generation would churn statuses
	// the UI has already rendered.
	if harnessProposedInBatch && resp.Accepted > 0 {
		if err := h.applyProblemEvolutionShortlist(r.Context(), run); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to shortlist harnesses")
			return
		}
	}
	if (scoredInBatch || progressInBatch) && resp.Accepted > 0 {
		if run.Mode == problemevolution.ModeTaskHarnessRewardOnly {
			if err := h.selectProblemEvolutionHarnessWinner(r.Context(), run); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to select winner harness")
				return
			}
		}
		stopConfig := problemEvolutionStopConfig(run)
		if err := h.applyProblemEvolutionSelection(r.Context(), run, stopConfig.EliteCount); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to apply selection")
			return
		}
		if err := h.enforceProblemEvolutionStopConditions(r.Context(), run, stopConfig); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to evaluate stop conditions")
			return
		}
	}
	if run.Mode == problemevolution.ModeTaskHarnessPersistent && iterationFinishedInBatch && resp.Accepted > 0 {
		if err := h.enforceProblemEvolutionPersistentStopConditions(r.Context(), run); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to evaluate persistent stop conditions")
			return
		}
	}
	graphVersion := run.GraphVersion
	if resp.Accepted > 0 {
		bumped, err := h.Queries.BumpProblemEvolutionRunGraphVersion(r.Context(), run.ID)
		if err == nil {
			graphVersion = bumped
		}
	}
	resp.GraphVersion = graphVersion
	// Re-read the run so a stop requested between claim and this report is
	// delivered on the response the daemon is already waiting for.
	current, err := h.Queries.GetProblemEvolutionRun(r.Context(), db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err == nil {
		resp.StopRequested = current.Status == "stopping"
		if resp.Accepted > 0 {
			h.publishProblemEvolutionGraphUpdated(uuidToString(current.WorkspaceID), current, graphVersion)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// HeartbeatProblemEvolutionRun keeps the claim alive and relays stop intent.
func (h *Handler) HeartbeatProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, token, ok := h.loadClaimedProblemEvolutionRunToken(w, r)
	if !ok {
		return
	}
	updated, err := h.Queries.HeartbeatProblemEvolutionRun(r.Context(), db.HeartbeatProblemEvolutionRunParams{
		ID:         run.ID,
		ClaimToken: token,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stop_requested": updated.Status == "stopping",
		"graph_version":  updated.GraphVersion,
		"status":         updated.Status,
	})
}

// CompleteProblemEvolutionRun records the terminal result of a run.
func (h *Handler) CompleteProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadDaemonProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	var req problemEvolutionCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, ok := parseUUIDOrBadRequest(w, req.ClaimToken, "claim_token")
	if !ok {
		return
	}
	if version := strings.TrimSpace(req.EvolverVersion); version != "" {
		if _, err := h.Queries.SetProblemEvolutionRunEvolverVersion(r.Context(), db.SetProblemEvolutionRunEvolverVersionParams{
			ID:             run.ID,
			ClaimToken:     token,
			EvolverVersion: problemevolution.TruncateFreeText(version),
		}); err != nil {
			writeError(w, http.StatusConflict, "claim is no longer valid")
			return
		}
	}
	best := pgtype.UUID{}
	if ref := strings.TrimSpace(req.BestCandidateRef); ref != "" {
		candidate, err := h.Queries.GetProblemEvolutionCandidateByRef(r.Context(), db.GetProblemEvolutionCandidateByRefParams{
			RunID:       run.ID,
			ExternalRef: ref,
		})
		if err == nil {
			best = candidate.ID
		}
	}
	completed, err := h.Queries.CompleteProblemEvolutionRun(r.Context(), db.CompleteProblemEvolutionRunParams{
		ID:               run.ID,
		ClaimToken:       token,
		BestCandidateID:  best,
		FinalCandidateID: best,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(completed.WorkspaceID), completed)
	h.publishProblemEvolutionRunCompleted(uuidToString(completed.WorkspaceID), completed)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(completed))
}

// FailProblemEvolutionRun records an unrecoverable failure reported by the
// daemon. Cancellation after a stop request lands here too, so that the
// stopping → cancelled transition stays server-owned.
func (h *Handler) FailProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadDaemonProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	var req problemEvolutionFailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, ok := parseUUIDOrBadRequest(w, req.ClaimToken, "claim_token")
	if !ok {
		return
	}
	if run.Status == "stopping" {
		cancelled, err := h.Queries.CancelProblemEvolutionRun(r.Context(), db.CancelProblemEvolutionRunParams{
			ID:          run.ID,
			WorkspaceID: run.WorkspaceID,
			ClaimToken:  token,
		})
		if err != nil {
			writeError(w, http.StatusConflict, "claim is no longer valid")
			return
		}
		h.publishProblemEvolutionRunChanged(uuidToString(cancelled.WorkspaceID), cancelled)
		writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(cancelled))
		return
	}
	failed, err := h.Queries.FailProblemEvolutionRun(r.Context(), db.FailProblemEvolutionRunParams{
		ID:            run.ID,
		WorkspaceID:   run.WorkspaceID,
		ClaimToken:    token,
		FailureReason: problemevolution.TruncateFreeText(req.FailureReason),
	})
	if err != nil {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(failed.WorkspaceID), failed)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(failed))
}

// ReleaseProblemEvolutionRun returns a claimed run to the queue, for a daemon
// shutting down cleanly.
func (h *Handler) ReleaseProblemEvolutionRun(w http.ResponseWriter, r *http.Request) {
	run, token, ok := h.loadClaimedProblemEvolutionRunToken(w, r)
	if !ok {
		return
	}
	released, err := h.Queries.ReleaseProblemEvolutionRun(r.Context(), db.ReleaseProblemEvolutionRunParams{
		ID:         run.ID,
		ClaimToken: token,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(released.WorkspaceID), released)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(released))
}

type problemEvolutionEventDisposition int

const (
	problemEvolutionEventInserted problemEvolutionEventDisposition = iota
	problemEvolutionEventDuplicate
)

type problemEvolutionEventOutcome struct {
	disposition problemEvolutionEventDisposition
	seq         int64
}

// persistProblemEvolutionEvent stores one event and applies its projection to
// the candidate row atomically. Retried delivery of the same client_event_id
// is a no-op, including when two copies arrive concurrently.
func (h *Handler) persistProblemEvolutionEvent(ctx context.Context, run db.ProblemEvolutionRun, event problemevolution.EvolverEvent) (problemEvolutionEventOutcome, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return problemEvolutionEventOutcome{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	txHandler := *h
	txHandler.Queries = h.Queries.WithTx(tx)
	txHandler.DB = tx
	outcome, err := txHandler.persistProblemEvolutionEventTx(ctx, run, event)
	if err != nil {
		return problemEvolutionEventOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return problemEvolutionEventOutcome{}, err
	}
	return outcome, nil
}

func (h *Handler) persistProblemEvolutionEventTx(ctx context.Context, run db.ProblemEvolutionRun, event problemevolution.EvolverEvent) (problemEvolutionEventOutcome, error) {
	if _, err := h.Queries.LockClaimedProblemEvolutionRun(ctx, db.LockClaimedProblemEvolutionRunParams{
		ID: run.ID, ClaimToken: run.ClaimToken,
	}); err != nil {
		return problemEvolutionEventOutcome{}, err
	}
	// Projectors run before insert so a projection failure cannot leave an
	// event without its derived state. The row lock makes the pre-check safe
	// against concurrent delivery of the same idempotency key.
	if existing, err := h.Queries.GetProblemEvolutionEventByClientID(ctx, db.GetProblemEvolutionEventByClientIDParams{
		RunID: run.ID, ClientEventID: event.ClientEventID,
	}); err == nil {
		return problemEvolutionEventOutcome{disposition: problemEvolutionEventDuplicate, seq: existing.Seq}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return problemEvolutionEventOutcome{}, err
	}
	candidateID := pgtype.UUID{}
	if event.CandidateRef != "" {
		candidate, err := h.projectProblemEvolutionCandidate(ctx, run, event)
		if err != nil {
			return problemEvolutionEventOutcome{}, err
		}
		candidateID = candidate.ID
	}
	payload := event.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if run.Mode == problemevolution.ModeTaskHarnessPersistent {
		if err := h.projectProblemEvolutionPersistentEvent(ctx, run, event); err != nil {
			return problemEvolutionEventOutcome{}, err
		}
	}
	row, err := h.Queries.InsertProblemEvolutionEvent(ctx, db.InsertProblemEvolutionEventParams{
		RunID:         run.ID,
		WorkspaceID:   run.WorkspaceID,
		ClientEventID: event.ClientEventID,
		EventType:     event.EventType,
		CandidateID:   candidateID,
		ActorType:     "evolver",
		ActorID:       uuidToString(run.ClaimedRuntimeID),
		Payload:       payload,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return problemEvolutionEventOutcome{}, err
		}
		existing, lookupErr := h.Queries.GetProblemEvolutionEventByClientID(ctx, db.GetProblemEvolutionEventByClientIDParams{
			RunID:         run.ID,
			ClientEventID: event.ClientEventID,
		})
		if lookupErr != nil {
			return problemEvolutionEventOutcome{}, lookupErr
		}
		return problemEvolutionEventOutcome{disposition: problemEvolutionEventDuplicate, seq: existing.Seq}, nil
	}
	if event.EventType == problemevolution.EventProgress {
		var progress problemevolution.ProgressPayload
		if err := json.Unmarshal(event.Payload, &progress); err != nil {
			return problemEvolutionEventOutcome{}, err
		}
		if progress.ModelCalls > 0 {
			if _, err := h.Queries.SetProblemEvolutionRunModelCalls(ctx, db.SetProblemEvolutionRunModelCallsParams{
				ID:             run.ID,
				ModelCallCount: int32(progress.ModelCalls),
			}); err != nil {
				return problemEvolutionEventOutcome{}, err
			}
		}
		if progress.ModelCalls > 0 || progress.Tokens > 0 || progress.Cost > 0 ||
			progress.InputTokens > 0 || progress.OutputTokens > 0 {
			if _, err := h.Queries.UpsertProblemEvolutionUsage(ctx, db.UpsertProblemEvolutionUsageParams{
				RunID: run.ID, WorkspaceID: run.WorkspaceID, SourceEventID: row.ID,
				Provider:   problemevolution.TruncateFreeText(progress.Provider),
				Model:      problemevolution.TruncateFreeText(progress.Model),
				ModelCalls: int32(progress.ModelCalls), InputTokens: progress.InputTokens,
				OutputTokens: progress.OutputTokens, Cost: numericFromFloat(progress.Cost),
			}); err != nil {
				return problemEvolutionEventOutcome{}, err
			}
		}
	}
	return problemEvolutionEventOutcome{disposition: problemEvolutionEventInserted, seq: row.Seq}, nil
}

// projectProblemEvolutionCandidate folds one event into the candidate row.
func (h *Handler) projectProblemEvolutionCandidate(ctx context.Context, run db.ProblemEvolutionRun, event problemevolution.EvolverEvent) (db.ProblemEvolutionCandidate, error) {
	switch event.EventType {
	case problemevolution.EventCandidateStarted:
		var payload problemevolution.CandidateStartedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		lane := payload.Lane
		if payload.Relation != "" {
			lane = problemevolution.LaneForRelation(payload.Relation)
		}
		if lane == "" {
			lane = problemevolution.LaneBaseline
		}
		operator := payload.Operator
		if operator == "" {
			operator = problemevolution.LaneBaseline
		}
		candidate, err := h.Queries.UpsertProblemEvolutionCandidate(ctx, db.UpsertProblemEvolutionCandidateParams{
			RunID:       run.ID,
			WorkspaceID: run.WorkspaceID,
			ExternalRef: event.CandidateRef,
			Generation:  int32(payload.Generation),
			Lane:        lane,
			Operator:    operator,
			Status:      "producing",
		})
		if err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if err := h.enforceProblemEvolutionRepairBudget(ctx, run, payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if err := h.recordProblemEvolutionLineage(ctx, run, candidate, payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if _, err := h.Queries.RecomputeProblemEvolutionRunProgress(ctx, run.ID); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		return candidate, nil
	case problemevolution.EventCandidateArtifact:
		var payload problemevolution.CandidateArtifactPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		// Path containment is re-checked here and not only in the daemon: the
		// server must never persist a ref that could later be read as an
		// absolute or escaping path.
		if _, err := problemevolution.ArtifactRelativePath(problemevolution.DefaultArtifactDir, payload.RelativePath); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if _, err := h.ensureProblemEvolutionCandidate(ctx, run, event.CandidateRef); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		candidate, err := h.Queries.SetProblemEvolutionCandidateArtifact(ctx, db.SetProblemEvolutionCandidateArtifactParams{
			RunID:        run.ID,
			ExternalRef:  event.CandidateRef,
			ArtifactRef:  payload.RelativePath,
			ArtifactHash: payload.ContentHash,
			Summary:      problemevolution.TruncateFreeText(payload.Summary),
		})
		if err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		kind := payload.Kind
		if kind == "" {
			kind = "answer"
		}
		if _, err := h.Queries.UpsertProblemEvolutionArtifact(ctx, db.UpsertProblemEvolutionArtifactParams{
			RunID:       run.ID,
			CandidateID: candidate.ID,
			WorkspaceID: run.WorkspaceID,
			Kind:        kind,
			StorageRef:  payload.RelativePath,
			ContentHash: payload.ContentHash,
			ContentType: problemEvolutionContentType(kind),
			SizeBytes:   payload.SizeBytes,
		}); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		return candidate, nil
	case problemevolution.EventCandidateScored:
		var payload problemevolution.CandidateScoredPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if _, err := h.ensureProblemEvolutionCandidate(ctx, run, event.CandidateRef); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		scoreJSON, err := json.Marshal(payload.Score)
		if err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		behaviorJSON, err := json.Marshal(payload.BehaviorProfile)
		if err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		status := "selectable"
		verdict := "scored"
		if !payload.Score.HardGatePassed {
			verdict = "hard_gate_failed"
		}
		candidate, err := h.Queries.SetProblemEvolutionCandidateScore(ctx, db.SetProblemEvolutionCandidateScoreParams{
			RunID:           run.ID,
			ExternalRef:     event.CandidateRef,
			Score:           scoreJSON,
			BehaviorProfile: behaviorJSON,
			Status:          status,
		})
		if err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if err := h.recordProblemEvolutionEvaluation(ctx, run, candidate, payload, verdict); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if err := h.recordProblemEvolutionHarnessReward(ctx, run, event.CandidateRef, payload.Score.Total); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if _, err := h.Queries.RecomputeProblemEvolutionRunProgress(ctx, run.ID); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		return candidate, nil
	case problemevolution.EventHarnessProposed:
		var payload problemevolution.HarnessProposedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		candidate, err := h.Queries.UpsertProblemEvolutionCandidate(ctx, db.UpsertProblemEvolutionCandidateParams{
			RunID:       run.ID,
			WorkspaceID: run.WorkspaceID,
			ExternalRef: event.CandidateRef,
			Generation:  0,
			Lane:        "harness",
			Operator:    "harness_proposal",
			Status:      "validating",
		})
		if err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if err := h.projectProblemEvolutionHarness(ctx, run, candidate, payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		return candidate, nil
	case problemevolution.EventCandidateFailed:
		var payload problemevolution.CandidateFailedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if _, err := h.ensureProblemEvolutionCandidate(ctx, run, event.CandidateRef); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		status := "failed"
		if payload.FailureClass == "timeout" {
			status = "timeout"
		}
		return h.Queries.SetProblemEvolutionCandidateStatus(ctx, db.SetProblemEvolutionCandidateStatusParams{
			RunID:        run.ID,
			ExternalRef:  event.CandidateRef,
			Status:       status,
			FailureClass: payload.FailureClass,
		})
	case problemevolution.EventCandidateFinished:
		var payload problemevolution.CandidateFinishedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		if _, err := h.ensureProblemEvolutionCandidate(ctx, run, event.CandidateRef); err != nil {
			return db.ProblemEvolutionCandidate{}, err
		}
		return h.Queries.SetProblemEvolutionCandidateStatus(ctx, db.SetProblemEvolutionCandidateStatusParams{
			RunID:        run.ID,
			ExternalRef:  event.CandidateRef,
			Status:       payload.Status,
			FailureClass: "",
		})
	default:
		return h.ensureProblemEvolutionCandidate(ctx, run, event.CandidateRef)
	}
}

func problemEvolutionContentType(kind string) string {
	switch kind {
	case "harness", "patch":
		return "text/plain"
	case "log":
		return "text/plain"
	case "report":
		return "application/json"
	default:
		return "text/markdown"
	}
}

// recordProblemEvolutionLineage writes the parent edges for a candidate.
// Parents are created on demand: events may arrive out of order, and a missing
// parent row would otherwise drop the edge permanently.
func (h *Handler) recordProblemEvolutionLineage(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	child db.ProblemEvolutionCandidate,
	payload problemevolution.CandidateStartedPayload,
) error {
	if payload.Relation == "" || len(payload.ParentIDs) == 0 {
		return nil
	}
	for index, parentRef := range payload.ParentIDs {
		if parentRef == child.ExternalRef {
			continue
		}
		parent, err := h.ensureProblemEvolutionCandidate(ctx, run, parentRef)
		if err != nil {
			return err
		}
		if _, err := h.Queries.UpsertProblemEvolutionCandidateEdge(ctx, db.UpsertProblemEvolutionCandidateEdgeParams{
			RunID:       run.ID,
			ParentID:    parent.ID,
			ChildID:     child.ID,
			Relation:    payload.Relation,
			ParentIndex: int32(index),
		}); err != nil {
			return err
		}
	}
	return nil
}

// enforceProblemEvolutionRepairBudget refuses a repair whose parent has already
// spent its reward-only feedback rounds, and charges the round otherwise.
//
// The bound is enforced here rather than trusted to the evolver because
// repeatedly repairing against the same reward is how a run stops solving the
// problem and starts guessing the verifier.
func (h *Handler) enforceProblemEvolutionRepairBudget(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	payload problemevolution.CandidateStartedPayload,
) error {
	if payload.Relation != problemevolution.RelationRepairOf {
		return nil
	}
	policy := h.problemEvolutionFeedbackPolicy(ctx, run)
	for _, parentRef := range payload.ParentIDs {
		parent, err := h.Queries.GetProblemEvolutionCandidateByRef(ctx, db.GetProblemEvolutionCandidateByRefParams{
			RunID:       run.ID,
			ExternalRef: parentRef,
		})
		if err != nil {
			// An unknown parent is created as a placeholder by the lineage
			// writer and has spent no rounds yet.
			continue
		}
		if !problemevolution.RepairAllowed(int(parent.FeedbackRounds), policy) {
			return problemevolution.ErrRepairBudgetExhausted
		}
	}
	for _, parentRef := range payload.ParentIDs {
		if _, err := h.Queries.BumpProblemEvolutionCandidateFeedbackRounds(ctx,
			db.BumpProblemEvolutionCandidateFeedbackRoundsParams{
				RunID:       run.ID,
				ExternalRef: parentRef,
			}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}

// recordProblemEvolutionEvaluation appends an immutable evaluation row. Attempts
// are numbered per candidate and phase so a repair round is visible as a second
// evaluation rather than overwriting the first.
func (h *Handler) recordProblemEvolutionEvaluation(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	candidate db.ProblemEvolutionCandidate,
	payload problemevolution.CandidateScoredPayload,
	verdict string,
) error {
	const phase = "search"
	existing, err := h.Queries.CountProblemEvolutionEvaluationAttempts(ctx, db.CountProblemEvolutionEvaluationAttemptsParams{
		CandidateID: candidate.ID,
		Phase:       phase,
	})
	if err != nil {
		return err
	}
	scoreJSON, err := json.Marshal(payload.Score)
	if err != nil {
		return err
	}
	behaviorJSON, err := json.Marshal(payload.BehaviorProfile)
	if err != nil {
		return err
	}
	projection, err := json.Marshal(problemevolution.ProjectFeedback(payload.Score))
	if err != nil {
		return err
	}
	_, err = h.Queries.UpsertProblemEvolutionEvaluation(ctx, db.UpsertProblemEvolutionEvaluationParams{
		RunID:                run.ID,
		CandidateID:          candidate.ID,
		WorkspaceID:          run.WorkspaceID,
		EvaluatorContractID:  run.EvaluatorContractID,
		EvaluatorContentHash: run.EvaluatorContentHash,
		Attempt:              int32(existing + 1),
		Phase:                phase,
		Verdict:              verdict,
		Score:                scoreJSON,
		BehaviorProfile:      behaviorJSON,
		FeedbackProjection:   projection,
		RuntimeSeconds:       candidate.RuntimeSeconds,
	})
	return err
}

// problemEvolutionStopConfig reads the run's stop policy, falling back to the
// defaults for any bound the caller left unset.
func problemEvolutionStopConfig(run db.ProblemEvolutionRun) problemevolution.StopConfig {
	config := problemevolution.StopConfig{}
	if len(run.StopConfig) > 0 {
		if err := json.Unmarshal(run.StopConfig, &config); err != nil {
			config = problemevolution.StopConfig{}
		}
	}
	if run.Mode == problemevolution.ModeTaskHarnessPersistent {
		return config.WithPersistentDefaults()
	}
	return config.WithDefaults()
}

// enforceProblemEvolutionStopConditions moves a run to `stopping` once a budget
// or target bound is reached. Budget exhaustion takes the same path as a user
// stop so in-flight candidates are drained the same way in both cases.
func (h *Handler) enforceProblemEvolutionStopConditions(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	config problemevolution.StopConfig,
) error {
	current, err := h.Queries.GetProblemEvolutionRun(ctx, db.GetProblemEvolutionRunParams{
		ID:          run.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return err
	}
	if current.Status != "running" && current.Status != "synthesizing" {
		return nil
	}
	gained := current.BestScore > run.BestScore
	tracked, err := h.Queries.BumpProblemEvolutionRunRoundsWithoutGain(ctx, db.BumpProblemEvolutionRunRoundsWithoutGainParams{
		ID:     current.ID,
		Gained: gained,
	})
	if err != nil {
		return err
	}
	stop, reason := problemevolution.ShouldStop(config, problemevolution.RunProgress{
		Generation:        int(tracked.Generation),
		CandidateCount:    int(tracked.CandidateCount),
		ModelCalls:        int(tracked.ModelCallCount),
		CostUSD:           problemEvolutionCost(tracked.TotalCost),
		BestScore:         tracked.BestScore,
		RoundsWithoutGain: int(tracked.RoundsWithoutGain),
	})
	if !stop {
		return nil
	}
	stopped, err := h.Queries.RequestStopProblemEvolutionRun(ctx, db.RequestStopProblemEvolutionRunParams{
		ID:          tracked.ID,
		WorkspaceID: tracked.WorkspaceID,
		StopReason:  reason,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	h.publishProblemEvolutionRunChanged(uuidToString(stopped.WorkspaceID), stopped)
	return nil
}

// applyProblemEvolutionSelection recomputes elite/pruned status for the run's
// current generation and pins the best candidate. Selection is server-owned:
// the evolver reports scores, the platform decides what survives.
func (h *Handler) applyProblemEvolutionSelection(ctx context.Context, run db.ProblemEvolutionRun, eliteCount int) error {
	candidates, err := h.Queries.ListProblemEvolutionCandidates(ctx, run.ID)
	if err != nil {
		return err
	}
	inputs := make([]problemevolution.SelectionInput, 0, len(candidates))
	byRef := make(map[string]db.ProblemEvolutionCandidate, len(candidates))
	for _, candidate := range candidates {
		byRef[candidate.ExternalRef] = candidate
		input := problemevolution.SelectionInput{
			CandidateRef:   candidate.ExternalRef,
			Status:         candidate.Status,
			Cost:           problemEvolutionCost(candidate.Cost),
			RuntimeSeconds: candidate.RuntimeSeconds,
		}
		if len(candidate.Score) > 0 {
			var score problemevolution.Score
			if err := json.Unmarshal(candidate.Score, &score); err == nil {
				input.Score = &score
			}
		}
		if len(candidate.BehaviorProfile) > 0 {
			var profile problemevolution.BehaviorProfile
			if err := json.Unmarshal(candidate.BehaviorProfile, &profile); err == nil {
				input.BehaviorProfile = &profile
			}
		}
		inputs = append(inputs, input)
	}
	result := problemevolution.SelectElite(inputs, eliteCount)
	for _, ref := range result.EliteRefs {
		if _, err := h.Queries.SetProblemEvolutionCandidateStatus(ctx, db.SetProblemEvolutionCandidateStatusParams{
			RunID:        run.ID,
			ExternalRef:  ref,
			Status:       "elite",
			FailureClass: "",
		}); err != nil {
			return err
		}
	}
	for _, ref := range result.PrunedRefs {
		candidate, ok := byRef[ref]
		// Only prune candidates that were still in play; a failed or timed-out
		// candidate keeps its own more specific status.
		if !ok || candidate.Status != "selectable" && candidate.Status != "elite" {
			continue
		}
		if _, err := h.Queries.SetProblemEvolutionCandidateStatus(ctx, db.SetProblemEvolutionCandidateStatusParams{
			RunID:        run.ID,
			ExternalRef:  ref,
			Status:       "pruned",
			FailureClass: "",
		}); err != nil {
			return err
		}
	}
	if result.BestRef == "" {
		return nil
	}
	best, ok := byRef[result.BestRef]
	if !ok {
		return nil
	}
	_, err = h.Queries.SetProblemEvolutionRunBestCandidate(ctx, db.SetProblemEvolutionRunBestCandidateParams{
		ID:              run.ID,
		BestCandidateID: best.ID,
	})
	return err
}

func (h *Handler) ensureProblemEvolutionCandidate(ctx context.Context, run db.ProblemEvolutionRun, ref string) (db.ProblemEvolutionCandidate, error) {
	return h.Queries.EnsureProblemEvolutionCandidate(ctx, db.EnsureProblemEvolutionCandidateParams{
		RunID:       run.ID,
		WorkspaceID: run.WorkspaceID,
		ExternalRef: ref,
	})
}

// buildProblemEvolutionInput projects the run and its frozen contract into the
// input.json the external evolver receives.
func (h *Handler) buildProblemEvolutionInput(ctx context.Context, run db.ProblemEvolutionRun) (problemevolution.EvolverInput, problemevolution.FeedbackPolicy, error) {
	if !run.EvaluatorContractID.Valid {
		return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("run has no evaluator contract")
	}
	contractRow, err := h.Queries.GetProblemEvolutionEvaluatorContract(ctx, db.GetProblemEvolutionEvaluatorContractParams{
		ID:          run.EvaluatorContractID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("evaluator contract not found")
	}
	if contractRow.Status != "frozen" {
		return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("evaluator contract is not frozen")
	}
	// The hash pinned at start must still match: an edited contract is a drift
	// failure, not a silent re-score.
	if run.EvaluatorContentHash != "" && contractRow.ContentHash != run.EvaluatorContentHash {
		return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("evaluator_contract_drift")
	}
	var contract problemevolution.EvaluatorContract
	if err := json.Unmarshal(contractRow.Contract, &contract); err != nil {
		return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
	}
	policy := problemevolution.DefaultFeedbackPolicy()
	if len(contractRow.FeedbackPolicy) > 0 {
		if err := json.Unmarshal(contractRow.FeedbackPolicy, &policy); err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
		}
	}
	var problem problemevolution.ProblemSpec
	if len(run.ProblemSpec) > 0 {
		if err := json.Unmarshal(run.ProblemSpec, &problem); err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
		}
	}
	if problem.ArtifactType == "" {
		problem.ArtifactType = run.ArtifactType
	}
	var taskSet *problemevolution.TaskSetInput
	if run.Mode == problemevolution.ModeTaskHarnessPersistent {
		if !run.TaskSetID.Valid {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("persistent harness run has no task set")
		}
		row, err := h.Queries.GetProblemEvolutionTaskSet(ctx, db.GetProblemEvolutionTaskSetParams{
			ID: run.TaskSetID, WorkspaceID: run.WorkspaceID,
		})
		if err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("task set not found")
		}
		var taskNames, holdoutNames []string
		if err := json.Unmarshal(row.TaskNames, &taskNames); err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("task set task_names is invalid")
		}
		if err := json.Unmarshal(row.HoldoutTaskNames, &holdoutNames); err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, errors.New("task set holdout_task_names is invalid")
		}
		taskSet = &problemevolution.TaskSetInput{
			Source: row.Source, DatasetRef: row.DatasetRef, DatasetRevision: row.DatasetRevision,
			TaskNames: taskNames, HoldoutTaskNames: holdoutNames,
			RolloutsPerTask: int(row.RolloutsPerTask), MaxParallel: int(row.MaxParallel),
		}
	}
	budget := problemevolution.DefaultBudget()
	if len(run.BudgetConfig) > 0 {
		if err := json.Unmarshal(run.BudgetConfig, &budget); err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
		}
	}
	if budget.Candidates <= 0 {
		budget = problemevolution.DefaultBudget()
	}
	// The run-level stop policy is the single user-facing source of truth for
	// model-call and monetary ceilings. Mirror the call ceiling into input.json
	// so the external evolver can stop before the platform has to intervene.
	budget.MaxModelCalls = problemEvolutionStopConfig(run).MaxModelCalls
	var model problemevolution.ModelConfig
	if len(run.ModelConfig) > 0 {
		if err := json.Unmarshal(run.ModelConfig, &model); err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
		}
	}
	iterationNumber := 0
	var persistentHarness *problemevolution.PersistentHarnessInput
	if run.Mode == problemevolution.ModeTaskHarnessPersistent {
		iterations, err := h.Queries.ListProblemEvolutionIterations(ctx, run.ID)
		if err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
		}
		if len(iterations) > 0 {
			iterationNumber = int(iterations[len(iterations)-1].Iteration) + 1
		}
		versions, err := h.Queries.ListProblemEvolutionHarnessVersions(ctx, run.ID)
		if err != nil {
			return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
		}
		if len(versions) > 0 {
			persistentHarness = &problemevolution.PersistentHarnessInput{
				Iteration: iterationNumber, InputVersionRef: versions[len(versions)-1].ContentHash,
				ContentHash: versions[len(versions)-1].ContentHash,
			}
		}
	}
	input := problemevolution.EvolverInput{
		SchemaVersion: problemevolution.SchemaVersion,
		RunID:         uuidToString(run.ID),
		Mode:          run.Mode,
		Generation:    0,
		Iteration:     iterationNumber,
		Problem:       problem,
		TaskSet:       taskSet,
		Harness:       persistentHarness,
		Evaluator: problemevolution.EvaluatorRef{
			ContractID:    uuidToString(contractRow.ID),
			ContentHash:   contractRow.ContentHash,
			Kind:          contract.Kind,
			Dimensions:    contract.Dimensions,
			PassThreshold: contract.PassThreshold,
			Invoke:        contract.Invoke,
		},
		Budget:   budget,
		Model:    model,
		Feedback: h.buildProblemEvolutionFeedback(ctx, run, policy),
		Seeds:    problemevolution.SeedPair{Search: run.SearchSeed},
		Output: problemevolution.OutputConfig{
			ArtifactDir:       problemevolution.DefaultArtifactDir,
			CandidateManifest: problemevolution.DefaultCandidateManifest,
		},
	}
	if err := input.Validate(); err != nil {
		return problemevolution.EvolverInput{}, problemevolution.FeedbackPolicy{}, err
	}
	return input, policy, nil
}

// buildProblemEvolutionFeedback assembles the reward-only bundle the next
// generation receives. Only elite parents are summarised, and every number
// passes through the policy projection.
func (h *Handler) buildProblemEvolutionFeedback(
	ctx context.Context,
	run db.ProblemEvolutionRun,
	policy problemevolution.FeedbackPolicy,
) problemevolution.FeedbackBundle {
	elite, err := h.Queries.ListProblemEvolutionEliteCandidates(ctx, db.ListProblemEvolutionEliteCandidatesParams{
		RunID:       run.ID,
		ResultLimit: 8,
	})
	if err != nil || len(elite) == 0 {
		return problemevolution.FeedbackBundle{Policy: policy}
	}
	parents := make([]problemevolution.ParentEvaluation, 0, len(elite))
	for _, candidate := range elite {
		var score problemevolution.Score
		if len(candidate.Score) == 0 {
			continue
		}
		if err := json.Unmarshal(candidate.Score, &score); err != nil {
			continue
		}
		parents = append(parents, problemevolution.ParentEvaluation{
			CandidateRef:  candidate.ExternalRef,
			Score:         score,
			FailureClass:  candidate.FailureClass,
			ChangeSummary: candidate.ChangeSummary,
			RoundsUsed:    int(candidate.FeedbackRounds),
		})
	}
	return problemevolution.BuildFeedbackBundle(parents, policy)
}

// ReportProblemEvolutionBlindValidation records the single-shot final verdict.
// It is separate from search scoring because a blind result computed with the
// search seed would prove nothing about generalisation.
func (h *Handler) ReportProblemEvolutionBlindValidation(w http.ResponseWriter, r *http.Request) {
	run, ok := h.loadDaemonProblemEvolutionRun(w, r)
	if !ok {
		return
	}
	var req struct {
		ClaimToken string                                  `json:"claim_token"`
		Outcome    problemevolution.BlindValidationOutcome `json:"outcome"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, ok := parseUUIDOrBadRequest(w, req.ClaimToken, "claim_token")
	if !ok {
		return
	}
	if !run.ClaimToken.Valid || run.ClaimToken != token {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return
	}
	seeds := problemevolution.SeedPair{Search: run.SearchSeed, Blind: run.BlindSeed}
	if err := problemevolution.ValidateBlindOutcome(req.Outcome, seeds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	candidate, err := h.Queries.GetProblemEvolutionCandidateByRef(r.Context(), db.GetProblemEvolutionCandidateByRefParams{
		RunID:       run.ID,
		ExternalRef: req.Outcome.CandidateRef,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "candidate not found")
		return
	}
	updated, err := h.Queries.SetProblemEvolutionRunBlindResult(r.Context(), db.SetProblemEvolutionRunBlindResultParams{
		ID:               run.ID,
		ClaimToken:       token,
		BlindCandidateID: candidate.ID,
		BlindScore:       pgtype.Float8{Float64: req.Outcome.Score.Total, Valid: true},
		OverfitGap: pgtype.Float8{
			Float64: problemevolution.OverfitGap(run.BestScore, req.Outcome.Score.Total),
			Valid:   true,
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record blind validation")
		return
	}
	h.publishProblemEvolutionRunChanged(uuidToString(updated.WorkspaceID), updated)
	writeJSON(w, http.StatusOK, problemEvolutionRunToResponse(updated))
}

func (h *Handler) loadDaemonProblemEvolutionRun(w http.ResponseWriter, r *http.Request) (db.ProblemEvolutionRun, bool) {
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runId"), "run_id")
	if !ok {
		return db.ProblemEvolutionRun{}, false
	}
	run, err := h.Queries.GetProblemEvolutionRunByID(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return db.ProblemEvolutionRun{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(run.WorkspaceID)) {
		return db.ProblemEvolutionRun{}, false
	}
	return run, true
}

func (h *Handler) loadClaimedProblemEvolutionRunToken(w http.ResponseWriter, r *http.Request) (db.ProblemEvolutionRun, pgtype.UUID, bool) {
	run, ok := h.loadDaemonProblemEvolutionRun(w, r)
	if !ok {
		return db.ProblemEvolutionRun{}, pgtype.UUID{}, false
	}
	var req problemEvolutionClaimTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return db.ProblemEvolutionRun{}, pgtype.UUID{}, false
	}
	token, ok := parseUUIDOrBadRequest(w, req.ClaimToken, "claim_token")
	if !ok {
		return db.ProblemEvolutionRun{}, pgtype.UUID{}, false
	}
	if !run.ClaimToken.Valid || uuidToString(run.ClaimToken) != uuidToString(token) {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return db.ProblemEvolutionRun{}, pgtype.UUID{}, false
	}
	return run, token, true
}

type problemEvolutionEventBody struct {
	events []problemevolution.EvolverEvent
}

func (h *Handler) loadClaimedProblemEvolutionRun(
	w http.ResponseWriter,
	r *http.Request,
	decode func(*json.Decoder) (string, []problemevolution.EvolverEvent, bool),
) (db.ProblemEvolutionRun, problemEvolutionEventBody, bool) {
	run, ok := h.loadDaemonProblemEvolutionRun(w, r)
	if !ok {
		return db.ProblemEvolutionRun{}, problemEvolutionEventBody{}, false
	}
	rawToken, events, ok := decode(json.NewDecoder(r.Body))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return db.ProblemEvolutionRun{}, problemEvolutionEventBody{}, false
	}
	token, ok := parseUUIDOrBadRequest(w, rawToken, "claim_token")
	if !ok {
		return db.ProblemEvolutionRun{}, problemEvolutionEventBody{}, false
	}
	if !run.ClaimToken.Valid || uuidToString(run.ClaimToken) != uuidToString(token) {
		writeError(w, http.StatusConflict, "claim is no longer valid")
		return db.ProblemEvolutionRun{}, problemEvolutionEventBody{}, false
	}
	return run, problemEvolutionEventBody{events: events}, true
}

func (h *Handler) publishProblemEvolutionGraphUpdated(workspaceID string, run db.ProblemEvolutionRun, graphVersion int64) {
	h.publish("problem_evolution_run:graph_updated", workspaceID, "system", "", map[string]any{
		"workspace_id":  workspaceID,
		"run_id":        uuidToString(run.ID),
		"graph_version": graphVersion,
	})
}
