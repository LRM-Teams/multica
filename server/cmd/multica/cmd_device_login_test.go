package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// newAutoWatchTestCmd builds a standalone cobra command with the flags
// configureSelectedWorkspace reads, mirroring newWorkspaceSwitchTestCmd's rationale
// (cmd_workspace_test.go) — the real loginCmd carries a parent root's flags
// we can't easily replicate here.
func newAutoWatchTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "login"}
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("workspace", "", "")
	cmd.Flags().String(callbackHostFlag, "", "")
	return cmd
}

// TestApplyWorkspacePositional locks the `multica setup <workspace>` /
// `--workspace` interplay: the positional sets the same flag
// configureSelectedWorkspace reads, but an explicitly-passed flag always wins over
// the positional (flag is the more specific signal).
func TestApplyWorkspacePositional(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "setup"}
		cmd.Flags().String("workspace", "", "")
		return cmd
	}

	t.Run("positional sets the flag", func(t *testing.T) {
		cmd := newCmd()
		if err := applyWorkspacePositional(cmd, []string{"my-workspace"}); err != nil {
			t.Fatalf("applyWorkspacePositional: %v", err)
		}
		got, _ := cmd.Flags().GetString("workspace")
		if got != "my-workspace" {
			t.Fatalf("workspace flag = %q, want %q", got, "my-workspace")
		}
	})

	// The slash is required by the product command shape and stripped before
	// resolving the selected workspace.
	t.Run("leading slash is stripped (Raft-aligned form)", func(t *testing.T) {
		cmd := newCmd()
		if err := applyWorkspacePositional(cmd, []string{"/my-workspace"}); err != nil {
			t.Fatalf("applyWorkspacePositional: %v", err)
		}
		got, _ := cmd.Flags().GetString("workspace")
		if got != "my-workspace" {
			t.Fatalf("workspace flag = %q, want %q (leading slash stripped)", got, "my-workspace")
		}
	})

	t.Run("no positional leaves the flag untouched", func(t *testing.T) {
		cmd := newCmd()
		if err := applyWorkspacePositional(cmd, nil); err != nil {
			t.Fatalf("applyWorkspacePositional: %v", err)
		}
		if cmd.Flags().Changed("workspace") {
			t.Fatal("workspace flag should not be marked changed when no positional was given")
		}
	})

	t.Run("explicit flag wins over positional", func(t *testing.T) {
		cmd := newCmd()
		if err := cmd.Flags().Set("workspace", "explicit-flag-value"); err != nil {
			t.Fatal(err)
		}
		if err := applyWorkspacePositional(cmd, []string{"positional-value"}); err != nil {
			t.Fatalf("applyWorkspacePositional: %v", err)
		}
		got, _ := cmd.Flags().GetString("workspace")
		if got != "explicit-flag-value" {
			t.Fatalf("workspace flag = %q, want the explicitly-set flag value to win", got)
		}
	})
}

func TestRequireWorkspacePath(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "workspace path", args: []string{"/my-workspace"}, want: true},
		{name: "bare setup", args: nil},
		{name: "bare slug", args: []string{"my-workspace"}},
		{name: "root only", args: []string{"/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireWorkspacePath(nil, tc.args)
			if (err == nil) != tc.want {
				t.Fatalf("requireWorkspacePath(%v) error = %v, want success=%v", tc.args, err, tc.want)
			}
		})
	}
}

func TestAutoWatchWorkspaces_WorkspaceFlagPinsBySlugOrID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workspaces" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "11111111-1111-1111-1111-111111111111", "name": "Alpha", "slug": "alpha"},
			{"id": "22222222-2222-2222-2222-222222222222", "name": "Beta", "slug": "beta"},
		})
	}))
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_TOKEN", "test-token")

	t.Run("by slug", func(t *testing.T) {
		cmd := newAutoWatchTestCmd()
		if err := cmd.Flags().Set("workspace", "beta"); err != nil {
			t.Fatal(err)
		}
		if err := configureSelectedWorkspace(cmd); err != nil {
			t.Fatalf("configureSelectedWorkspace: %v", err)
		}
		cfg, err := cli.LoadCLIConfig()
		if err != nil {
			t.Fatalf("LoadCLIConfig: %v", err)
		}
		if cfg.WorkspaceID != "22222222-2222-2222-2222-222222222222" {
			t.Errorf("workspace_id = %q, want Beta's id", cfg.WorkspaceID)
		}
	})

	t.Run("by id", func(t *testing.T) {
		cmd := newAutoWatchTestCmd()
		if err := cmd.Flags().Set("workspace", "11111111-1111-1111-1111-111111111111"); err != nil {
			t.Fatal(err)
		}
		if err := configureSelectedWorkspace(cmd); err != nil {
			t.Fatalf("configureSelectedWorkspace: %v", err)
		}
		cfg, err := cli.LoadCLIConfig()
		if err != nil {
			t.Fatalf("LoadCLIConfig: %v", err)
		}
		if cfg.WorkspaceID != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("workspace_id = %q, want Alpha's id", cfg.WorkspaceID)
		}
	})

	t.Run("unknown workspace errors and does not fall back to auto-pick", func(t *testing.T) {
		cmd := newAutoWatchTestCmd()
		if err := cmd.Flags().Set("workspace", "does-not-exist"); err != nil {
			t.Fatal(err)
		}
		if err := configureSelectedWorkspace(cmd); err == nil {
			t.Fatal("expected error for unknown workspace, got nil")
		}
	})

	t.Run("empty flag requires an explicit workspace", func(t *testing.T) {
		cmd := newAutoWatchTestCmd()
		if err := configureSelectedWorkspace(cmd); err == nil {
			t.Fatal("expected an error when no workspace is selected")
		}
	})
}

// fakeDeviceAuthServer scripts the three /api/device/token responses the
// poll loop must handle: pending -> slow_down -> success, exercising the
// backoff without a real 5s sleep between polls (the test's own interval
// stays at the tiny value it declares).
func fakeDeviceAuthServer(t *testing.T, responses []func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	var call int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device/token" {
			http.NotFound(w, r)
			return
		}
		i := atomic.AddInt32(&call, 1) - 1
		if int(i) >= len(responses) {
			responses[len(responses)-1](w)
			return
		}
		responses[i](w)
	}))
}

func writeDeviceTokenError(w http.ResponseWriter, code string) {
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func TestPollDeviceToken_BacksOffOnSlowDownThenSucceeds(t *testing.T) {
	srv := fakeDeviceAuthServer(t, []func(w http.ResponseWriter){
		func(w http.ResponseWriter) { writeDeviceTokenError(w, "authorization_pending") },
		func(w http.ResponseWriter) { writeDeviceTokenError(w, "slow_down") },
		func(w http.ResponseWriter) {
			json.NewEncoder(w).Encode(issueDeviceTokenResponse{Token: "mul_test", ExpiresInDays: 90})
		},
	})
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "", "")
	code := deviceCodeResponse{DeviceCode: "dc_test", Interval: 1, ExpiresIn: 30}

	token, expiresInDays, err := pollDeviceToken(newAutoWatchTestCmd(), client, code)
	if err != nil {
		t.Fatalf("pollDeviceToken: %v", err)
	}
	if token != "mul_test" || expiresInDays != 90 {
		t.Fatalf("token=%q expiresInDays=%d, want mul_test/90", token, expiresInDays)
	}
}

func TestPollDeviceToken_AccessDeniedStopsImmediately(t *testing.T) {
	srv := fakeDeviceAuthServer(t, []func(w http.ResponseWriter){
		func(w http.ResponseWriter) { writeDeviceTokenError(w, "access_denied") },
	})
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "", "")
	code := deviceCodeResponse{DeviceCode: "dc_test", Interval: 1, ExpiresIn: 30}

	if _, _, err := pollDeviceToken(newAutoWatchTestCmd(), client, code); err == nil {
		t.Fatal("expected error for access_denied, got nil")
	}
}

func TestPollDeviceToken_ExpiredTokenStopsImmediately(t *testing.T) {
	srv := fakeDeviceAuthServer(t, []func(w http.ResponseWriter){
		func(w http.ResponseWriter) { writeDeviceTokenError(w, "expired_token") },
	})
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "", "")
	code := deviceCodeResponse{DeviceCode: "dc_test", Interval: 1, ExpiresIn: 30}

	if _, _, err := pollDeviceToken(newAutoWatchTestCmd(), client, code); err == nil {
		t.Fatal("expected error for expired_token, got nil")
	}
}

// TestPollDeviceToken_StopsAtDeadlineIfNeverApproved proves the poll loop
// does not spin forever against a permanently-pending code — it must
// respect ExpiresIn even if the server never returns a terminal state.
func TestPollDeviceToken_StopsAtDeadlineIfNeverApproved(t *testing.T) {
	srv := fakeDeviceAuthServer(t, []func(w http.ResponseWriter){
		func(w http.ResponseWriter) { writeDeviceTokenError(w, "authorization_pending") },
	})
	defer srv.Close()

	client := cli.NewAPIClient(srv.URL, "", "")
	// interval=1s, expires_in=1s: the first poll's Sleep(interval) already
	// crosses the deadline, so the loop must exit on its next deadline
	// check rather than polling indefinitely.
	code := deviceCodeResponse{DeviceCode: "dc_test", Interval: 1, ExpiresIn: 1}

	start := time.Now()
	_, _, err := pollDeviceToken(newAutoWatchTestCmd(), client, code)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("pollDeviceToken took %v, want it to stop near the 1s deadline", elapsed)
	}
}
