package daemon

import (
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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

// TestWSHeartbeatFreshnessSuppressesHTTP pins the WS-vs-HTTP coordination:
// once a runtime acked over WS within the freshness window the HTTP
// heartbeat loop must skip it to avoid duplicate DB writes.
func TestWSHeartbeatFreshnessSuppressesHTTP(t *testing.T) {
	d := New(Config{HeartbeatInterval: 15 * time.Second}, slog.Default())

	if d.wsHeartbeatRecentlyAcked("runtime-1") {
		t.Fatalf("expected unrecorded runtime to be stale")
	}

	d.recordWSHeartbeatAck("runtime-1")
	if !d.wsHeartbeatRecentlyAcked("runtime-1") {
		t.Fatalf("expected just-acked runtime to be fresh")
	}

	// Force the entry past the freshness window.
	d.wsHBMu.Lock()
	d.wsHBLastAck["runtime-1"] = time.Now().Add(-d.wsHeartbeatFreshness() - time.Second)
	d.wsHBMu.Unlock()
	if d.wsHeartbeatRecentlyAcked("runtime-1") {
		t.Fatalf("expected aged runtime to be stale (HTTP heartbeat must resume)")
	}

	d.recordWSHeartbeatAck("runtime-2")
	d.clearWSHeartbeatAcks()
	if d.wsHeartbeatRecentlyAcked("runtime-2") {
		t.Fatalf("expected clearWSHeartbeatAcks to drop all entries")
	}
}
