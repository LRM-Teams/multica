package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
)

func newThreadUnfollowTestCmd(target string) *cobra.Command {
	cmd := &cobra.Command{Use: "unfollow"}
	cmd.Flags().String("target", target, "")
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("server-url", "", "")
	return cmd
}

func TestRunThreadUnfollowPostsRawTargetToAgentTransport(t *testing.T) {
	tests := []struct {
		name   string
		target string
	}{
		{
			name:   "channel message target",
			target: "#engineering:message-456",
		},
		{
			name:   "dm message target",
			target: "dm:@alice:message-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawRequest bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawRequest = true
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/api/agent/threads/unfollow" {
					t.Errorf("path = %s, want /api/agent/threads/unfollow", r.URL.Path)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode body: %v", err)
				}
				if body["target"] != tt.target {
					t.Errorf("body target = %q, want %q", body["target"], tt.target)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			t.Setenv("HOME", t.TempDir())
			t.Setenv("MULTICA_SERVER_URL", srv.URL)
			t.Setenv("MULTICA_TOKEN", "test-token")

			cmd := newThreadUnfollowTestCmd(tt.target)
			if err := runThreadUnfollow(cmd, nil); err != nil {
				t.Fatalf("runThreadUnfollow: %v", err)
			}
			if !sawRequest {
				t.Fatal("expected the test server to receive an unfollow request")
			}
		})
	}
}

func TestRunThreadUnfollowRejectsInvalidTarget(t *testing.T) {
	cmd := newThreadUnfollowTestCmd("")
	if err := runThreadUnfollow(cmd, nil); err == nil {
		t.Fatal("expected invalid target error")
	}
}
