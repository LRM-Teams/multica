package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestAgentInboxStorePersistsMessageItemAndIdempotentACK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	owner := "11111111-1111-4111-8111-111111111111"
	store := newAgentAppInboxStore(owner, path)
	message := protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 7, Content: "hello"}
	item, err := store.Mint(AgentAppInboxMintInput{AppID: agentInboxAppID, NotificationClass: "message", SourceRef: AgentAppInboxSourceRef{Kind: "message", ID: message.ID, Revision: "3"}, Message: &message, Title: "message", Summary: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	restored := newAgentAppInboxStore(owner, path)
	if err := restored.Restore(); err != nil {
		t.Fatal(err)
	}
	got, ok := restored.Item(item.ItemID)
	if !ok || got.Message == nil || got.Message.ID != message.ID {
		t.Fatalf("restored item=%+v found=%v", got, ok)
	}
	if !restored.Ack(item.ItemID) || restored.Ack(item.ItemID) {
		t.Fatal("message ACK was not idempotent")
	}
}

func TestAgentInboxRoutesRequireCredentialProxyAuthentication(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, BindingStateRoot: root, BindingsRoot: root, MachineID: "machine-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerTestAgentProxyServerCredential(t, d, "workspace-1", "runtime-1", testInboxAgentID, "agent-token")
	// The helper's registration key is intentionally test-scoped; add the wire
	// token key used by the route itself so this exercises the real middleware.
	tokenHash := sha256.Sum256([]byte("agent-token"))
	d.agentProxyCredentialMu.Lock()
	d.agentProxyCredentials[tokenHash] = authenticatedAgentProxy{Inbox: InboxKey{WorkspaceID: "workspace-1", AgentID: testInboxAgentID}, RuntimeID: "runtime-1"}
	d.agentProxyCredentialMu.Unlock()
	mux := http.NewServeMux()
	d.registerLocalControlRoutes(mux)
	for name, token := range map[string]string{"missing": "", "forged": "mpt_forged"} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/inbox", nil)
			if token != "" {
				req.Header.Set(AgentProxyAuthHeader, "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated inbox status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
	valid := httptest.NewRequest(http.MethodGet, "/inbox", nil)
	valid.Header.Set(AgentProxyAuthHeader, "Bearer agent-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, valid)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("valid Agent Proxy credential was rejected: %s", rec.Body.String())
	}
}

const (
	testInboxAgentID    = "11111111-1111-4111-8111-111111111111"
	testInboxReminderID = "22222222-2222-4222-8222-222222222222"
)

func testReminderMintInput() AgentAppInboxMintInput {
	return AgentAppInboxMintInput{
		AppID: reminderInboxAppID, NotificationClass: reminderDueClass,
		SourceRef: AgentAppInboxSourceRef{Kind: "reminder", ID: testInboxReminderID, Revision: "3"},
		Title:     "Review the release", Summary: "Reminder due",
	}
}

func TestAgentAppInboxStableUpsertPersistsV3AndRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-inbox.json")
	store := newAgentAppInboxStore(testInboxAgentID, path)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	first, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatalf("mint first item: %v", err)
	}
	now = now.Add(time.Hour)
	updated := testReminderMintInput()
	updated.Title = "Updated preview"
	second, err := store.Mint(updated)
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if second.ItemID != "reminder:"+testInboxReminderID+":3" || second.CreatedAtMS != first.CreatedAtMS || second.Title != "Updated preview" {
		t.Fatalf("stable upsert = %+v, first = %+v", second, first)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted state: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope["version"] != float64(3) {
		t.Fatalf("persisted envelope = %s, err=%v", raw, err)
	}

	restored := newAgentAppInboxStore(testInboxAgentID, path)
	if err := restored.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	items := restored.List()
	if len(items) != 1 || items[0] != second {
		t.Fatalf("restored items = %+v, want %+v", items, second)
	}
}

func TestAgentAppInboxRejectsTamperedPersistedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-inbox.json")
	store := newAgentAppInboxStore(testInboxAgentID, path)
	item, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	item.ItemID = "forged"
	raw, _ := json.Marshal(agentAppInboxState{Version: 3, Items: []AgentAppInboxItem{item}, AcknowledgedSources: []AgentAppInboxAcknowledgedSource{}, AckIntents: []AgentAppInboxAckIntent{}})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write tampered state: %v", err)
	}
	if err := newAgentAppInboxStore(testInboxAgentID, path).Restore(); err == nil {
		t.Fatal("tampered item identity restored successfully")
	}
}

func TestAgentAppInboxAckRecordsDurableSourceAndRequiresSourceAck(t *testing.T) {
	store := newAgentAppInboxStore(testInboxAgentID, filepath.Join(t.TempDir(), "app-inbox.json"))
	item, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	store.beforeAck = func(AgentAppInboxItem) bool { return false }
	if store.Ack(item.ItemID) {
		t.Fatal("ACK succeeded after source rejected it")
	}
	if len(store.List()) != 1 || len(store.ListAcknowledgedSources()) != 0 {
		t.Fatal("rejected ACK changed durable state")
	}
	store.beforeAck = func(AgentAppInboxItem) bool { return true }
	if !store.Ack(item.ItemID) {
		t.Fatal("ACK failed after source accepted it")
	}
	acknowledged := store.ListAcknowledgedSources()
	if len(store.List()) != 0 || len(acknowledged) != 1 || acknowledged[0].ItemID != item.ItemID {
		t.Fatalf("acknowledged state items=%+v sources=%+v", store.List(), acknowledged)
	}
}

func TestAgentAppInboxAckPersistenceFailureKeepsReplayableState(t *testing.T) {
	store := newAgentAppInboxStore(testInboxAgentID, filepath.Join(t.TempDir(), "state.json"))
	item, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatal(err)
	}
	writes := store.writeState
	store.writeState = func(string, []byte) error { return errors.New("disk unavailable") }
	if intent := store.BeginServerAuthorizedAckIntent(item.ItemID, "attempt-1"); intent != nil || len(store.ListAckIntents()) != 0 {
		t.Fatalf("non-durable ACK intent remained in memory: %+v", store.ListAckIntents())
	}
	store.writeState = writes
	if store.BeginServerAuthorizedAckIntent(item.ItemID, "attempt-1") == nil {
		t.Fatal("persist ACK intent")
	}
	store.beforeServerAuthorized = func(AgentAppInboxItem, AgentAppInboxAckIntent) bool { return true }
	store.writeState = func(string, []byte) error { return errors.New("disk unavailable") }
	if store.CompleteServerAuthorizedAck(item.ItemID, "attempt-1") {
		t.Fatal("completed ACK without durable local state")
	}
	if len(store.List()) != 1 || len(store.ListAckIntents()) != 1 || len(store.ListAcknowledgedSources()) != 0 {
		t.Fatalf("failed completion did not preserve replay state: items=%+v intents=%+v acknowledged=%+v", store.List(), store.ListAckIntents(), store.ListAcknowledgedSources())
	}
	store.writeState = writes
	if !store.CompleteServerAuthorizedAck(item.ItemID, "attempt-1") {
		t.Fatal("replayed ACK completion failed")
	}
}

func TestAgentAppInboxNoticeUsesContentFreeIdleInputAndSuppressesDuplicate(t *testing.T) {
	runtime := &idleMessageFakeRuntime{}
	pool := newAgentRuntimePool()
	pool.slots[testInboxAgentID+"\x00runtime-1"] = &agentRuntimeSlot{backend: runtime}
	notice := agent.ResidentPendingNotice{PendingAppItems: 2}
	if err := pool.deliverAppInboxNotice(context.Background(), testInboxAgentID, "runtime-1", notice, "items-a"); err != nil {
		t.Fatalf("deliver idle App Inbox notice: %v", err)
	}
	if err := pool.deliverAppInboxNotice(context.Background(), testInboxAgentID, "runtime-1", notice, "items-a"); err != nil {
		t.Fatalf("suppress duplicate App Inbox notice: %v", err)
	}
	if notices := runtime.noticeSnapshot(); len(notices) != 1 || notices[0].PendingAppItems != notice.PendingAppItems || notices[0].TotalPending != 0 || len(notices[0].ChangedTargets) != 0 {
		t.Fatalf("idle App Inbox notices = %+v, want one content-free notice", notices)
	}
}

func TestAgentAppInboxRestoredAckIntentRetriesSameAttempt(t *testing.T) {
	root := t.TempDir()
	var upstreamAttempts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ItemID            string                 `json:"itemId"`
			AppID             string                 `json:"appId"`
			NotificationClass string                 `json:"notificationClass"`
			SourceRef         AgentAppInboxSourceRef `json:"sourceRef"`
			AckAttemptID      string                 `json:"ackAttemptId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode retry request: %v", err)
		}
		upstreamAttempts = append(upstreamAttempts, request.AckAttemptID)
		writeAgentInboxJSON(w, http.StatusOK, map[string]any{
			"ok": true, "itemId": request.ItemID, "appId": request.AppID,
			"notificationClass": request.NotificationClass, "sourceRef": request.SourceRef,
			"sourceEventId": "33333333-3333-4333-8333-333333333333",
			"ackAttemptId":  request.AckAttemptID, "replayed": true,
		})
	}))
	defer upstream.Close()
	cfg := Config{WorkspacesRoot: root, BindingStateRoot: root, BindingsRoot: root, MachineID: "machine-1", WorkspaceID: "workspace-1", ServerBaseURL: upstream.URL}
	first := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	store, err := first.agentAppInboxes.Store(testInboxAgentID)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatal(err)
	}
	intent := store.BeginServerAuthorizedAckIntent(item.ItemID, "44444444-4444-4444-8444-444444444444")
	if intent == nil {
		t.Fatal("persist ACK intent")
	}
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", testInboxAgentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: testInboxAgentID, Prefix: "sk_agent_test", Token: "agent-token",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	restarted := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerTestAgentProxyServerCredential(t, restarted, "workspace-1", "runtime-1", testInboxAgentID, "agent-token")
	restarted.reminderCache.receipts[testInboxReminderID] = []reminderDueReceipt{{
		Job:         protocol.ReminderTimerJob{OwnerAgentID: testInboxAgentID, ReminderID: testInboxReminderID, Version: 3},
		ServerAcked: true, ServerFired: true, WakeEnqueued: true,
	}}
	restarted.retryAgentAppInboxAckIntents(context.Background(), "workspace-1")
	restored, err := restarted.agentAppInboxes.Store(testInboxAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(upstreamAttempts) != 1 || upstreamAttempts[0] != intent.AckAttemptID {
		t.Fatalf("retried attempt IDs = %+v, want %s", upstreamAttempts, intent.AckAttemptID)
	}
	if len(restored.List()) != 0 || len(restored.ListAckIntents()) != 0 || len(restored.ListAcknowledgedSources()) != 1 {
		t.Fatalf("restored ACK state items=%+v intents=%+v acknowledged=%+v", restored.List(), restored.ListAckIntents(), restored.ListAcknowledgedSources())
	}
	if receipts := restarted.reminderCache.pendingFireReceipts(); len(receipts) != 0 {
		t.Fatalf("Reminder receipts after recovered ACK = %+v", receipts)
	}
}

func TestAgentInboxHTTPAggregatesWithoutPreparingMessageCoverage(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, BindingStateRoot: root, BindingsRoot: root, MachineID: "machine-1", WorkspaceID: "workspace-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coordinator := &MessageCoordinator{
		key:        InboxKey{WorkspaceID: "workspace-1", AgentID: testInboxAgentID},
		boundaries: map[string]int64{"channel:one": 2},
	}
	registerTestInbox(t, d, coordinator.key, "runtime-1", coordinator)
	store, err := d.agentAppInboxes.Store(testInboxAgentID)
	if err != nil {
		t.Fatalf("open app inbox: %v", err)
	}
	if _, err := store.Mint(testReminderMintInput()); err != nil {
		t.Fatalf("mint app item: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/internal/agent-api/inbox", nil)
	request.Header.Set("X-Agent-ID", testInboxAgentID)
	request.Header.Set("X-Workspace-ID", "workspace-1")
	d.agentAppInboxHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("inbox status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response agentInboxSnapshotResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.PendingMessages != 1 || response.PendingTargets != 0 || response.PendingAppItems != 1 || len(response.Items) != 1 {
		t.Fatalf("aggregate response = %+v", response)
	}
	if got := coordinator.Boundaries()["channel:one"]; got != 2 {
		t.Fatalf("inbox check advanced boundary to %d", got)
	}
}

func TestAgentInboxHTTPAckIsStrictAndConsumesExactReminderReceipt(t *testing.T) {
	root := t.TempDir()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/app-sources/ack" || r.Header.Get("Authorization") != "Bearer agent-token" {
			t.Errorf("upstream request path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		var request struct {
			ItemID            string                 `json:"itemId"`
			AppID             string                 `json:"appId"`
			NotificationClass string                 `json:"notificationClass"`
			SourceRef         AgentAppInboxSourceRef `json:"sourceRef"`
			AckAttemptID      string                 `json:"ackAttemptId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		writeAgentInboxJSON(w, http.StatusOK, map[string]any{
			"ok": true, "itemId": request.ItemID, "appId": request.AppID,
			"notificationClass": request.NotificationClass, "sourceRef": request.SourceRef,
			"sourceEventId": "33333333-3333-4333-8333-333333333333",
			"ackAttemptId":  request.AckAttemptID, "replayed": false,
		})
	}))
	defer upstream.Close()
	cfg := Config{WorkspacesRoot: root, BindingStateRoot: root, BindingsRoot: root, MachineID: "machine-1", WorkspaceID: "workspace-1", ServerBaseURL: upstream.URL}
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", testInboxAgentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: testInboxAgentID, Prefix: "sk_agent_test", Token: "agent-token",
	}, time.Now()); err != nil {
		t.Fatalf("cache agent credential: %v", err)
	}
	d := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	registerTestAgentProxyServerCredential(t, d, "workspace-1", "runtime-1", testInboxAgentID, "agent-token")
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: testInboxAgentID}, "runtime-1", &MessageCoordinator{key: InboxKey{WorkspaceID: "workspace-1", AgentID: testInboxAgentID}})
	d.reminderCache.receipts[testInboxReminderID] = []reminderDueReceipt{{
		Job:         protocol.ReminderTimerJob{OwnerAgentID: testInboxAgentID, ReminderID: testInboxReminderID, Version: 3},
		ServerAcked: true, WakeEnqueued: true,
	}}
	d.reminderCache.fences[testInboxReminderID] = reminderVersionFence{OwnerAgentID: testInboxAgentID, Version: 3}
	store, err := d.agentAppInboxes.Store(testInboxAgentID)
	if err != nil {
		t.Fatalf("open app inbox: %v", err)
	}
	item, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	bad := httptest.NewRecorder()
	badRequest := httptest.NewRequest(http.MethodPost, "/internal/agent-api/inbox/ack", bytes.NewBufferString(`{"itemId":"`+item.ItemID+`","extra":true}`))
	badRequest.Header.Set("X-Agent-ID", testInboxAgentID)
	badRequest.Header.Set("X-Workspace-ID", "workspace-1")
	d.agentAppInboxAckHandler().ServeHTTP(bad, badRequest)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown ACK field status = %d body=%s", bad.Code, bad.Body.String())
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/internal/agent-api/inbox/ack", bytes.NewBufferString(`{"itemId":"`+item.ItemID+`"}`))
	request.Header.Set("X-Agent-ID", testInboxAgentID)
	request.Header.Set("X-Workspace-ID", "workspace-1")
	d.agentAppInboxAckHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ACK status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.List()) != 0 || len(d.reminderCache.pendingFireReceipts()) != 0 {
		t.Fatalf("ACK did not retire item and receipt: items=%+v receipts=%+v", store.List(), d.reminderCache.pendingFireReceipts())
	}

	replayedItem, err := store.Mint(testReminderMintInput())
	if err != nil {
		t.Fatalf("rematerialize acknowledged source: %v", err)
	}
	replayedRecorder := httptest.NewRecorder()
	replayedRequest := httptest.NewRequest(http.MethodPost, "/internal/agent-api/inbox/ack", bytes.NewBufferString(`{"itemId":"`+replayedItem.ItemID+`"}`))
	replayedRequest.Header.Set("X-Agent-ID", testInboxAgentID)
	replayedRequest.Header.Set("X-Workspace-ID", "workspace-1")
	d.agentAppInboxAckHandler().ServeHTTP(replayedRecorder, replayedRequest)
	if replayedRecorder.Code != http.StatusOK || len(store.List()) != 0 {
		t.Fatalf("replayed source ACK status=%d items=%+v body=%s", replayedRecorder.Code, store.List(), replayedRecorder.Body.String())
	}
}
