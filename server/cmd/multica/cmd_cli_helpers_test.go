package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewAPIClientUsesLocalCredentialProxyForAgentRuns(t *testing.T) {
	var gotRequest bool
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequest = true
		if r.URL.Path != "/api/agent/reminders/list" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("agent process sent authorization %q", got)
		}
		for name, want := range map[string]string{"X-Agent-ID": "agent-1", "X-Workspace-ID": "workspace-1"} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		for _, name := range []string{"X-Task-ID", "X-Agent-Inbox-Event-ID", "X-Agent-Inbox-Delivery-ID", "X-Agent-Inbox-Lease-Token"} {
			if got := r.Header.Get(name); got != "" {
				t.Errorf("%s = %q, want absent", name, got)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"reminders": []any{}})
	}))
	defer local.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(local.URL, "http://"))
	if err != nil {
		t.Fatalf("local proxy port: %v", err)
	}
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_SERVER_URL", "https://server.example.invalid")
	t.Setenv("MULTICA_TOKEN", "user-token-must-not-leave-agent")
	t.Setenv("MULTICA_TASK_ID", "retired-task")
	t.Setenv("MULTICA_AGENT_INBOX_EVENT_ID", "retired-event")
	t.Setenv("MULTICA_AGENT_INBOX_DELIVERY_ID", "retired-delivery")
	t.Setenv("MULTICA_AGENT_INBOX_LEASE_TOKEN", "retired-lease")

	client, err := newAPIClient(&cobra.Command{})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	if client.BaseURL != local.URL || client.Token != "" || client.AgentID != "agent-1" {
		t.Fatalf("agent client = %+v, want local credential proxy without bearer", client)
	}
	if client.TaskID != "" || client.AgentInboxEventID != "" || client.AgentInboxDeliveryID != "" || client.AgentInboxLeaseToken != "" {
		t.Fatalf("agent client retained retired execution context: %+v", client)
	}
	var response map[string]any
	if err := client.PostJSON(t.Context(), "/api/agent/reminders/list", map[string]any{"status": "active"}, &response); err != nil {
		t.Fatalf("local API request: %v", err)
	}
	if !gotRequest {
		t.Fatal("agent request did not reach local credential proxy")
	}
}

func TestNewAPIClientRequiresDaemonPortForAgentRuns(t *testing.T) {
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_DAEMON_PORT", "0")
	if _, err := newAPIClient(&cobra.Command{}); err == nil || !strings.Contains(err.Error(), "MULTICA_DAEMON_PORT") {
		t.Fatalf("newAPIClient error = %v, want daemon port requirement", err)
	}
}

func TestAgentAPIPathSelectionUsesDaemonExecutionWithoutBearer(t *testing.T) {
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_TOKEN", "")
	t.Setenv("MULTICA_TOKEN_FILE", "")
	if !isAgentAPIToken(&cobra.Command{}) {
		t.Fatal("daemon agent execution must select dedicated Agent APIs")
	}
	if !isAgentAPITokenAmbient() {
		t.Fatal("daemon agent execution must select dedicated Agent APIs in resolvers")
	}
}

func TestFetchWorkspacesUsesLocalCredentialProxyForDaemonAgentRun(t *testing.T) {
	var gotRequest bool
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequest = true
		if r.Method != http.MethodGet || r.URL.Path != "/api/agent/workspace" {
			t.Errorf("request = %s %s, want GET /api/agent/workspace", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("agent process sent authorization %q", got)
		}
		for name, want := range map[string]string{"X-Agent-ID": "agent-1", "X-Workspace-ID": "workspace-1"} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "workspace-1", "name": "One", "slug": "one"})
	}))
	defer local.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(local.URL, "http://"))
	if err != nil {
		t.Fatalf("local proxy port: %v", err)
	}
	t.Setenv("MULTICA_AGENT_ID", "agent-1")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-1")
	t.Setenv("MULTICA_DAEMON_PORT", port)
	t.Setenv("MULTICA_TOKEN", "")
	t.Setenv("MULTICA_TOKEN_FILE", "")

	workspaces, err := fetchWorkspaces(t.Context(), &cobra.Command{})
	if err != nil {
		t.Fatalf("fetchWorkspaces: %v", err)
	}
	if !gotRequest || len(workspaces) != 1 || workspaces[0].ID != "workspace-1" {
		t.Fatalf("workspaces = %+v, local proxy hit=%v", workspaces, gotRequest)
	}
}
