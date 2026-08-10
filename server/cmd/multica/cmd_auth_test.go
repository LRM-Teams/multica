package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

// newTestComputerIdentityStore returns a machine-wide Computer identity store
// rooted under the given HOME (test temp dir).
func newTestComputerIdentityStore(t *testing.T, home string) *computer.IdentityStore {
	t.Helper()
	return computer.NewIdentityStore(filepath.Join(home, ".multica"))
}

// cliProfileConfigPath is the machine-wide CLI config path under HOME.
func cliProfileConfigPath(home string) string {
	return filepath.Join(home, ".multica", "config.json")
}

// testCmd returns a minimal cobra.Command with the --profile persistent flag
// registered, matching the rootCmd setup used in production.
func testCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("profile", "", "")
	return cmd
}

func TestResolveTokenUsesTokenFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "")

	tokenFile := t.TempDir() + "/token"
	if err := os.WriteFile(tokenFile, []byte("  token-from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv("MULTICA_TOKEN_FILE", tokenFile)

	if got := resolveToken(testCmd()); got != "token-from-file" {
		t.Fatalf("resolveToken() = %q, want token-from-file", got)
	}
}

func TestResolveTokenAgentContextDoesNotFallbackToProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "")
	t.Setenv("MULTICA_TOKEN_FILE", t.TempDir()+"/missing-token")
	t.Setenv("MULTICA_AGENT_ID", "agent-123")
	t.Setenv("MULTICA_TASK_ID", "task-456")

	if err := cli.SaveCLIConfig(cli.CLIConfig{Token: "profile-token-must-not-leak"}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if got := resolveToken(testCmd()); got != "" {
		t.Fatalf("resolveToken() = %q, want empty in agent context with missing token file", got)
	}
}

func TestInAgentExecutionContextRequiresAgentID(t *testing.T) {
	t.Setenv("MULTICA_AGENT_ID", "")
	t.Setenv("MULTICA_TASK_ID", "legacy-task-only")

	if inAgentExecutionContext() {
		t.Fatal("task ID alone must not turn an ordinary CLI invocation into agent execution")
	}
}

func TestResolveAppURL(t *testing.T) {
	cmd := testCmd()

	t.Run("prefers configured app URL over localhost env", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "http://localhost:3000")
		t.Setenv("FRONTEND_ORIGIN", "http://localhost:13000")
		if err := cli.SaveCLIConfig(cli.CLIConfig{AppURL: "https://multica.ai"}); err != nil {
			t.Fatalf("SaveCLIConfig: %v", err)
		}

		if got := resolveAppURL(cmd); got != "https://multica.ai" {
			t.Fatalf("resolveAppURL() = %q, want %q", got, "https://multica.ai")
		}
	})

	t.Run("prefers self-host app URL over localhost env", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "http://localhost:3000")
		if err := cli.SaveCLIConfig(cli.CLIConfig{
			ServerURL: "https://api.leagent.me",
			AppURL:    "https://www.leagent.me",
		}); err != nil {
			t.Fatalf("SaveCLIConfig: %v", err)
		}

		if got := resolveAppURL(cmd); got != "https://www.leagent.me" {
			t.Fatalf("resolveAppURL() = %q, want %q", got, "https://www.leagent.me")
		}
	})

	t.Run("uses MULTICA_APP_URL when no config exists", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "http://localhost:14000")
		t.Setenv("FRONTEND_ORIGIN", "http://localhost:13000")

		if got := resolveAppURL(cmd); got != "http://localhost:14000" {
			t.Fatalf("resolveAppURL() = %q, want %q", got, "http://localhost:14000")
		}
	})

	t.Run("falls back to FRONTEND_ORIGIN", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "")
		t.Setenv("FRONTEND_ORIGIN", "http://localhost:13026")

		if got := resolveAppURL(cmd); got != "http://localhost:13026" {
			t.Fatalf("resolveAppURL() = %q, want %q", got, "http://localhost:13026")
		}
	})
}

func TestTryResolveAppURL(t *testing.T) {
	cmd := testCmd()

	t.Run("prefers configured app URL over localhost env", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "http://localhost:3000")
		if err := cli.SaveCLIConfig(cli.CLIConfig{AppURL: "https://app.internal.example"}); err != nil {
			t.Fatalf("SaveCLIConfig: %v", err)
		}

		if got := tryResolveAppURL(cmd); got != "https://app.internal.example" {
			t.Fatalf("tryResolveAppURL() = %q, want %q", got, "https://app.internal.example")
		}
	})

	t.Run("returns env fallback when no config exists", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "http://localhost:3000")

		if got := tryResolveAppURL(cmd); got != "http://localhost:3000" {
			t.Fatalf("tryResolveAppURL() = %q, want %q", got, "http://localhost:3000")
		}
	})

	t.Run("returns empty when neither config nor env exists", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("MULTICA_APP_URL", "")
		t.Setenv("FRONTEND_ORIGIN", "")

		if got := tryResolveAppURL(cmd); got != "" {
			t.Fatalf("tryResolveAppURL() = %q, want empty", got)
		}
	})
}

func TestConfiguredAppURLRespectsProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := cli.SaveCLIConfig(cli.CLIConfig{AppURL: "https://default.example"}); err != nil {
		t.Fatalf("SaveCLIConfig(default): %v", err)
	}
	if err := cli.SaveCLIConfigForProfile(cli.CLIConfig{AppURL: "https://staging.example"}, "staging"); err != nil {
		t.Fatalf("SaveCLIConfigForProfile(staging): %v", err)
	}

	cmd := testCmd()
	if err := cmd.Flags().Set("profile", "staging"); err != nil {
		t.Fatalf("set profile: %v", err)
	}

	if got := configuredAppURL(cmd); got != "https://staging.example" {
		t.Fatalf("configuredAppURL() = %q, want %q", got, "https://staging.example")
	}
}

func TestResolveCallbackBinding(t *testing.T) {
	// Fake outbound detector: pretends the CLI has a fixed LAN IP regardless
	// of which server it dials.
	fixed := func(ip string) func(string) net.IP {
		return func(string) net.IP { return net.ParseIP(ip).To4() }
	}
	failing := func(string) net.IP { return nil }

	cases := []struct {
		name         string
		flagHost     string
		serverURL    string
		appURL       string
		detect       func(string) net.IP
		wantCallback string
		wantBind     string
	}{
		{
			name:         "public app URL stays on loopback",
			appURL:       "https://multica.ai",
			serverURL:    "https://api.multica.ai",
			detect:       failing,
			wantCallback: "localhost",
			wantBind:     "127.0.0.1",
		},
		{
			name:         "localhost app URL stays on loopback",
			appURL:       "http://localhost:3000",
			serverURL:    "http://localhost:8080",
			detect:       failing,
			wantCallback: "localhost",
			wantBind:     "127.0.0.1",
		},
		{
			name:         "same-machine self-host uses loopback (CLI IP matches app IP)",
			appURL:       "http://192.168.0.28:3000",
			serverURL:    "http://192.168.0.28:8080",
			detect:       fixed("192.168.0.28"),
			wantCallback: "localhost",
			wantBind:     "127.0.0.1",
		},
		{
			name:         "cross-machine self-host points callback at CLI's LAN IP",
			appURL:       "http://192.168.0.28:3000",
			serverURL:    "http://192.168.0.28:8080",
			detect:       fixed("192.168.0.47"),
			wantCallback: "192.168.0.47",
			wantBind:     "0.0.0.0",
		},
		{
			name:         "outbound detection failure falls back to app IP",
			appURL:       "http://192.168.0.28:3000",
			serverURL:    "http://192.168.0.28:8080",
			detect:       failing,
			wantCallback: "192.168.0.28",
			wantBind:     "0.0.0.0",
		},
		{
			name:         "--callback-host flag overrides everything",
			flagHost:     "cli.internal.example",
			appURL:       "https://multica.ai",
			serverURL:    "https://api.multica.ai",
			detect:       fixed("10.0.0.5"),
			wantCallback: "cli.internal.example",
			wantBind:     "0.0.0.0",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotCallback, gotBind := resolveCallbackBinding(tc.flagHost, tc.serverURL, tc.appURL, tc.detect)
			if gotCallback != tc.wantCallback {
				t.Errorf("callback host = %q, want %q", gotCallback, tc.wantCallback)
			}
			if gotBind != tc.wantBind {
				t.Errorf("bind addr = %q, want %q", gotBind, tc.wantBind)
			}
		})
	}
}

func TestBrowserOpenCommand(t *testing.T) {
	url := "https://multica.ai/login?cli_callback=http%3A%2F%2F172.20.1.2%3A1234&cli_state=abc123"
	cases := []struct {
		name     string
		goos     string
		wsl      bool
		wantCmd  string
		wantArgs []string
		wantErr  bool
	}{
		{
			name:     "macOS uses open",
			goos:     "darwin",
			wantCmd:  "open",
			wantArgs: []string{url},
		},
		{
			name:     "Linux uses xdg-open",
			goos:     "linux",
			wantCmd:  "xdg-open",
			wantArgs: []string{url},
		},
		{
			name:     "WSL opens the Windows browser",
			goos:     "linux",
			wsl:      true,
			wantCmd:  "cmd.exe",
			wantArgs: []string{"/c", "start", "", `"` + url + `"`},
		},
		{
			name:     "Windows uses url handler",
			goos:     "windows",
			wantCmd:  "rundll32",
			wantArgs: []string{"url.dll,FileProtocolHandler", url},
		},
		{
			name:    "unsupported platform errors",
			goos:    "plan9",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotCmd, gotArgs, err := browserOpenCommand(tc.goos, tc.wsl, url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("browserOpenCommand() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("browserOpenCommand() unexpected error: %v", err)
			}
			if gotCmd != tc.wantCmd {
				t.Fatalf("cmd = %q, want %q", gotCmd, tc.wantCmd)
			}
			if strings.Join(gotArgs, "\x00") != strings.Join(tc.wantArgs, "\x00") {
				t.Fatalf("args = %#v, want %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}

// TestLoginTokenFlagWiring asserts the production loginCmd flag is registered
// the way #1994 needs it to be: a String flag (not Bool) with a NoOptDefVal
// so `--token` (no value) keeps its legacy prompt-mode behavior. This is the
// load-bearing regression guard — without these asserts a future change that
// reverts the flag to Bool could pass while a synthetic stand-in test happily
// keeps testing string-flag parsing.
func TestLoginTokenFlagWiring(t *testing.T) {
	tokenFlag := loginCmd.Flags().Lookup("token")
	if tokenFlag == nil {
		t.Fatal("loginCmd is missing the --token flag")
	}
	if got := tokenFlag.Value.Type(); got != "string" {
		t.Fatalf("loginCmd --token type = %q, want %q (regressed to bool?)", got, "string")
	}
	if tokenFlag.NoOptDefVal != tokenPromptSentinel {
		t.Fatalf("loginCmd --token NoOptDefVal = %q, want %q (legacy `multica login --token` prompt mode would break)", tokenFlag.NoOptDefVal, tokenPromptSentinel)
	}
}

// TestLoginTokenFlagParsing exercises every documented invocation form
// against a cobra command wired up exactly the same way as the production
// loginCmd, then runs runAuthLogin's flag-resolution logic to confirm the
// right downstream branch is taken: `--token mul_xxx` and `--token=mul_xxx`
// both consume the value (the bug from #1994), `--token` alone falls
// through to the prompt sentinel (preserves the legacy headless form), and
// no flag at all leaves the browser flow untouched.
func TestLoginTokenFlagParsing(t *testing.T) {
	type want struct {
		changed         bool
		resolvedToken   string // empty == "fall through to prompt"
		expectsPrompted bool
	}

	cases := []struct {
		name string
		argv []string
		want want
	}{
		{
			name: "space-separated value (the form from #1994)",
			argv: []string{"--token", "mul_xxx"},
			want: want{changed: true, resolvedToken: "mul_xxx"},
		},
		{
			name: "equals-separated value",
			argv: []string{"--token=mul_yyy"},
			want: want{changed: true, resolvedToken: "mul_yyy"},
		},
		{
			name: "no value falls through to prompt (legacy CLI_INSTALL.md form)",
			argv: []string{"--token"},
			want: want{changed: true, expectsPrompted: true},
		},
		{
			name: "explicit empty value also falls through to prompt",
			argv: []string{"--token="},
			want: want{changed: true, expectsPrompted: true},
		},
		{
			name: "no flag at all → browser flow",
			argv: []string{},
			want: want{changed: false},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "login"}
			// Mirror loginCmd's exact flag wiring. If init() in cmd_login.go
			// regresses, TestLoginTokenFlagWiring catches that; here we test
			// the parsing behavior given the documented wiring.
			cmd.Flags().String("token", "", "")
			cmd.Flags().Lookup("token").NoOptDefVal = tokenPromptSentinel

			if err := cmd.ParseFlags(tc.argv); err != nil {
				t.Fatalf("ParseFlags(%v) error: %v", tc.argv, err)
			}
			if cmd.Flags().Changed("token") != tc.want.changed {
				t.Fatalf("Changed(token) = %v, want %v for argv=%v", cmd.Flags().Changed("token"), tc.want.changed, tc.argv)
			}
			if !tc.want.changed {
				return
			}

			// Replay runAuthLogin's resolution logic so the test fails if
			// either the flag wiring OR the space-form recovery breaks.
			tokenFlag, _ := cmd.Flags().GetString("token")
			positional := cmd.Flags().Args()
			if tokenFlag == tokenPromptSentinel && len(positional) == 1 {
				tokenFlag = positional[0]
			}

			if tc.want.expectsPrompted {
				if tokenFlag != tokenPromptSentinel && tokenFlag != "" {
					t.Fatalf("expected prompt fall-through, got resolved token %q", tokenFlag)
				}
			} else {
				if tokenFlag != tc.want.resolvedToken {
					t.Fatalf("resolved token = %q, want %q", tokenFlag, tc.want.resolvedToken)
				}
			}
		})
	}
}

func TestNormalizeAPIBaseURL(t *testing.T) {
	t.Run("converts websocket base URL", func(t *testing.T) {
		if got := normalizeAPIBaseURL("ws://localhost:18106/ws"); got != "http://localhost:18106" {
			t.Fatalf("normalizeAPIBaseURL() = %q, want %q", got, "http://localhost:18106")
		}
	})

	t.Run("keeps http base URL", func(t *testing.T) {
		if got := normalizeAPIBaseURL("http://localhost:8080"); got != "http://localhost:8080" {
			t.Fatalf("normalizeAPIBaseURL() = %q, want %q", got, "http://localhost:8080")
		}
	})

	t.Run("falls back to raw value for invalid URL", func(t *testing.T) {
		if got := normalizeAPIBaseURL("://bad-url"); got != "://bad-url" {
			t.Fatalf("normalizeAPIBaseURL() = %q, want %q", got, "://bad-url")
		}
	})
}

// TestValidateLoginTokenPrefix pins the accepted PAT prefix set for
// `multica login --token`. The original implementation hardcoded `mul_`
// only, which rejected legitimate Multica Cloud Node PATs (`mcn_`) at
// the CLI even though the server's middleware would have accepted them.
// If a future change drops `mcn_` from the list (or accidentally
// broadens the set to anything-goes), this test fails.
func TestValidateLoginTokenPrefix(t *testing.T) {
	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{name: "mul_ PAT", token: "mul_abc123", wantErr: false},
		{name: "mcn_ Cloud Node PAT", token: "mcn_abc123", wantErr: false},
		{name: "empty token", token: "", wantErr: true},
		{name: "no prefix", token: "abc123", wantErr: true},
		{name: "wrong prefix mdt_", token: "mdt_abc123", wantErr: true},
		{name: "wrong prefix mat_", token: "mat_abc123", wantErr: true},
		{name: "case-sensitive: MUL_ rejected", token: "MUL_abc123", wantErr: true},
		{name: "leading whitespace not allowed (callers TrimSpace first)", token: " mul_abc", wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateLoginTokenPrefix(tc.token)
			if tc.wantErr && err == nil {
				t.Fatalf("validateLoginTokenPrefix(%q) = nil, want error", tc.token)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateLoginTokenPrefix(%q) = %v, want nil", tc.token, err)
			}
		})
	}

	// The error string is user-facing; make sure it lists every accepted
	// prefix so users hitting it can self-serve. Hardcoding the exact
	// prefixes here is deliberate — if someone adds a new prefix to
	// loginTokenPrefixes they should also update the docs / this test.
	err := validateLoginTokenPrefix("nope_xxx")
	if err == nil {
		t.Fatal("expected error for unknown prefix")
	}
	for _, p := range []string{"mul_", "mcn_"} {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("error %q does not mention prefix %q", err.Error(), p)
		}
	}
}

// #2488: Cloud login origin is always the canonical api.leagent.me base and
// cannot be redirected by MULTICA_SERVER_URL or a flag/profile.
func TestCloudLoginOriginIsCanonicalNotRedirectable(t *testing.T) {
	old := cloudServerBaseURL
	t.Cleanup(func() { cloudServerBaseURL = old })

	cloudServerBaseURL = cli.OfficialCloudAPIURL
	t.Setenv("MULTICA_SERVER_URL", "http://evil.example:9999")

	got := cloudServerURL()
	if got != "https://api.leagent.me" || strings.Contains(got, "evil.example") {
		t.Fatalf("cloudServerURL = %q, want canonical api.leagent.me (ignoring env)", got)
	}
}

func TestCloudLoginOriginHonorsTestOverride(t *testing.T) {
	old := cloudServerBaseURL
	t.Cleanup(func() { cloudServerBaseURL = old })
	cloudServerBaseURL = "http://127.0.0.1:12345"
	if got := cloudServerURL(); got != "http://127.0.0.1:12345" {
		t.Fatalf("cloudServerURL test override = %q", got)
	}
}

// #2488: logout clears the user session but retains Computer Identity.
func TestLogoutClearsSessionAndRetainsIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a machine identity and a session token.
	store := newTestComputerIdentityStore(t, home)
	store.Load("")
	if err := os.WriteFile(cliProfileConfigPath(home), []byte(`{"token":"mul_secret","server_url":"https://leagent.me"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := testCmd()
	if err := runAuthLogout(cmd, nil); err != nil {
		t.Fatalf("runAuthLogout: %v", err)
	}

	cfg, err := cli.LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" {
		t.Fatalf("token not cleared after logout: %q", cfg.Token)
	}
	// Identity must be retained.
	identityPath := filepath.Join(home, ".multica", "daemon.id")
	if _, err := os.Stat(identityPath); err != nil {
		t.Fatalf("Computer Identity was not retained after logout: %v", err)
	}
}
