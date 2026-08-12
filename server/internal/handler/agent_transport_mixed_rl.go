package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AgentTransportUploadTurnCapture accepts the daemon-owned Pi capture over the
// durable agent credential transport. The request identity never controls its
// scope: path run, credential agent/workspace, runtime, Pi session, and the
// server-issued capture boundary must all match one stored run-agent.
func (h *Handler) AgentTransportUploadTurnCapture(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runID"), "runID")
	if !ok {
		return
	}
	var upload protocol.TurnCaptureUpload
	if err := json.NewDecoder(r.Body).Decode(&upload); err != nil {
		writeError(w, http.StatusBadRequest, "decode turn capture: "+err.Error())
		return
	}
	payloadAgentID, err := util.ParseUUID(upload.AgentID)
	if err != nil || payloadAgentID != source.origin.agentID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	payloadRuntimeID, err := util.ParseUUID(upload.RuntimeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}
	capture, err := turnCaptureFromProtocol(runID, upload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := h.Queries.GetMixedRLRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "mixed run not found")
		return
	}
	agent, err := h.Queries.GetMixedRLRunAgent(r.Context(), db.GetMixedRLRunAgentParams{RunID: runID, RunAgentID: capture.RunAgentID})
	if err != nil || !trustedTurnCaptureScopeMatches(source, run, agent, payloadAgentID, payloadRuntimeID, upload.Turn.PiSessionID, upload.Turn.CaptureBoundary) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	result, err := service.NewProviderCaptureService(h.Queries, h.TxStarter).AcceptTrustedTurnCapture(r.Context(), capture)
	if err != nil {
		writeError(w, http.StatusConflict, "accept turn capture: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, protocol.TurnCaptureUploadResponse{Accepted: true, TurnID: result.Turn.TurnID.String()})
}

// AgentTransportReportTurnCaptureGap records a settled turn that lacks a
// provider capture. WebSocket activity transitions remain the sole owner of
// the corresponding counter lifecycle.
func (h *Handler) AgentTransportReportTurnCaptureGap(w http.ResponseWriter, r *http.Request) {
	source, ok := h.requireAgentTransportSource(w, r)
	if !ok {
		return
	}
	runID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "runID"), "runID")
	if !ok {
		return
	}
	var gap protocol.TurnCaptureGapReport
	if err := json.NewDecoder(r.Body).Decode(&gap); err != nil {
		writeError(w, http.StatusBadRequest, "decode turn capture gap: "+err.Error())
		return
	}
	payloadAgentID, err := util.ParseUUID(gap.AgentID)
	if err != nil || payloadAgentID != source.origin.agentID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	payloadRuntimeID, err := util.ParseUUID(gap.RuntimeID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}
	runAgentID, err := util.ParseUUID(gap.RunAgentID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run_agent_id")
		return
	}
	turnID, err := util.ParseUUID(gap.TurnID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid turn_id")
		return
	}
	if gap.OccurredAt == "" {
		writeError(w, http.StatusBadRequest, "missing occurred_at")
		return
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, gap.OccurredAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid occurred_at")
		return
	}
	run, err := h.Queries.GetMixedRLRun(r.Context(), runID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	agent, err := h.Queries.GetMixedRLRunAgent(r.Context(), db.GetMixedRLRunAgentParams{RunID: runID, RunAgentID: runAgentID})
	if err != nil || !trustedTurnCaptureScopeMatches(source, run, agent, payloadAgentID, payloadRuntimeID, "", gap.CaptureBoundary) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
		return
	}
	if _, err := service.NewProviderCaptureService(h.Queries, h.TxStarter).AcceptTrustedTurnCaptureGap(r.Context(), service.TrustedTurnCaptureGap{
		RunID: runID, RunAgentID: runAgentID, TurnID: turnID, TurnOrdinal: gap.TurnOrdinal, Reason: gap.Reason, Summary: []byte(`{"source":"daemon"}`), OccurredAt: occurredAt,
	}); err != nil {
		writeError(w, http.StatusConflict, "record turn capture gap: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, protocol.TurnCaptureGapResponse{Accepted: true})
}

func trustedTurnCaptureScopeMatches(source agentTransportSource, run db.EnvDispatchRun, runAgent db.EnvDispatchRunAgent, payloadAgentID, payloadRuntimeID pgtype.UUID, piSessionID, captureBoundary string) bool {
	return run.WorkspaceID == source.origin.workspaceID &&
		payloadAgentID == source.origin.agentID &&
		runAgent.ExecutionAgentID == source.origin.agentID &&
		runAgent.RuntimeID == payloadRuntimeID &&
		(piSessionID == "" || runAgent.PiSessionID == piSessionID) &&
		runAgent.CaptureBoundary == captureBoundary
}

// turnCaptureFromProtocol preserves only the captured provider-call fields the
// current trusted wire payload carries. Visible-action and consumption records
// remain absent because this DTO has no canonical channel evidence for them.
func turnCaptureFromProtocol(runID pgtype.UUID, upload protocol.TurnCaptureUpload) (service.TrustedTurnCapture, error) {
	runAgentID, err := util.ParseUUID(upload.Turn.RunAgentID)
	if err != nil {
		return service.TrustedTurnCapture{}, fmt.Errorf("invalid run_agent_id")
	}
	turnID, err := util.ParseUUID(upload.Turn.TurnID)
	if err != nil {
		return service.TrustedTurnCapture{}, fmt.Errorf("invalid turn_id")
	}
	batchID, err := util.ParseUUID(upload.CaptureBatchID)
	if err != nil {
		return service.TrustedTurnCapture{}, fmt.Errorf("invalid capture_batch_id")
	}
	completedAt := time.Time{}
	if upload.Turn.CompletedAt != "" {
		completedAt, err = time.Parse(time.RFC3339Nano, upload.Turn.CompletedAt)
		if err != nil {
			return service.TrustedTurnCapture{}, fmt.Errorf("invalid completed_at")
		}
	}
	calls := make([]service.ProviderCallInput, 0, len(upload.ProviderCalls))
	for _, call := range upload.ProviderCalls {
		startedAt, completedCallAt, err := captureCallTimes(call.StartedAt, call.CompletedAt)
		if err != nil {
			return service.TrustedTurnCapture{}, err
		}
		eligible := call.Status == "completed" && call.ResponseComplete && (call.StopReason == "stop" || call.StopReason == "toolUse")
		calls = append(calls, service.ProviderCallInput{CallID: call.CallID, RunID: runID, RunAgentID: runAgentID, TurnID: turnID, PiSessionID: upload.Turn.PiSessionID, CallOrdinal: call.CallOrdinal, Provider: call.Provider, Model: call.Model, APIKind: call.APIKind, RawProviderRequest: call.RawProviderRequest, FinalAssistantMessage: call.FinalAssistantMessage, Status: call.Status, StopReason: call.StopReason, ResponseComplete: call.ResponseComplete, TrainingEligible: eligible, AReALSessionID: call.AReaLSessionID, AReALCallID: call.AReaLCallID, RequestHash: call.RequestHash, ResponseHash: call.ResponseHash, StartedAt: startedAt, CompletedAt: completedCallAt})
	}
	if upload.Turn.TurnOrdinal <= 0 {
		return service.TrustedTurnCapture{}, fmt.Errorf("positive turn_ordinal is required")
	}
	return service.TrustedTurnCapture{RunID: runID, RunAgentID: runAgentID, TurnID: turnID, TurnOrdinal: upload.Turn.TurnOrdinal, Batch: service.TurnCaptureBatchInput{CaptureBatchID: batchID, TurnID: turnID, CaptureBoundary: upload.Turn.CaptureBoundary, CallCount: int32(len(calls)), PayloadHash: upload.PayloadHash}, Calls: calls, CompletedAt: completedAt}, nil
}

func captureCallTimes(started, completed string) (time.Time, time.Time, error) {
	var err error
	var startedAt, completedAt time.Time
	if started != "" {
		startedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid provider call started_at")
		}
	}
	if completed != "" {
		completedAt, err = time.Parse(time.RFC3339Nano, completed)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid provider call completed_at")
		}
	}
	return startedAt, completedAt, nil
}
