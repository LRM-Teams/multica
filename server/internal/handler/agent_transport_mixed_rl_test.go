package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

func TestAgentTransportUploadTurnCapture_AcknowledgesLateCaptureFromProviderService(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	fixture := seedMixedRunDeliveryFixture(t)

	var piSessionID, captureBoundary string
	err := testPool.QueryRow(fixture.ctx, `
		SELECT pi_session_id, capture_boundary
		FROM env_dispatch_run_agent
		WHERE run_id = $1 AND run_agent_id = $2
	`, fixture.runID, fixture.runAgentID).Scan(&piSessionID, &captureBoundary)
	require.NoError(t, err)

	turnID := util.MustParseUUID("70000000-0000-4000-8000-0000000002a9")
	_, err = testPool.Exec(fixture.ctx, `
		INSERT INTO env_dispatch_resident_turn (
			turn_id, run_id, run_agent_id, turn_ordinal, status
		) VALUES ($1, $2, $3, 1, 'active')
	`, turnID, fixture.runID, fixture.runAgentID)
	require.NoError(t, err)

	snapshotID := "sha256:handler-late-" + fixture.runID.String()
	snapshotHash := snapshotID
	_, err = testPool.Exec(fixture.ctx, `
		UPDATE env_dispatch_run
		SET status = 'freezing'
		WHERE run_id = $1 AND status = 'running'
	`, fixture.runID)
	require.NoError(t, err)
	_, err = testHandler.Queries.CreateMixedRLFrozenSnapshot(fixture.ctx, db.CreateMixedRLFrozenSnapshotParams{
		SnapshotID: snapshotID, RunID: fixture.runID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`), SnapshotHash: snapshotHash,
	})
	require.NoError(t, err)
	_, err = testHandler.Queries.CompleteMixedRLRunWithSnapshot(fixture.ctx, db.CompleteMixedRLRunWithSnapshotParams{
		TerminalStatus: "completed", RunID: fixture.runID, SnapshotID: snapshotID, SnapshotHash: snapshotHash,
	})
	require.NoError(t, err)

	upload := validTurnCaptureUpload()
	upload.AgentID = fixture.agentID
	upload.RuntimeID = fixture.runtimeID
	upload.Turn.RunAgentID = fixture.runAgentID.String()
	upload.Turn.PiSessionID = piSessionID
	upload.Turn.CaptureBoundary = captureBoundary
	upload.Turn.TurnID = turnID.String()
	upload.CaptureBatchID = "70000000-0000-4000-8000-0000000002aa"

	req := withChatTestWorkspaceCtx(t, newRequest(http.MethodPost, "/api/v1/env-dispatch/runs/"+fixture.runID.String()+"/turn-captures", upload))
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", fixture.agentID)
	req = withURLParam(req, "runID", fixture.runID.String())
	recorder := httptest.NewRecorder()
	testHandler.AgentTransportUploadTurnCapture(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response protocol.TurnCaptureUploadResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Accepted)
	assert.True(t, response.Late)
	assert.Equal(t, snapshotID, response.SnapshotID)
	assert.Equal(t, "completed", response.RunStatus)
	assert.Equal(t, upload.CaptureBatchID, response.CaptureBatchID)
	assert.Equal(t, upload.Turn.TurnID, response.TurnID)
	assert.Equal(t, len(upload.ProviderCalls), response.ProviderCallCount)
	assert.Equal(t, len(upload.VisibleActions), response.VisibleActionCount)
	assert.Equal(t, len(upload.Consumptions), response.ConsumptionCount)
	assert.NotContains(t, recorder.Body.String(), "raw_provider_request")
	assert.NotContains(t, recorder.Body.String(), "final_assistant_message")
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

func TestTurnCaptureUploadResponseSerializesZeroCountsWithoutProviderPayload(t *testing.T) {
	body, err := json.Marshal(protocol.TurnCaptureUploadResponse{Accepted: true, TurnID: "turn-1"})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"accepted": true,
		"turn_id": "turn-1",
		"provider_call_count": 0,
		"visible_action_count": 0,
		"consumption_count": 0
	}`, string(body))
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
			Status:                "completed", StopReason: "toolUse", ResponseComplete: true,
			RequestHash: "sha256:req", ResponseHash: "sha256:resp",
			StartedAt: "2026-08-12T10:00:01Z", CompletedAt: "2026-08-12T10:00:04Z",
		}, {
			CallID: "C18", CallOrdinal: 18, Provider: "synthetic", Model: "synthetic-model", APIKind: "messages",
			RawProviderRequest:    json.RawMessage(`{"model":"synthetic-model","messages":[]}`),
			FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"cut"}]}`),
			Status:                "completed", StopReason: "length", ResponseComplete: true,
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
	expectedActionID := util.MustParseUUID(uuid.NewSHA1(uuid.NameSpaceURL, []byte(upload.CaptureBatchID+":action:1")).String())
	expectedConsumptionID := util.MustParseUUID(uuid.NewSHA1(uuid.NameSpaceURL, []byte(upload.CaptureBatchID+":consumption:1")).String())
	assert.Equal(t, expectedActionID, capture.Actions[0].ActionID)
	assert.Equal(t, expectedConsumptionID, capture.Consumptions[0].ConsumptionID)
	require.Len(t, capture.Calls, 2)
	assert.True(t, capture.Calls[0].TrainingEligible, "complete toolUse calls are training-eligible")
	assert.False(t, capture.Calls[1].TrainingEligible, "length-stop calls remain audit-only")
}

func TestTurnCaptureFromProtocol_RejectsInvalidTrustedBatchRecords(t *testing.T) {
	runID := util.MustParseUUID("70000000-0000-4000-8000-0000000002d1")
	tests := []struct {
		name    string
		mutate  func(*protocol.TurnCaptureUpload)
		message string
	}{
		{
			name: "duplicate action ordinal",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.VisibleActions = append(upload.VisibleActions, upload.VisibleActions[0])
				upload.VisibleActions[1].CanonicalID = "70000000-0000-4000-8000-0000000002da"
			},
			message: "visible action ordinal",
		},
		{
			name: "regressing action ordinal",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.VisibleActions[0].ActionOrdinal = 2
				upload.VisibleActions = append(upload.VisibleActions, protocol.TurnCaptureVisibleAction{
					Kind: "reaction", CanonicalID: "70000000-0000-4000-8000-0000000002db",
					ProducerCallID: "C17", ActionOrdinal: 1,
				})
			},
			message: "visible action ordinal",
		},
		{
			name: "nonpositive action ordinal",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.VisibleActions[0].ActionOrdinal = 0
			},
			message: "visible action ordinal",
		},
		{
			name: "invalid canonical id",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.VisibleActions[0].CanonicalID = "not-a-uuid"
			},
			message: "canonical_id",
		},
		{
			name: "missing request hash",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.ProviderCalls[0].RequestHash = " "
			},
			message: "request_hash",
		},
		{
			name: "missing response hash",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.ProviderCalls[0].ResponseHash = ""
			},
			message: "response_hash",
		},
		{
			name: "nested secret field",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.ProviderCalls[0].RawProviderRequest = json.RawMessage(`{"messages":[],"transport":{"auth":{"client-secret":"do-not-log"}}}`)
			},
			message: "client-secret",
		},
		{
			name: "malformed provider request json",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.ProviderCalls[0].RawProviderRequest = json.RawMessage(`{"messages":`)
			},
			message: "valid JSON",
		},
		{
			name: "invalid consumption channel message id",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.Consumptions[0].ChannelMessageID = "not-a-uuid"
			},
			message: "channel_message_id",
		},
		{
			name: "action references foreign call",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.VisibleActions[0].ProducerCallID = "foreign-call"
			},
			message: "producer_call_id",
		},
		{
			name: "consumption references foreign call",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.Consumptions[0].EffectiveFromCallID = "foreign-call"
			},
			message: "effective_from_call_id",
		},
		{
			name: "provider call completion before start",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.ProviderCalls[0].StartedAt = "2026-08-12T10:00:04Z"
				upload.ProviderCalls[0].CompletedAt = "2026-08-12T10:00:03Z"
			},
			message: "before started_at",
		},
		{
			name: "turn completion before start",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.Turn.StartedAt = "2026-08-12T10:00:09Z"
				upload.Turn.CompletedAt = "2026-08-12T10:00:08Z"
			},
			message: "before started_at",
		},
		{
			name: "invalid turn started timestamp",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.Turn.StartedAt = "not-a-timestamp"
			},
			message: "turn started_at",
		},
		{
			name: "invalid provider call completed timestamp",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.ProviderCalls[0].CompletedAt = "not-a-timestamp"
			},
			message: "provider call completed_at",
		},
		{
			name: "invalid visible action timestamp",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.VisibleActions[0].SucceededAt = "not-a-timestamp"
			},
			message: "visible action succeeded_at",
		},
		{
			name: "invalid consumption timestamp",
			mutate: func(upload *protocol.TurnCaptureUpload) {
				upload.Consumptions[0].ConsumedAt = "not-a-timestamp"
			},
			message: "consumption consumed_at",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upload := validTurnCaptureUpload()
			tc.mutate(&upload)
			_, err := turnCaptureFromProtocol(runID, upload)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.message)
			assert.NotContains(t, err.Error(), "do-not-log")
		})
	}
}

func TestTurnCaptureFromProtocol_RejectsNestedCredentialKeysWithoutLeakingValues(t *testing.T) {
	runID := util.MustParseUUID("70000000-0000-4000-8000-0000000002d1")
	tests := []struct {
		name  string
		field string
		raw   string
	}{
		{name: "refresh token", field: "refresh_token", raw: `{"request":{"auth":{"refresh_token":"sentinel-refresh-value"}}}`},
		{name: "session token", field: "session-token", raw: `{"request":{"headers":[{"session-token":"sentinel-session-value"}]}}`},
		{name: "identity token", field: "id_token", raw: `{"request":{"identity":{"id_token":"sentinel-id-value"}}}`},
		{name: "private key", field: "private_key", raw: `{"request":{"signing":{"private_key":"sentinel-private-value"}}}`},
		{name: "secret access key", field: "secret_access_key", raw: `{"request":{"aws":{"secret_access_key":"sentinel-access-value"}}}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upload := validTurnCaptureUpload()
			upload.ProviderCalls[0].RawProviderRequest = json.RawMessage(tc.raw)
			_, err := turnCaptureFromProtocol(runID, upload)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.field)
			assert.NotContains(t, err.Error(), "sentinel-")
		})
	}
}

func TestCaptureJSONForbiddenField_AllowsBenignRelatedFieldNames(t *testing.T) {
	field, forbidden, err := captureJSONForbiddenField(json.RawMessage(`{
		"metrics":{"refresh_token_count":2},
		"signing":{"private_key_id":"synthetic-key-id"},
		"session":{"session_token_expires_at":"2026-08-12T12:00:00Z"}
	}`))
	require.NoError(t, err)
	assert.False(t, forbidden)
	assert.Empty(t, field)
}

func TestTurnCaptureFromProtocol_DerivesRecordIDsFromBatchKindAndOrdinal(t *testing.T) {
	runID := util.MustParseUUID("70000000-0000-4000-8000-0000000002e1")
	upload := validTurnCaptureUpload()
	first, err := turnCaptureFromProtocol(runID, upload)
	require.NoError(t, err)

	upload.Turn.TurnID = "70000000-0000-4000-8000-0000000002ea"
	upload.VisibleActions[0].CanonicalID = "70000000-0000-4000-8000-0000000002eb"
	upload.Consumptions[0].ChannelMessageID = "70000000-0000-4000-8000-0000000002ec"
	second, err := turnCaptureFromProtocol(runID, upload)
	require.NoError(t, err)
	assert.Equal(t, first.Actions[0].ActionID, second.Actions[0].ActionID)
	assert.Equal(t, first.Consumptions[0].ConsumptionID, second.Consumptions[0].ConsumptionID)
	assert.NotEqual(t, first.Actions[0].ActionID, first.Consumptions[0].ConsumptionID)

	upload.CaptureBatchID = "70000000-0000-4000-8000-0000000002ed"
	third, err := turnCaptureFromProtocol(runID, upload)
	require.NoError(t, err)
	assert.NotEqual(t, first.Actions[0].ActionID, third.Actions[0].ActionID)
	assert.NotEqual(t, first.Consumptions[0].ConsumptionID, third.Consumptions[0].ConsumptionID)
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
			Status:                "completed", StopReason: "stop", ResponseComplete: true,
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
		Status:                "completed", StopReason: "stop", ResponseComplete: true,
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
		TurnID: util.MustParseUUID("70000000-0000-4000-8000-0000000002f2"),
		Batch:  service.TurnCaptureBatchInput{CaptureBatchID: util.MustParseUUID("70000000-0000-4000-8000-0000000002f1")},
		Calls:  []service.ProviderCallInput{{CallID: "C1"}},
	}
	resp := turnCaptureResponseFromResult(result, capture)
	assert.True(t, resp.Accepted)
	assert.True(t, resp.Late, "post-freeze uploads must be acknowledged as late audit events")
	assert.Equal(t, "sha256:frozen-snapshot", resp.SnapshotID)
	assert.Equal(t, "completed", resp.RunStatus)
	assert.Equal(t, "70000000-0000-4000-8000-0000000002f2", resp.TurnID)
	assert.Equal(t, "70000000-0000-4000-8000-0000000002f1", resp.CaptureBatchID)
	assert.Equal(t, 1, resp.ProviderCallCount)
	assert.Zero(t, resp.VisibleActionCount)
	assert.NotContains(t, mustJSON(t, resp), "raw_provider_request")
}

func validTurnCaptureUpload() protocol.TurnCaptureUpload {
	return protocol.TurnCaptureUpload{
		CaptureBatchID: "70000000-0000-4000-8000-0000000002d2",
		PayloadHash:    "sha256:batch",
		Turn: protocol.TurnCaptureTurn{
			TurnID: "70000000-0000-4000-8000-0000000002d3", RunAgentID: "70000000-0000-4000-8000-0000000002d4",
			PiSessionID: "pi-session", CaptureBoundary: "boundary-1", TurnOrdinal: 1,
			StartedAt: "2026-08-12T10:00:00Z", CompletedAt: "2026-08-12T10:00:08Z",
		},
		ProviderCalls: []protocol.TurnCaptureProviderCall{syntheticTurnCaptureCall("C17", 17)},
		VisibleActions: []protocol.TurnCaptureVisibleAction{{
			Kind: "message", CanonicalID: "70000000-0000-4000-8000-0000000002d5",
			ProducerCallID: "C17", ActionOrdinal: 1, SucceededAt: "2026-08-12T10:00:05Z",
		}},
		Consumptions: []protocol.TurnCaptureConsumption{{
			ChannelMessageID: "70000000-0000-4000-8000-0000000002d6", Source: "message_check",
			EffectiveFromCallID: "C17", ConsumedAt: "2026-08-12T10:00:06Z",
		}},
	}
}

func syntheticTurnCaptureCall(callID string, ordinal int64) protocol.TurnCaptureProviderCall {
	return protocol.TurnCaptureProviderCall{
		CallID: callID, CallOrdinal: ordinal, Provider: "synthetic", Model: "m", APIKind: "messages",
		RawProviderRequest:    json.RawMessage(`{"model":"m"}`),
		FinalAssistantMessage: json.RawMessage(`{"role":"assistant","blocks":[{"type":"text","text":"ok"}]}`),
		Status:                "completed", StopReason: "stop", ResponseComplete: true,
		RequestHash: "sha256:req-" + callID, ResponseHash: "sha256:resp-" + callID,
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	return string(body)
}
