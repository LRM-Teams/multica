package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestCreateAgentCredential_DerivesBindingFromAgentAndRuntimeOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	seedHandlerTestRuntimeOwner(t, testUserID)
	agentID := createHandlerTestAgent(t, "agent-credential-issuance", nil)
	req := withURLParam(newRequest(http.MethodPost, "/api/agents/"+agentID+"/credentials", map[string]any{
		"expires_in_days": 7,
	}), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.CreateAgentCredential(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgentCredential: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateAgentCredentialResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" || resp.Prefix == "" || resp.ExpiresAt == nil {
		t.Fatalf("incomplete credential response: %#v", resp)
	}
	if resp.AgentID != agentID {
		t.Fatalf("response agent_id = %q, want %q", resp.AgentID, agentID)
	}

	credential, err := testHandler.Queries.GetAgentCredentialByHash(context.Background(), auth.HashToken(resp.Token))
	if err != nil {
		t.Fatalf("load created credential by hash: %v", err)
	}
	if uuidToString(credential.AgentID) != agentID {
		t.Fatalf("credential agent_id = %q, want %q", uuidToString(credential.AgentID), agentID)
	}
	if uuidToString(credential.WorkspaceID) != testWorkspaceID {
		t.Fatalf("credential workspace_id = %q, want %q", uuidToString(credential.WorkspaceID), testWorkspaceID)
	}
	if uuidToString(credential.UserID) != testUserID {
		t.Fatalf("credential user_id = %q, want %q", uuidToString(credential.UserID), testUserID)
	}
}

func TestCreateAgentCredential_RejectsCallerSuppliedBindingFields(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	seedHandlerTestRuntimeOwner(t, testUserID)
	agentID := createHandlerTestAgent(t, "agent-credential-free-triple", nil)
	for _, field := range []string{"agent_id", "workspace_id", "user_id"} {
		body := map[string]any{
			"expires_in_days": 1,
			field:             "00000000-0000-0000-0000-000000000000",
		}
		req := withURLParam(newRequest(http.MethodPost, "/api/agents/"+agentID+"/credentials", body), "id", agentID)
		w := httptest.NewRecorder()
		testHandler.CreateAgentCredential(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d: %s", field, w.Code, w.Body.String())
		}
	}
}

func TestCreateAgentCredential_RejectsAgentActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	targetID := createHandlerTestAgent(t, "agent-credential-target", nil)
	hostID := createHandlerTestAgent(t, "agent-credential-host", nil)
	req := withURLParam(newRequest(http.MethodPost, "/api/agents/"+targetID+"/credentials", map[string]any{
		"expires_in_days": 1,
	}), "id", targetID)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", hostID)
	w := httptest.NewRecorder()
	testHandler.CreateAgentCredential(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for agent actor issuing credential, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgentCredential_RejectsPlainNonOwnerMember(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	seedHandlerTestRuntimeOwner(t, testUserID)
	agentID, _, memberID := privateAgentTestFixture(t)
	req := withURLParam(newRequestAs(memberID, http.MethodPost, "/api/agents/"+agentID+"/credentials", map[string]any{
		"expires_in_days": 1,
	}), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.CreateAgentCredential(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for plain non-owner member, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateAgentCredential_RejectsAgentOwnerWhoIsNotRuntimeOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	seedHandlerTestRuntimeOwner(t, testUserID)
	agentID, ownerID, _ := privateAgentTestFixture(t)
	req := withURLParam(newRequestAs(ownerID, http.MethodPost, "/api/agents/"+agentID+"/credentials", map[string]any{
		"expires_in_days": 1,
	}), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.CreateAgentCredential(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for agent owner on someone else's runtime, got %d: %s", w.Code, w.Body.String())
	}
}

func seedHandlerTestRuntimeOwner(t *testing.T, ownerID string) {
	t.Helper()

	runtimeID := handlerTestRuntimeID(t)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET owner_id = $1 WHERE id = $2`, ownerID, runtimeID); err != nil {
		t.Fatalf("seed runtime owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET owner_id = NULL WHERE id = $1`, runtimeID)
	})
}

func seedHandlerTestRuntimeDaemonID(t *testing.T, daemonID string) {
	t.Helper()

	runtimeID := handlerTestRuntimeID(t)
	var oldDaemonID pgtype.Text
	if err := testPool.QueryRow(context.Background(), `SELECT daemon_id FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&oldDaemonID); err != nil {
		t.Fatalf("load runtime daemon id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET daemon_id = $1 WHERE id = $2`, daemonID, runtimeID); err != nil {
		t.Fatalf("seed runtime daemon id: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET daemon_id = $1 WHERE id = $2`, oldDaemonID, runtimeID)
	})
}

func seedHandlerTestRuntimeDaemonIDNull(t *testing.T) {
	t.Helper()

	runtimeID := handlerTestRuntimeID(t)
	var oldDaemonID pgtype.Text
	if err := testPool.QueryRow(context.Background(), `SELECT daemon_id FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&oldDaemonID); err != nil {
		t.Fatalf("load runtime daemon id: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET daemon_id = NULL WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("clear runtime daemon id: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET daemon_id = $1 WHERE id = $2`, oldDaemonID, runtimeID)
	})
}

func seedHandlerTestRuntimeCapabilities(t *testing.T, capabilities []string) {
	t.Helper()

	runtimeID := handlerTestRuntimeID(t)
	var oldMetadata []byte
	if err := testPool.QueryRow(context.Background(), `SELECT metadata FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&oldMetadata); err != nil {
		t.Fatalf("load runtime metadata: %v", err)
	}
	metadata, err := json.Marshal(map[string]any{"capabilities": capabilities})
	if err != nil {
		t.Fatalf("marshal runtime capabilities: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET metadata = $1 WHERE id = $2`, metadata, runtimeID); err != nil {
		t.Fatalf("seed runtime capabilities: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent_runtime SET metadata = $1 WHERE id = $2`, oldMetadata, runtimeID)
	})
}

func TestEnsureDaemonAgentCredential_DerivesOwnerFromRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "agent-credential-daemon-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-ensure", nil)

	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", nil, testWorkspaceID, daemonID)
	req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
	w := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("EnsureDaemonAgentCredential: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateAgentCredentialResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" || resp.AgentID != agentID || resp.ExpiresAt == nil {
		t.Fatalf("unexpected response: %#v", resp)
	}
	credential, err := testHandler.Queries.GetAgentCredentialByHash(context.Background(), auth.HashToken(resp.Token))
	if err != nil {
		t.Fatalf("load created credential by hash: %v", err)
	}
	if uuidToString(credential.WorkspaceID) != testWorkspaceID || uuidToString(credential.UserID) != testUserID || uuidToString(credential.AgentID) != agentID {
		t.Fatalf("credential binding workspace/user/agent = %s/%s/%s", uuidToString(credential.WorkspaceID), uuidToString(credential.UserID), uuidToString(credential.AgentID))
	}
	remaining := time.Until(credential.ExpiresAt.Time)
	if !credential.ExpiresAt.Valid || remaining < 23*time.Hour || remaining > 25*time.Hour {
		t.Fatalf("daemon-issued credential expires_at = %v, want bounded future expiry", credential.ExpiresAt)
	}
}

func TestEnsureDaemonAgentCredential_RequiresDaemonTokenAndRuntimeBinding(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "agent-credential-daemon-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-reject", nil)

	patReq := newRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", nil)
	patReq = withRouteParams(patReq, "runtimeId", runtimeID, "agentId", agentID)
	patRec := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(patRec, patReq)
	if patRec.Code != http.StatusForbidden {
		t.Fatalf("expected PAT/JWT path 403, got %d: %s", patRec.Code, patRec.Body.String())
	}

	mismatchReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", nil, testWorkspaceID, daemonID+"-other")
	mismatchReq = withRouteParams(mismatchReq, "runtimeId", runtimeID, "agentId", agentID)
	mismatchRec := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(mismatchRec, mismatchReq)
	if mismatchRec.Code != http.StatusForbidden {
		t.Fatalf("expected daemon/runtime mismatch 403, got %d: %s", mismatchRec.Code, mismatchRec.Body.String())
	}
}

func TestEnsureDaemonAgentCredential_RejectsUnboundRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "agent-credential-daemon-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonIDNull(t)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-null", nil)

	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", nil, testWorkspaceID, daemonID)
	req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
	rec := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected unbound runtime 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDrainAgentInbox_CredentialTransportRuntimeSkipsDeliveryTokenMint(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "agent-credential-no-mint-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	seedHandlerTestRuntimeCapabilities(t, []string{protocol.DaemonCapabilityAgentCredentialTransport})
	agentName := "Agent Credential No Mint " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "agent-credential-no-mint-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent channel member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") credential transport", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("agent-credential-no-mint"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, daemonID)
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 || drainResp.Events[0].Task == nil {
		t.Fatalf("drain response missing event task: %s", drainRec.Body.String())
	}
	if drainResp.Events[0].Task.AuthToken != "" {
		t.Fatalf("credential-transport runtime must not receive #452 auth_token")
	}
	var tokenCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_inbox_token WHERE inbox_event_id = $1`, drainResp.Events[0].ID).Scan(&tokenCount); err != nil {
		t.Fatalf("count inbox token rows: %v", err)
	}
	if tokenCount != 0 {
		t.Fatalf("agent_inbox_token rows = %d, want 0", tokenCount)
	}
}

func TestAgentCredentialTransportRequiresInboxLease(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	req := newRequest(http.MethodPost, "/api/agent/messages/send", nil)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	if _, ok := testHandler.requireAgentTransportSource(w, req); ok {
		t.Fatal("agent_credential must not be accepted without inbox freshness headers")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestAgentCredentialTransportAllowsActiveInboxDeliveryThroughMiddleware(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	fixture := seedAgentCredentialTransportFixture(t)
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(testHandler.Queries, nil, nil))
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Post("/api/agent/messages/send", testHandler.AgentTransportSendMessage)
	})

	clientID := "agent-credential-transport-" + uuid.NewString()
	body := map[string]any{
		"target": "#" + channelNameForTransportTest(t, fixture.channelID),
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "huaji",
		}},
		"client_message_id": clientID,
	}
	sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", body)
	sendReq.Header.Set("Authorization", "Bearer "+fixture.credentialToken)
	sendReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	sendReq.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
	sendReq.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
	sendReq.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	sendRec := httptest.NewRecorder()
	router.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("agent credential transport send: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	var taskAuditRows, inboxAuditRows int
	if err := testPool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE task_id IS NOT NULL),
			count(*) FILTER (WHERE inbox_event_id = $1)
		FROM agent_task_transport_audit
		WHERE agent_id = $2 AND action = 'message_send' AND client_message_id = $3`,
		fixture.event.ID, fixture.agentID, clientID).Scan(&taskAuditRows, &inboxAuditRows); err != nil {
		t.Fatalf("count transport audit rows: %v", err)
	}
	if taskAuditRows != 0 || inboxAuditRows != 1 {
		t.Fatalf("transport audit task rows=%d inbox rows=%d, want 0/1", taskAuditRows, inboxAuditRows)
	}
}

func TestAgentCredentialTransportA2AReplyKeepsInheritedExchange(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	fixture := seedAgentCredentialTransportFixture(t)
	peerDisplayName := "Agent Credential A2A Peer " + uuid.NewString()[:8]
	peerID := createHandlerTestAgent(t, peerDisplayName, nil)
	channel := createAgentAgentDMChannelForTest(t, fixture.agentID, peerID)
	lowID, highID, ok := normalizedAgentDMPair(parseUUID(fixture.agentID), parseUUID(peerID))
	if !ok {
		t.Fatal("normalize agent credential A2A pair failed")
	}

	var peerHandle string
	if err := testPool.QueryRow(ctx, `
		SELECT name
		FROM agent
		WHERE id = $1`, peerID).Scan(&peerHandle); err != nil {
		t.Fatalf("load peer handle: %v", err)
	}

	var exchangeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_dm_exchange (
		  workspace_id, channel_id, agent_low_id, agent_high_id,
		  next_sender_agent_id, matter_id, turn_count
		)
		VALUES ($1, $2, $3, $4, $5, gen_random_uuid(), 1)
		RETURNING id`,
		testWorkspaceID, channel.ID, lowID, highID, fixture.agentID,
	).Scan(&exchangeID); err != nil {
		t.Fatalf("seed inherited agent credential exchange: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET agent_dm_exchange_id = $2,
		    agent_dm_turn = 1
		WHERE id = $1`, fixture.event.ID, exchangeID); err != nil {
		t.Fatalf("bind agent credential inbox event to exchange: %v", err)
	}

	event, err := testHandler.Queries.GetAgentInboxEvent(ctx, parseUUID(fixture.event.ID))
	if err != nil {
		t.Fatalf("load bound agent credential inbox event: %v", err)
	}
	synthetic := agentInboxSyntheticTask(event, parseUUID(handlerTestRuntimeID(t)))
	if uuidToString(synthetic.AgentDmExchangeID) != exchangeID {
		t.Fatalf("synthetic exchange_id = %q, want %q", uuidToString(synthetic.AgentDmExchangeID), exchangeID)
	}
	if !synthetic.AgentDmTurn.Valid || synthetic.AgentDmTurn.Int32 != 1 {
		t.Fatalf("synthetic agent_dm_turn = %+v, want 1", synthetic.AgentDmTurn)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(testHandler.Queries, nil, nil))
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Post("/api/agent/messages/send", testHandler.AgentTransportSendMessage)
	})
	clientID := "agent-credential-a2a-" + uuid.NewString()
	sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":            "dm:@" + peerHandle,
		"content":           "automatic A2A reply",
		"client_message_id": clientID,
	})
	sendReq.Header.Set("Authorization", "Bearer "+fixture.credentialToken)
	sendReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	sendReq.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
	sendReq.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
	sendReq.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	sendRec := httptest.NewRecorder()
	router.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("agent credential A2A send: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}

	var exchangeCount, turnCount int
	var state, latestMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_dm_exchange
		WHERE workspace_id = $1
		  AND agent_low_id = $2
		  AND agent_high_id = $3`,
		testWorkspaceID, lowID, highID,
	).Scan(&exchangeCount); err != nil {
		t.Fatalf("count agent credential A2A exchanges: %v", err)
	}
	if exchangeCount != 1 {
		t.Fatalf("agent credential A2A exchanges = %d, want inherited exchange only", exchangeCount)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT turn_count, state, latest_message_id
		FROM agent_dm_exchange
		WHERE id = $1`, exchangeID).Scan(&turnCount, &state, &latestMessageID); err != nil {
		t.Fatalf("load inherited agent credential exchange: %v", err)
	}
	if turnCount != 2 || state != "active" || latestMessageID == "" {
		t.Fatalf("inherited exchange turn_count=%d state=%q latest_message_id=%q, want 2/active/non-empty", turnCount, state, latestMessageID)
	}
}

func TestAgentCredentialTransportRejectsInvalidFreshness(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, tc := range []struct {
		name       string
		mutate     func(t *testing.T, fixture agentCredentialTransportFixture) (eventID, deliveryID, leaseToken string)
		wantStatus int
	}{
		{
			name: "wrong event",
			mutate: func(t *testing.T, fixture agentCredentialTransportFixture) (string, string, string) {
				t.Helper()
				return uuid.NewString(), fixture.event.DeliveryID, fixture.event.LeaseToken
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "wrong delivery",
			mutate: func(t *testing.T, fixture agentCredentialTransportFixture) (string, string, string) {
				t.Helper()
				return fixture.event.ID, uuid.NewString(), fixture.event.LeaseToken
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "wrong lease token",
			mutate: func(t *testing.T, fixture agentCredentialTransportFixture) (string, string, string) {
				t.Helper()
				return fixture.event.ID, fixture.event.DeliveryID, uuid.NewString()
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "expired lease",
			mutate: func(t *testing.T, fixture agentCredentialTransportFixture) (string, string, string) {
				t.Helper()
				if _, err := testPool.Exec(context.Background(), `
					UPDATE agent_event_delivery
					SET lease_expires_at = now() - interval '1 second'
					WHERE id = $1`, fixture.event.DeliveryID); err != nil {
					t.Fatalf("expire delivery: %v", err)
				}
				return fixture.event.ID, fixture.event.DeliveryID, fixture.event.LeaseToken
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "stale delivery with newer lease",
			mutate: func(t *testing.T, fixture agentCredentialTransportFixture) (string, string, string) {
				t.Helper()
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO agent_event_delivery (workspace_id, agent_session_id, inbox_event_id, runtime_id, status)
					SELECT workspace_id, agent_session_id, id, $2, 'leased'
					FROM agent_inbox_event
					WHERE id = $1`, fixture.event.ID, handlerTestRuntimeID(t)); err != nil {
					t.Fatalf("insert newer delivery: %v", err)
				}
				return fixture.event.ID, fixture.event.DeliveryID, fixture.event.LeaseToken
			},
			wantStatus: http.StatusConflict,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := seedAgentCredentialTransportFixture(t)
			eventID, deliveryID, leaseToken := tc.mutate(t, fixture)
			req := newRequest(http.MethodPost, "/api/agent/messages/send", nil)
			req = withChatTestWorkspaceCtx(t, req)
			req.Header.Set("X-Actor-Source", "agent_credential")
			req.Header.Set("X-Agent-ID", fixture.agentID)
			req.Header.Set("X-Agent-Inbox-Event-ID", eventID)
			req.Header.Set("X-Agent-Inbox-Delivery-ID", deliveryID)
			req.Header.Set("X-Agent-Inbox-Lease-Token", leaseToken)
			w := httptest.NewRecorder()
			if _, ok := testHandler.requireAgentTransportSource(w, req); ok {
				t.Fatal("agent_credential transport source unexpectedly accepted invalid freshness")
			}
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

type agentCredentialTransportFixture struct {
	agentID         string
	channelID       string
	event           AgentInboxEventResponse
	credentialToken string
}

func seedAgentCredentialTransportFixture(t *testing.T) agentCredentialTransportFixture {
	t.Helper()

	ctx := context.Background()
	agentName := "Agent Credential Transport " + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	channelID := seedChannelForTest(t, "agent-credential-transport-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent channel member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "[@"+agentName+"](mention://agent/"+agentID+") credential transport", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("agent-credential-transport"), 0)
	if err != nil {
		t.Fatalf("insert mention trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))

	drainReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agent-inbox/drain", nil, testWorkspaceID, "agent-credential-transport-daemon")
	drainReq = withURLParam(drainReq, "runtimeId", runtimeID)
	drainRec := httptest.NewRecorder()
	testHandler.DrainAgentInboxByRuntime(drainRec, drainReq)
	if drainRec.Code != http.StatusOK {
		t.Fatalf("drain inbox: status=%d body=%s", drainRec.Code, drainRec.Body.String())
	}
	var drainResp DrainAgentInboxResponse
	if err := json.Unmarshal(drainRec.Body.Bytes(), &drainResp); err != nil {
		t.Fatalf("decode drain response: %v", err)
	}
	if len(drainResp.Events) != 1 {
		t.Fatalf("drain response events=%d, want 1: %s", len(drainResp.Events), drainRec.Body.String())
	}
	event := drainResp.Events[0]
	if event.Task == nil {
		t.Fatalf("drain response missing task: %s", drainRec.Body.String())
	}

	rawToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate agent credential token: %v", err)
	}
	if _, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: tokenPrefixForTest(rawToken),
		AgentID:     parseUUID(agentID),
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(testUserID),
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("create agent credential: %v", err)
	}
	return agentCredentialTransportFixture{
		agentID:         agentID,
		channelID:       channelID,
		event:           event,
		credentialToken: rawToken,
	}
}

func TestAgentCredentialAuthSetsBoundActorHeaders(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "agent-credential-auth", nil)
	rawToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate agent credential token: %v", err)
	}
	credential, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: tokenPrefixForTest(rawToken),
		AgentID:     parseUUID(agentID),
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(testUserID),
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("create agent credential: %v", err)
	}

	var gotActorSource, gotUserID, gotAgentID, gotCredentialID, gotWorkspaceID, gotTaskID string
	handler := middleware.Auth(testHandler.Queries, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotActorSource = r.Header.Get("X-Actor-Source")
		gotUserID = r.Header.Get("X-User-ID")
		gotAgentID = r.Header.Get("X-Agent-ID")
		gotCredentialID = r.Header.Get("X-Agent-Credential-ID")
		gotWorkspaceID = r.Header.Get("X-Workspace-ID")
		gotTaskID = r.Header.Get("X-Task-ID")
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Workspace-ID", "forged-workspace")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if gotActorSource != "agent_credential" {
		t.Fatalf("X-Actor-Source = %q, want agent_credential", gotActorSource)
	}
	if gotUserID != testUserID || gotAgentID != agentID || gotCredentialID != uuidToString(credential.ID) || gotWorkspaceID != testWorkspaceID {
		t.Fatalf("bound headers mismatch: user=%q agent=%q credential=%q workspace=%q", gotUserID, gotAgentID, gotCredentialID, gotWorkspaceID)
	}
	if gotTaskID != "" {
		t.Fatalf("agent credential auth must not synthesize X-Task-ID, got %q", gotTaskID)
	}

	var lastUsed pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `SELECT last_used_at FROM agent_credential WHERE id = $1`, credential.ID).Scan(&lastUsed); err != nil {
		t.Fatalf("load last_used_at: %v", err)
	}
	if !lastUsed.Valid {
		t.Fatal("expected agent credential auth to touch last_used_at")
	}

	if _, err := testHandler.Queries.RevokeAgentCredential(ctx, credential.ID); err != nil {
		t.Fatalf("revoke agent credential: %v", err)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked credential status = %d, want 401", w.Code)
	}
}

func TestAgentEnv_AgentCredentialActorSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	targetID := createHandlerTestAgent(t, "env-agent-credential-target", nil)
	hostAgentID := createHandlerTestAgent(t, "env-agent-credential-host", nil)
	if _, err := testPool.Exec(context.Background(), `UPDATE agent SET custom_env = '{"K":"v"}' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("failed to set custom_env: %v", err)
	}

	req := newRequest(http.MethodGet, "/api/agents/"+targetID+"/env", nil)
	req = withURLParam(req, "id", targetID)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", hostAgentID)
	req.Header.Del("X-Task-ID")
	w := httptest.NewRecorder()
	testHandler.GetAgentEnv(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when X-Actor-Source=agent_credential, got %d: %s", w.Code, w.Body.String())
	}
}

func tokenPrefixForTest(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}
