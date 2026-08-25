package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func createMachineUpgradeSiblingRuntimes(t *testing.T, ownerID string) (string, string, string) {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "machine-upgrade-" + uuid.NewString()
	create := func(provider string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
				workspace_id, daemon_id, name, runtime_mode, provider, status,
				device_info, metadata, last_seen_at
			) VALUES ($1, $2, $3, 'local', $4, 'online', 'test machine',
				'{"capabilities":["machine_upgrade_v1"]}'::jsonb, now())
			RETURNING id`, testWorkspaceID, daemonID, provider+"-"+uuid.NewString(), provider).Scan(&id); err != nil {
			t.Fatalf("create machine upgrade runtime: %v", err)
		}
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id) })
		return id
	}
	return create("claude"), create("codex"), daemonID
}

func bindMachineUpgradeWorkspace(t *testing.T, daemonID, workspaceID, ownerID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, $4, TRUE)
		ON CONFLICT (daemon_id, workspace_id)
		DO UPDATE SET user_id = EXCLUDED.user_id, active = TRUE, revoked_at = NULL`, daemonID, workspaceID, ownerID, "machine-upgrade-test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`, daemonID, workspaceID)
	})
}

func initiateMachineUpgrade(t *testing.T, userID, daemonID, target string) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	req := newRequestAsUser(userID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": target,
		"request_id":     uuid.NewString(),
	})
	req = withURLParam(req, "daemonId", daemonID)
	w := httptest.NewRecorder()
	testHandler.CreateMachineUpgrade(w, req)
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestMachineUpgrade_NoCurrentSocketFailsInsteadOfQueuing(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)

	createdW, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	if createdW.Code != http.StatusConflict || created["code"] != "no_current_socket" {
		t.Fatalf("upgrade without a live socket = %d %s, want no_current_socket", createdW.Code, createdW.Body.String())
	}

	retryW, retry := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	if retryW.Code != http.StatusConflict || retry["code"] != "no_current_socket" {
		t.Fatalf("retry without a live socket = %d %s", retryW.Code, retryW.Body.String())
	}
}

func TestMachineUpgrade_AllowsOnlyComputerOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	computerOwnerID := createRuntimeLocalSkillTestMember(t, "member")
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, computerOwnerID)
	workspaceAdminID := createRuntimeLocalSkillTestMember(t, "admin")
	for label, workspaceManagerID := range map[string]string{
		"Workspace owner": testUserID,
		"Workspace admin": workspaceAdminID,
	} {
		nonOwnerReq := newRequestAsUser(workspaceManagerID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{"target_version": "v9.9.9", "request_id": uuid.NewString()})
		nonOwnerReq = withURLParam(nonOwnerReq, "daemonId", daemonID)
		nonOwnerW := httptest.NewRecorder()
		testHandler.CreateMachineUpgrade(nonOwnerW, nonOwnerReq)
		if nonOwnerW.Code != http.StatusForbidden {
			t.Fatalf("canonical create by non-owner %s = %d: %s", label, nonOwnerW.Code, nonOwnerW.Body.String())
		}
	}
	computerOwnerW, _ := initiateMachineUpgrade(t, computerOwnerID, daemonID, "v9.9.9")
	if computerOwnerW.Code != http.StatusConflict {
		t.Fatalf("canonical create by Computer owner = %d: %s", computerOwnerW.Code, computerOwnerW.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(computerOwnerW.Body.Bytes(), &body); err != nil || body["code"] != "no_current_socket" {
		t.Fatalf("owner without socket = %s err=%v", computerOwnerW.Body.String(), err)
	}

	pinTestRuntime(t, runtimeID, "0.3.85")
	pinnedW, _ := initiateMachineUpgrade(t, computerOwnerID, daemonID, "v9.9.9")
	if pinnedW.Code != http.StatusConflict {
		t.Fatalf("canonical create on pinned runtime = %d: %s", pinnedW.Code, pinnedW.Body.String())
	}
	if err := json.Unmarshal(pinnedW.Body.Bytes(), &body); err != nil || body["code"] != "no_current_socket" {
		t.Fatalf("Runtime pin incorrectly gated Computer upgrade = %s err=%v", pinnedW.Body.String(), err)
	}

	if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET pinned_version = NULL, metadata = '{"launched_by":"desktop","capabilities":["machine_upgrade_v1"]}'::jsonb WHERE id = $1`, runtimeID); err != nil {
		t.Fatal(err)
	}
	desktopW, _ := initiateMachineUpgrade(t, computerOwnerID, daemonID, "v9.9.9")
	if desktopW.Code != http.StatusConflict {
		t.Fatalf("canonical create on desktop-managed runtime = %d: %s", desktopW.Code, desktopW.Body.String())
	}
	if err := json.Unmarshal(desktopW.Body.Bytes(), &body); err != nil || body["code"] != "no_current_socket" {
		t.Fatalf("Runtime launch metadata incorrectly gated Computer upgrade = %s err=%v", desktopW.Body.String(), err)
	}
}

func TestMachineUpgrade_WithoutRuntimeDispatchesToOwnedComputer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	daemonID := "machine-upgrade-no-runtime-" + uuid.NewString()
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID})
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ready, err := json.Marshal(protocol.Message{
		Type: protocol.EventWorkspaceDaemonReady,
		Payload: mustMarshalJSON(protocol.WorkspaceReadyPayload{
			WorkspaceID: testWorkspaceID, DaemonInstanceID: "instance-no-runtime",
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for !hub.HasWorkspaceDaemon(daemonID, testWorkspaceID) {
		if time.Now().After(deadline) {
			t.Fatal("Computer without a Runtime did not become ready")
		}
		time.Sleep(time.Millisecond)
	}

	local := *testHandler
	local.DaemonHub = hub
	nonOwnerID := createRuntimeLocalSkillTestMember(t, "member")
	nonOwnerReq := newRequestAsUser(nonOwnerID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v0.4.25-alpha.8",
		"request_id":     uuid.NewString(),
	})
	nonOwnerReq = withURLParam(nonOwnerReq, "daemonId", daemonID)
	nonOwnerW := httptest.NewRecorder()
	local.CreateMachineUpgrade(nonOwnerW, nonOwnerReq)
	if nonOwnerW.Code != http.StatusForbidden {
		t.Fatalf("upgrade without a Runtime by non-owner = %d: %s", nonOwnerW.Code, nonOwnerW.Body.String())
	}

	requestID := uuid.NewString()
	ownerReq := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v0.4.25-alpha.8",
		"request_id":     requestID,
	})
	ownerReq = withURLParam(ownerReq, "daemonId", daemonID)
	ownerW := httptest.NewRecorder()
	local.CreateMachineUpgrade(ownerW, ownerReq)
	if ownerW.Code != http.StatusAccepted {
		t.Fatalf("upgrade without a Runtime by Computer owner = %d: %s", ownerW.Code, ownerW.Body.String())
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Computer without a Runtime did not receive computer:upgrade: %v", err)
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil || message.Type != protocol.EventComputerUpgrade {
		t.Fatalf("Computer without a Runtime frame = %+v err=%v", message, err)
	}
	var payload protocol.ComputerUpgradePayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil || payload.RequestID != requestID || payload.TargetVersion != "v0.4.25-alpha.8" {
		t.Fatalf("Computer without a Runtime upgrade payload = %+v err=%v", payload, err)
	}
}

func TestMachineUpgrade_DispatchesComputerUpgradeToOneLiveBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, siblingWorkspaceID, testUserID)

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := r.URL.Query().Get("workspace")
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: workspaceID})
	}))
	t.Cleanup(server.Close)

	dialReady := func(workspaceID string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?workspace="+workspaceID, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		ready, err := json.Marshal(protocol.Message{
			Type: protocol.EventWorkspaceDaemonReady,
			Payload: mustMarshalJSON(protocol.WorkspaceReadyPayload{
				WorkspaceID: workspaceID, DaemonInstanceID: "instance-" + workspaceID,
				ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for hub.WorkspaceDaemonConnectionCount(daemonID, workspaceID) != 1 {
			if time.Now().After(deadline) {
				t.Fatalf("Binding %s did not become ready", workspaceID)
			}
			time.Sleep(time.Millisecond)
		}
		return conn
	}
	firstConn := dialReady(testWorkspaceID)
	secondConn := dialReady(siblingWorkspaceID)

	local := *testHandler
	local.DaemonHub = hub
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v9.9.9",
		"request_id":     uuid.NewString(),
	})
	req = withURLParam(req, "daemonId", daemonID)
	w := httptest.NewRecorder()
	local.CreateMachineUpgrade(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create machine upgrade = %d: %s", w.Code, w.Body.String())
	}

	readUpgrade := func(conn *websocket.Conn) (protocol.ComputerUpgradePayload, bool) {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return protocol.ComputerUpgradePayload{}, false
		}
		var message protocol.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != protocol.EventComputerUpgrade {
			t.Fatalf("unexpected Binding frame %q", message.Type)
		}
		var payload protocol.ComputerUpgradePayload
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		return payload, true
	}
	first, firstOK := readUpgrade(firstConn)
	second, secondOK := readUpgrade(secondConn)
	if firstOK == secondOK {
		t.Fatalf("live Binding delivery first=%v second=%v, want exactly one current socket", firstOK, secondOK)
	}
	got := first
	if secondOK {
		got = second
	}
	if got.RequestID == "" || got.TargetVersion != "v9.9.9" {
		t.Fatalf("computer:upgrade payload = %+v", got)
	}
}

func TestMachineUpgrade_DispatchesComputerUpgradeToNextLiveBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)
	siblingWorkspaceID := createBindingTestWorkspace(t, testUserID, "owner")
	firstWorkspaceID, secondWorkspaceID := testWorkspaceID, siblingWorkspaceID
	if firstWorkspaceID > secondWorkspaceID {
		firstWorkspaceID, secondWorkspaceID = secondWorkspaceID, firstWorkspaceID
	}
	bindMachineUpgradeWorkspace(t, daemonID, firstWorkspaceID, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, secondWorkspaceID, testUserID)

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: r.URL.Query().Get("workspace")})
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?workspace="+secondWorkspaceID, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ready, err := json.Marshal(protocol.Message{
		Type: protocol.EventWorkspaceDaemonReady,
		Payload: mustMarshalJSON(protocol.WorkspaceReadyPayload{
			WorkspaceID: secondWorkspaceID, DaemonInstanceID: "instance-" + secondWorkspaceID,
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceDaemonConnectionCount(daemonID, secondWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("live Binding did not become ready")
		}
		time.Sleep(time.Millisecond)
	}

	local := *testHandler
	local.DaemonHub = hub
	req := newRequestAsUser(testUserID, http.MethodPost, "/api/daemons/"+daemonID+"/upgrades", map[string]string{
		"target_version": "v9.9.9",
		"request_id":     uuid.NewString(),
	})
	req = withURLParam(req, "daemonId", daemonID)
	w := httptest.NewRecorder()
	local.CreateMachineUpgrade(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create machine upgrade = %d: %s", w.Code, w.Body.String())
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("live Binding did not receive computer:upgrade: %v", err)
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil || message.Type != protocol.EventComputerUpgrade {
		t.Fatalf("live Binding frame = %+v err=%v", message, err)
	}
}

func TestMachineUpgrade_InboundProgressAndDonePublishRealtime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	bus := events.New()
	got := make(chan events.Event, 2)
	bus.Subscribe(protocol.EventComputerUpgradeProgress, func(e events.Event) { got <- e })
	bus.Subscribe(protocol.EventComputerUpgradeDone, func(e events.Event) { got <- e })

	local := *testHandler
	local.Bus = bus
	identity := daemonws.ClientIdentity{DaemonID: "computer-" + uuid.NewString(), WorkspaceID: testWorkspaceID}

	progress, err := json.Marshal(protocol.ComputerUpgradeProgressPayload{
		RequestID: "req-1", Phase: "staging", Message: "downloading",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := local.HandleWorkspaceDaemonFrame(context.Background(), identity, "instance-1", protocol.EventComputerUpgradeProgress, progress); err != nil {
		t.Fatalf("progress frame: %v", err)
	}
	select {
	case event := <-got:
		if event.Type != protocol.EventComputerUpgradeProgress || event.WorkspaceID != testWorkspaceID {
			t.Fatalf("progress event = %+v", event)
		}
		payload, ok := event.Payload.(map[string]any)
		if !ok || payload["requestId"] != "req-1" || payload["phase"] != "staging" {
			t.Fatalf("progress payload = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("did not publish computer:upgrade:progress")
	}

	done, err := json.Marshal(protocol.ComputerUpgradeDonePayload{
		RequestID: "req-1", OK: true, NewVersion: "v9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := local.HandleWorkspaceDaemonFrame(context.Background(), identity, "instance-1", protocol.EventComputerUpgradeDone, done); err != nil {
		t.Fatalf("done frame: %v", err)
	}
	select {
	case event := <-got:
		if event.Type != protocol.EventComputerUpgradeDone {
			t.Fatalf("done event = %+v", event)
		}
		payload, ok := event.Payload.(map[string]any)
		if !ok || payload["ok"] != true || payload["newVersion"] != "v9.9.9" {
			t.Fatalf("done payload = %#v", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("did not publish computer:upgrade:done")
	}
}

func TestMachineUpgrade_DispatchDoesNotNeedCloudReceipt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	_, _, daemonID := createMachineUpgradeSiblingRuntimes(t, testUserID)
	bindMachineUpgradeWorkspace(t, daemonID, testWorkspaceID, testUserID)

	createdW, created := initiateMachineUpgrade(t, testUserID, daemonID, "v9.9.9")
	if createdW.Code != http.StatusConflict || created["code"] != "no_current_socket" {
		t.Fatalf("dispatch without a socket = %d %s", createdW.Code, createdW.Body.String())
	}
}
