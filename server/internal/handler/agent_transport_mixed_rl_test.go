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

func TestTurnCaptureUploadResponseDoesNotExposeProviderPayload(t *testing.T) {
	body, err := json.Marshal(protocol.TurnCaptureUploadResponse{Accepted: true, TurnID: "turn-1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"accepted":true,"turn_id":"turn-1"}`, string(body))
	assert.NotContains(t, string(body), "raw_provider_request")
}
