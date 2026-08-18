package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func coverageCommitTestDaemon(t *testing.T) (*Daemon, *MessageCoordinator, CoverageOffer, string) {
	t.Helper()
	root := t.TempDir()
	coordinator := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	acceptCoverageTestMessage(t, coordinator, "message-1", "channel:one", 1)
	offer, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
	if err != nil {
		t.Fatalf("PrepareCoverage: %v", err)
	}
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerTestInbox(t, d, coordinator.key, "runtime-1", coordinator)
	token := prepareCoverageCommitCredential(t, d, coordinator.key, "runtime-1")
	return d, coordinator, offer, token
}

func prepareCoverageCommitCredential(t *testing.T, d *Daemon, key InboxKey, runtimeID string) string {
	t.Helper()
	transport, err := d.prepareAgentProxyCLITransport(key, runtimeID, "coverage-launch", filepath.Join(t.TempDir(), "multica"))
	if err != nil {
		t.Fatalf("prepare Agent Proxy credential: %v", err)
	}
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Errorf("close Agent Proxy credential: %v", err)
		}
	})
	token, err := os.ReadFile(transport.tokenFile)
	if err != nil {
		t.Fatalf("read Agent Proxy token: %v", err)
	}
	return strings.TrimSpace(string(token))
}

func coverageCommitRequest(t *testing.T, handler http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, MessageCoverageCommitPath, strings.NewReader(body))
	request.Header.Set(AgentProxyTokenHeader, token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestCredentialProxyCoverageCommitUsesOpaqueReceiptScopeAndIsIdempotent(t *testing.T) {
	d, coordinator, offer, token := coverageCommitTestDaemon(t)
	handler := d.credentialProxyMessageCoverageCommitHandler()

	for attempt := 1; attempt <= 2; attempt++ {
		body, err := json.Marshal(map[string]string{"receipt_id": offer.ReceiptID})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, MessageCoverageCommitPath, bytes.NewReader(body))
		request.Header.Set(AgentProxyTokenHeader, token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("commit attempt %d status=%d body=%s", attempt, recorder.Code, recorder.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode commit response: %v", err)
		}
		if response["status"] != "committed" || len(response) != 1 {
			t.Fatalf("commit response = %+v, want only committed status", response)
		}
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 1 {
		t.Fatalf("committed boundary = %d, want 1", got)
	}
}

func TestCredentialProxyCoverageCommitRejectsForgedOrCallerSuppliedAuthority(t *testing.T) {
	d, coordinator, offer, token := coverageCommitTestDaemon(t)
	handler := d.credentialProxyMessageCoverageCommitHandler()

	for name, body := range map[string]string{
		"forged receipt":   `{"receipt_id":"forged"}`,
		"caller Workspace": `{"receipt_id":"` + offer.ReceiptID + `","workspace_id":"workspace-2"}`,
		"caller Agent":     `{"receipt_id":"` + offer.ReceiptID + `","agent_id":"agent-2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := coverageCommitRequest(t, handler, token, body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("rejected commit advanced boundary to %d", got)
	}
}

func TestCredentialProxyCoverageCommitReportsExpiredReceipt(t *testing.T) {
	d, coordinator, offer, token := coverageCommitTestDaemon(t)
	coordinator.mu.Lock()
	receipt := coordinator.coverageReceipts[offer.ReceiptID]
	receipt.expiresAt = coordinator.coverageNow().Add(-1)
	coordinator.mu.Unlock()

	recorder := coverageCommitRequest(t, d.credentialProxyMessageCoverageCommitHandler(), token, `{"receipt_id":"`+offer.ReceiptID+`"}`)
	if recorder.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s, want 410", recorder.Code, recorder.Body.String())
	}
}

func TestCredentialProxyCoverageCommitRouteIsMachineLocalOnly(t *testing.T) {
	publicRouter, err := os.ReadFile(filepath.Join("..", "..", "cmd", "server", "router.go"))
	if err != nil {
		t.Fatalf("read public server router: %v", err)
	}
	if bytes.Contains(publicRouter, []byte("/credential-proxy/messages/coverage/commit")) {
		t.Fatal("coverage commit route leaked into the public service router")
	}

	d, _, offer, token := coverageCommitTestDaemon(t)
	recorder := coverageCommitRequest(t, d.credentialProxyMessageCoverageCommitHandler(), token, `{"receipt_id":"`+offer.ReceiptID+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("machine-local handler status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCredentialProxyCoverageCommitDoesNotReturnMessageOrBoundaryData(t *testing.T) {
	d, _, offer, token := coverageCommitTestDaemon(t)
	request := httptest.NewRequest(http.MethodPost, MessageCoverageCommitPath, strings.NewReader(`{"receipt_id":"`+offer.ReceiptID+`"}`))
	request.Header.Set(AgentProxyTokenHeader, token)
	recorder := httptest.NewRecorder()
	d.credentialProxyMessageCoverageCommitHandler().ServeHTTP(recorder, request)
	for _, forbidden := range []string{"message", "boundary", "workspace", "agent", "credential", "receipt"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("commit response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestCredentialProxyCommitCoverageRoutesToExactReceiptOwner(t *testing.T) {
	first := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"})
	second := newCoverageTestCoordinator(t, InboxKey{WorkspaceID: "workspace-2", AgentID: "agent-2"})
	firstMessage := testDelivery("message-1", "channel:one", 1, "delivery-1")
	secondMessage := testDelivery("message-2", "channel:two", 1, "delivery-2")
	if _, err := first.Accept(context.Background(), firstMessage); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Accept(context.Background(), secondMessage); err != nil {
		t.Fatal(err)
	}
	offer, err := second.PrepareCoverage(CoverageRequest{Kind: CoverageRead, Target: "channel:two", ThroughSeq: 1, Messages: []protocol.AgentMessageProjection{secondMessage.Message}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerTestInbox(t, d, first.key, "runtime-1", first)
	registerTestInbox(t, d, second.key, "runtime-2", second)

	token := prepareCoverageCommitCredential(t, d, second.key, "runtime-2")
	if err := d.CredentialProxy().CommitCoverage(token, offer.ReceiptID); err != nil {
		t.Fatalf("CommitCoverage: %v", err)
	}
	if first.Boundaries()["channel:one"] != 0 || second.Boundaries()["channel:two"] != 1 {
		t.Fatalf("boundaries first=%v second=%v", first.Boundaries(), second.Boundaries())
	}
}

func TestCredentialProxyCoverageCommitRejectsMissingOrWrongCredential(t *testing.T) {
	d, coordinator, offer, _ := coverageCommitTestDaemon(t)
	handler := d.credentialProxyMessageCoverageCommitHandler()
	body := `{"receipt_id":"` + offer.ReceiptID + `"}`

	for name, token := range map[string]string{"missing": "", "unknown": "map_forged"} {
		t.Run(name, func(t *testing.T) {
			recorder := coverageCommitRequest(t, handler, token, body)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s, want 401", recorder.Code, recorder.Body.String())
			}
		})
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("invalid credential advanced boundary to %d", got)
	}
}

func TestCredentialProxyCoverageCommitRejectsReceiptFromAnotherAuthenticatedInbox(t *testing.T) {
	d, coordinator, offer, _ := coverageCommitTestDaemon(t)
	wrongKey := InboxKey{WorkspaceID: "workspace-2", AgentID: "agent-2"}
	wrongCoordinator := newCoverageTestCoordinator(t, wrongKey)
	registerTestInbox(t, d, wrongKey, "runtime-2", wrongCoordinator)
	wrongToken := prepareCoverageCommitCredential(t, d, wrongKey, "runtime-2")

	recorder := coverageCommitRequest(t, d.credentialProxyMessageCoverageCommitHandler(), wrongToken, `{"receipt_id":"`+offer.ReceiptID+`"}`)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", recorder.Code, recorder.Body.String())
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("cross-Inbox credential advanced boundary to %d", got)
	}
}

func TestCredentialProxyCoverageCommitRejectsCredentialForAnotherWorkspace(t *testing.T) {
	d, coordinator, offer, _ := coverageCommitTestDaemon(t)
	wrongToken := prepareCoverageCommitCredential(t, d, InboxKey{WorkspaceID: "workspace-2", AgentID: "agent-1"}, "runtime-2")

	recorder := coverageCommitRequest(t, d.credentialProxyMessageCoverageCommitHandler(), wrongToken, `{"receipt_id":"`+offer.ReceiptID+`"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", recorder.Code, recorder.Body.String())
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("cross-Workspace credential advanced boundary to %d", got)
	}
}

func TestCredentialProxyCoverageCommitRecordsBoundedRunnerDiagnostic(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
	)
	key := InboxKey{WorkspaceID: workspaceID, AgentID: agentID}
	coordinator := newCoverageTestCoordinator(t, key)
	acceptCoverageTestMessage(t, coordinator, "message-1", "channel:one", 1)
	offer, err := coordinator.PrepareCoverage(CoverageRequest{Kind: CoverageCheck, Limit: 1})
	if err != nil {
		t.Fatalf("PrepareCoverage: %v", err)
	}
	d := New(Config{WorkspacesRoot: t.TempDir(), HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerTestInbox(t, d, key, runtimeID, coordinator)
	token := prepareCoverageCommitCredential(t, d, key, runtimeID)
	root := filepath.Join(t.TempDir(), "logs")
	store, err := diagnosticlog.Open(diagnosticlog.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d.runnerInstanceID = "runner-generation-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: diagnosticlog.EnvironmentProduction, runnerGeneration: d.runnerInstanceID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}

	recorder := coverageCommitRequest(t, d.credentialProxyMessageCoverageCommitHandler(), token, `{"receipt_id":"`+offer.ReceiptID+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	path := filepath.Join(root, "runners", "production", workspaceID+".log")
	records := readRunnerDiagnosticRecords(t, path)
	record := requireDiagnosticPhase(t, records, "coverage_commit")
	if record["event"] != string(diagnosticlog.EventDeliveryStateChanged) || record["component"] != "credential_proxy" {
		t.Fatalf("coverage diagnostic classification = %#v", record)
	}
	if record["outcome"] != "accepted" {
		t.Fatalf("coverage diagnostic outcome = %#v", record)
	}
	if _, found := record["reason_code"]; found {
		t.Fatalf("accepted coverage diagnostic invented reason_code: %#v", record)
	}
	if record["agent_id"] != agentID || record["runtime_id"] != runtimeID {
		t.Fatalf("coverage diagnostic identity = %#v", record)
	}
	assertDiagnosticFileExcludes(t, path, token, offer.ReceiptID, "message-1", "channel:one")
}

func TestCredentialProxyCoverageCommitInvalidCredentialLogIsSafe(t *testing.T) {
	var logs bytes.Buffer
	d, _, offer, _ := coverageCommitTestDaemon(t)
	d.logger = slog.New(slog.NewTextHandler(&logs, nil))
	forgedToken := "map_secret-canary"

	recorder := coverageCommitRequest(t, d.credentialProxyMessageCoverageCommitHandler(), forgedToken, `{"receipt_id":"`+offer.ReceiptID+`"}`)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(logs.String(), "invalid_agent_proxy_credential") {
		t.Fatalf("invalid credential rejection was not classified: %s", logs.String())
	}
	for _, forbidden := range []string{forgedToken, offer.ReceiptID, "workspace-1", "agent-1"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("invalid credential log leaked %q: %s", forbidden, logs.String())
		}
	}
}
