package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
)

const channelMuteTestID = "11111111-1111-1111-1111-111111111111"

func newChannelMuteTestCmd(t *testing.T, target string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "mute"}
	cmd.Flags().String("target", "", "")
	if err := cmd.Flags().Set("target", target); err != nil {
		t.Fatalf("set target flag: %v", err)
	}
	return cmd
}

func newChannelMemberAddTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("target", "", "")
	cmd.Flags().String("output", "table", "")
	return cmd
}

func TestSetChannelMuteResolvesNameAndUsesAgentEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-123")

	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channels":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": channelMuteTestID, "name": "斗地主开发"}})
		case r.Method == http.MethodPut && r.URL.Path == "/api/channels/"+channelMuteTestID+"/agent-mute":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "muted": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	if err := setChannelMute(newChannelMuteTestCmd(t, "#斗地主开发"), true); err != nil {
		t.Fatalf("setChannelMute: %v", err)
	}
	want := []string{
		"GET /api/channels",
		"PUT /api/channels/" + channelMuteTestID + "/agent-mute",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests[%d] = %q, want %q (all=%v)", i, requests[i], want[i], requests)
		}
	}
}

func TestSetChannelUnmuteUsesAgentEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-123")

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/channels":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": channelMuteTestID, "name": "斗地主开发"}})
		case r.URL.Path == "/api/channels/"+channelMuteTestID+"/agent-mute":
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	if err := setChannelMute(newChannelMuteTestCmd(t, "#斗地主开发"), false); err != nil {
		t.Fatalf("setChannelMute: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/channels/"+channelMuteTestID+"/agent-mute" {
		t.Fatalf("unmute request = %s %s, want DELETE /api/channels/%s/agent-mute", gotMethod, gotPath, channelMuteTestID)
	}
}

// TestChannelMemberAddCommandIsRegistered verifies the `channel member add`
// subcommand and its required --target flag are wired up.
func TestChannelMemberAddCommandIsRegistered(t *testing.T) {
	memberCmd, _, err := channelCmd.Find([]string{"member"})
	if err != nil || memberCmd == nil || memberCmd.Name() != "member" {
		t.Fatalf("channel member subcommand not registered; got %#v (err %v)", memberCmd, err)
	}
	addCmd, _, err := memberCmd.Find([]string{"add"})
	if err != nil || addCmd == nil || addCmd.Name() != "add" {
		t.Fatalf("channel member add subcommand not registered; got %#v (err %v)", addCmd, err)
	}
	if addCmd.Flags().Lookup("target") == nil {
		t.Fatal("channel member add missing --target flag")
	}
}

// TestRunChannelMemberAddResolvesNamesAndPostsBatch verifies that agent refs
// given as display names are resolved to ids against /api/agents, the channel
// name against /api/channels, and a single batch POST is sent.
func TestRunChannelMemberAddResolvesNamesAndPostsBatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-123")

	var gotPath, gotMethod string
	var gotBody map[string]any
	var agentsCalled, channelsCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/agents":
			agentsCalled = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "agent-lao-hu", "name": "lao_hu", "display_name": "老胡"},
				{"id": "agent-xiao-lin", "name": "xiao_lin", "display_name": "小林"},
			})
		case r.URL.Path == "/api/channels":
			channelsCalled = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "channel-doudizhu", "name": "斗地主开发"},
			})
		case r.URL.Path == "/api/channels/channel-doudizhu/members/batch":
			gotMethod = r.Method
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd := newChannelMemberAddTestCmd()
	_ = cmd.Flags().Set("target", "#斗地主开发")
	if err := runChannelMemberAdd(cmd, []string{"老胡", "小林"}); err != nil {
		t.Fatalf("runChannelMemberAdd: %v", err)
	}

	if !agentsCalled {
		t.Fatal("expected /api/agents to be called for name resolution")
	}
	if !channelsCalled {
		t.Fatal("expected /api/channels to be called for name resolution")
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/channels/channel-doudizhu/members/batch" {
		t.Fatalf("path = %q, want the batch endpoint", gotPath)
	}
	members, ok := gotBody["members"].([]any)
	if !ok || len(members) != 2 {
		t.Fatalf("body members = %#v, want 2 entries", gotBody["members"])
	}
	for _, m := range members {
		row, ok := m.(map[string]any)
		if !ok || row["member_type"] != "agent" {
			t.Fatalf("batch entry not agent-only: %#v", m)
		}
	}
}

// TestRunChannelMemberAddRejectsUnknownAgent verifies a bad agent name surfaces
// a clear error instead of silently posting a partial batch.
func TestRunChannelMemberAddRejectsUnknownAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-123")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agents":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case "/api/channels":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "ch-1", "name": "general"}})
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)

	cmd := newChannelMemberAddTestCmd()
	_ = cmd.Flags().Set("target", "ch-1")
	err := runChannelMemberAdd(cmd, []string{"Ghost"})
	if err == nil {
		t.Fatal("expected an error for an unknown agent name")
	}
}

// TestRunChannelMemberAddRequiresTarget verifies the --target flag is required.
func TestRunChannelMemberAddRequiresTarget(t *testing.T) {
	cmd := newChannelMemberAddTestCmd()
	if err := runChannelMemberAdd(cmd, []string{"a"}); err == nil {
		t.Fatal("expected missing --target error")
	}
}
