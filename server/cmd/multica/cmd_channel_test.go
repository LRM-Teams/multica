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
