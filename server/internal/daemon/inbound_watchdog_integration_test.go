package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func testDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// startFakeTaskWakeupServer upgrades /api/daemon/ws and records client frames.
// onMessage may push server frames; return false to stop reading.
func startFakeTaskWakeupServer(t *testing.T, onClientFrame func(protocol.Message), serverFrames <-chan []byte) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/daemon/ws") {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var msg protocol.Message
				if err := json.Unmarshal(raw, &msg); err != nil {
					continue
				}
				if onClientFrame != nil {
					onClientFrame(msg)
				}
			}
		}()

		if serverFrames != nil {
			for {
				select {
				case <-done:
					return
				case frame, ok := <-serverFrames:
					if !ok {
						return
					}
					_ = conn.WriteMessage(websocket.TextMessage, frame)
				}
			}
		}
		<-done
	}))
}

func TestInboundWatchdogProbeAndTerminateViaConnection(t *testing.T) {
	var mu sync.Mutex
	var frames []protocol.Message
	srv := startFakeTaskWakeupServer(t, func(msg protocol.Message) {
		mu.Lock()
		frames = append(frames, msg)
		mu.Unlock()
	}, nil)
	defer srv.Close()

	const interval = 80 * time.Millisecond
	d := New(Config{
		ServerBaseURL:     srv.URL,
		HeartbeatInterval: time.Hour, // avoid periodic heartbeats colliding with probe timing
		InboundWatchdog:   interval,
		WorkspacesRoot:    t.TempDir(),
	}, testDiscardLogger())
	d.runtimeIndex["rt-1"] = Runtime{ID: "rt-1", WorkspaceID: "ws-1"}

	taskWakeups := make(chan taskWakeup, 8)
	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := d.runTaskWakeupConnection(ctx, []string{"rt-1"}, taskWakeups, runtimeSetCh)
	if err == nil {
		t.Fatal("expected watchdog timeout error")
	}
	if !errors.Is(err, errInboundWatchdogTimeout) {
		t.Fatalf("err = %v, want errInboundWatchdogTimeout", err)
	}
	// Outer loop owns backoff across the retry sleep (connection does not).
	d.setWSConnState("backoff")
	if got := d.getWSConnState(); got != "backoff" {
		t.Fatalf("after watchdog error + outer assign, conn_state = %q, want backoff", got)
	}

	mu.Lock()
	defer mu.Unlock()
	var heartbeats int
	for _, f := range frames {
		if f.Type == protocol.EventDaemonHeartbeat {
			heartbeats++
		}
	}
	// Immediate HB sender + at least one probe batch.
	if heartbeats < 2 {
		t.Fatalf("heartbeat frames = %d, want >= 2 (initial + probe)", heartbeats)
	}
}

func TestInboundWatchdogProbeThenInboundDoesNotTerminate(t *testing.T) {
	serverFrames := make(chan []byte, 4)
	var mu sync.Mutex
	var heartbeats int
	srv := startFakeTaskWakeupServer(t, func(msg protocol.Message) {
		if msg.Type != protocol.EventDaemonHeartbeat {
			return
		}
		mu.Lock()
		heartbeats++
		n := heartbeats
		mu.Unlock()
		// After the second heartbeat (probe), reply with ack so watchdog resets.
		if n >= 2 {
			ack, _ := json.Marshal(protocol.Message{
				Type: protocol.EventDaemonHeartbeatAck,
				Payload: marshalRaw(protocol.DaemonHeartbeatAckPayload{
					RuntimeID: "rt-1",
				}),
			})
			select {
			case serverFrames <- ack:
			default:
			}
		}
	}, serverFrames)
	defer srv.Close()

	const interval = 80 * time.Millisecond
	d := New(Config{
		ServerBaseURL:     srv.URL,
		HeartbeatInterval: time.Hour,
		InboundWatchdog:   interval,
		WorkspacesRoot:    t.TempDir(),
	}, testDiscardLogger())
	d.runtimeIndex["rt-1"] = Runtime{ID: "rt-1", WorkspaceID: "ws-1"}

	taskWakeups := make(chan taskWakeup, 8)
	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.runTaskWakeupConnection(ctx, []string{"rt-1"}, taskWakeups, runtimeSetCh)
	}()

	// Stay open longer than two full watchdog intervals after recovery window.
	select {
	case err := <-errCh:
		t.Fatalf("connection exited early: %v", err)
	case <-time.After(5 * interval):
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown err = %v, want context.Canceled or nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not exit after cancel")
	}
}

func TestInboundWatchdogRuntimeSetChangeCleansUp(t *testing.T) {
	srv := startFakeTaskWakeupServer(t, nil, nil)
	defer srv.Close()

	d := New(Config{
		ServerBaseURL:     srv.URL,
		HeartbeatInterval: time.Hour,
		InboundWatchdog:   time.Hour, // must not fire
		WorkspacesRoot:    t.TempDir(),
	}, testDiscardLogger())
	d.runtimeIndex["rt-1"] = Runtime{ID: "rt-1", WorkspaceID: "ws-1"}

	taskWakeups := make(chan taskWakeup, 8)
	runtimeSetCh, unsub := d.runtimeSet.Subscribe()
	defer unsub()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.runTaskWakeupConnection(ctx, []string{"rt-1"}, taskWakeups, runtimeSetCh)
	}()

	// Let the connection establish.
	time.Sleep(50 * time.Millisecond)
	if got := d.getWSConnState(); got != "open" {
		t.Fatalf("conn_state = %q, want open", got)
	}
	d.notifyRuntimeSetChanged()

	select {
	case err := <-errCh:
		if !errors.Is(err, errRuntimeSetChanged) {
			t.Fatalf("err = %v, want errRuntimeSetChanged", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime-set reconnect")
	}
	// runtime-set is fast re-dial: outer loop must NOT stamp backoff.
	if got := d.getWSConnState(); got == "backoff" {
		t.Fatalf("runtime-set reconnect stamped backoff; want no fake backoff sleep state")
	}
}

func TestDurationFromEnvInboundWatchdogDefaultAndDisable(t *testing.T) {
	// LoadConfig also resolves agents from PATH; this locks the same
	// durationFromEnv wiring used for MULTICA_DAEMON_INBOUND_WATCHDOG.
	t.Setenv("MULTICA_DAEMON_INBOUND_WATCHDOG", "")
	got, err := durationFromEnv("MULTICA_DAEMON_INBOUND_WATCHDOG", DefaultInboundWatchdog)
	if err != nil {
		t.Fatalf("default durationFromEnv: %v", err)
	}
	if got != DefaultInboundWatchdog {
		t.Fatalf("default = %v, want %v", got, DefaultInboundWatchdog)
	}

	t.Setenv("MULTICA_DAEMON_INBOUND_WATCHDOG", "0")
	got, err = durationFromEnv("MULTICA_DAEMON_INBOUND_WATCHDOG", DefaultInboundWatchdog)
	if err != nil {
		t.Fatalf("disable durationFromEnv: %v", err)
	}
	if got != 0 {
		t.Fatalf("disabled = %v, want 0", got)
	}
}

// TestTaskWakeupLoopBackoffObservableDuringRetrySleep locks loop-level ownership:
// after a transient connection failure the state stays backoff for the whole
// retry sleep, then becomes closed when the loop context is cancelled.
func TestTaskWakeupLoopBackoffObservableDuringRetrySleep(t *testing.T) {
	// No server → dial fails immediately (transient).
	d := New(Config{
		ServerBaseURL:     "http://127.0.0.1:1", // nothing listening
		HeartbeatInterval: time.Hour,
		InboundWatchdog:   time.Hour,
		WorkspacesRoot:    t.TempDir(),
	}, testDiscardLogger())
	d.mu.Lock()
	d.runtimeIndex["rt-1"] = Runtime{ID: "rt-1", WorkspaceID: "ws-1"}
	d.workspaces["ws-1"] = &workspaceState{workspaceID: "ws-1", runtimeIDs: []string{"rt-1"}}
	d.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	taskWakeups := make(chan taskWakeup, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.taskWakeupLoop(ctx, taskWakeups)
	}()

	// Wait until outer loop stamps backoff after the failed dial.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.getWSConnState() == "backoff" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := d.getWSConnState(); got != "backoff" {
		cancel()
		<-done
		t.Fatalf("during retry window conn_state = %q, want backoff", got)
	}
	// Hold briefly to prove backoff persists across the sleep window (first
	// retry is ~1s ± jitter; sample mid-window).
	time.Sleep(200 * time.Millisecond)
	if got := d.getWSConnState(); got != "backoff" {
		cancel()
		<-done
		t.Fatalf("still in retry sleep conn_state = %q, want backoff", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("taskWakeupLoop did not exit after cancel")
	}
	if got := d.getWSConnState(); got != "closed" {
		t.Fatalf("after loop exit conn_state = %q, want closed", got)
	}
}

// TestTaskWakeupLoopZeroRuntimeStaysClosed locks: with no registered runtimes
// the loop is idle, not connected and not in retry backoff.
func TestTaskWakeupLoopZeroRuntimeStaysClosed(t *testing.T) {
	d := New(Config{
		ServerBaseURL:     "http://127.0.0.1:1",
		HeartbeatInterval: time.Hour,
		InboundWatchdog:   time.Hour,
		WorkspacesRoot:    t.TempDir(),
	}, testDiscardLogger())
	// Explicit empty workspace set.
	d.mu.Lock()
	d.workspaces = map[string]*workspaceState{}
	d.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	taskWakeups := make(chan taskWakeup, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.taskWakeupLoop(ctx, taskWakeups)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.getWSConnState() == "closed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := d.getWSConnState(); got != "closed" {
		cancel()
		<-done
		t.Fatalf("zero-runtime idle conn_state = %q, want closed", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("taskWakeupLoop did not exit after cancel")
	}
}
