package main

import (
	"fmt"
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

func TestRunThreadUnfollowUsesThreadFollowDeleteEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		wantChannel string
		wantMessage string
	}{
		{
			name:        "channel message target",
			target:      "#channel-123:message-456",
			wantChannel: "channel-123",
			wantMessage: "message-456",
		},
		{
			name:        "workspace channel message target",
			target:      "#workspace-123:channel-789:message-abc",
			wantChannel: "channel-789",
			wantMessage: "message-abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawRequest bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawRequest = true
				wantPath := fmt.Sprintf("/api/channels/%s/messages/%s/thread/follow", tt.wantChannel, tt.wantMessage)
				if r.Method != http.MethodDelete {
					t.Errorf("method = %s, want DELETE", r.Method)
				}
				if r.URL.Path != wantPath {
					t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
				}
				w.WriteHeader(http.StatusNoContent)
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
	cmd := newThreadUnfollowTestCmd("#channel-only")
	if err := runThreadUnfollow(cmd, nil); err == nil {
		t.Fatal("expected invalid target error")
	}
}
