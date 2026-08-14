package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerAttachmentReplayIgnoresHistoricalOfflineRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "attachment-replay-stale-runtime"
	currentRuntimeID := seedMachineLockedRuntime(t, daemonID, "current")
	historicalRuntimeID := seedMachineLockedRuntime(t, daemonID, "historical")
	foreignRuntimeID := seedMachineLockedRuntime(t, "attachment-replay-other-computer", "foreign")
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, historicalRuntimeID); err != nil {
		t.Fatal(err)
	}

	hub := daemonws.NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	readyPayload, err := json.Marshal(protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID:      testWorkspaceID,
		DaemonInstanceID: "attachment-replay-instance",
		ActiveCapabilities: []string{
			protocol.DaemonCapabilityWorkspaceRunnerAttachment,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: readyPayload})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceRunnerConnectionCount(daemonID, testWorkspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("Workspace Runner did not become current")
		}
		time.Sleep(time.Millisecond)
	}

	h := *testHandler
	h.DaemonHub = hub
	identity := daemonws.ClientIdentity{DaemonID: daemonID, WorkspaceID: testWorkspaceID}
	foreignRequest, err := json.Marshal(protocol.WorkspaceRunnerAttachmentReplayRequest{
		RuntimeCursors: map[string]int64{currentRuntimeID: 0, foreignRuntimeID: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceRunnerFrame(ctx, identity, "attachment-replay-instance", protocol.EventAgentAttachmentReplayReq, foreignRequest); err == nil || !strings.Contains(err.Error(), "outside Runner scope") {
		t.Fatalf("cross-Computer Runtime replay error = %v, want authorization rejection", err)
	}

	request, err := json.Marshal(protocol.WorkspaceRunnerAttachmentReplayRequest{
		RuntimeCursors: map[string]int64{currentRuntimeID: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.HandleWorkspaceRunnerFrame(ctx, identity, "attachment-replay-instance", protocol.EventAgentAttachmentReplayReq, request); err != nil {
		t.Fatalf("current Runtime replay blocked by historical offline Runtime %s: %v", historicalRuntimeID, err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != protocol.EventAgentAttachmentReplayEnd {
		t.Fatalf("replay response type = %q, want %q", frame.Type, protocol.EventAgentAttachmentReplayEnd)
	}
	var end protocol.WorkspaceRunnerAttachmentReplayEnd
	if err := json.Unmarshal(frame.Payload, &end); err != nil {
		t.Fatal(err)
	}
	if len(end.RuntimeCursors) != 1 || end.RuntimeCursors[currentRuntimeID] != 0 {
		t.Fatalf("replay end cursors = %#v, want current Runtime only", end.RuntimeCursors)
	}
}
