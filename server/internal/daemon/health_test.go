package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHealthHandlerReportsCLIVersionAndActiveTaskCount(t *testing.T) {
	t.Parallel()

	d := &Daemon{
		cfg: Config{
			CLIVersion:    "v9.9.9",
			DaemonID:      "daemon-test",
			DeviceName:    "dev",
			ServerBaseURL: "http://localhost:8080",
		},
		workspaces: map[string]*workspaceState{},
		logger:     slog.Default(),
	}
	d.activeTasks.Store(3)
	d.ready.Store(true) // preflight done -> status should be "running"

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	d.healthHandler(time.Now()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Decode into a raw map so the test locks in the exact wire-level JSON
	// keys — the desktop TS client depends on snake_case (cli_version,
	// active_task_count), so a silent struct-tag rename must fail here.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if got, want := raw["cli_version"], "v9.9.9"; got != want {
		t.Errorf("cli_version key: got %v, want %q", got, want)
	}
	// JSON numbers decode to float64 through map[string]any.
	if got, want := raw["active_task_count"], float64(3); got != want {
		t.Errorf("active_task_count key: got %v, want %v", got, want)
	}
	if got, want := raw["status"], "running"; got != want {
		t.Errorf("status key: got %v, want %q", got, want)
	}
	// The desktop relies on the `os` key (runtime.GOOS) to detect a daemon it
	// can't manage (e.g. Linux-in-WSL behind a Windows desktop). A rename or
	// drop would silently re-break #3916, so lock both the key and its value.
	if got, want := raw["os"], runtime.GOOS; got != want {
		t.Errorf("os key: got %v, want %q", got, want)
	}

	// Also round-trip into the typed struct as a separate check that the
	// field values match, independent of key naming.
	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode typed response: %v", err)
	}
	if resp.CLIVersion != "v9.9.9" {
		t.Errorf("CLIVersion: got %q, want %q", resp.CLIVersion, "v9.9.9")
	}
	if resp.ActiveTaskCount != 3 {
		t.Errorf("ActiveTaskCount: got %d, want 3", resp.ActiveTaskCount)
	}
}

func TestLocalMachineUpgradeControlRequiresProfileSecretAndUsesCanonicalOperation(t *testing.T) {
	var upstreamRequest map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemons/daemon-1/upgrades" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer daemon-owner-token" {
			t.Fatalf("upstream authorization = %q", got)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			t.Fatalf("upstream workspace = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(MachineUpgradeControlOperation{ID: "upgrade-1", DaemonID: "daemon-1", RequestedTarget: "v10.0.0", Phase: "queued"})
	}))
	defer upstream.Close()
	client := NewClient(upstream.URL)
	client.SetToken("daemon-owner-token")
	d := &Daemon{cfg: Config{DaemonID: "daemon-1", WorkspaceID: "workspace-1", LocalControlToken: "profile-secret"}, client: client}
	handler := d.localMachineUpgradeHandler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/machine-upgrades", bytes.NewBufferString(`{"request_id":"same-request","target_version":"v10.0.0"}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mutation status = %d, want 401", unauthorized.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/machine-upgrades", bytes.NewBufferString(`{"request_id":"same-request","target_version":"v10.0.0"}`))
	req.Header.Set("X-Multica-Control-Token", "profile-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated mutation status = %d body=%s", rec.Code, rec.Body.String())
	}
	if upstreamRequest["request_id"] != "same-request" || upstreamRequest["target_version"] != "v10.0.0" {
		t.Fatalf("canonical request = %#v", upstreamRequest)
	}
}

// TestHealthHandlerReportsStartingUntilReady pins the liveness/readiness split:
// the health server binds and answers before preflight finishes, but it must
// report "starting" until d.ready is set, and only then "running". Otherwise a
// slow or failing preflight would be misreported to `daemon start` (and the
// desktop) as a fully started daemon.
func TestHealthHandlerReportsStartingUntilReady(t *testing.T) {
	t.Parallel()

	d := &Daemon{
		cfg:        Config{CLIVersion: "v1.0.0"},
		workspaces: map[string]*workspaceState{},
		logger:     slog.Default(),
	}
	handler := d.healthHandler(time.Now())

	readStatus := func() string {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		var resp HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp.Status
	}

	if got := readStatus(); got != "starting" {
		t.Fatalf("status before ready: got %q, want \"starting\"", got)
	}

	d.ready.Store(true)

	if got := readStatus(); got != "running" {
		t.Fatalf("status after ready: got %q, want \"running\"", got)
	}
}

func TestHealthHandlerActiveTaskCountTracksCounter(t *testing.T) {
	t.Parallel()

	d := &Daemon{
		cfg:        Config{CLIVersion: "v1.0.0"},
		workspaces: map[string]*workspaceState{},
		logger:     slog.Default(),
	}
	handler := d.healthHandler(time.Now())

	// Simulate the pollLoop increment/decrement protocol.
	d.activeTasks.Add(1)
	d.activeTasks.Add(1)
	assertActiveTaskCount(t, handler, 2)

	d.activeTasks.Add(-1)
	assertActiveTaskCount(t, handler, 1)

	d.activeTasks.Add(-1)
	assertActiveTaskCount(t, handler, 0)
}

func TestShutdownHandlerPostCancelsDaemonContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Daemon{cancelFunc: cancel}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/shutdown", nil)
	d.shutdownHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("daemon context was not cancelled after POST /shutdown")
	}
}

func TestShutdownHandlerRejectsNonPost(t *testing.T) {
	t.Parallel()

	cancelled := false
	d := &Daemon{cancelFunc: func() { cancelled = true }}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/shutdown", nil)
	d.shutdownHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
	// Give the handler's deferred cancel goroutine a moment to fire
	// in case a bug causes it to run anyway.
	time.Sleep(10 * time.Millisecond)
	if cancelled {
		t.Fatal("GET request should not trigger cancellation")
	}
}

func TestEnvironmentSwitchPrepareWaitsForActiveWorkAndReleaseReopensClaims(t *testing.T) {
	d := &Daemon{cfg: Config{LocalControlToken: "owner-secret"}}
	d.activeTasks.Store(1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/environment-switch/prepare", nil)
	req.Header.Set("X-Multica-Control-Token", "owner-secret")
	done := make(chan struct{})
	go func() {
		d.localEnvironmentSwitchPrepareHandler().ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("prepare returned before active work drained")
	default:
	}
	d.activeTasks.Store(0)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prepare did not complete after active work drained")
	}
	if rec.Code != http.StatusOK || !d.claimBarrierDrained() || !d.environmentSwitchPrepared.Load() {
		t.Fatalf("prepare = status %d body %q barrier=%v owned=%v", rec.Code, rec.Body.String(), d.claimBarrierDrained(), d.environmentSwitchPrepared.Load())
	}

	releaseRec := httptest.NewRecorder()
	releaseReq := httptest.NewRequest(http.MethodPost, "/environment-switch/release", nil)
	releaseReq.Header.Set("X-Multica-Control-Token", "owner-secret")
	d.localEnvironmentSwitchReleaseHandler().ServeHTTP(releaseRec, releaseReq)
	if releaseRec.Code != http.StatusOK || d.pauseClaims || d.environmentSwitchPrepared.Load() {
		t.Fatalf("release = status %d body %q pause=%v owned=%v", releaseRec.Code, releaseRec.Body.String(), d.pauseClaims, d.environmentSwitchPrepared.Load())
	}
}

func TestEnvironmentSwitchPrepareRequiresOwnerControlToken(t *testing.T) {
	d := &Daemon{cfg: Config{LocalControlToken: "owner-secret"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/environment-switch/prepare", nil)
	d.localEnvironmentSwitchPrepareHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || d.pauseClaims || d.environmentSwitchPrepared.Load() {
		t.Fatalf("unauthorized prepare = status %d pause=%v owned=%v", rec.Code, d.pauseClaims, d.environmentSwitchPrepared.Load())
	}
}

func TestCredentialProxyMessageCheckDrainsCoordinatorWithoutExecutionIdentity(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for i := int64(1); i <= 4; i++ {
		delivery := testDelivery(fmt.Sprintf("message-%d", i), "channel:one", i, fmt.Sprintf("delivery-%d", i))
		if _, err := coordinator.Accept(context.Background(), delivery); err != nil {
			t.Fatalf("Accept: %v", err)
		}
	}
	d := &Daemon{
		messageCoordinators: map[string]*MessageCoordinator{"agent-1": coordinator},
	}
	handler := d.credentialProxyMessageCheckHandler()

	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/check", bytes.NewBufferString(`{"agent_id":"agent-1","task_id":"stale-task"}`)))
	if legacy.Code != http.StatusBadRequest {
		t.Fatalf("legacy task payload status = %d, want 400", legacy.Code)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/check", bytes.NewBufferString(`{"agent_id":"agent-1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("message check status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result MessageCheckResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode message check: %v", err)
	}
	if len(result.Messages) != messageCheckDefaultLimit || !result.HasMore || result.Remaining != 1 || result.CoverageReceipt == "" {
		t.Fatalf("message check result = %+v", result)
	}
	if got := coordinator.Boundaries(); len(got) != 0 {
		t.Fatalf("message check advanced boundary before CLI output: %+v", got)
	}
	if err := coordinator.CommitCoverage(result.CoverageReceipt); err != nil {
		t.Fatalf("commit checked coverage: %v", err)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != messageCheckDefaultLimit {
		t.Fatalf("committed check boundary = %d, want %d", got, messageCheckDefaultLimit)
	}
}

func TestCredentialProxyMessageReadUsesCachedCredentialAndWritesTargetBoundary(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)

	var upstreamBody map[string]any
	invalidCoverageResponse := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/read" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer cached-token" {
			t.Errorf("Authorization = %q, want cached Agent credential", got)
		}
		for header, want := range map[string]string{
			"X-Workspace-ID": "workspace-1", "X-Agent-ID": "agent-1",
		} {
			if got := r.Header.Get(header); got != want {
				t.Errorf("%s = %q, want %q", header, got, want)
			}
		}
		for _, header := range []string{"X-Task-ID", "X-Agent-Inbox-Event-ID", "X-Agent-Inbox-Delivery-ID", "X-Agent-Inbox-Lease-Token"} {
			if got := r.Header.Get(header); got != "" {
				t.Errorf("%s = %q, want absent", header, got)
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		messageTarget := "channel:one"
		if invalidCoverageResponse {
			messageTarget = "channel:other"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"action": "message_read", "target": "#one", "context_target": "channel:one",
			"seenUpToSeq": 7, "messages": []any{map[string]any{
				"id": "message-7", "target": messageTarget, "seq": 7, "content": "read context",
			}}, "limit": 2,
		})
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: root, ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "mac_test", Token: "cached-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	d := &Daemon{
		cfg:                 cfg,
		messageCoordinators: map[string]*MessageCoordinator{"agent-1": coordinator},
	}
	handler := d.credentialProxyMessageReadHandler()

	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/read", bytes.NewBufferString(`{"agent_id":"agent-1","task_id":"stale-task","workspace_id":"workspace-1","target":"#one"}`)))
	if legacy.Code != http.StatusBadRequest {
		t.Fatalf("legacy task payload status = %d, want 400", legacy.Code)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/read", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","around_seq":42,"limit":2}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("message read status = %d body=%s", rec.Code, rec.Body.String())
	}
	if upstreamBody["target"] != "#one" || upstreamBody["around_seq"] != float64(42) || upstreamBody["limit"] != float64(2) {
		t.Fatalf("upstream request = %+v", upstreamBody)
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode message read response: %v", err)
	}
	if _, found := response["context_target"]; found {
		t.Fatalf("proxy leaked Context Boundary target: %+v", response)
	}
	if _, found := response["seenUpToSeq"]; found {
		t.Fatalf("proxy leaked Context Boundary sequence: %+v", response)
	}
	receiptID, _ := response[MessageCoverageReceiptField].(string)
	if receiptID == "" {
		t.Fatalf("proxy omitted internal read coverage receipt: %+v", response)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 0 {
		t.Fatalf("message read advanced boundary before CLI output: %d", got)
	}
	if err := coordinator.CommitCoverage(receiptID); err != nil {
		t.Fatalf("commit read coverage: %v", err)
	}
	if got, err := d.CredentialProxy().SeenUpToSeq("agent-1", "channel:one"); err != nil || got != 7 {
		t.Fatalf("seen boundary = %d, %v; want 7, nil", got, err)
	}
	boundaries, healthy, err := loadConsumedSeqs(filepath.Join(root, consumedSeqsFileName))
	if err != nil || !healthy || boundaries["channel:one"] != 7 {
		t.Fatalf("durable boundaries = %+v healthy=%v err=%v", boundaries, healthy, err)
	}

	invalidCoverageResponse = true
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/read", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","limit":2}`)))
	if invalid.Code != http.StatusBadGateway {
		t.Fatalf("cross-target read coverage status=%d body=%s, want 502", invalid.Code, invalid.Body.String())
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 7 {
		t.Fatalf("invalid read coverage changed boundary to %d", got)
	}
}

func TestCredentialProxySearchAndResolveNeverExposeOrPrepareCoverage(t *testing.T) {
	root := t.TempDir()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/messages/search":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action": "message_search", "results": []any{}, MessageCoverageReceiptField: "forged-service-receipt",
			})
		case "/api/agent/messages/resolve":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action": "message_resolve", "message": map[string]any{"id": "message-1"}, MessageCoverageReceiptField: "forged-service-receipt",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "mac_test", Token: "cached-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: cfg, messageCoordinators: map[string]*MessageCoordinator{}}

	for name, test := range map[string]struct {
		handler http.HandlerFunc
		body    string
	}{
		"search":  {d.credentialProxyMessageSearchHandler(), `{"agent_id":"agent-1","workspace_id":"workspace-1","query":"needle","limit":10}`},
		"resolve": {d.credentialProxyMessageResolveHandler(), `{"agent_id":"agent-1","workspace_id":"workspace-1","message_id":"message-1"}`},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/"+name, strings.NewReader(test.body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var response map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if _, leaked := response[MessageCoverageReceiptField]; leaked {
				t.Fatalf("non-covering %s leaked or invented a receipt: %#v", name, response)
			}
		})
	}
}

func TestCredentialProxyMessageSendSavesDraftBeforeNetworkAndClearsKnownSuccess(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)

	var targetCalls, sendCalls int
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, header := range []string{"X-Task-ID", "X-Agent-Inbox-Event-ID", "X-Agent-Inbox-Delivery-ID", "X-Agent-Inbox-Lease-Token"} {
			if got := r.Header.Get(header); got != "" {
				t.Errorf("%s = %q, want absent", header, got)
			}
		}
		switch r.URL.Path {
		case "/api/agent/messages/target":
			targetCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"target": "#one", "context_target": "channel:one"})
		case "/api/agent/messages/send":
			sendCalls++
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Errorf("decode send request: %v", err)
			}
			draft, found, err := NewMessageDraftStore(root).Load(DraftKey{WorkspaceID: "workspace-1", AgentID: "agent-1", Target: "#one"}, time.Now())
			if err != nil || !found || draft.ContextTarget != "channel:one" || draft.SeenUpToSeq != 0 || draft.IdempotencyKey != sent["client_message_id"] {
				t.Errorf("Draft was not atomically saved before upstream send: draft=%+v found=%v err=%v request=%+v", draft, found, err, sent)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"action": "message_send", "target": "#one", "created": true, "message": map[string]any{"id": "message-1", "client_message_id": "private"}, "transport_id": "private"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	d := credentialProxySendTestDaemon(t, root, upstream.URL, coordinator)
	rec := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","content":"hello"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("message send status=%d body=%s", rec.Code, rec.Body.String())
	}
	if targetCalls != 1 || sendCalls != 1 {
		t.Fatalf("target/send calls=%d/%d, want 1/1", targetCalls, sendCalls)
	}
	if sent["client_message_id"] == "" || sent["seen_up_to_seq"] != float64(0) || sent["context_target"] != "channel:one" {
		t.Fatalf("upstream send = %+v", sent)
	}
	if _, found, err := d.CredentialProxy().LoadMessageDraft("workspace-1", "agent-1", "#one", time.Now()); err != nil || found {
		t.Fatalf("successful send Draft found=%v err=%v", found, err)
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode send response: %v", err)
	}
	if _, leaked := response["transport_id"]; leaked {
		t.Fatalf("proxy leaked internal transport id: %+v", response)
	}
	if message, _ := response["message"].(map[string]any); message["client_message_id"] != nil {
		t.Fatalf("proxy leaked internal message identity: %+v", response)
	}
}

func TestCredentialProxyMessageSendKeepsDraftWhenUpstreamOutcomeIsUnknown(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/messages/target":
			_ = json.NewEncoder(w).Encode(map[string]any{"target": "#one", "context_target": "channel:one"})
		case "/api/agent/messages/send":
			http.Error(w, "outcome unavailable", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	d := credentialProxySendTestDaemon(t, root, upstream.URL, coordinator)
	rec := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","content":"hello"}`)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("message send status=%d body=%s", rec.Code, rec.Body.String())
	}
	draft, found, err := d.CredentialProxy().LoadMessageDraft("workspace-1", "agent-1", "#one", time.Now())
	if err != nil || !found || draft.IdempotencyKey == "" || draft.Content != "hello" {
		t.Fatalf("Draft after unknown outcome = %+v found=%v err=%v", draft, found, err)
	}
}

func TestCredentialProxyMessageSendHoldsLocalPendingWithoutUpstreamSend(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	for sequence := int64(1); sequence <= 4; sequence++ {
		if _, err := coordinator.Accept(context.Background(), testDelivery(fmt.Sprintf("message-%d", sequence), "channel:one", sequence, fmt.Sprintf("delivery-%d", sequence))); err != nil {
			t.Fatalf("Accept %d: %v", sequence, err)
		}
	}

	var sends int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/messages/target" {
			_ = json.NewEncoder(w).Encode(map[string]any{"target": "#one", "context_target": "channel:one"})
			return
		}
		if r.URL.Path == "/api/agent/messages/send" {
			sends++
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	d := credentialProxySendTestDaemon(t, root, upstream.URL, coordinator)
	rec := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","content":"reply"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("local hold status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sends != 0 {
		t.Fatalf("local Pending invoked upstream send %d times", sends)
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode local hold: %v", err)
	}
	if response["state"] != "held" || response["newMessageCount"] != float64(4) {
		t.Fatalf("local hold response=%+v", response)
	}
	if messages, _ := response["heldMessages"].([]any); len(messages) != 3 {
		t.Fatalf("held Messages=%+v, want newest three", response["heldMessages"])
	}
	receipt, _ := response[MessageCoverageReceiptField].(string)
	if receipt == "" {
		t.Fatalf("local hold omitted coverage receipt: %+v", response)
	}
	if boundary := coordinator.Boundaries()["channel:one"]; boundary != 0 {
		t.Fatalf("pre-output held boundary=%d, want 0", boundary)
	}
	if got := len(coordinator.pending["channel:one"]); got != 4 {
		t.Fatalf("pre-output Pending count=%d, want 4", got)
	}
	firstDraft, found, err := d.CredentialProxy().LoadMessageDraft("workspace-1", "agent-1", "#one", time.Now())
	if err != nil || !found || firstDraft.SeenUpToSeq != 4 || firstDraft.HoldCount != 1 {
		t.Fatalf("held Draft=%+v found=%v err=%v", firstDraft, found, err)
	}

	repeated := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(repeated, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","content":"reply"}`)))
	if repeated.Code != http.StatusOK {
		t.Fatalf("repeated local hold status=%d body=%s", repeated.Code, repeated.Body.String())
	}
	var repeatedResponse map[string]any
	if err := json.Unmarshal(repeated.Body.Bytes(), &repeatedResponse); err != nil {
		t.Fatalf("decode repeated local hold: %v", err)
	}
	repeatedReceipt, _ := repeatedResponse[MessageCoverageReceiptField].(string)
	if repeatedReceipt == "" {
		t.Fatalf("repeated local hold omitted coverage receipt: %+v", repeatedResponse)
	}
	repeatedDraft, found, err := d.CredentialProxy().LoadMessageDraft("workspace-1", "agent-1", "#one", time.Now())
	if err != nil || !found || repeatedDraft.IdempotencyKey != firstDraft.IdempotencyKey || repeatedDraft.HoldCount != 1 {
		t.Fatalf("repeated held Draft=%+v first=%+v found=%v err=%v", repeatedDraft, firstDraft, found, err)
	}
	if sends != 0 || len(coordinator.pending["channel:one"]) != 4 || coordinator.Boundaries()["channel:one"] != 0 {
		t.Fatalf("repeated hold sent=%d pending=%d boundary=%d", sends, len(coordinator.pending["channel:one"]), coordinator.Boundaries()["channel:one"])
	}
	if err := coordinator.CommitCoverage(repeatedReceipt); err != nil {
		t.Fatalf("commit held coverage: %v", err)
	}
	if boundary, known := coordinator.ContextBoundary("channel:one"); !known || boundary != 4 {
		t.Fatalf("committed held boundary=%d known=%v, want 4 true", boundary, known)
	}
}

func TestCredentialProxyMessageSendConsumesServerRaceHoldAndKeepsDraft(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if err := coordinator.MarkRead("channel:one", 2); err != nil {
		t.Fatalf("seed Context Boundary: %v", err)
	}
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/messages/target":
			_ = json.NewEncoder(w).Encode(map[string]any{"target": "#one", "context_target": "channel:one"})
		case "/api/agent/messages/send":
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Errorf("decode send request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"action": "message_send", "target": "#one", "state": "held", "outcome": "held",
				"heldMessages": []map[string]any{{"id": "message-5", "seq": 5}}, "newMessageCount": 3,
				"shownMessageCount": 1, "omittedMessageCount": 2, "seenUpToSeq": 2, "latestSeq": 5,
				"transport_id": "private", "producerFactId": "private",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	d := credentialProxySendTestDaemon(t, root, upstream.URL, coordinator)
	rec := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","content":"reply"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("server race hold status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sent["seen_up_to_seq"] != float64(2) || sent["context_target"] != "channel:one" {
		t.Fatalf("server race preflight request=%+v", sent)
	}
	if boundary, known := coordinator.ContextBoundary("channel:one"); !known || boundary != 2 {
		t.Fatalf("pre-output held boundary=%d known=%v, want 2 true", boundary, known)
	}
	draft, found, err := d.CredentialProxy().LoadMessageDraft("workspace-1", "agent-1", "#one", time.Now())
	if err != nil || !found || draft.SeenUpToSeq != 5 || draft.HoldCount != 1 {
		t.Fatalf("server-held Draft=%+v found=%v err=%v", draft, found, err)
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode server hold: %v", err)
	}
	receipt, _ := response[MessageCoverageReceiptField].(string)
	if receipt == "" {
		t.Fatalf("server hold omitted local coverage receipt: %+v", response)
	}
	for _, private := range []string{"seenUpToSeq", "latestSeq", "transport_id", "producerFactId"} {
		if _, leaked := response[private]; leaked {
			t.Fatalf("proxy leaked %s: %+v", private, response)
		}
	}
	if err := coordinator.CommitCoverage(receipt); err != nil {
		t.Fatalf("commit server-held coverage: %v", err)
	}
	if boundary, known := coordinator.ContextBoundary("channel:one"); !known || boundary != 5 {
		t.Fatalf("committed held boundary=%d known=%v, want 5 true", boundary, known)
	}
}

func TestCredentialProxyMessageSendDraftReusesIdentityAndAnywayOnlyOnReplay(t *testing.T) {
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	completeCoordinatorRecovery(t, coordinator)
	if err := coordinator.MarkRead("channel:one", 2); err != nil {
		t.Fatalf("seed Context Boundary: %v", err)
	}
	var sent map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/send" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decode replay request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"action": "message_send", "target": "#one", "created": true, "message": map[string]any{"id": "message-1"}})
	}))
	defer upstream.Close()

	d := credentialProxySendTestDaemon(t, root, upstream.URL, coordinator)
	if _, err := d.CredentialProxy().SaveNormalMessageDraft("workspace-1", "agent-1", MessageDraft{
		Target: "#one", ContextTarget: "channel:one", Content: "saved reply", IdempotencyKey: "stable-id", SeenUpToSeq: 2,
	}, time.Now()); err != nil {
		t.Fatalf("save Draft: %v", err)
	}
	rec := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","send_draft":true,"anyway":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("Draft replay status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sent["client_message_id"] != "stable-id" || sent["content"] != "saved reply" || sent["bypass_freshness"] != true {
		t.Fatalf("replay request=%+v", sent)
	}
	if _, found, err := d.CredentialProxy().LoadMessageDraft("workspace-1", "agent-1", "#one", time.Now()); err != nil || found {
		t.Fatalf("successful Draft replay found=%v err=%v", found, err)
	}

	invalid := httptest.NewRecorder()
	d.credentialProxyMessageSendHandler().ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","anyway":true}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("normal --anyway status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	for _, body := range []string{
		`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","send_draft":true,"content":"replacement"}`,
		`{"agent_id":"agent-1","workspace_id":"workspace-1","target":"#one","send_draft":true,"attachment_ids":["replacement"]}`,
	} {
		replacement := httptest.NewRecorder()
		d.credentialProxyMessageSendHandler().ServeHTTP(replacement, httptest.NewRequest(http.MethodPost, "/credential-proxy/messages/send", bytes.NewBufferString(body)))
		if replacement.Code != http.StatusBadRequest {
			t.Fatalf("replacement Draft request status=%d body=%s", replacement.Code, replacement.Body.String())
		}
	}
}

func credentialProxySendTestDaemon(t *testing.T, root, serverURL string, coordinator *MessageCoordinator) *Daemon {
	t.Helper()
	cfg := Config{WorkspacesRoot: root, ServerBaseURL: serverURL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "mac_test", Token: "cached-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	d := &Daemon{
		cfg:                 cfg,
		messageCoordinators: map[string]*MessageCoordinator{"agent-1": coordinator},
		messageDraftStore:   NewMessageDraftStore(root),
	}
	return d
}

func assertActiveTaskCount(t *testing.T, h http.HandlerFunc, want int64) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ActiveTaskCount != want {
		t.Errorf("active_task_count: got %d, want %d", resp.ActiveTaskCount, want)
	}
}
