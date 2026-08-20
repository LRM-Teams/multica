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
	var daemonID pgtype.Text
	if err := testPool.QueryRow(context.Background(), `SELECT daemon_id FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&daemonID); err != nil {
		t.Fatalf("load runtime daemon_id: %v", err)
	}
	// LRM-1570: ownership is machine-level, established via an active
	// computer_workspace_bindings row for the runtime's daemon.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'handler-test-owner', TRUE)
	`, daemonID.String, testWorkspaceID, ownerID); err != nil {
		t.Fatalf("seed runtime owner binding: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`, daemonID.String, testWorkspaceID)
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

type agentCredentialTransportFixture struct {
	agentID         string
	channelID       string
	event           AgentInboxEventResponse
	credentialToken string
}

func seedAgentCredentialTransportFixture(t *testing.T) agentCredentialTransportFixture {
	t.Helper()

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	var deliveryID, leaseToken string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_event_delivery (
			workspace_id, agent_session_id, inbox_event_id, runtime_id,
			status, lease_expires_at
		)
		SELECT workspace_id, agent_session_id, id, runtime_id,
		       'leased', now() + interval '10 minutes'
		FROM agent_inbox_event
		WHERE id = $1
		RETURNING id::text, lease_token::text`, taskID).Scan(&deliveryID, &leaseToken); err != nil {
		t.Fatalf("create product task delivery fixture: %v", err)
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
		agentID:   agentID,
		channelID: channelID,
		event: AgentInboxEventResponse{
			ID:           taskID,
			DeliveryID:   deliveryID,
			LeaseToken:   leaseToken,
			ChannelID:    channelID,
			AgentID:      agentID,
			RequiresWake: true,
		},
		credentialToken: rawToken,
	}
}

func tokenPrefixForTest(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12]
}

func TestAgentCredentialChatTransportDoesNotUseActiveInboxContextOrTurn(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := seedAgentCredentialTransportFixture(t)
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
		t.Fatalf("credential read without inbox context: status=%d body=%s", readRec.Code, readRec.Body.String())
	}

	listReq := newRequest(http.MethodPost, "/api/agent/reminders/list", map[string]any{"status": "active"})
	authHeaders(listReq)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("credential reminder list without inbox context: status=%d body=%s", listRec.Code, listRec.Body.String())
	}

	// A durable credential authorizes every direct chat send. In particular,
	// the second send must not depend on an already-consumed Inbox lease or a
	// currently active runtime turn.
	messageIDs := make(map[string]struct{}, 2)
	for _, content := range []string{
		"credential chat without inbox context: first",
		"credential chat without inbox context: second",
	} {
		sendReq := newRequest(http.MethodPost, "/api/agent/messages/send", map[string]any{
			"target":            target,
			"content":           content,
			"client_message_id": "credential-chat-" + uuid.NewString(),
		})
		authHeaders(sendReq)
		sendRec := httptest.NewRecorder()
		router.ServeHTTP(sendRec, sendReq)
		if sendRec.Code != http.StatusCreated {
			t.Fatalf("credential send %q without inbox or turn context: status=%d body=%s", content, sendRec.Code, sendRec.Body.String())
		}
		var sent AgentTransportSendResponse
		if err := json.Unmarshal(sendRec.Body.Bytes(), &sent); err != nil {
			t.Fatalf("decode credential send %q: %v", content, err)
		}
		if sent.Message.ID == "" {
			t.Fatalf("credential send %q returned no message id: %+v", content, sent)
		}
		if _, duplicate := messageIDs[sent.Message.ID]; duplicate {
			t.Fatalf("credential sends reused message id %s", sent.Message.ID)
		}
		messageIDs[sent.Message.ID] = struct{}{}
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
