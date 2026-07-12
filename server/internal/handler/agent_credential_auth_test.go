package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

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
