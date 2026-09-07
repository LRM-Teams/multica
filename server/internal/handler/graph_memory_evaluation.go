// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

// Graph Memory evaluation protocol API (test-only, Handoff 7 §6).
//
// Every endpoint is owner/admin-only AND fail-closed behind the plane's
// process gate plus workspace allowlist: a disabled plane answers 503 even
// for owners. The harness drives run/episode lifecycle, appends usage
// evidence, and moves the official-score state machine; the arm itself is
// enforced by the recall/gateway/capture/injection seams, never here.

func graphMemoryEvaluationDisabled(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "graph memory evaluation protocol is disabled")
}

func writeGraphMemoryEvaluationError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, service.ErrGraphMemoryEvaluationDisabled):
		graphMemoryEvaluationDisabled(w)
	case errors.Is(err, service.ErrGraphMemoryEvaluationNotFound):
		writeError(w, http.StatusNotFound, "graph memory evaluation object not found")
	case errors.Is(err, service.ErrGraphMemoryEvaluationPolicyLock):
		writeError(w, http.StatusConflict, "channel already has a live evaluation episode")
	case errors.Is(err, service.ErrGraphMemoryEvaluationClosure):
		writeError(w, http.StatusConflict, "episode closure conditions not met")
	case errors.Is(err, service.ErrGraphMemoryEvaluationEvidence):
		writeError(w, http.StatusConflict, "official scoring evidence incomplete")
	case errors.Is(err, service.ErrGraphMemoryEvaluationState):
		writeError(w, http.StatusBadRequest, "invalid graph memory evaluation request")
	default:
		writeError(w, http.StatusInternalServerError, "graph memory evaluation request failed")
	}
	return true
}

// graphMemoryEvaluationPersistenceOff reports whether the channel currently
// runs a live persistence_off evaluation episode (test-only plane). The
// capture and legacy-injection seams call this with whatever executor they
// already hold (pool or open tx); false when the plane is absent or on any
// error — enforcement only ever narrows behavior while an episode is live.
func graphMemoryEvaluationPersistenceOff(ctx context.Context, exec dbExecutor, workspaceID, channelID pgtype.UUID) bool {
	if exec == nil || !workspaceID.Valid || !channelID.Valid {
		return false
	}
	var arm string
	if err := exec.QueryRow(ctx, `
		SELECT arm FROM graph_memory_evaluation_episode
		WHERE workspace_id=$1 AND channel_id=$2 AND status IN ('pending','running')
		LIMIT 1`, workspaceID, channelID).Scan(&arm); err != nil {
		return false
	}
	return arm == service.GraphMemoryEvaluationArmPersistenceOff
}

// CreateGraphMemoryEvaluationRun serves POST /workspaces/{id}/graph-memory-evaluation/runs.
func (h *Handler) CreateGraphMemoryEvaluationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run the graph memory evaluation protocol")
		return
	}
	if h.GraphMemoryEvaluation == nil {
		graphMemoryEvaluationDisabled(w)
		return
	}
	var req struct {
		RunID          string `json:"run_id"`
		Label          string `json:"label"`
		CreatedByActor string `json:"created_by_actor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := h.GraphMemoryEvaluation.CreateRun(r.Context(), service.GraphMemoryEvaluationRunInput{
		WorkspaceID: workspaceID, RunID: req.RunID, Label: req.Label, CreatedByActor: req.CreatedByActor,
	})
	if writeGraphMemoryEvaluationError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"run_id": req.RunID, "status": "running"})
}

// ListGraphMemoryEvaluationRuns serves GET /workspaces/{id}/graph-memory-evaluation/runs.
func (h *Handler) ListGraphMemoryEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can read the graph memory evaluation protocol")
		return
	}
	if h.GraphMemoryEvaluation == nil {
		graphMemoryEvaluationDisabled(w)
		return
	}
	runs, err := h.GraphMemoryEvaluation.ListRuns(r.Context(), workspaceID, 50)
	if writeGraphMemoryEvaluationError(w, err) {
		return
	}
	if runs == nil {
		runs = []service.GraphMemoryEvaluationRunView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// GetGraphMemoryEvaluationRun serves GET /workspaces/{id}/graph-memory-evaluation/runs/{runId}.
func (h *Handler) GetGraphMemoryEvaluationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can read the graph memory evaluation protocol")
		return
	}
	if h.GraphMemoryEvaluation == nil {
		graphMemoryEvaluationDisabled(w)
		return
	}
	run, episodes, err := h.GraphMemoryEvaluation.GetRun(r.Context(), workspaceID, chi.URLParam(r, "runId"))
	if writeGraphMemoryEvaluationError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "episodes": episodes})
}

// CompleteGraphMemoryEvaluationRun serves POST .../runs/{runId}/complete.
func (h *Handler) CompleteGraphMemoryEvaluationRun(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run the graph memory evaluation protocol")
		return
	}
	if h.GraphMemoryEvaluation == nil {
		graphMemoryEvaluationDisabled(w)
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.GraphMemoryEvaluation.CompleteRun(r.Context(), workspaceID, chi.URLParam(r, "runId"), req.Status); writeGraphMemoryEvaluationError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// CreateGraphMemoryEvaluationEpisode serves POST .../runs/{runId}/episodes.
func (h *Handler) CreateGraphMemoryEvaluationEpisode(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run the graph memory evaluation protocol")
		return
	}
	if h.GraphMemoryEvaluation == nil {
		graphMemoryEvaluationDisabled(w)
		return
	}
	var req struct {
		EpisodeID         string `json:"episode_id"`
		ChannelID         string `json:"channel_id"`
		PrimaryAgentID    string `json:"primary_agent_id"`
		Arm               string `json:"arm"`
		SessionGeneration string `json:"session_generation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	err := h.GraphMemoryEvaluation.CreateEpisode(r.Context(), service.GraphMemoryEvaluationEpisodeInput{
		WorkspaceID: workspaceID, RunID: chi.URLParam(r, "runId"),
		EpisodeID: req.EpisodeID, ChannelID: req.ChannelID, PrimaryAgentID: req.PrimaryAgentID,
		Arm: req.Arm, SessionGeneration: req.SessionGeneration,
	})
	if writeGraphMemoryEvaluationError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"episode_id": req.EpisodeID, "status": "pending"})
}

func (h *Handler) serveGraphMemoryEvaluationEpisodeAction(w http.ResponseWriter, r *http.Request, action string) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can run the graph memory evaluation protocol")
		return
	}
	if h.GraphMemoryEvaluation == nil {
		graphMemoryEvaluationDisabled(w)
		return
	}
	runID, episodeID := chi.URLParam(r, "runId"), chi.URLParam(r, "episodeId")
	var err error
	switch action {
	case "start":
		var req struct {
			InputMessageID string `json:"input_message_id"`
		}
		if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.GraphMemoryEvaluation.StartEpisode(r.Context(), workspaceID, runID, episodeID, req.InputMessageID)
	case "settle":
		var req struct {
			OutputMessageID  string                                     `json:"output_message_id"`
			ClosureChecklist map[string]service.GraphMemoryClosureState `json:"closure_checklist"`
		}
		if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.GraphMemoryEvaluation.SettleEpisode(r.Context(), workspaceID, runID, episodeID, req.OutputMessageID, req.ClosureChecklist)
	case "fail":
		var req struct {
			Reason string `json:"reason"`
		}
		if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.GraphMemoryEvaluation.FailEpisode(r.Context(), workspaceID, runID, episodeID, req.Reason)
	case "usage":
		var req struct {
			Kind    string         `json:"kind"`
			Payload map[string]any `json:"payload"`
		}
		if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.GraphMemoryEvaluation.RecordUsage(r.Context(), service.GraphMemoryUsageEventInput{
			WorkspaceID: workspaceID, RunID: runID, EpisodeID: episodeID, Kind: req.Kind, Payload: req.Payload,
		})
	case "official-score":
		var req struct {
			State        string         `json:"state"`
			Score        map[string]any `json:"score"`
			EvidenceHash string         `json:"evidence_hash"`
		}
		if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		err = h.GraphMemoryEvaluation.MarkOfficialScore(r.Context(), workspaceID, runID, episodeID, req.State, req.Score, req.EvidenceHash)
	default:
		writeError(w, http.StatusNotFound, "unknown episode action")
		return
	}
	if writeGraphMemoryEvaluationError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"episode_id": episodeID, "action": action})
}

// StartGraphMemoryEvaluationEpisode serves POST .../episodes/{episodeId}/start.
func (h *Handler) StartGraphMemoryEvaluationEpisode(w http.ResponseWriter, r *http.Request) {
	h.serveGraphMemoryEvaluationEpisodeAction(w, r, "start")
}

// SettleGraphMemoryEvaluationEpisode serves POST .../episodes/{episodeId}/settle.
func (h *Handler) SettleGraphMemoryEvaluationEpisode(w http.ResponseWriter, r *http.Request) {
	h.serveGraphMemoryEvaluationEpisodeAction(w, r, "settle")
}

// FailGraphMemoryEvaluationEpisode serves POST .../episodes/{episodeId}/fail.
func (h *Handler) FailGraphMemoryEvaluationEpisode(w http.ResponseWriter, r *http.Request) {
	h.serveGraphMemoryEvaluationEpisodeAction(w, r, "fail")
}

// RecordGraphMemoryEvaluationUsage serves POST .../episodes/{episodeId}/usage.
func (h *Handler) RecordGraphMemoryEvaluationUsage(w http.ResponseWriter, r *http.Request) {
	h.serveGraphMemoryEvaluationEpisodeAction(w, r, "usage")
}

// MarkGraphMemoryEvaluationOfficialScore serves POST .../episodes/{episodeId}/official-score.
func (h *Handler) MarkGraphMemoryEvaluationOfficialScore(w http.ResponseWriter, r *http.Request) {
	h.serveGraphMemoryEvaluationEpisodeAction(w, r, "official-score")
}
