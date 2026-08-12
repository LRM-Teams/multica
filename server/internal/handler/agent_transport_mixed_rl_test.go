package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTurnCaptureFromProtocol_RejectsNonPositiveTurnOrdinal(t *testing.T) {
	_, err := turnCaptureFromProtocol(util.MustParseUUID("70000000-0000-4000-8000-000000000281"), protocol.TurnCaptureUpload{
		CaptureBatchID: "70000000-0000-4000-8000-000000000282",
		Turn: protocol.TurnCaptureTurn{
			TurnID: "70000000-0000-4000-8000-000000000283", RunAgentID: "70000000-0000-4000-8000-000000000284",
			CaptureBoundary: "boundary", TurnOrdinal: 0,
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "turn_ordinal")
}

func TestAgentTransportUploadTurnCapture_RejectsPayloadAgentDifferentFromCredential(t *testing.T) {
	credentialAgentID := "70000000-0000-4000-8000-000000000291"
	payloadAgentID := "70000000-0000-4000-8000-000000000292"
	req := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/v1/env-dispatch/runs/run/turn-captures", map[string]any{
		"agent_id": payloadAgentID,
	}))
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", credentialAgentID)
	route := chi.NewRouteContext()
	route.URLParams.Add("runID", "70000000-0000-4000-8000-000000000293")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	recorder := httptest.NewRecorder()
	(&Handler{}).AgentTransportUploadTurnCapture(recorder, req)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestTrustedTurnCaptureScopeMatchesRejectsCredentialRuntimeAndRunnerMismatches(t *testing.T) {
	workspaceID := util.MustParseUUID("70000000-0000-4000-8000-0000000002a1")
	credentialAgentID := util.MustParseUUID("70000000-0000-4000-8000-0000000002a2")
	runtimeID := util.MustParseUUID("70000000-0000-4000-8000-0000000002a3")
	source := agentTransportSource{origin: chatOutputOrigin{workspaceID: workspaceID, agentID: credentialAgentID}}
	run := db.EnvDispatchRun{WorkspaceID: workspaceID}
	runAgent := db.EnvDispatchRunAgent{ExecutionAgentID: credentialAgentID, RuntimeID: runtimeID, PiSessionID: "pi-1", CaptureBoundary: "boundary-1"}
	assert.True(t, trustedTurnCaptureScopeMatches(source, run, runAgent, credentialAgentID, runtimeID, "pi-1", "boundary-1"))
	assert.False(t, trustedTurnCaptureScopeMatches(source, run, runAgent, util.MustParseUUID("70000000-0000-4000-8000-0000000002a4"), runtimeID, "pi-1", "boundary-1"), "payload agent must equal credential agent")
	assert.False(t, trustedTurnCaptureScopeMatches(source, run, runAgent, credentialAgentID, util.MustParseUUID("70000000-0000-4000-8000-0000000002a5"), "pi-1", "boundary-1"), "runtime must match stored run-agent")
	foreignRunner := runAgent
	foreignRunner.ExecutionAgentID = util.MustParseUUID("70000000-0000-4000-8000-0000000002a6")
	assert.False(t, trustedTurnCaptureScopeMatches(source, run, foreignRunner, credentialAgentID, runtimeID, "pi-1", "boundary-1"), "stored runner scope must equal credential agent")
}

func TestTrustedTurnCaptureScopeMatchesRejectsPiSessionAndCaptureBoundaryMismatches(t *testing.T) {
	workspaceID := util.MustParseUUID("70000000-0000-4000-8000-0000000002b1")
	credentialAgentID := util.MustParseUUID("70000000-0000-4000-8000-0000000002b2")
	runtimeID := util.MustParseUUID("70000000-0000-4000-8000-0000000002b3")
	source := agentTransportSource{origin: chatOutputOrigin{workspaceID: workspaceID, agentID: credentialAgentID}}
	run := db.EnvDispatchRun{WorkspaceID: workspaceID}
	runAgent := db.EnvDispatchRunAgent{ExecutionAgentID: credentialAgentID, RuntimeID: runtimeID, PiSessionID: "pi-session", CaptureBoundary: "boundary-active"}
	assert.False(t, trustedTurnCaptureScopeMatches(source, run, runAgent, credentialAgentID, runtimeID, "pi-other", "boundary-active"), "pi session must match stored run-agent")
	assert.False(t, trustedTurnCaptureScopeMatches(source, run, runAgent, credentialAgentID, runtimeID, "pi-session", "boundary-stale"), "capture boundary must match stored run-agent")
	foreignWorkspace := db.EnvDispatchRun{WorkspaceID: util.MustParseUUID("70000000-0000-4000-8000-0000000002b4")}
	assert.False(t, trustedTurnCaptureScopeMatches(source, foreignWorkspace, runAgent, credentialAgentID, runtimeID, "pi-session", "boundary-active"), "workspace must match credential")
}

func TestTurnCaptureUploadResponseDoesNotExposeProviderPayload(t *testing.T) {
	body, err := json.Marshal(protocol.TurnCaptureUploadResponse{Accepted: true, TurnID: "turn-1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"accepted":true,"turn_id":"turn-1"}`, string(body))
	assert.NotContains(t, string(body), "raw_provider_request")
}

func TestTurnCaptureFromProtocol_MapsAtomicBatchActionsConsumptionsAndEligibility(t *testing.T) {
	runID := util.MustParseUUID("70000000-0000-4000-8000-0000000002c1")
	upload := protocol.TurnCaptureUpload{
		AgentID: "70000000-0000-4000-8000-0000000002c2", RuntimeID: "70000000-0000-4000-8000-0000000002c3",
		CaptureBatchID: "70000000-0000-4000-8000-0000000002c4",
		Turn: protocol.TurnCaptureTurn{
			TurnID: "70000000-0000-4000-8000-0000000002c5", RunAgentID: "70000000-0000-4000-8000-0000000002c6",
			PiSessionID: "pi-session", CaptureBoundary: "boundary-1", TurnOrdinal: 4,
			Status: "settled", CompletedAt: "2026-08-12T10:00:08Z",
		},
		ProviderCalls: []protocol.TurnCaptureProviderCall{{
			CallID: "C17", CallOrdinal: 17, Provider: "synthetic", Model: "synthetic-model", APIKind: "messages",
			RawProviderRequest:    json.RawMessage(`{"model":"synthetic-model","messages":[]}`),
			FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
			Status: "completed", StopReason: "toolUse", ResponseComplete: true,
			RequestHash: "sha256:req", ResponseHash: "sha256:resp",
			StartedAt: "2026-08-12T10:00:01Z", CompletedAt: "2026-08-12T10:00:04Z",
		}, {
			CallID: "C18", CallOrdinal: 18, Provider: "synthetic", Model: "synthetic-model", APIKind: "messages",
			RawProviderRequest:    json.RawMessage(`{"model":"synthetic-model","messages":[]}`),
			FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"cut"}]}`),
			Status: "completed", StopReason: "length", ResponseComplete: true,
			RequestHash: "sha256:req2", ResponseHash: "sha256:resp2",
			StartedAt: "2026-08-12T10:00:05Z", CompletedAt: "2026-08-12T10:00:06Z",
		}},
		VisibleActions: []protocol.TurnCaptureVisibleAction{{
			Kind: "message", CanonicalID: "70000000-0000-4000-8000-0000000002c7",
			ProducerCallID: "C17", ActionOrdinal: 1, SucceededAt: "2026-08-12T10:00:05Z",
		}},
		Consumptions: []protocol.TurnCaptureConsumption{{
			ChannelMessageID: "70000000-0000-4000-8000-0000000002c8", Source: "message_check",
			EffectiveFromCallID: "C17", ConsumedAt: "2026-08-12T10:00:00Z",
		}},
		PayloadHash: "sha256:batch",
	}

	capture, err := turnCaptureFromProtocol(runID, upload)
	require.NoError(t, err)
	assert.Equal(t, int32(2), capture.Batch.CallCount)
	assert.Equal(t, int32(1), capture.Batch.ActionCount, "atomic batch must count trusted visible actions")
	assert.Equal(t, int32(1), capture.Batch.ConsumptionCount, "atomic batch must count trusted consumptions")
	require.Len(t, capture.Actions, 1)
	assert.Equal(t, "message", capture.Actions[0].Kind)
	assert.Equal(t, "C17", capture.Actions[0].ProducerCallID)
	require.Len(t, capture.Consumptions, 1)
	assert.Equal(t, "message_check", capture.Consumptions[0].Source)
	assert.Equal(t, "C17", capture.Consumptions[0].EffectiveFromCallID)
	require.Len(t, capture.Calls, 2)
	assert.True(t, capture.Calls[0].TrainingEligible, "complete toolUse calls are training-eligible")
	assert.False(t, capture.Calls[1].TrainingEligible, "length-stop calls remain audit-only")
}

func TestTurnCaptureFromProtocol_RejectsMissingPayloadHashAndAuthMaterial(t *testing.T) {
	runID := util.MustParseUUID("70000000-0000-4000-8000-0000000002d1")
	base := protocol.TurnCaptureUpload{
		CaptureBatchID: "70000000-0000-4000-8000-0000000002d2",
		Turn: protocol.TurnCaptureTurn{
			TurnID: "70000000-0000-4000-8000-0000000002d3", RunAgentID: "70000000-0000-4000-8000-0000000002d4",
			PiSessionID: "pi-session", CaptureBoundary: "boundary-1", TurnOrdinal: 1,
		},
		ProviderCalls: []protocol.TurnCaptureProviderCall{{
			CallID: "C1", CallOrdinal: 1, Provider: "synthetic", Model: "m", APIKind: "messages",
			RawProviderRequest:    json.RawMessage(`{"model":"m"}`),
			FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
			Status: "completed", StopReason: "stop", ResponseComplete: true,
			RequestHash: "sha256:req", ResponseHash: "sha256:resp",
		}},
	}

	_, err := turnCaptureFromProtocol(runID, base)
	require.Error(t, err, "payload_hash is required for integrity")
	assert.Contains(t, err.Error(), "payload_hash")

	withAuth := base
	withAuth.PayloadHash = "sha256:batch"
	withAuth.ProviderCalls = []protocol.TurnCaptureProviderCall{{
		CallID: "C1", CallOrdinal: 1, Provider: "synthetic", Model: "m", APIKind: "messages",
		RawProviderRequest:    json.RawMessage(`{"model":"m","authorization":"Bearer secret","api_key":"secret"}`),
		FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
		Status: "completed", StopReason: "stop", ResponseComplete: true,
		RequestHash: "sha256:req", ResponseHash: "sha256:resp",
	}}
	_, err = turnCaptureFromProtocol(runID, withAuth)
	require.Error(t, err, "transport auth material must be rejected at the capture boundary")
	assert.Contains(t, err.Error(), "authorization")
}

func TestTurnCaptureFromProtocol_RejectsOverlappingOrRegressingCallOrdinals(t *testing.T) {
	runID := util.MustParseUUID("70000000-0000-4000-8000-0000000002e1")
	upload := protocol.TurnCaptureUpload{
		CaptureBatchID: "70000000-0000-4000-8000-0000000002e2",
		PayloadHash:    "sha256:batch",
		Turn: protocol.TurnCaptureTurn{
			TurnID: "70000000-0000-4000-8000-0000000002e3", RunAgentID: "70000000-0000-4000-8000-0000000002e4",
			PiSessionID: "pi-session", CaptureBoundary: "boundary-1", TurnOrdinal: 1,
		},
		ProviderCalls: []protocol.TurnCaptureProviderCall{
			syntheticTurnCaptureCall("C2", 2),
			syntheticTurnCaptureCall("C2-dup", 2),
		},
	}
	_, err := turnCaptureFromProtocol(runID, upload)
	require.Error(t, err, "overlapping call ordinals must reject the whole batch")

	upload.ProviderCalls = []protocol.TurnCaptureProviderCall{
		syntheticTurnCaptureCall("C3", 3),
		syntheticTurnCaptureCall("C2", 2),
	}
	_, err = turnCaptureFromProtocol(runID, upload)
	require.Error(t, err, "regressing call ordinals must reject the whole batch")
}

func TestTurnCaptureResponseFromResult_RoutesPostFreezeLateAudit(t *testing.T) {
	result := service.TrustedTurnCaptureResult{
		Late:       true,
		SnapshotID: "sha256:frozen-snapshot",
		Run:        service.EnvDispatchRunRecord{Status: "completed"},
	}
	capture := service.TrustedTurnCapture{
		Batch: service.TurnCaptureBatchInput{CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-0000000002f1")},
		Calls: []service.ProviderCallInput{{CallID: "C1"}},
	}
	resp := turnCaptureResponseFromResult(result, capture)
	assert.True(t, resp.Accepted)
	assert.True(t, resp.Late, "post-freeze uploads must be acknowledged as late audit events")
	assert.Equal(t, "sha256:frozen-snapshot", resp.SnapshotID)
	assert.Equal(t, "completed", resp.RunStatus)
	assert.Equal(t, "70000000-0000-4000-8000-0000000002f1", resp.CaptureBatchID)
	assert.Equal(t, 1, resp.ProviderCallCount)
	assert.Zero(t, resp.VisibleActionCount)
	assert.NotContains(t, mustJSON(t, resp), "raw_provider_request")
}

func syntheticTurnCaptureCall(callID string, ordinal int64) protocol.TurnCaptureProviderCall {
	return protocol.TurnCaptureProviderCall{
		CallID: callID, CallOrdinal: ordinal, Provider: "synthetic", Model: "m", APIKind: "messages",
		RawProviderRequest:    json.RawMessage(`{"model":"m"}`),
		FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
		Status: "completed", StopReason: "stop", ResponseComplete: true,
		RequestHash: "sha256:req-" + callID, ResponseHash: "sha256:resp-" + callID,
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return string(body)
}
