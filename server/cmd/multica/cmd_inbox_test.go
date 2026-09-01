package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/daemon"
	"github.com/spf13/cobra"
)

func setInboxProxyEnv(t *testing.T, serverURL string) {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(serverURL, "http://"))
	if err != nil {
		t.Fatalf("local proxy port: %v", err)
	}
	tokenFile := filepath.Join(t.TempDir(), "agent-proxy.token")
	if err := os.WriteFile(tokenFile, []byte("mpt_test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv(daemon.AgentProxyTokenFileEnv, tokenFile)
}

func TestRunInboxCheckUsesLocalAggregateInbox(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != localAgentInboxPath {
			t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, localAgentInboxPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mpt_test-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Agent-ID"); got != "agent-1" {
			t.Errorf("X-Agent-ID = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"source": "app", "itemId": "reminder:one:1", "appId": "system.reminder",
				"notificationClass": "due", "sourceRef": map[string]string{"kind": "reminder", "id": "one", "revision": "1"},
			}},
		})
	}))
	defer srv.Close()
	setInboxProxyEnv(t, srv.URL)

	var output bytes.Buffer
	if err := runInboxCheckWithWriter(&cobra.Command{}, &output); err != nil {
		t.Fatalf("runInboxCheckWithWriter: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if got := output.String(); !strings.Contains(got, "App items: 1") || !strings.Contains(got, "reminder:one:1") {
		t.Fatalf("output = %q", got)
	}
}

func TestRunInboxAckUsesLocalAggregateInbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != localAgentInboxAckPath {
			t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, localAgentInboxAckPath)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["itemId"] != "reminder:one:1" || len(body) != 1 {
			t.Errorf("body = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "itemId": "reminder:one:1", "remaining_app_items": 2})
	}))
	defer srv.Close()
	setInboxProxyEnv(t, srv.URL)

	cmd := &cobra.Command{}
	cmd.Flags().String("item-id", "", "")
	if err := cmd.Flags().Set("item-id", "reminder:one:1"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := runInboxAck(cmd, nil); err != nil {
		t.Fatalf("runInboxAck: %v", err)
	}
	if got := output.String(); !strings.Contains(got, "Inbox item reminder:one:1 acknowledged; 2 app item(s) remain.") {
		t.Fatalf("output = %q", got)
	}
}
