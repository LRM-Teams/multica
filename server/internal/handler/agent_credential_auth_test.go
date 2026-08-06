package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/middleware"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentCredentialAuditFailTxStarter struct {
	inner txStarter
}

func (s *agentCredentialAuditFailTxStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &agentCredentialAuditFailTx{Tx: tx}, nil
}

type agentCredentialAuditFailTx struct {
	pgx.Tx
}

type agentCredentialFailRow struct {
	err error
}

func (r agentCredentialFailRow) Scan(...any) error {
	return r.err
}

func (t *agentCredentialAuditFailTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "INSERT INTO activity_log") {
		for _, arg := range args {
			if action, ok := arg.(string); ok && action == agentCredentialActivityDaemonEnsured {
				return agentCredentialFailRow{err: errors.New("injected daemon credential audit failure")}
			}
		}
	}
	return t.Tx.QueryRow(ctx, sql, args...)
}

func installAgentCredentialAuditFailure(h *Handler) func() {
	previous := h.TxStarter
	h.TxStarter = &agentCredentialAuditFailTxStarter{inner: previous}
	return func() {
		h.TxStarter = previous
	}
}

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

func TestCreateAgentCredential_AllowsCallerChosenNoExpiry(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Product decision (Frank, 2026-07-28): manually issued agent credentials
	// may be non-expiring, like GitHub PATs. Tightening this requires a new
	// product decision; archive/member-delete revocation remains mandatory.
	seedHandlerTestRuntimeOwner(t, testUserID)
	agentID := createHandlerTestAgent(t, "agent-credential-no-expiry", nil)
	req := withURLParam(newRequest(http.MethodPost, "/api/agents/"+agentID+"/credentials", map[string]any{}), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.CreateAgentCredential(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgentCredential: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateAgentCredentialResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" || resp.ExpiresAt != nil {
		t.Fatalf("manual no-expiry credential response = %#v, want token with nil expires_at", resp)
	}

	var expiresAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `
		SELECT expires_at
		FROM agent_credential
		WHERE id = $1
	`, resp.ID).Scan(&expiresAt); err != nil {
		t.Fatalf("load manual credential expiry: %v", err)
	}
	if expiresAt.Valid {
		t.Fatalf("manual credential expires_at = %v, want NULL", expiresAt)
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

func TestEnsureDaemonAgentCredential_ReusesOnlyLiveCachedCredential(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	daemonID := "agent-credential-reuse-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-reuse", nil)

	ensure := func(body any) (int, CreateAgentCredentialResponse) {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", body, testWorkspaceID, daemonID)
		req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
		w := httptest.NewRecorder()
		testHandler.EnsureDaemonAgentCredential(w, req)
		var resp CreateAgentCredentialResponse
		if w.Body.Len() > 0 {
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
			}
		}
		return w.Code, resp
	}

	status, issued := ensure(nil)
	if status != http.StatusCreated || issued.Token == "" || issued.ID == "" {
		t.Fatalf("initial ensure = %d %#v, want newly issued credential", status, issued)
	}

	status, reused := ensure(map[string]any{"credential_id": issued.ID})
	if status != http.StatusOK || !reused.Reused || reused.ID != issued.ID || reused.Token != "" {
		t.Fatalf("reuse ensure = %d %#v, want tokenless exact-ID reuse", status, reused)
	}

	if _, err := testHandler.Queries.RevokeAgentCredential(context.Background(), parseUUID(issued.ID)); err != nil {
		t.Fatalf("revoke cached credential: %v", err)
	}
	status, rotated := ensure(map[string]any{"credential_id": issued.ID})
	if status != http.StatusCreated || rotated.Token == "" || rotated.ID == issued.ID || rotated.RotationReason != "revoked" {
		t.Fatalf("revoked ensure = %d %#v, want rotated credential", status, rotated)
	}
	var auditReused bool
	var auditReason string
	if err := testPool.QueryRow(context.Background(), `
		SELECT (details->>'reused')::boolean, details->>'rotation_reason'
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'agent_id' = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, agentCredentialActivityDaemonEnsured, agentID).Scan(&auditReused, &auditReason); err != nil {
		t.Fatalf("load daemon ensure audit: %v", err)
	}
	if auditReused || auditReason != "revoked" {
		t.Fatalf("daemon ensure audit reused/reason = %v/%q, want false/revoked", auditReused, auditReason)
	}
}

func TestEnsureDaemonAgentCredential_RevokesSupersededLiveCachedCredentialOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "agent-credential-supersede-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-supersede", nil)

	manualToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate manual sibling credential token: %v", err)
	}
	manualCredential, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(manualToken),
		TokenPrefix: tokenPrefix(manualToken),
		AgentID:     parseUUID(agentID),
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(testUserID),
		ExpiresAt:   pgtype.Timestamptz{},
	})
	if err != nil {
		t.Fatalf("create manual sibling credential: %v", err)
	}

	ensure := func(body any) (int, CreateAgentCredentialResponse) {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", body, testWorkspaceID, daemonID)
		req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
		w := httptest.NewRecorder()
		testHandler.EnsureDaemonAgentCredential(w, req)
		var resp CreateAgentCredentialResponse
		if w.Body.Len() > 0 {
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
			}
		}
		return w.Code, resp
	}

	status, issued := ensure(nil)
	if status != http.StatusCreated || issued.Token == "" || issued.ID == "" {
		t.Fatalf("initial ensure = %d %#v, want newly issued credential", status, issued)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_credential
		SET expires_at = now() + interval '30 minutes'
		WHERE id = $1
	`, issued.ID); err != nil {
		t.Fatalf("move cached credential into refresh window: %v", err)
	}

	status, rotated := ensure(map[string]any{"credential_id": issued.ID})
	if status != http.StatusCreated || rotated.Token == "" || rotated.ID == issued.ID || rotated.RotationReason != "near_expiry" {
		t.Fatalf("near-expiry ensure = %d %#v, want rotated credential", status, rotated)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(issued.Token)); !isNotFound(err) {
		t.Fatalf("superseded credential remains live: %v", err)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(rotated.Token)); err != nil {
		t.Fatalf("replacement credential is not live: %v", err)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(manualToken)); err != nil {
		t.Fatalf("manual sibling credential was revoked: %v", err)
	}

	var (
		revokedAt                   pgtype.Timestamptz
		auditSupersededCredentialID string
		auditSupersededRevoked      bool
	)
	if err := testPool.QueryRow(ctx, `
		SELECT revoked_at
		FROM agent_credential
		WHERE id = $1
	`, issued.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("load superseded credential: %v", err)
	}
	if !revokedAt.Valid {
		t.Fatal("superseded credential revoked_at is NULL")
	}
	if err := testPool.QueryRow(ctx, `
		SELECT
			details->>'superseded_credential_id',
			(details->>'superseded_credential_revoked')::boolean
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'agent_id' = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, agentCredentialActivityDaemonEnsured, agentID).Scan(
		&auditSupersededCredentialID,
		&auditSupersededRevoked,
	); err != nil {
		t.Fatalf("load daemon rotation audit: %v", err)
	}
	if auditSupersededCredentialID != issued.ID || !auditSupersededRevoked {
		t.Fatalf(
			"daemon rotation audit superseded id/revoked = %q/%v, want %q/true",
			auditSupersededCredentialID,
			auditSupersededRevoked,
			issued.ID,
		)
	}

	status, retried := ensure(map[string]any{"credential_id": issued.ID})
	if status != http.StatusCreated || retried.Token == "" || retried.ID == issued.ID || retried.ID == rotated.ID || retried.RotationReason != "revoked" {
		t.Fatalf("replayed superseded ensure = %d %#v, want one replacement for revoked old credential", status, retried)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(rotated.Token)); !isNotFound(err) {
		t.Fatalf("first replacement remains live after replaying superseded credential: %v", err)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(retried.Token)); err != nil {
		t.Fatalf("retry replacement credential is not live: %v", err)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(manualToken)); err != nil {
		t.Fatalf("manual sibling credential was revoked by replay cleanup: %v", err)
	}

	status, fromManual := ensure(map[string]any{"credential_id": uuidToString(manualCredential.ID)})
	if status != http.StatusCreated || fromManual.Token == "" || fromManual.ID == uuidToString(manualCredential.ID) || fromManual.RotationReason != "not_daemon_issued" {
		t.Fatalf("manual cached ensure = %d %#v, want separate daemon credential", status, fromManual)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(manualToken)); err != nil {
		t.Fatalf("submitted manual credential was revoked: %v", err)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(retried.Token)); !isNotFound(err) {
		t.Fatalf("prior daemon credential remains live after manual-id ensure: %v", err)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(fromManual.Token)); err != nil {
		t.Fatalf("daemon credential issued from manual-id ensure is not live: %v", err)
	}
}

func TestEnsureDaemonAgentCredential_ConcurrentMissingCacheLeavesOneDaemonCredential(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "agent-credential-concurrent-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-concurrent", nil)

	type ensureResult struct {
		status int
		body   CreateAgentCredentialResponse
		err    error
	}
	start := make(chan struct{})
	results := make(chan ensureResult, 2)
	for range 2 {
		go func() {
			<-start
			req := newDaemonTokenRequest(
				http.MethodPost,
				"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential",
				nil,
				testWorkspaceID,
				daemonID,
			)
			req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
			w := httptest.NewRecorder()
			testHandler.EnsureDaemonAgentCredential(w, req)
			var resp CreateAgentCredentialResponse
			err := json.NewDecoder(w.Body).Decode(&resp)
			results <- ensureResult{status: w.Code, body: resp, err: err}
		}()
	}
	close(start)

	responses := make([]CreateAgentCredentialResponse, 0, 2)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("decode concurrent ensure response: %v", result.err)
		}
		if result.status != http.StatusCreated || result.body.Token == "" || result.body.ID == "" {
			t.Fatalf("concurrent ensure = %d %#v, want newly issued credential", result.status, result.body)
		}
		responses = append(responses, result.body)
	}
	if responses[0].ID == responses[1].ID {
		t.Fatalf("concurrent ensures returned the same raw credential: %q", responses[0].ID)
	}

	var unrevokedDaemonCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_credential
		WHERE agent_id = $1
		  AND workspace_id = $2
		  AND user_id = $3
		  AND issuance_source = 'daemon'
		  AND revoked_at IS NULL
	`, agentID, testWorkspaceID, testUserID).Scan(&unrevokedDaemonCount); err != nil {
		t.Fatalf("count unrevoked daemon credentials: %v", err)
	}
	if unrevokedDaemonCount != 1 {
		t.Fatalf("unrevoked daemon credentials = %d, want 1", unrevokedDaemonCount)
	}
	duplicateToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate duplicate daemon credential token: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_credential (
			token_hash,
			token_prefix,
			agent_id,
			workspace_id,
			user_id,
			expires_at,
			issuance_source
		)
		VALUES ($1, $2, $3, $4, $5, now() + interval '24 hours', 'daemon')
	`, auth.HashToken(duplicateToken), tokenPrefix(duplicateToken), agentID, testWorkspaceID, testUserID); !isUniqueViolation(err) {
		t.Fatalf("second unrevoked daemon credential error = %v, want unique violation", err)
	}

	authenticCount := 0
	for _, response := range responses {
		if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(response.Token)); err == nil {
			authenticCount++
		} else if !isNotFound(err) {
			t.Fatalf("authenticate concurrent credential %q: %v", response.ID, err)
		}
	}
	if authenticCount != 1 {
		t.Fatalf("authentic concurrent credentials = %d, want 1", authenticCount)
	}
}

func TestEnsureDaemonAgentCredential_DoesNotRevokeScopeMismatchCredential(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "agent-credential-scope-mismatch-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-daemon-scope-target", nil)
	otherAgentID := createHandlerTestAgent(t, "agent-credential-daemon-scope-other", nil)

	otherToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate other credential token: %v", err)
	}
	otherCredential, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(otherToken),
		TokenPrefix: tokenPrefix(otherToken),
		AgentID:     parseUUID(otherAgentID),
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(testUserID),
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(daemonAgentCredentialTTL), Valid: true},
	})
	if err != nil {
		t.Fatalf("create other agent credential: %v", err)
	}

	req := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential",
		map[string]any{"credential_id": uuidToString(otherCredential.ID)},
		testWorkspaceID,
		daemonID,
	)
	req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
	w := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(w, req)

	var resp CreateAgentCredentialResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	if w.Code != http.StatusCreated || resp.Token == "" || resp.RotationReason != "not_found_or_scope_mismatch" {
		t.Fatalf("scope-mismatch ensure = %d %#v, want safe replacement", w.Code, resp)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(otherToken)); err != nil {
		t.Fatalf("scope-mismatch credential was revoked: %v", err)
	}

	var recordedSuperseded bool
	if err := testPool.QueryRow(ctx, `
		SELECT details ? 'superseded_credential_id'
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'agent_id' = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, agentCredentialActivityDaemonEnsured, agentID).Scan(&recordedSuperseded); err != nil {
		t.Fatalf("load scope-mismatch audit: %v", err)
	}
	if recordedSuperseded {
		t.Fatal("scope-mismatch audit incorrectly records a superseded credential")
	}
}

func TestEnsureDaemonAgentCredential_DoesNotRevokePreviousRuntimeOwnerCredential(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "agent-credential-owner-scope-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-owner-scope", nil)

	oldOwnerToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate previous owner credential token: %v", err)
	}
	oldOwnerCredential, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(oldOwnerToken),
		TokenPrefix: tokenPrefix(oldOwnerToken),
		AgentID:     parseUUID(agentID),
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(testUserID),
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(30 * time.Minute), Valid: true},
	})
	if err != nil {
		t.Fatalf("create previous owner credential: %v", err)
	}

	newOwnerID := createWorkspaceMemberUser(
		t,
		"Credential Runtime Owner "+uuid.NewString()[:8],
		"credential-runtime-owner-"+uuid.NewString()+"@multica.test",
	)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_runtime
		SET owner_id = $1
		WHERE id = $2
	`, newOwnerID, runtimeID); err != nil {
		t.Fatalf("change runtime owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			UPDATE agent_runtime
			SET owner_id = $1
			WHERE id = $2
		`, testUserID, runtimeID)
	})

	req := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential",
		map[string]any{"credential_id": uuidToString(oldOwnerCredential.ID)},
		testWorkspaceID,
		daemonID,
	)
	req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
	w := httptest.NewRecorder()
	testHandler.EnsureDaemonAgentCredential(w, req)

	var resp CreateAgentCredentialResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, w.Body.String())
	}
	if w.Code != http.StatusCreated || resp.Token == "" || resp.RotationReason != "not_found_or_scope_mismatch" {
		t.Fatalf("previous-owner ensure = %d %#v, want current-owner replacement", w.Code, resp)
	}
	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(oldOwnerToken)); err != nil {
		t.Fatalf("previous runtime owner credential was revoked: %v", err)
	}

	var newCredentialOwnerID string
	if err := testPool.QueryRow(ctx, `
		SELECT user_id::text
		FROM agent_credential
		WHERE id = $1
	`, resp.ID).Scan(&newCredentialOwnerID); err != nil {
		t.Fatalf("load replacement credential owner: %v", err)
	}
	if newCredentialOwnerID != newOwnerID {
		t.Fatalf("replacement credential owner = %s, want %s", newCredentialOwnerID, newOwnerID)
	}
}

func TestEnsureDaemonAgentCredential_AuditFailureRollsBackRotation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	daemonID := "agent-credential-audit-rollback-" + uuid.NewString()
	runtimeID := handlerTestRuntimeID(t)
	seedHandlerTestRuntimeOwner(t, testUserID)
	seedHandlerTestRuntimeDaemonID(t, daemonID)
	agentID := createHandlerTestAgent(t, "agent-credential-audit-rollback", nil)

	ensure := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/agents/"+agentID+"/credential", body, testWorkspaceID, daemonID)
		req = withRouteParams(req, "runtimeId", runtimeID, "agentId", agentID)
		w := httptest.NewRecorder()
		testHandler.EnsureDaemonAgentCredential(w, req)
		return w
	}

	issuedRec := ensure(nil)
	var issued CreateAgentCredentialResponse
	if err := json.NewDecoder(issuedRec.Body).Decode(&issued); err != nil {
		t.Fatalf("decode initial response: %v; body=%s", err, issuedRec.Body.String())
	}
	if issuedRec.Code != http.StatusCreated || issued.Token == "" || issued.ID == "" {
		t.Fatalf("initial ensure = %d %#v, want newly issued credential", issuedRec.Code, issued)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_credential
		SET expires_at = now() + interval '30 minutes'
		WHERE id = $1
	`, issued.ID); err != nil {
		t.Fatalf("move cached credential into refresh window: %v", err)
	}

	var (
		credentialsBefore int
		auditsBefore      int
	)
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_credential
		WHERE agent_id = $1
	`, agentID).Scan(&credentialsBefore); err != nil {
		t.Fatalf("count credentials before failed rotation: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'agent_id' = $3
	`, testWorkspaceID, agentCredentialActivityDaemonEnsured, agentID).Scan(&auditsBefore); err != nil {
		t.Fatalf("count audits before failed rotation: %v", err)
	}

	restore := installAgentCredentialAuditFailure(testHandler)
	defer restore()
	rotatedRec := ensure(map[string]any{"credential_id": issued.ID})
	if rotatedRec.Code != http.StatusInternalServerError {
		t.Fatalf("audit failure rotate = %d %s, want 500", rotatedRec.Code, rotatedRec.Body.String())
	}

	if _, err := testHandler.Queries.GetAgentCredentialByHash(ctx, auth.HashToken(issued.Token)); err != nil {
		t.Fatalf("old credential is not live after rollback: %v", err)
	}
	var (
		revokedAt        pgtype.Timestamptz
		credentialsAfter int
		auditsAfter      int
	)
	if err := testPool.QueryRow(ctx, `
		SELECT revoked_at
		FROM agent_credential
		WHERE id = $1
	`, issued.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("load old credential after rollback: %v", err)
	}
	if revokedAt.Valid {
		t.Fatalf("old credential revoked_at = %v after audit rollback", revokedAt.Time)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_credential
		WHERE agent_id = $1
	`, agentID).Scan(&credentialsAfter); err != nil {
		t.Fatalf("count credentials after failed rotation: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'agent_id' = $3
	`, testWorkspaceID, agentCredentialActivityDaemonEnsured, agentID).Scan(&auditsAfter); err != nil {
		t.Fatalf("count audits after failed rotation: %v", err)
	}
	if credentialsAfter != credentialsBefore || auditsAfter != auditsBefore {
		t.Fatalf(
			"failed rotation persisted credentials/audits = %d/%d, want %d/%d",
			credentialsAfter,
			auditsAfter,
			credentialsBefore,
			auditsBefore,
		)
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
		"target":            "#" + channelNameForTransportTest(t, fixture.channelID),
		"content":           "credential transport delivery",
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

func TestAgentCredentialArchiveRevokesImmediatelyAndRestoreDoesNotResurrect(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "agent-credential-archive-boundary", nil)
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

	authHandler := middleware.Auth(testHandler.Queries, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)

	w := httptest.NewRecorder()
	authHandler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("precondition auth status = %d, want 204", w.Code)
	}

	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	w = httptest.NewRecorder()
	authHandler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("archived agent credential status = %d, want 401", w.Code)
	}

	var revokedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `SELECT revoked_at FROM agent_credential WHERE id = $1`, credential.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("load revoked_at: %v", err)
	}
	if !revokedAt.Valid {
		t.Fatal("archive boundary must durably revoke the credential")
	}
	var reason string
	var revokedCount int
	if err := testPool.QueryRow(ctx, `
		SELECT details->>'reason', (details->>'revoked_count')::integer
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_credential_revoked'
		  AND details->>'agent_id' = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, agentID).Scan(&reason, &revokedCount); err != nil {
		t.Fatalf("load archive revoke audit: %v", err)
	}
	if reason != "agent_archived" || revokedCount != 1 {
		t.Fatalf("archive revoke audit reason/count = %q/%d, want agent_archived/1", reason, revokedCount)
	}

	if _, err := testPool.Exec(ctx, `UPDATE agent SET archived_at = NULL, archived_by = NULL WHERE id = $1`, agentID); err != nil {
		t.Fatalf("restore agent: %v", err)
	}
	w = httptest.NewRecorder()
	authHandler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("restored agent old credential status = %d, want 401", w.Code)
	}
}

func TestAgentCredentialOwnerMemberDeleteRevokesImmediately(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, _, memberID := privateAgentTestFixture(t)
	rawToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate agent credential token: %v", err)
	}
	credential, err := testHandler.Queries.CreateAgentCredential(ctx, db.CreateAgentCredentialParams{
		TokenHash:   auth.HashToken(rawToken),
		TokenPrefix: tokenPrefixForTest(rawToken),
		AgentID:     parseUUID(agentID),
		WorkspaceID: parseUUID(testWorkspaceID),
		UserID:      parseUUID(memberID),
		ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("create member-bound credential: %v", err)
	}

	if _, err := testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, memberID); err != nil {
		t.Fatalf("delete owner membership: %v", err)
	}

	var revokedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `SELECT revoked_at FROM agent_credential WHERE id = $1`, credential.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("load revoked_at: %v", err)
	}
	if !revokedAt.Valid {
		t.Fatal("member delete boundary must durably revoke the credential")
	}
	var reason string
	var revokedCount int
	if err := testPool.QueryRow(ctx, `
		SELECT details->>'reason', (details->>'revoked_count')::integer
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_credential_revoked'
		  AND details->>'owner_user_id' = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, memberID).Scan(&reason, &revokedCount); err != nil {
		t.Fatalf("load member revoke audit: %v", err)
	}
	if reason != "owner_membership_deleted" || revokedCount != 1 {
		t.Fatalf("member revoke audit reason/count = %q/%d, want owner_membership_deleted/1", reason, revokedCount)
	}

	authHandler := middleware.Auth(testHandler.Queries, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	authHandler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("deleted-member credential status = %d, want 401", w.Code)
	}
}

func TestAgentCredentialInsertSerializesWithArchive(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "agent-credential-archive-race", nil)
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin archive tx: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent in tx: %v", err)
	}

	insertDone := make(chan error, 1)
	go func() {
		rawToken, tokenErr := auth.GenerateAgentCredentialToken()
		if tokenErr != nil {
			insertDone <- tokenErr
			return
		}
		_, insertErr := testHandler.Queries.CreateAgentCredential(context.Background(), db.CreateAgentCredentialParams{
			TokenHash:   auth.HashToken(rawToken),
			TokenPrefix: tokenPrefixForTest(rawToken),
			AgentID:     parseUUID(agentID),
			WorkspaceID: parseUUID(testWorkspaceID),
			UserID:      parseUUID(testUserID),
			ExpiresAt:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		})
		insertDone <- insertErr
	}()

	select {
	case err := <-insertDone:
		t.Fatalf("credential insert crossed uncommitted archive boundary: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit archive tx: %v", err)
	}
	select {
	case err := <-insertDone:
		if err == nil {
			t.Fatal("credential insert after archive commit must fail closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("credential insert did not unblock after archive commit")
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

// LRM-1055: ambient / channel_role_changed wakes have channel_id but no
// chat_session_id. Transport auth must resolve origin from the channel and
// not hard-reject on the missing session.
func TestAgentCredentialTransportAllowsChannelBoundWakeWithoutChatSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	fixture := seedAgentCredentialTransportFixture(t)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET chat_session_id = NULL,
		    reason = 'channel_role_changed',
		    delivery_mode = 'execute',
		    response_mode = 'public_response'
		WHERE id = $1`, fixture.event.ID); err != nil {
		t.Fatalf("clear chat_session on channel-bound wake: %v", err)
	}

	event, err := testHandler.Queries.GetAgentInboxEvent(ctx, parseUUID(fixture.event.ID))
	if err != nil {
		t.Fatalf("reload inbox event: %v", err)
	}
	if event.ChatSessionID.Valid {
		t.Fatal("expected chat_session_id cleared")
	}
	origin, ok := testHandler.chatOutputOriginForTask(ctx, event)
	if !ok || uuidToString(origin.channelID) != fixture.channelID || uuidToString(origin.agentID) != fixture.agentID {
		t.Fatalf("channel origin = %+v ok=%v, want channel=%s agent=%s", origin, ok, fixture.channelID, fixture.agentID)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(testHandler.Queries, nil, nil))
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Post("/api/agent/messages/read", testHandler.AgentTransportReadMessages)
		r.Post("/api/agent/messages/send", testHandler.AgentTransportSendMessage)
		r.Post("/api/agent/reminders/list", testHandler.AgentTransportListReminders)
	})

	authHeaders := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+fixture.credentialToken)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		req.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
		req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
		req.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	}

	target := "#" + channelNameForTransportTest(t, fixture.channelID)
	readReq := newRequest(http.MethodPost, "/api/agent/messages/read", map[string]any{
		"target": target,
		"limit":  5,
	})
	authHeaders(readReq)
	readRec := httptest.NewRecorder()
	router.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("channel-bound read without chat_session: status=%d body=%s", readRec.Code, readRec.Body.String())
	}

	listReq := newRequest(http.MethodPost, "/api/agent/reminders/list", map[string]any{"status": "active"})
	authHeaders(listReq)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("channel-bound reminder list without chat_session: status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	clientID := "lrm-1055-role-wake-" + uuid.NewString()
	sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":            target,
		"content":           "channel role wake reply",
		"client_message_id": clientID,
	})
	authHeaders(sendReq)
	sendRec := httptest.NewRecorder()
	router.ServeHTTP(sendRec, sendReq)
	if sendRec.Code != http.StatusCreated {
		t.Fatalf("channel_role_changed send without chat_session: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
}

func TestAgentCredentialTransportAmbientManagerMaySpeakWithoutChatSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	fixture := seedAgentCredentialTransportFixture(t)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET chat_session_id = NULL,
		    reason = 'ambient',
		    delivery_mode = 'observe',
		    response_mode = 'no_public_output'
		WHERE id = $1`, fixture.event.ID); err != nil {
		t.Fatalf("convert wake to ambient observe: %v", err)
	}

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.Auth(testHandler.Queries, nil, nil))
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Post("/api/agent/messages/send", testHandler.AgentTransportSendMessage)
		r.Post("/api/agent/reminders/list", testHandler.AgentTransportListReminders)
	})
	authHeaders := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+fixture.credentialToken)
		req.Header.Set("X-Workspace-ID", testWorkspaceID)
		req.Header.Set("X-Agent-Inbox-Event-ID", fixture.event.ID)
		req.Header.Set("X-Agent-Inbox-Delivery-ID", fixture.event.DeliveryID)
		req.Header.Set("X-Agent-Inbox-Lease-Token", fixture.event.LeaseToken)
	}
	target := "#" + channelNameForTransportTest(t, fixture.channelID)

	// Non-manager ambient: list/read origin works, send stays blocked by response_mode.
	listReq := newRequest(http.MethodPost, "/api/agent/reminders/list", map[string]any{"status": "active"})
	authHeaders(listReq)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ambient reminder list: status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	blockedSend := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":            target,
		"content":           "blocked ambient reply",
		"client_message_id": "lrm-1055-ambient-blocked-" + uuid.NewString(),
	})
	authHeaders(blockedSend)
	blockedRec := httptest.NewRecorder()
	router.ServeHTTP(blockedRec, blockedSend)
	if blockedRec.Code != http.StatusForbidden {
		t.Fatalf("non-manager ambient send status=%d, want 403: %s", blockedRec.Code, blockedRec.Body.String())
	}
	if !strings.Contains(blockedRec.Body.String(), "no_public_output") {
		t.Fatalf("non-manager ambient send body=%s, want response_mode error", blockedRec.Body.String())
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE channel_member SET role = 'manager'
		WHERE member_type = 'agent' AND member_id = $1 AND channel_id = $2`,
		fixture.agentID, fixture.channelID); err != nil {
		t.Fatalf("promote ambient agent to channel manager: %v", err)
	}

	okSend := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
		"target":            target,
		"content":           "manager ambient reply",
		"client_message_id": "lrm-1055-ambient-manager-" + uuid.NewString(),
	})
	authHeaders(okSend)
	okRec := httptest.NewRecorder()
	router.ServeHTTP(okRec, okSend)
	if okRec.Code != http.StatusCreated {
		t.Fatalf("manager ambient send: status=%d body=%s", okRec.Code, okRec.Body.String())
	}
}

func TestChatOutputOriginForTaskFallsBackToChannelID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "origin-channel-fallback-"+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "origin-channel-fallback-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed channel member: %v", err)
	}

	task := db.AgentInboxEvent{
		WorkspaceID: parseUUID(testWorkspaceID),
		ChannelID:   parseUUID(channelID),
		AgentID:     parseUUID(agentID),
	}
	origin, ok := testHandler.chatOutputOriginForTask(ctx, task)
	if !ok {
		t.Fatal("expected channel_id origin fallback")
	}
	if uuidToString(origin.channelID) != channelID || uuidToString(origin.agentID) != agentID {
		t.Fatalf("origin=%+v, want channel=%s agent=%s", origin, channelID, agentID)
	}

	// Issue-only wake: no channel, no session → no transport origin.
	issueOnly := db.AgentInboxEvent{
		WorkspaceID: parseUUID(testWorkspaceID),
		AgentID:     parseUUID(agentID),
	}
	if _, ok := testHandler.chatOutputOriginForTask(ctx, issueOnly); ok {
		t.Fatal("issue-only wake must not resolve a channel transport origin")
	}
}
