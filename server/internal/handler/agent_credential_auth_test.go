package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

func TestAgentCredentialTransportStillForbidden(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	req := newRequest(http.MethodPost, "/api/agent/messages/send", nil)
	req.Header.Set("X-Actor-Source", "agent_credential")
	req.Header.Set("X-Agent-ID", "00000000-0000-0000-0000-000000000001")
	w := httptest.NewRecorder()
	if _, ok := testHandler.requireAgentTransportSource(w, req); ok {
		t.Fatal("agent_credential must not be accepted by generic transport before freshness/delivery validation lands")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
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
