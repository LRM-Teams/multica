package daemon

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestTaskWakeupURL(t *testing.T) {
	if taskWakeupReadLimit < 10<<20 {
		t.Fatalf("taskWakeupReadLimit = %d, want at least 10MiB to match daemonws hub", taskWakeupReadLimit)
	}
	tests := []struct {
		name       string
		baseURL    string
		runtimeIDs []string
		want       string
	}{
		{
			name:       "http base",
			baseURL:    "http://localhost:8080",
			runtimeIDs: []string{"runtime-b", "runtime-a"},
			want:       "ws://localhost:8080/api/daemon/ws?runtime_ids=runtime-a%2Cruntime-b",
		},
		{
			name:       "https base",
			baseURL:    "https://api.example.com",
			runtimeIDs: []string{"runtime-1"},
			want:       "wss://api.example.com/api/daemon/ws?runtime_ids=runtime-1",
		},
		{
			name:       "base path",
			baseURL:    "https://api.example.com/multica",
			runtimeIDs: []string{"runtime-1"},
			want:       "wss://api.example.com/multica/api/daemon/ws?runtime_ids=runtime-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := taskWakeupURL(tt.baseURL, tt.runtimeIDs)
			if err != nil {
				t.Fatalf("taskWakeupURL: %v", err)
			}
			if got != tt.want {
				t.Fatalf("taskWakeupURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTaskWakeupDialer_ProxyAndNoProxyRouteContract(t *testing.T) {
	const helperEnv = "MULTICA_TEST_WAKEUP_PROXY_HELPER"
	if os.Getenv(helperEnv) == "1" {
		dialer := taskWakeupDialer()
		if dialer.Proxy == nil {
			t.Fatal("task wakeup dialer has nil Proxy; HTTP_PROXY would direct-connect")
		}

		proxied, err := dialer.Proxy(&http.Request{URL: &url.URL{
			Scheme: "http",
			Host:   "proxied.example",
		}})
		if err != nil {
			t.Fatalf("proxy decision for proxied target: %v", err)
		}
		if proxied == nil || proxied.String() != "http://127.0.0.1:3128" {
			t.Fatalf("proxied target decision = %v, want http://127.0.0.1:3128", proxied)
		}

		bypassed, err := dialer.Proxy(&http.Request{URL: &url.URL{
			Scheme: "http",
			Host:   "bypass.example",
		}})
		if err != nil {
			t.Fatalf("proxy decision for NO_PROXY target: %v", err)
		}
		if bypassed != nil {
			t.Fatalf("NO_PROXY target used proxy %v, want direct connection", bypassed)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestTaskWakeupDialer_ProxyAndNoProxyRouteContract$")
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			name = entry[:idx]
		}
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env,
		helperEnv+"=1",
		"HTTP_PROXY=http://127.0.0.1:3128",
		"NO_PROXY=bypass.example",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proxy contract subprocess failed: %v\n%s", err, output)
	}
}

func TestLegacyRuntimeWakeSocketDoesNotExecuteControlAcknowledgements(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		ack, _ := json.Marshal(protocol.Message{
			Type: protocol.EventDaemonHeartbeatAck,
			Payload: marshalRaw(protocol.DaemonHeartbeatAckPayload{
				RuntimeID: "runtime-1",
				Status:    "ok",
				PendingRestart: &protocol.DaemonHeartbeatPendingRestart{
					ID: "must-not-run",
				},
			}),
		})
		if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
			t.Error(err)
			return
		}
		wake, _ := json.Marshal(protocol.Message{
			Type: protocol.EventDaemonTaskAvailable,
			Payload: marshalRaw(protocol.TaskAvailablePayload{
				RuntimeID: "runtime-1",
				TaskID:    "task-1",
			}),
		})
		if err := conn.WriteMessage(websocket.TextMessage, wake); err != nil {
			t.Error(err)
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	wakeups := make(chan taskWakeup, 1)
	readDone := make(chan error, 1)
	go func() {
		readDone <- d.readTaskWakeupMessages(conn, wakeups, nil, nil)
	}()
	select {
	case wake := <-wakeups:
		if wake.runtimeID != "runtime-1" {
			t.Fatalf("wake runtime = %q", wake.runtimeID)
		}
	case <-time.After(time.Second):
		t.Fatal("task wake socket stopped after ignored control acknowledgement")
	}
	_ = conn.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("task wake reader did not stop")
	}
}
