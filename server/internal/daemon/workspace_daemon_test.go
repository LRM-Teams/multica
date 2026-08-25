package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceDaemonURLIsScopedWithoutRuntimeIDs(t *testing.T) {
	got, err := workspaceDaemonURL("https://api.example.com/multica", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	const want = "wss://api.example.com/multica/api/daemon/connect?workspace_id=workspace-1"
	if got != want {
		t.Fatalf("workspaceDaemonURL() = %q, want %q", got, want)
	}
}

func TestWorkspaceDaemonReadyPingAndReconnectUseFixedIdentity(t *testing.T) {
	type observation struct {
		ready protocol.WorkspaceReadyPayload
		pong  protocol.WorkspacePongPayload
	}
	observations := make(chan observation, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var readyFrame protocol.Message
		if err := json.Unmarshal(raw, &readyFrame); err != nil {
			t.Error(err)
			return
		}
		var ready protocol.WorkspaceReadyPayload
		if readyFrame.Type != protocol.EventWorkspaceDaemonReady || json.Unmarshal(readyFrame.Payload, &ready) != nil {
			t.Errorf("invalid ready frame: %+v", readyFrame)
			return
		}
		ping, _ := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonPing, Payload: marshalRaw(protocol.WorkspacePingPayload{PingID: "ping-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, ping); err != nil {
			t.Error(err)
			return
		}
		var pongFrame protocol.Message
		var pong protocol.WorkspacePongPayload
		for pongFrame.Type != protocol.EventWorkspaceDaemonPong {
			_, raw, err = conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if json.Unmarshal(raw, &pongFrame) != nil {
				continue
			}
		}
		if json.Unmarshal(pongFrame.Payload, &pong) != nil {
			t.Errorf("invalid pong frame: %s", pongFrame.Payload)
			return
		}
		observations <- observation{ready: ready, pong: pong}
	}))
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", DeviceName: "ubuntu-build-host", CLIVersion: "0.4.24-alpha.91"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.instanceID = "instance-1"
	d.client.SetWorkspaceDaemonToken("workspace-1", "workspace-token", time.Now().Add(time.Minute))
	runner, err := d.newWorkspaceDaemon("workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go runner.Run(ctx)
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case got := <-observations:
			if got.ready.WorkspaceID != "workspace-1" || got.ready.DaemonInstanceID != "instance-1" || got.ready.DeviceName != "ubuntu-build-host" || got.ready.OS != normalizeGOOS(runtime.GOOS) || got.ready.CLIVersion != "0.4.24-alpha.91" || got.pong.PingID != "ping-1" {
				t.Fatalf("reconnect observation = %+v", got)
			}
			capabilities := make(map[string]bool, len(got.ready.ActiveCapabilities))
			for _, capability := range got.ready.ActiveCapabilities {
				capabilities[capability] = true
			}
			if !capabilities[protocol.DaemonCapabilityWorkspaceDaemonAgentProcess] || !capabilities[protocol.DaemonCapabilityWorkspaceDaemonAgentReset] {
				t.Fatalf("WorkspaceDaemon capabilities = %v, want Agent process and reset", got.ready.ActiveCapabilities)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Runner connection %d", attempt+1)
		}
	}
}

func TestWorkspaceDaemonRunReturnsWhenBindingContextCancelsLiveSocket(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		select {
		case <-connected:
		default:
			close(connected)
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.instanceID = "instance-1"
	d.client.SetWorkspaceDaemonToken("workspace-1", "workspace-token", time.Now().Add(time.Minute))
	runner, err := d.newWorkspaceDaemon("workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("Runner did not connect")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after Binding ctx cancel")
	}
}

func TestWorkspaceDaemonOwnsOneProcessManagerPerWorkspace(t *testing.T) {
	d := New(Config{DaemonID: "daemon-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	firstRunner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	secondRunner, err := d.newWorkspaceDaemon("ws-2")
	if err != nil {
		t.Fatal(err)
	}
	first := firstRunner.processes
	second := secondRunner.processes
	if first == nil || second == nil || second == first {
		t.Fatal("different WorkspaceDaemons unexpectedly share a process manager")
	}
	firstAck, err := first.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("start in first manager: %v", err)
	}
	secondAck, err := second.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-2"})
	if err != nil {
		t.Fatalf("start in second manager: %v", err)
	}
	if firstAck.AgentInstanceID == "" || secondAck.AgentInstanceID == "" || firstAck.QueueState != protocol.AgentStartQueueStarting || secondAck.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("WorkspaceDaemon managers were not isolated: first=%+v second=%+v", firstAck, secondAck)
	}
}

func TestWorkspaceDaemonAcceptsScopedStartAndReturnsAckThenStatus(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	frames := make(chan protocol.Message, 6)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("workspace_id") != "ws-1" || r.Header.Get("Authorization") != "Bearer workspace-token" {
			http.Error(w, "unexpected runner scope", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var ready protocol.Message
		if err := json.Unmarshal(raw, &ready); err != nil {
			t.Error(err)
			return
		}
		frames <- ready
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.AgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		var accepted protocol.AgentStartAckPayload
		var sawAck, sawActive, sawStartingActivity, sawOnlineActivity bool
		for !sawAck || !sawActive || !sawStartingActivity || !sawOnlineActivity {
			_, raw, err = conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Error(err)
				return
			}
			switch msg.Type {
			case protocol.EventAgentStartAck:
				if err := json.Unmarshal(msg.Payload, &accepted); err != nil {
					t.Error(err)
					return
				}
				sawAck = true
			case protocol.EventAgentStatus:
				var candidate protocol.AgentStatusPayload
				if json.Unmarshal(msg.Payload, &candidate) == nil && candidate.Status == protocol.AgentStatusActive {
					sawActive = true
				}
			case protocol.EventAgentSession:
				var session protocol.AgentSessionPayload
				if json.Unmarshal(msg.Payload, &session) == nil && session.ProviderSessionID == "" {
					t.Error("agent:session without a provider session id")
					return
				}
			case protocol.EventAgentActivity:
				if !sawActive {
					t.Error("agent:activity arrived before agent:status active")
					return
				}
				var activity protocol.AgentActivityPayload
				if err := json.Unmarshal(msg.Payload, &activity); err != nil {
					t.Error(err)
					return
				}
				if activity.Snapshot.ActivityKind == "" || activity.Summary.Label == "" {
					t.Error("Activity wire omitted daemon presentation")
					return
				}
				sawStartingActivity = sawStartingActivity || activity.Snapshot.DetailKind == "starting"
				sawOnlineActivity = sawOnlineActivity || activity.Snapshot.DetailKind == "idle"
			}
			frames <- msg
		}
		stop, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStop, Payload: marshalRaw(protocol.AgentStopPayload{AgentID: accepted.AgentID})})
		if err := conn.WriteMessage(websocket.TextMessage, stop); err != nil {
			t.Error(err)
			return
		}
		for i := 0; i < 1; i++ {
			_, raw, err = conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var stoppedFrame protocol.Message
			if err := json.Unmarshal(raw, &stoppedFrame); err != nil {
				t.Error(err)
				return
			}
			frames <- stoppedFrame
		}
	}))
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.inboxes.Close()
	})
	go func() { errCh <- runner.runConnection(ctx) }()
	var ready, ack, status, inactive protocol.Message
	var startingActivities, onlineActivities int
	deadline := time.Now().Add(2 * time.Second)
	for ready.Type == "" || ack.Type == "" || status.Type == "" || inactive.Type == "" {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Runner frames")
		}
		select {
		case msg := <-frames:
			switch msg.Type {
			case protocol.EventWorkspaceDaemonReady:
				ready = msg
			case protocol.EventAgentStartAck:
				ack = msg
			case protocol.EventAgentStatus:
				var candidate protocol.AgentStatusPayload
				if err := json.Unmarshal(msg.Payload, &candidate); err != nil {
					t.Fatal(err)
				}
				if candidate.Status == protocol.AgentStatusActive {
					status = msg
				} else {
					inactive = msg
				}
			case protocol.EventAgentActivity:
				// Raft 1.0.16 (#2929): managed spawn publishes working/starting.
				// Resident runtimes then settle that state from their completed
				// initialization because no initial turn exists to do it later.
				var activity protocol.AgentActivityPayload
				if err := json.Unmarshal(msg.Payload, &activity); err != nil {
					t.Fatal(err)
				}
				if activity.Snapshot.ActivityKind == "" || activity.Summary.Label == "" {
					t.Fatal("Activity wire omitted daemon presentation")
				}
				switch activity.Snapshot.DetailKind {
				case "starting":
					startingActivities++
				case "idle":
					onlineActivities++
				default:
					t.Fatalf("managed start/stop invented unexpected Activity: %+v", activity)
				}
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for Runner frames")
		}
	}
	if startingActivities != 1 {
		t.Fatalf("managed start emitted %d Starting… Activities, want Raft's single spawn broadcast", startingActivities)
	}
	if onlineActivities != 1 {
		t.Fatalf("managed resident start emitted %d Online Activities, want one", onlineActivities)
	}
	if ready.Type != protocol.EventWorkspaceDaemonReady {
		t.Fatalf("ready frame = %+v", ready)
	}
	var accepted protocol.AgentStartAckPayload
	if err := json.Unmarshal(ack.Payload, &accepted); err != nil {
		t.Fatal(err)
	}
	var active protocol.AgentStatusPayload
	if err := json.Unmarshal(status.Payload, &active); err != nil {
		t.Fatal(err)
	}
	if accepted.AgentID == "" || active.AgentID != accepted.AgentID || active.Status != protocol.AgentStatusActive {
		t.Fatalf("ack=%+v status=%+v", accepted, active)
	}
	var stopped protocol.AgentStatusPayload
	if err := json.Unmarshal(inactive.Payload, &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.AgentID != accepted.AgentID || stopped.Status != protocol.AgentStatusInactive {
		t.Fatalf("inactive status=%+v, want launch %q", stopped, accepted.AgentID)
	}
	<-errCh
}

// computerUpgradeDoneTestServer runs a minimal WS server that reads the
// Runner's ready frame, sends one computer:upgrade command, then waits for
// the computer:upgrade:done reply and hands it back on the returned channel.
func computerUpgradeDoneTestServer(t *testing.T) (*httptest.Server, chan protocol.Message) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	done := make(chan protocol.Message, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil { // ready frame
			t.Error(err)
			return
		}
		upgrade, _ := json.Marshal(protocol.Message{
			Type:    protocol.EventComputerUpgrade,
			Payload: marshalRaw(protocol.ComputerUpgradePayload{RequestID: "upgrade-1", TargetVersion: "v2.0.0"}),
		})
		if err := conn.WriteMessage(websocket.TextMessage, upgrade); err != nil {
			t.Error(err)
			return
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var msg protocol.Message
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			if msg.Type == protocol.EventComputerUpgradeDone {
				done <- msg
				return
			}
		}
	}))
	return server, done
}

// TestWorkspaceDaemonReportsForwardFailureForComputerUpgrade covers the
// non-busy failure path: any handleComputerControl error for
// computer:upgrade must still produce a computer:upgrade:done{ok:false}
// frame, not just a local log line the cloud never sees.
func TestWorkspaceDaemonReportsForwardFailureForComputerUpgrade(t *testing.T) {
	server, done := computerUpgradeDoneTestServer(t)
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.handleComputerControl = func(context.Context, string, protocol.ComputerUpgradePayload) error {
		return errors.New("forward to Computer failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = runner.runConnection(ctx) }()

	select {
	case msg := <-done:
		var payload protocol.ComputerUpgradeDonePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.OK || payload.Error != "forward_failed" || payload.RequestID != "upgrade-1" {
			t.Fatalf("computer:upgrade:done = %+v, want ok=false error=forward_failed requestId=upgrade-1", payload)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for computer:upgrade:done after a non-busy forward failure")
	}
}

// TestWorkspaceDaemonReportsControlBusyForComputerUpgrade keeps the existing
// "control_busy" code stable: the frontend already special-cases it.
func TestWorkspaceDaemonReportsControlBusyForComputerUpgrade(t *testing.T) {
	server, done := computerUpgradeDoneTestServer(t)
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.handleComputerControl = func(context.Context, string, protocol.ComputerUpgradePayload) error {
		return computer.ErrComputerControlBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = runner.runConnection(ctx) }()

	select {
	case msg := <-done:
		var payload protocol.ComputerUpgradeDonePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.OK || payload.Error != "control_busy" || payload.RequestID != "upgrade-1" {
			t.Fatalf("computer:upgrade:done = %+v, want ok=false error=control_busy requestId=upgrade-1", payload)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for computer:upgrade:done after a busy forward failure")
	}
}

func TestWorkspaceDaemonRuntimeReplacementStopsOldLaunchBeforeNewActivity(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serverResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverResult <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))

		readFrame := func(want func(protocol.Message) bool) (protocol.Message, error) {
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return protocol.Message{}, err
				}
				var frame protocol.Message
				if json.Unmarshal(raw, &frame) == nil && want(frame) {
					return frame, nil
				}
			}
		}
		writeCommand := func(eventType string, payload any) error {
			raw, err := json.Marshal(protocol.Message{Type: eventType, Payload: marshalRaw(payload)})
			if err != nil {
				return err
			}
			return conn.WriteMessage(websocket.TextMessage, raw)
		}
		waitForType := func(eventType string) (protocol.Message, error) {
			return readFrame(func(frame protocol.Message) bool { return frame.Type == eventType })
		}

		if _, err := waitForType(protocol.EventWorkspaceDaemonReady); err != nil {
			serverResult <- fmt.Errorf("read Runner ready: %w", err)
			return
		}

		oldStart := protocol.AgentStartPayload{
			AgentID: "agent-1", RuntimeID: "runtime-codex"}
		if err := writeCommand(protocol.EventDaemonAgentStart, oldStart); err != nil {
			serverResult <- err
			return
		}
		var oldAck, oldActive, oldActivity bool
		for !oldAck || !oldActive || !oldActivity {
			_, err := readFrame(func(frame protocol.Message) bool {
				switch frame.Type {
				case protocol.EventAgentStartAck:
					var ack protocol.AgentStartAckPayload
					if json.Unmarshal(frame.Payload, &ack) == nil && ack.AgentID == oldStart.AgentID {
						oldAck = true
					}
				case protocol.EventAgentStatus:
					var status protocol.AgentStatusPayload
					if json.Unmarshal(frame.Payload, &status) == nil && status.AgentID == oldStart.AgentID && status.Status == protocol.AgentStatusActive {
						oldActive = true
					}
				case protocol.EventAgentActivity:
					var activity protocol.AgentActivityPayload
					if json.Unmarshal(frame.Payload, &activity) == nil && activity.Snapshot.AgentID == oldStart.AgentID {
						oldActivity = true
					}
				}
				return oldAck && oldActive && oldActivity
			})
			if err != nil {
				serverResult <- fmt.Errorf("wait for Codex launch: %w", err)
				return
			}
		}

		if err := writeCommand(protocol.EventDaemonAgentStop, protocol.AgentStopPayload{
			AgentID: oldStart.AgentID}); err != nil {
			serverResult <- err
			return
		}
		var oldInactive bool
		var stopOrderingErr error
		for !oldInactive {
			_, err := readFrame(func(frame protocol.Message) bool {
				switch frame.Type {
				case protocol.EventAgentStatus:
					var status protocol.AgentStatusPayload
					if json.Unmarshal(frame.Payload, &status) == nil && status.AgentID == oldStart.AgentID && status.Status == protocol.AgentStatusInactive {
						oldInactive = true
					}
				case protocol.EventAgentActivity:
					var activity protocol.AgentActivityPayload
					if json.Unmarshal(frame.Payload, &activity) == nil && activity.Snapshot.AgentID == oldStart.AgentID && activity.Snapshot.ActivityKind == protocol.ActivityKindOffline && activity.Snapshot.DetailKind == "stopped" {
						if !oldInactive {
							stopOrderingErr = errors.New("Stopped Activity arrived before inactive status")
							return true
						}
					}
				}
				return oldInactive
			})
			if err != nil {
				serverResult <- fmt.Errorf("wait for Codex stop: %w", err)
				return
			}
			if stopOrderingErr != nil {
				serverResult <- stopOrderingErr
				return
			}
		}

		newStart := protocol.AgentStartPayload{
			AgentID: "agent-1", RuntimeID: "runtime-grok"}
		if err := writeCommand(protocol.EventDaemonAgentStart, newStart); err != nil {
			serverResult <- err
			return
		}
		var newAck, newActive, newActivity bool
		for !newAck || !newActive || !newActivity {
			_, err := readFrame(func(frame protocol.Message) bool {
				switch frame.Type {
				case protocol.EventAgentStartAck:
					var ack protocol.AgentStartAckPayload
					if json.Unmarshal(frame.Payload, &ack) == nil && ack.AgentID == newStart.AgentID {
						newAck = true
					}
				case protocol.EventAgentStatus:
					var status protocol.AgentStatusPayload
					if json.Unmarshal(frame.Payload, &status) == nil && status.AgentID == newStart.AgentID && status.Status == protocol.AgentStatusActive {
						newActive = true
					}
				case protocol.EventAgentActivity:
					var activity protocol.AgentActivityPayload
					if json.Unmarshal(frame.Payload, &activity) == nil && activity.Snapshot.AgentID == newStart.AgentID {
						newActivity = true
					}
				}
				return newAck && newActive && newActivity
			})
			if err != nil {
				serverResult <- fmt.Errorf("wait for Grok launch: %w", err)
				return
			}
		}
		serverResult <- nil
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	for _, runtimeID := range []string{"runtime-codex", "runtime-grok"} {
		d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: "ws-1"}
	}
	d.mu.Unlock()
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.Close()
		runner.inboxes.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.runConnection(ctx) }()

	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for Raft-aligned Runtime replacement")
	}
	if current, found := runner.processes.Snapshot("agent-1"); !found || current.RuntimeID != "runtime-grok" {
		t.Fatalf("replacement Runtime was not retained: %+v found=%v", current, found)
	}
	cancel()
	select {
	case <-runnerDone:
	case <-time.After(time.Second):
		t.Fatal("WorkspaceDaemon did not stop after replacement test")
	}
}

func TestWorkspaceDaemonProviderStartSurvivesControlConnectionClose(t *testing.T) {
	// After a Machine Upgrade the successor reconnects, receives agent:start,
	// then the control socket that delivered that start can die while Codex is
	// still booting. Raft still starts the process on the Computer lifetime.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	acked := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.AgentStartPayload{
			AgentID: "agent-1", RuntimeID: "runtime-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil {
				continue
			}
			if frame.Type == protocol.EventAgentStartAck {
				close(acked)
				<-closed
				return
			}
		}
	}))
	defer server.Close()

	hold := make(chan struct{})
	started := make(chan context.Context, 1)
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.ensureResidentRuntime = func(ctx context.Context, _, _ string, _ *agent.PiRunIdentity) error {
		<-hold
		started <- ctx
		return ctx.Err()
	}
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.inboxes.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- runner.runConnection(ctx) }()
	select {
	case <-acked:
	case <-ctx.Done():
		t.Fatal("timed out waiting for agent:start acknowledgement")
	}
	close(closed)
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("control connection did not close after start acknowledgement")
	}
	close(hold)
	select {
	case providerCtx := <-started:
		if err := providerCtx.Err(); err != nil {
			t.Fatalf("provider start used cancelled control-connection context: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("provider start did not continue after control connection closed")
	}
	launch, ok := runner.processes.Snapshot("agent-1")
	if !ok || !launch.Managed {
		t.Fatal("provider start after upgrade reconnect dropped the managed launch")
	}
}

func TestWorkspaceDaemonStopCancelsBlockedProviderStart(t *testing.T) {
	const (
		workspaceID     = "ws-1"
		runtimeID       = "runtime-1"
		agentID         = "agent-1"
		agentInstanceID = "launch-1"
	)
	d := New(Config{WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceDaemon(t, d, workspaceID, nil)

	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		close(providerStarted)
		<-releaseProvider
		return nil
	}
	type startResult struct {
		status protocol.AgentStatusPayload
		err    error
	}
	startDone := make(chan startResult, 1)
	go func() {
		_, status, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
			AgentID: agentID, RuntimeID: runtimeID})
		startDone <- startResult{status: status, err: err}
	}()
	select {
	case <-providerStarted:
	case <-time.After(time.Second):
		t.Fatal("provider start did not reach the asynchronous startup seam")
	}

	statuses := make(chan protocol.AgentStatusPayload, 2)
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- runner.stopManagedAgent(context.Background(), protocol.AgentStopPayload{
			AgentID: agentID}, nil, func(eventType string, payload any) error {
			if eventType == protocol.EventAgentStatus {
				statuses <- payload.(protocol.AgentStatusPayload)
			}
			return nil
		})
	}()
	select {
	case err := <-stopDone:
		t.Fatalf("stop settled before blocked startup released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseProvider)

	result := <-startDone
	if !errors.Is(result.err, errManagedAgentStartStopped) {
		t.Fatalf("blocked start error = %v, want stop suppression", result.err)
	}
	if result.status.Status == protocol.AgentStatusActive {
		t.Fatalf("blocked start published Active after Stop: %+v", result.status)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("stop blocked start: %v", err)
	}
	select {
	case status := <-statuses:
		if status.AgentID != agentID || status.Status != protocol.AgentStatusInactive {
			t.Fatalf("stop status = %+v", status)
		}
	default:
		t.Fatal("Stop did not publish the terminal inactive status")
	}
	if _, found := runner.processes.Snapshot(agentID); found {
		t.Fatal("stopped launch survived in the process manager")
	}

	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	_, status, _, err := runner.startManagedAgent(context.Background(), protocol.AgentStartPayload{
		AgentID: agentID, RuntimeID: runtimeID})
	if err != nil || status.Status != protocol.AgentStatusActive {
		t.Fatalf("fresh start after settled Stop = status %+v, error %v", status, err)
	}
}

func TestWorkspaceDaemonDuplicateStartDoesNotSpawnProviderTwice(t *testing.T) {
	// One Workspace control cycle carries a heartbeat per Runtime. Until the
	// first start reports Active, the server can therefore replay the same
	// immutable start dispatch several times in one burst. Raft's hasStarting
	// fence acknowledges those replays without starting the provider again.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	twoAcks := make(chan struct{})
	active := make(chan protocol.AgentStatusPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.AgentStartPayload{
			AgentID: "agent-1", RuntimeID: "runtime-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		acks := 0
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil {
				continue
			}
			switch frame.Type {
			case protocol.EventAgentStartAck:
				acks++
				if acks == 2 {
					close(twoAcks)
				}
			case protocol.EventAgentStatus:
				var status protocol.AgentStatusPayload
				if json.Unmarshal(frame.Payload, &status) == nil && status.Status == protocol.AgentStatusActive {
					active <- status
					return
				}
			}
		}
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	providerStarts := make(chan struct{}, 2)
	releaseProvider := make(chan struct{})
	providerReleased := false
	defer func() {
		if !providerReleased {
			close(releaseProvider)
		}
	}()
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		providerStarts <- struct{}{}
		<-releaseProvider
		return nil
	}
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.inboxes.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go runner.runConnection(ctx)
	select {
	case <-twoAcks:
	case <-ctx.Done():
		t.Fatal("timed out waiting for duplicate start acknowledgements")
	}
	select {
	case <-providerStarts:
	case <-ctx.Done():
		t.Fatal("provider start did not begin")
	}
	select {
	case <-providerStarts:
		t.Fatal("duplicate start dispatch spawned the provider twice")
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseProvider)
	providerReleased = true
	select {
	case status := <-active:
		if status.AgentID != "agent-1" {
			t.Fatalf("active status = %+v", status)
		}
	case <-ctx.Done():
		t.Fatal("deduplicated start did not settle Active")
	}
}

func TestWorkspaceDaemonFailedProviderStartPublishesInactiveOnCurrentConnection(t *testing.T) {
	// An accepted start that never becomes Active must not look like residency.
	// After upgrade the server keeps the desired launch; inactive is what
	// lets reconcile send agent:start again.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	got := make(chan protocol.AgentStatusPayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.AgentStartPayload{
			AgentID: "agent-1", RuntimeID: "runtime-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil {
				continue
			}
			if frame.Type != protocol.EventAgentStatus {
				continue
			}
			var status protocol.AgentStatusPayload
			if json.Unmarshal(frame.Payload, &status) != nil {
				continue
			}
			got <- status
			return
		}
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		return errors.New("codex app-server did not start")
	}
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.inboxes.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = runner.runConnection(ctx) }()
	select {
	case status := <-got:
		if status.AgentID != "agent-1" || status.Status != protocol.AgentStatusInactive {
			t.Fatalf("failed start status = %+v, want inactive launch-1", status)
		}
	case <-ctx.Done():
		t.Fatal("failed provider start did not publish inactive status")
	}
}

func TestWorkspaceDaemonOwnsCurrentControlPlaneHeartbeat(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	heartbeats := make(chan protocol.DaemonHeartbeatRequestPayload, 1)
	acks := make(chan *HeartbeatResponse, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Error(err)
			return
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil {
				continue
			}
			if frame.Type == protocol.EventWorkspaceDaemonReady {
				var ready protocol.WorkspaceReadyPayload
				if json.Unmarshal(frame.Payload, &ready) != nil {
					t.Error("invalid ready payload")
					return
				}
				found := false
				for _, capability := range ready.ActiveCapabilities {
					found = found || capability == protocol.DaemonCapabilityWorkspaceDaemonControlPlane
				}
				if !found {
					t.Error("ready did not advertise WorkspaceDaemon control plane")
					return
				}
				continue
			}
			if frame.Type != protocol.EventDaemonHeartbeat {
				continue
			}
			var heartbeat protocol.DaemonHeartbeatRequestPayload
			if json.Unmarshal(frame.Payload, &heartbeat) != nil {
				t.Error("invalid heartbeat payload")
				return
			}
			heartbeats <- heartbeat
			ack, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonHeartbeatAck, Payload: marshalRaw(protocol.DaemonHeartbeatAckPayload{
				RuntimeID: heartbeat.RuntimeID, Status: "ok",
			})})
			if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
				t.Error(err)
				return
			}
			<-r.Context().Done()
			return
		}
	}))
	defer server.Close()

	d := New(Config{
		ServerBaseURL: server.URL, DaemonID: "daemon-1",
		WorkspacesRoot: t.TempDir(), HeartbeatInterval: 10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.controlHeartbeatAck = func(_ context.Context, ack *HeartbeatResponse) {
		acks <- ack
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	go runner.runConnection(ctx)
	select {
	case heartbeat := <-heartbeats:
		if heartbeat.RuntimeID != "runtime-1" {
			t.Fatalf("heartbeat = %+v, want runtime-1", heartbeat)
		}
	case <-ctx.Done():
		t.Fatal("WorkspaceDaemon did not send the current control-plane heartbeat")
	}
	select {
	case ack := <-acks:
		if ack.RuntimeID != "runtime-1" || ack.Status != "ok" {
			t.Fatalf("consumed heartbeat acknowledgement = %+v", ack)
		}
	case <-ctx.Done():
		t.Fatal("WorkspaceDaemon did not consume its heartbeat acknowledgement")
	}
}

func TestWorkspaceDaemonStartCreatesCoordinatorForCommandRuntime(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.AgentStartPayload{
			AgentID: "agent-1", RuntimeID: "runtime-new"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		for responses := 0; responses < 3; responses++ {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil || frame.Type != protocol.EventAgentStartAck {
				continue
			}
			started <- struct{}{}
			return
		}
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-old"] = Runtime{ID: "runtime-old", WorkspaceID: "ws-1"}
	d.runtimeIndex["runtime-new"] = Runtime{ID: "runtime-new", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go runner.runConnection(ctx)
	select {
	case <-started:
		_, runtimeID, ok := runner.messageCoordinator("agent-1")
		if !ok || runtimeID != "runtime-new" {
			t.Fatalf("coordinator runtime=%q ok=%v, want runtime-new", runtimeID, ok)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for start-owned coordinator")
	}
}

func TestWorkspaceDaemonAcknowledgesCanonicalMessageDeliveryWithoutRuntime(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	acknowledgements := make(chan protocol.AgentDeliverAckPayload, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		delivery, _ := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: marshalRaw(protocol.AgentDeliverPayload{
			AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
			Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"},
		})})
		for attempt := 0; attempt < 2; attempt++ {
			if err := conn.WriteMessage(websocket.TextMessage, delivery); err != nil {
				t.Error(err)
				return
			}
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				var message protocol.Message
				if err := json.Unmarshal(raw, &message); err != nil {
					t.Error(err)
					return
				}
				if message.Type != protocol.EventAgentDeliverAck {
					continue
				}
				var ack protocol.AgentDeliverAckPayload
				if err := json.Unmarshal(message.Payload, &ack); err != nil {
					t.Error(err)
					return
				}
				acknowledgements <- ack
				break
			}
		}
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		return errors.New("resident Runtime unavailable")
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	// Delivery acknowledgement depends only on coordinator acceptance. No
	// resident Runtime slot or provider client exists in this transport test.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	runner, err := d.newWorkspaceDaemon("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	registerTestWorkspaceDaemonInbox(t, runner, InboxKey{WorkspaceID: "ws-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.inboxes.Close()
	})
	go func() { errCh <- runner.runConnection(ctx) }()
	for attempt := 0; attempt < 2; attempt++ {
		var ack protocol.AgentDeliverAckPayload
		select {
		case ack = <-acknowledgements:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Runner delivery acknowledgement %d", attempt+1)
		}
		if ack.AgentID != "agent-1" || ack.Seq != 1 || ack.DeliveryID != "delivery-1" {
			t.Fatalf("delivery acknowledgement=%+v", ack)
		}
	}
	<-errCh
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("Pending after duplicate delivery = %+v, want exactly message-1", pending)
	}
	if boundary := coordinator.Boundaries()["channel:one"]; boundary != 0 {
		t.Fatalf("Context Boundary after ACK = %d, want 0", boundary)
	}
}
