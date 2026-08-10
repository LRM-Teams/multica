package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

// TestEstablishWorkspaceBindingPersistsLocalBinding verifies #2489's local
// Binding record is written machine-wide only after the server accepts it and
// returns the scoped execution credential.
func TestEstablishWorkspaceBindingPersistsLocalBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path == "" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"workspace_id":"ws-123","credential":"binding-token","credential_expires_at":"2030-01-02T03:04:05Z"}`))
	}))
	defer server.Close()
	if err := os.MkdirAll(filepath.Join(home, ".multica"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".multica", "config.json"), []byte(`{"environment":"test","server_url":"`+server.URL+`","app_url":"`+server.URL+`","workspace_id":"ws-123","token":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := establishWorkspaceBinding(testCmd()); err != nil {
		t.Fatalf("establishWorkspaceBinding: %v", err)
	}

	bs := computer.NewBindingsStore(filepath.Join(home, ".multica", "computer"))
	all, err := bs.All()
	if err != nil {
		t.Fatalf("load bindings: %v", err)
	}
	found := false
	for _, b := range all {
		if b.WorkspaceID == "ws-123" && b.Active {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a local binding for workspace ws-123, got %+v", all)
	}
}

func TestCloudCLIConfigUsesLeAgentOrigins(t *testing.T) {
	cfg := cloudCLIConfig()
	if cfg.Environment != "production" {
		t.Fatalf("environment = %q, want production", cfg.Environment)
	}
	if cfg.ReleaseChannel != "latest" {
		t.Fatalf("release_channel = %q, want latest", cfg.ReleaseChannel)
	}
	if cfg.ServerURL != "https://api.leagent.me" {
		t.Fatalf("server_url = %q, want https://api.leagent.me", cfg.ServerURL)
	}
	if cfg.AppURL != "https://www.leagent.me" {
		t.Fatalf("app_url = %q, want https://www.leagent.me", cfg.AppURL)
	}
}

func TestSetupEnvironmentFlagsExposeOnlyProductionOrExplicitTestOrigin(t *testing.T) {
	for _, command := range []*cobra.Command{setupCmd, setupCloudCmd} {
		if command.Flags().Lookup("environment") == nil || command.Flags().Lookup("test-url") == nil || command.Flags().Lookup("channel") == nil {
			t.Fatalf("%s must expose --environment, --test-url, and --channel", command.Use)
		}
	}
}

func TestResidentMatchesSetupTargetRequiresEnvironmentOriginAndChannel(t *testing.T) {
	cfg := cli.CLIConfig{
		Environment: "test", ReleaseChannel: "alpha",
		ServerURL: "https://test.leagent.me", AppURL: "https://test.leagent.me",
	}
	matching := map[string]any{
		"server_url": "https://test.leagent.me", "environment": "test", "release_channel": "alpha",
	}
	got, err := residentMatchesSetupTarget(matching, cfg)
	if err != nil || !got {
		t.Fatalf("matching resident: got=%v err=%v", got, err)
	}
	for key, value := range map[string]any{
		"server_url": "https://other.example", "environment": "production", "release_channel": "latest",
	} {
		drifted := map[string]any{
			"server_url": "https://test.leagent.me", "environment": "test", "release_channel": "alpha",
		}
		drifted[key] = value
		if got, err := residentMatchesSetupTarget(drifted, cfg); err != nil || got {
			t.Fatalf("%s drift: got=%v err=%v", key, got, err)
		}
	}
}

// TestPersistSelfHostConfigIfReachable verifies the fix for the
// setup-wipes-token bug: a failed reachability probe must leave the existing
// config (and its auth token) untouched, instead of overwriting it before the
// probe and bailing — which left the user logged out with no recovery.
func TestPersistSelfHostConfigIfReachable(t *testing.T) {
	t.Run("unreachable server preserves existing config and token", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		existing := cli.CLIConfig{
			ServerURL:   "https://api.old.example",
			AppURL:      "https://old.example",
			WorkspaceID: "ws-1",
			Token:       "mul_existing_token",
		}
		if err := cli.SaveCLIConfig(existing); err != nil {
			t.Fatalf("seed config: %v", err)
		}

		proceed, err := persistSelfHostConfigIfReachable(
			"https://api.new.example", "https://new.example", "",
			func(string) bool { return false },
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proceed {
			t.Fatalf("proceed: want false for unreachable server")
		}

		got, err := cli.LoadCLIConfig()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if got.Token != "mul_existing_token" {
			t.Fatalf("token: want preserved, got %q", got.Token)
		}
		if got.ServerURL != "https://api.old.example" {
			t.Fatalf("server_url: want unchanged, got %q", got.ServerURL)
		}
	})

	t.Run("reachable server writes new self-host config", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())

		proceed, err := persistSelfHostConfigIfReachable(
			"https://api.new.example", "https://new.example", "",
			func(string) bool { return true },
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !proceed {
			t.Fatalf("proceed: want true for reachable server")
		}

		got, err := cli.LoadCLIConfig()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if got.ServerURL != "https://api.new.example" || got.AppURL != "https://new.example" {
			t.Fatalf("config not written: %+v", got)
		}
	})
}

func TestSetupCallbackHostFlagWiring(t *testing.T) {
	for _, cmd := range []*cobra.Command{setupCmd, setupCloudCmd, setupSelfHostCmd} {
		cmd := cmd
		t.Run(cmd.Use, func(t *testing.T) {
			flag := cmd.Flags().Lookup(callbackHostFlag)
			if flag == nil {
				t.Fatalf("%s is missing --%s", cmd.Use, callbackHostFlag)
			}
			if got := flag.Value.Type(); got != "string" {
				t.Fatalf("%s --%s type = %q, want string", cmd.Use, callbackHostFlag, got)
			}
		})
	}
}

// TestResolveSelfHostServerURL covers GitHub #3912: `setup self-host` must
// honor MULTICA_SERVER_URL when --server-url is not passed, instead of always
// defaulting to localhost (which left self-hosters stuck on an "unreachable"
// error). The flag still wins over the env var.
func TestResolveSelfHostServerURL(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{}
		c.Flags().String("server-url", "", "")
		c.Flags().Int("port", 8080, "")
		return c
	}

	t.Run("env var honored when flag absent", func(t *testing.T) {
		t.Setenv("MULTICA_SERVER_URL", "https://api.internal.co")
		serverURL, userProvided := resolveSelfHostServerURL(newCmd())
		if serverURL != "https://api.internal.co" {
			t.Fatalf("server_url: want env value, got %q", serverURL)
		}
		if !userProvided {
			t.Fatalf("userProvided: want true for env-sourced URL")
		}
	})

	t.Run("flag wins over env", func(t *testing.T) {
		t.Setenv("MULTICA_SERVER_URL", "https://env.example")
		cmd := newCmd()
		if err := cmd.Flags().Set("server-url", "https://flag.example"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		serverURL, userProvided := resolveSelfHostServerURL(cmd)
		if serverURL != "https://flag.example" {
			t.Fatalf("server_url: want flag value, got %q", serverURL)
		}
		if !userProvided {
			t.Fatalf("userProvided: want true for flag-sourced URL")
		}
	})

	t.Run("falls back to localhost with --port when neither set", func(t *testing.T) {
		t.Setenv("MULTICA_SERVER_URL", "")
		cmd := newCmd()
		if err := cmd.Flags().Set("port", "9090"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		serverURL, userProvided := resolveSelfHostServerURL(cmd)
		if serverURL != "http://localhost:9090" {
			t.Fatalf("server_url: want localhost default, got %q", serverURL)
		}
		if userProvided {
			t.Fatalf("userProvided: want false for localhost fallback")
		}
	})

	// MULTICA_SERVER_URL is documented as a ws:// daemon address; the probe and
	// stored config need an http(s) base, so the ws/wss + /ws form must be
	// normalized just like every other command does.
	t.Run("normalizes the documented ws:// daemon form", func(t *testing.T) {
		t.Setenv("MULTICA_SERVER_URL", "wss://api.internal.co/ws")
		serverURL, userProvided := resolveSelfHostServerURL(newCmd())
		if serverURL != "https://api.internal.co" {
			t.Fatalf("server_url: want normalized https base, got %q", serverURL)
		}
		if !userProvided {
			t.Fatalf("userProvided: want true for env-sourced URL")
		}
	})
}

// TestSelfHostAppURLHonorsEnv pins the app-url half of the GitHub #3912 fix:
// setup self-host resolves --app-url through the same FlagOrEnv path, so
// MULTICA_APP_URL is honored when the flag is absent.
func TestSelfHostAppURLHonorsEnv(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("app-url", "", "")

	t.Run("env honored when flag absent", func(t *testing.T) {
		t.Setenv("MULTICA_APP_URL", "https://app.internal.co")
		if got := cli.FlagOrEnv(cmd, "app-url", "MULTICA_APP_URL", ""); got != "https://app.internal.co" {
			t.Fatalf("app_url: want env value, got %q", got)
		}
	})

	t.Run("flag wins over env", func(t *testing.T) {
		t.Setenv("MULTICA_APP_URL", "https://env.example")
		if err := cmd.Flags().Set("app-url", "https://flag.example"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		if got := cli.FlagOrEnv(cmd, "app-url", "MULTICA_APP_URL", ""); got != "https://flag.example" {
			t.Fatalf("app_url: want flag value, got %q", got)
		}
	})
}

func TestProbeApp(t *testing.T) {
	t.Run("reachable app", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		if !probeApp(server.URL) {
			t.Fatalf("probeApp: want true for reachable app")
		}
	})

	t.Run("unreachable app", func(t *testing.T) {
		if probeApp("http://127.0.0.1:1") {
			t.Fatalf("probeApp: want false for unreachable app")
		}
	})
}

func TestServerHostIsLocal(t *testing.T) {
	cases := []struct {
		name   string
		server string
		want   bool
	}{
		{"localhost", "http://localhost:8080", true},
		{"127.0.0.1", "http://127.0.0.1:8080", true},
		{"IPv6 loopback", "http://[::1]:8080", true},
		{"LAN IP", "http://192.168.0.28:8080", false},
		{"public FQDN", "https://api.internal.co", false},
		{"unparseable", "://bad", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := serverHostIsLocal(tc.server); got != tc.want {
				t.Errorf("serverHostIsLocal(%q) = %v, want %v", tc.server, got, tc.want)
			}
		})
	}
}

// #2487/#2496: setup starts the machine-wide detached resident and does NOT
// install an OS supervisor service (LaunchAgent / systemd / Scheduled Task).
func TestStartDaemonAfterSetupStartsDetachedResidentWithoutService(t *testing.T) {
	var called bool
	prevEstablish := establishWorkspaceBindingAfterSetup
	prev := startResidentAfterSetup
	prevWait := waitForWorkspaceBindingAcceptanceAfterSetup
	establishWorkspaceBindingAfterSetup = func(*cobra.Command) error { return nil }
	startResidentAfterSetup = func(*cobra.Command) error { called = true; return nil }
	waitForWorkspaceBindingAcceptanceAfterSetup = func(*cobra.Command) error { return nil }
	t.Cleanup(func() {
		establishWorkspaceBindingAfterSetup = prevEstablish
		startResidentAfterSetup = prev
		waitForWorkspaceBindingAcceptanceAfterSetup = prevWait
	})

	if err := startDaemonAfterSetup(&cobra.Command{}); err != nil {
		t.Fatalf("startDaemonAfterSetup: %v", err)
	}
	if !called {
		t.Fatal("setup must start the detached resident, not install an OS service")
	}
}

func TestStartDaemonAfterSetupPropagatesResidentFailure(t *testing.T) {
	prevEstablish := establishWorkspaceBindingAfterSetup
	prev := startResidentAfterSetup
	establishWorkspaceBindingAfterSetup = func(*cobra.Command) error { return nil }
	startResidentAfterSetup = func(*cobra.Command) error { return errors.New("spawn failed") }
	t.Cleanup(func() {
		establishWorkspaceBindingAfterSetup = prevEstablish
		startResidentAfterSetup = prev
	})
	if err := startDaemonAfterSetup(&cobra.Command{}); err == nil {
		t.Fatal("resident-start failure must surface as a setup error")
	}
}
