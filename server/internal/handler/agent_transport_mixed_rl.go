package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
// Agent-declared producer/call provenance fields are ignored; only the
// authenticated credential scope and daemon-observed batch contents are trusted.
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
	writeJSON(w, http.StatusOK, turnCaptureResponseFromResult(result, capture))
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

// turnCaptureFromProtocol maps the daemon wire payload into the trusted service
// input. It never trusts agent-declared ownership or provenance beyond the
// authenticated credential scope already validated by the handler.
func turnCaptureFromProtocol(runID pgtype.UUID, upload protocol.TurnCaptureUpload) (service.TrustedTurnCapture, error) {
	if upload.Turn.TurnOrdinal <= 0 {
		return service.TrustedTurnCapture{}, fmt.Errorf("positive turn_ordinal is required")
	}
	if strings.TrimSpace(upload.PayloadHash) == "" {
		return service.TrustedTurnCapture{}, fmt.Errorf("payload_hash is required")
	}
	runAgentID, err := parseCanonicalCaptureUUID(upload.Turn.RunAgentID)
	if err != nil {
		return service.TrustedTurnCapture{}, fmt.Errorf("invalid run_agent_id")
	}
	turnID, err := parseCanonicalCaptureUUID(upload.Turn.TurnID)
	if err != nil {
		return service.TrustedTurnCapture{}, fmt.Errorf("invalid turn_id")
	}
	batchID, err := parseCanonicalCaptureUUID(upload.CaptureBatchID)
	if err != nil {
		return service.TrustedTurnCapture{}, fmt.Errorf("invalid capture_batch_id")
	}
	turnStartedAt, err := parseCaptureTimestamp(upload.Turn.StartedAt, "turn started_at")
	if err != nil {
		return service.TrustedTurnCapture{}, err
	}
	completedAt, err := parseCaptureTimestamp(upload.Turn.CompletedAt, "turn completed_at")
	if err != nil {
		return service.TrustedTurnCapture{}, err
	}
	if !turnStartedAt.IsZero() && !completedAt.IsZero() && completedAt.Before(turnStartedAt) {
		return service.TrustedTurnCapture{}, fmt.Errorf("turn completed_at is before started_at")
	}

	callOrdinals := make([]int64, len(upload.ProviderCalls))
	for i := range upload.ProviderCalls {
		callOrdinals[i] = upload.ProviderCalls[i].CallOrdinal
	}
	if err := validateStrictPositiveOrdinals(callOrdinals, "provider call ordinal"); err != nil {
		return service.TrustedTurnCapture{}, err
	}
	calls := make([]service.ProviderCallInput, 0, len(upload.ProviderCalls))
	callIDs := make(map[string]struct{}, len(upload.ProviderCalls))
	for _, call := range upload.ProviderCalls {
		callID := strings.TrimSpace(call.CallID)
		if callID == "" {
			return service.TrustedTurnCapture{}, fmt.Errorf("provider call call_id is required")
		}
		if _, duplicate := callIDs[callID]; duplicate {
			return service.TrustedTurnCapture{}, fmt.Errorf("duplicate provider call call_id")
		}
		callIDs[callID] = struct{}{}
		if strings.TrimSpace(call.RequestHash) == "" {
			return service.TrustedTurnCapture{}, fmt.Errorf("provider call request_hash is required")
		}
		if strings.TrimSpace(call.ResponseHash) == "" {
			return service.TrustedTurnCapture{}, fmt.Errorf("provider call response_hash is required")
		}
		if field, forbidden, err := captureJSONForbiddenField(call.RawProviderRequest); err != nil {
			return service.TrustedTurnCapture{}, err
		} else if forbidden {
			return service.TrustedTurnCapture{}, fmt.Errorf("provider request contains forbidden transport authentication field %q", field)
		}
		startedAt, completedCallAt, err := captureCallTimes(call.StartedAt, call.CompletedAt)
		if err != nil {
			return service.TrustedTurnCapture{}, err
		}
		if !startedAt.IsZero() && !completedCallAt.IsZero() && completedCallAt.Before(startedAt) {
			return service.TrustedTurnCapture{}, fmt.Errorf("provider call completed_at is before started_at")
		}
		eligible := call.Status == "completed" && call.ResponseComplete && (call.StopReason == "stop" || call.StopReason == "toolUse")
		calls = append(calls, service.ProviderCallInput{
			CallID: callID, RunID: runID, RunAgentID: runAgentID, TurnID: turnID,
			PiSessionID: upload.Turn.PiSessionID, CallOrdinal: call.CallOrdinal,
			Provider: call.Provider, Model: call.Model, APIKind: call.APIKind,
			RawProviderRequest: call.RawProviderRequest, FinalAssistantMessage: call.FinalAssistantMessage,
			Status: call.Status, StopReason: call.StopReason, ResponseComplete: call.ResponseComplete,
			TrainingEligible: eligible, AReALSessionID: call.AReaLSessionID, AReALCallID: call.AReaLCallID,
			RequestHash: call.RequestHash, ResponseHash: call.ResponseHash,
			StartedAt: startedAt, CompletedAt: completedCallAt,
		})
	}

	actionOrdinals := make([]int64, len(upload.VisibleActions))
	for i := range upload.VisibleActions {
		actionOrdinals[i] = upload.VisibleActions[i].ActionOrdinal
	}
	if err := validateStrictPositiveOrdinals(actionOrdinals, "visible action ordinal"); err != nil {
		return service.TrustedTurnCapture{}, err
	}
	actions := make([]service.VisibleActionInput, 0, len(upload.VisibleActions))
	for _, action := range upload.VisibleActions {
		if action.Kind != "message" && action.Kind != "reaction" {
			return service.TrustedTurnCapture{}, fmt.Errorf("unsupported visible action kind %q", action.Kind)
		}
		canonicalID, err := parseCanonicalCaptureUUID(action.CanonicalID)
		if err != nil {
			return service.TrustedTurnCapture{}, fmt.Errorf("invalid visible action canonical_id")
		}
		producerCallID := strings.TrimSpace(action.ProducerCallID)
		if _, found := callIDs[producerCallID]; !found {
			return service.TrustedTurnCapture{}, fmt.Errorf("visible action producer_call_id must reference a provider call in the same batch")
		}
		succeededAt, err := parseCaptureTimestamp(action.SucceededAt, "visible action succeeded_at")
		if err != nil {
			return service.TrustedTurnCapture{}, err
		}
		actions = append(actions, service.VisibleActionInput{
			ActionID: deterministicCaptureRecordID(batchID, "action", action.ActionOrdinal),
			RunID:    runID, RunAgentID: runAgentID, TurnID: turnID,
			Kind: action.Kind, CanonicalID: canonicalID, ProducerCallID: producerCallID,
			ActionOrdinal: action.ActionOrdinal, Status: "succeeded", CreatedAt: succeededAt,
		})
	}

	consumptions := make([]service.MessageConsumptionInput, 0, len(upload.Consumptions))
	for i, consumption := range upload.Consumptions {
		if consumption.Source != "accept_message_batch" && consumption.Source != "message_check" {
			return service.TrustedTurnCapture{}, fmt.Errorf("unsupported consumption source %q", consumption.Source)
		}
		messageID, err := parseCanonicalCaptureUUID(consumption.ChannelMessageID)
		if err != nil {
			return service.TrustedTurnCapture{}, fmt.Errorf("invalid consumption channel_message_id")
		}
		effectiveFromCallID := strings.TrimSpace(consumption.EffectiveFromCallID)
		if _, found := callIDs[effectiveFromCallID]; !found {
			return service.TrustedTurnCapture{}, fmt.Errorf("consumption effective_from_call_id must reference a provider call in the same batch")
		}
		consumedAt, err := parseCaptureTimestamp(consumption.ConsumedAt, "consumption consumed_at")
		if err != nil {
			return service.TrustedTurnCapture{}, err
		}
		consumptions = append(consumptions, service.MessageConsumptionInput{
			ConsumptionID: deterministicCaptureRecordID(batchID, "consumption", int64(i+1)),
			RunID:         runID, RunAgentID: runAgentID, TurnID: turnID,
			ChannelMessageID: messageID, Source: consumption.Source,
			EffectiveFromCallID: effectiveFromCallID, ConsumedAt: consumedAt,
		})
	}

	return service.TrustedTurnCapture{
		RunID: runID, RunAgentID: runAgentID, TurnID: turnID, TurnOrdinal: upload.Turn.TurnOrdinal,
		Batch: service.TurnCaptureBatchInput{
			CaptureBatchID: batchID, TurnID: turnID, CaptureBoundary: upload.Turn.CaptureBoundary,
			CallCount: int32(len(calls)), ActionCount: int32(len(actions)), ConsumptionCount: int32(len(consumptions)),
			PayloadHash: upload.PayloadHash,
		},
		Calls: calls, Actions: actions, Consumptions: consumptions, CompletedAt: completedAt,
	}, nil
}

func parseCanonicalCaptureUUID(raw string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil || raw != parsed.String() {
		return pgtype.UUID{}, fmt.Errorf("UUID is not canonical")
	}
	var value pgtype.UUID
	copy(value.Bytes[:], parsed[:])
	value.Valid = true
	return value, nil
}

func captureJSONForbiddenField(raw json.RawMessage) (string, bool, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false, fmt.Errorf("provider request must be valid JSON")
	}
	if _, ok := payload.(map[string]any); !ok {
		return "", false, fmt.Errorf("provider request must be a JSON object")
	}
	found := make(map[string]string)
	var collect func(any)
	collect = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.Map(func(r rune) rune {
					if r >= 'A' && r <= 'Z' {
						return r + ('a' - 'A')
					}
					if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
						return r
					}
					return -1
				}, key)
				if _, exists := found[normalized]; !exists {
					found[normalized] = key
				}
				collect(child)
			}
		case []any:
			for _, child := range typed {
				collect(child)
			}
		}
	}
	collect(payload)
	for _, normalized := range []string{
		"authorization", "proxyauthorization", "xapikey", "apikey",
		"accesstoken", "refreshtoken", "sessiontoken", "awssessiontoken",
		"securitytoken", "idtoken", "authtoken", "bearertoken",
		"clientsecret", "privatekey", "clientprivatekey", "sshprivatekey",
		"serviceaccountprivatekey", "secretaccesskey", "awssecretaccesskey",
		"accesskeysecret", "secretkey", "credential", "credentials", "password",
		"setcookie", "cookies", "cookie", "secret",
	} {
		if key, ok := found[normalized]; ok {
			return key, true, nil
		}
	}
	return "", false, nil
}

func validateStrictPositiveOrdinals(values []int64, label string) error {
	seen := make(map[int64]struct{}, len(values))
	var previous int64
	for _, value := range values {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", label)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("duplicate %s %d", label, value)
		}
		if previous > 0 && value < previous {
			return fmt.Errorf("regressing %ss", label)
		}
		seen[value] = struct{}{}
		previous = value
	}
	return nil
}

func parseCaptureTimestamp(raw, field string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s", field)
	}
	return parsed, nil
}

func deterministicCaptureRecordID(batchID pgtype.UUID, kind string, ordinal int64) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.NewSHA1(uuid.NameSpaceURL, []byte(
		batchID.String()+":"+kind+":"+fmt.Sprint(ordinal),
	)), Valid: true}
}

func captureCallTimes(started, completed string) (time.Time, time.Time, error) {
	startedAt, err := parseCaptureTimestamp(started, "provider call started_at")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	completedAt, err := parseCaptureTimestamp(completed, "provider call completed_at")
	return startedAt, completedAt, err
}

// turnCaptureResponseFromResult maps an accepted (or late) capture onto the
// wire acknowledgement contract without exposing raw provider payloads.
func turnCaptureResponseFromResult(result service.TrustedTurnCaptureResult, capture service.TrustedTurnCapture) protocol.TurnCaptureUploadResponse {
	resp := protocol.TurnCaptureUploadResponse{
		Accepted:           true,
		CaptureBatchID:     capture.Batch.CaptureBatchID.String(),
		TurnID:             capture.TurnID.String(),
		ProviderCallCount:  len(capture.Calls),
		VisibleActionCount: len(capture.Actions),
		ConsumptionCount:   len(capture.Consumptions),
		RunStatus:          result.Run.Status,
		Late:               result.Late,
		SnapshotID:         result.SnapshotID,
	}
	if result.Turn.TurnID.Valid {
		resp.TurnID = result.Turn.TurnID.String()
	}
	return resp
}
