package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestLatencyDefaults(t *testing.T) {
	if DefaultPollInterval != 2*time.Second {
		t.Fatalf("DefaultPollInterval = %s, want 2s", DefaultPollInterval)
	}
	if taskMessageFlushInterval != 200*time.Millisecond {
		t.Fatalf("taskMessageFlushInterval = %s, want 200ms", taskMessageFlushInterval)
	}
}

func TestIsSafeAgentName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"claude", true},
		{"cursor-agent", true},
		{"kiro_cli", true},
		{"v1.2", true},
		{"Claude2", true},
		{"", false},
		{"a b", false},
		{"a/b", false},
		{"a;b", false},
		{"a$b", false},
		{"a`b", false},
		{"a'b", false},
		{`a"b`, false},
	} {
		if got := isSafeAgentName(tc.in); got != tc.want {
			t.Errorf("isSafeAgentName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestBuildLoginShellResolveScript_ShapeAndContent(t *testing.T) {
	got := buildLoginShellResolveScript([]string{"claude", "cursor-agent"})
	// Must list exactly the names we asked for, in order.
	if !strings.Contains(got, "for n in claude cursor-agent;") {
		t.Errorf("script missing expected for-loop header:\n%s", got)
	}
	// Must strip aliases AND functions before `command -v` — otherwise
	// `alias claude=...` in .zshrc shadows the real binary, which is the
	// exact case behind #2512. The order matters (unalias/unset -f BEFORE
	// command -v); we assert by relative position.
	idxUnalias := strings.Index(got, `unalias "$n" 2>/dev/null`)
	idxUnsetFn := strings.Index(got, `unset -f "$n" 2>/dev/null`)
	idxLookup := strings.Index(got, `command -v "$n"`)
	if idxUnalias < 0 || idxUnsetFn < 0 || idxLookup < 0 {
		t.Fatalf("script missing unalias/unset -f/command -v steps:\n%s", got)
	}
	if !(idxUnalias < idxLookup && idxUnsetFn < idxLookup) {
		t.Errorf("unalias/unset -f must precede command -v:\n%s", got)
	}
	// Must canonicalise via `cd ... && pwd -P` to break out of symlinked
	// per-shell prefix dirs (fnm/nvm/volta) before the spawned shell exits.
	if !strings.Contains(got, "pwd -P") {
		t.Errorf("script missing pwd -P canonicalisation:\n%s", got)
	}
	// Output must be tab-separated `<name>\t<path>` so the parser can split.
	if !strings.Contains(got, `printf '%s\t%s\n'`) {
		t.Errorf("script missing tab-separated printf:\n%s", got)
	}
}

// TestResolveAgentsViaLoginShell_ResolvesViaInteractiveShell verifies the
// motivating bug scenario: a binary that lives in a directory which is NOT on
// the daemon's PATH but IS added to PATH by the user's interactive shell rc
// file gets resolved to a canonical absolute path.
//
// We simulate this by:
//   - creating a temp dir containing an executable named "fakeclaude"
//   - removing every other dir from PATH (so exec.LookPath misses)
//   - pointing SHELL at /bin/sh and using ENV (sourced on -i) to add the dir
//
// Skipped on Windows (no POSIX shell), and skipped if /bin/sh is missing or
// doesn't honour ENV (which would defeat the simulation — not the function's
// fault).
func TestResolveAgentsViaLoginShell_ResolvesViaInteractiveShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell not available on Windows")
	}
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		t.Skipf("no /bin/sh available: %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "fakeclaude")
	// A trivially executable script. We only need it to exist and be
	// marked +x; the resolver never runs it.
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	// Prove the precondition: with binDir absent from PATH, the daemon
	// would normally miss this binary.
	t.Setenv("PATH", "/usr/bin:/bin")
	if _, err := lookPathInPath("fakeclaude"); err == nil {
		t.Skip("PATH leak — test environment already exposes fakeclaude without shell help")
	}

	// Wire the interactive shell to add binDir to PATH on startup. POSIX
	// sh reads $ENV when invoked with -i, so we write a tiny rc file that
	// prepends binDir.
	rc := filepath.Join(t.TempDir(), "sh.rc")
	if err := os.WriteFile(rc, []byte("export PATH=\""+binDir+":$PATH\"\n"), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}
	t.Setenv("SHELL", sh)
	t.Setenv("ENV", rc)

	got := resolveAgentsViaLoginShell([]string{"fakeclaude", "kiro-cli"})
	resolved, ok := got["fakeclaude"]
	if !ok {
		t.Fatalf("expected fakeclaude in resolved map, got %v", got)
	}
	// Must be an absolute path, must exist, must point at our fake binary
	// (resolving any symlinks t.TempDir may have introduced — macOS's
	// /var → /private/var symlink is the usual culprit).
	if !filepath.IsAbs(resolved) {
		t.Errorf("expected absolute path, got %q", resolved)
	}
	wantCanonical, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		t.Fatalf("eval symlinks for expected path: %v", err)
	}
	if resolved != wantCanonical {
		t.Errorf("resolved = %q, want canonical %q", resolved, wantCanonical)
	}
}

func TestResolveAgentsViaLoginShell_SkipsUnsupportedShell(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/fish")
	got := resolveAgentsViaLoginShell([]string{"claude"})
	if len(got) != 0 {
		t.Errorf("expected empty map for unsupported shell, got %v", got)
	}
}

func TestResolveAgentsViaLoginShell_EmptyShellNoCrash(t *testing.T) {
	t.Setenv("SHELL", "")
	got := resolveAgentsViaLoginShell([]string{"claude"})
	if len(got) != 0 {
		t.Errorf("expected empty map when SHELL unset, got %v", got)
	}
}

func TestResolveAgentsViaLoginShell_EmptyInput(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	got := resolveAgentsViaLoginShell(nil)
	if len(got) != 0 {
		t.Errorf("expected empty map for nil input, got %v", got)
	}
}

// lookPathInPath is a thin wrapper used by the test above; matches what
// exec.LookPath would do but lets the test be explicit about which call it's
// asserting against.
func lookPathInPath(name string) (string, error) {
	return exec.LookPath(name)
}

// TestOfficialCloudHostMatchesSharedConstant guards against the daemon-side
// officialCloudHost and cmd_setup.go's ServerURL default silently drifting
// apart — both must derive from cli.OfficialCloudAPIHost, the single source
// of truth (task #29, domain unification).
func TestOfficialCloudHostMatchesSharedConstant(t *testing.T) {
	if officialCloudHost != cli.OfficialCloudAPIHost {
		t.Fatalf("officialCloudHost = %q, want cli.OfficialCloudAPIHost (%q)", officialCloudHost, cli.OfficialCloudAPIHost)
	}
}

func TestIsOfficialCloudServer(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{"canonical cloud API", "https://api.leagent.me", true},
		{"legacy bare cloud host", "https://leagent.me/", true},
		{"legacy www cloud host", "https://WWW.LEAGENT.ME", true},
		{"legacy cloud over plain http", "http://leagent.me", true},
		{"localhost is self-host", "http://localhost:8080", false},
		{"loopback ip is self-host", "http://127.0.0.1:8080", false},
		{"lan ip is self-host", "http://192.168.0.28:8080", false},
		{"third-party host is self-host", "https://multica.example.com", false},
		// Staging / preview / future subdomains deliberately follow the
		// safer self-host default until explicitly opted in.
		{"legacy api host is self-host", "https://api.multica.ai", false},
		{"staging subdomain is self-host", "https://staging.leagent.me", false},
		{"preview subdomain is self-host", "https://api-preview.leagent.me", false},
		// Malformed inputs must not falsely match.
		{"empty string is self-host", "", false},
		{"garbage string is self-host", "::not a url::", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOfficialCloudServer(tc.url); got != tc.want {
				t.Errorf("isOfficialCloudServer(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestNormalizeServerBaseURLMigratesLegacyCloudHosts(t *testing.T) {
	for _, raw := range []string{
		"https://leagent.me",
		"https://www.leagent.me/",
		"wss://leagent.me/ws",
	} {
		got, err := NormalizeServerBaseURL(raw)
		if err != nil {
			t.Fatalf("NormalizeServerBaseURL(%q): %v", raw, err)
		}
		if got != cli.OfficialCloudAPIURL {
			t.Errorf("NormalizeServerBaseURL(%q) = %q, want %q", raw, got, cli.OfficialCloudAPIURL)
		}
	}

	const testOrigin = "https://api.test.leagent.me"
	got, err := NormalizeServerBaseURL(testOrigin)
	if err != nil || got != testOrigin {
		t.Fatalf("test origin must remain configurable: got %q, err=%v", got, err)
	}
}

// stageFakeAgent writes an executable `claude` script into a temp dir and
// points PATH (and the daemon-id env var) so LoadConfig can run end-to-end
// without poking the host's real agent installation. Returns the staged PATH
// so tests that need to add their own dirs can extend it.
func stageFakeAgent(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	writeFakeExecutable(t, binDir, "claude")
	t.Setenv("PATH", binDir)
	t.Setenv("MULTICA_DAEMON_ID", "11111111-1111-1111-1111-111111111111")
	// Clear any inherited env-var override so the test sees the URL-based
	// default, not whatever the developer happens to have exported.
	t.Setenv("MULTICA_DAEMON_AUTO_UPDATE", "")
	return binDir
}

// Machine Upgrade #2379 retires periodic release mutation. Legacy settings
// remain parseable but must be truthful no-ops for every server origin.
func TestResolveWorkspacesRootDefaultsToMulticaHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MULTICA_WORKSPACES_ROOT", "")

	got, err := ResolveWorkspacesRoot("")
	if err != nil {
		t.Fatalf("ResolveWorkspacesRoot: %v", err)
	}
	want := filepath.Join(home, ".multica", "workspaces")
	if got != want {
		t.Fatalf("ResolveWorkspacesRoot = %q, want %q", got, want)
	}
}

func TestLoadConfig_ReleaseDetectionDefaultsOn(t *testing.T) {
	stageFakeAgent(t)
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ReleaseDetectionConfigSource != "auto_detect" {
		t.Fatalf("ReleaseDetectionConfigSource = %q, want auto_detect", cfg.ReleaseDetectionConfigSource)
	}
}

// Legacy enable/disable environment values remain parseable but cannot disable
// release detection or re-enable automatic installation.
func TestLoadConfig_AutoUpdateLegacyEnvKeepsSelfHostDetectOnly(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("MULTICA_DAEMON_AUTO_UPDATE", "false")
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ReleaseDetectionConfigSource != "auto_detect" {
		t.Fatalf("ReleaseDetectionConfigSource = %q, want auto_detect", cfg.ReleaseDetectionConfigSource)
	}
}

// Hosted cloud uses the same detect-only contract. The WSS form also exercises
// NormalizeServerBaseURL before detection configuration is resolved.
func TestLoadConfig_ReleaseDetectionUsesSameCloudContract(t *testing.T) {
	stageFakeAgent(t)
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "wss://leagent.me/ws",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ReleaseDetectionConfigSource != "auto_detect" {
		t.Fatalf("ReleaseDetectionConfigSource = %q, want auto_detect", cfg.ReleaseDetectionConfigSource)
	}
}

func TestLoadConfig_AutoUpdateTrueEnvCannotEnableInstallation(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("MULTICA_DAEMON_AUTO_UPDATE", "true")
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ReleaseDetectionConfigSource != "auto_detect" {
		t.Fatalf("ReleaseDetectionConfigSource = %q, want auto_detect", cfg.ReleaseDetectionConfigSource)
	}
}

func TestLoadConfig_AutoUpdateFalseEnvCannotDisableDetection(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("MULTICA_DAEMON_AUTO_UPDATE", "false")
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "https://leagent.me",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ReleaseDetectionConfigSource != "auto_detect" {
		t.Fatalf("ReleaseDetectionConfigSource = %q, want auto_detect", cfg.ReleaseDetectionConfigSource)
	}
}

// The legacy --no-auto-update flag remains accepted but cannot disable release
// detection or change the explicit-only installation contract.
func TestLoadConfig_AutoUpdateNoFlagIsCompatibilityNoOp(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("MULTICA_DAEMON_AUTO_UPDATE", "true")
	cfg, err := LoadConfig(Overrides{
		ServerURL:         "https://leagent.me",
		WorkspacesRoot:    t.TempDir(),
		DisableAutoUpdate: true,
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ReleaseDetectionConfigSource != "auto_detect" {
		t.Fatalf("ReleaseDetectionConfigSource = %q, want auto_detect", cfg.ReleaseDetectionConfigSource)
	}
}

// TestResolveAgentsViaLoginShell_StripsAliasShadowing locks down the fix for
// #2512: when the user's rc file declares an alias with the same name as the
// agent CLI, the resolver must still return the real binary on PATH, not the
// alias text. The previous revision of this code passed the rest of the test
// suite but silently dropped this case (alias text is not absolute, so the
// `case "$p" in /*)` filter rejected it).
func TestResolveAgentsViaLoginShell_StripsAliasShadowing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell not available on Windows")
	}
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		t.Skipf("no /bin/sh available: %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "fakeclaude")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	// rc adds binDir to PATH AND defines an alias that shadows the bare
	// name with a non-existent path. The pre-fix script would see the
	// alias, see that its target isn't absolute, and silently drop the
	// agent. With unalias/unset -f in place, command -v falls through to
	// the PATH search and finds binPath.
	rc := filepath.Join(t.TempDir(), "sh.rc")
	rcBody := "export PATH=\"" + binDir + ":$PATH\"\n" +
		"alias fakeclaude=\"/nonexistent/wrapper-from-rc\"\n"
	if err := os.WriteFile(rc, []byte(rcBody), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}

	// Strip PATH so exec.LookPath misses fakeclaude — same precondition as
	// the happy-path test, so we know the shell did the resolution.
	t.Setenv("PATH", "/usr/bin:/bin")
	if _, err := lookPathInPath("fakeclaude"); err == nil {
		t.Skip("PATH leak — fakeclaude already visible to the daemon without shell help")
	}
	// Sanity-check that the simulated environment can actually load aliases.
	// If the host /bin/sh doesn't honour $ENV in -i mode (rare but possible
	// on minimal Linux images), skipping is more honest than asserting on a
	// scenario the test couldn't actually set up.
	t.Setenv("SHELL", sh)
	t.Setenv("ENV", rc)
	probe, err := exec.Command(sh, "-ilc", "alias fakeclaude 2>/dev/null").Output()
	if err != nil || !strings.Contains(string(probe), "fakeclaude") {
		t.Skipf("test host's /bin/sh did not load alias from $ENV; cannot simulate shadowing (probe=%q err=%v)", string(probe), err)
	}

	got := resolveAgentsViaLoginShell([]string{"fakeclaude"})
	resolved, ok := got["fakeclaude"]
	if !ok {
		t.Fatalf("expected fakeclaude in resolved map despite alias shadowing, got %v", got)
	}
	wantCanonical, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		t.Fatalf("eval symlinks for expected path: %v", err)
	}
	if resolved != wantCanonical {
		t.Errorf("resolved = %q, want canonical %q (got the alias instead of the PATH binary?)", resolved, wantCanonical)
	}
}

// TestResolveAgentsViaLoginShell_HardTimeoutOnBackgroundedStdout exercises the
// failure mode Cmd.WaitDelay guards against: an rc file that backgrounds a
// long-running process inheriting stdout. Killing the shell on context
// cancel does not close the inherited pipe, so cmd.Output() would hang on
// EOF until the survivor exits. The hard deadline must be roughly
// loginShellResolveTimeout + loginShellResolveWaitDelay, not the survivor's
// lifetime.
func TestResolveAgentsViaLoginShell_HardTimeoutOnBackgroundedStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell not available on Windows")
	}
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		t.Skipf("no /bin/sh available: %v", err)
	}

	// rc backgrounds a sleeper that holds stdout for far longer than any
	// reasonable WaitDelay. The resolver script never gets to print
	// anything (we never even reach the for-loop because rc is still
	// being sourced when the sleeper forks), but that's exactly the
	// scenario we care about — we don't want to leak time-to-startup.
	rc := filepath.Join(t.TempDir(), "sh.rc")
	rcBody := "( sleep 60 ) &\n"
	if err := os.WriteFile(rc, []byte(rcBody), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}
	t.Setenv("SHELL", sh)
	t.Setenv("ENV", rc)

	// Cap = context timeout + wait delay + generous slack for goroutine
	// scheduling. A bug that disables WaitDelay would blow past 60s here.
	cap := loginShellResolveTimeout + loginShellResolveWaitDelay + 3*time.Second
	start := time.Now()
	done := make(chan struct{})
	go func() {
		_ = resolveAgentsViaLoginShell([]string{"claude"})
		close(done)
	}()
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > cap {
			t.Errorf("resolver took %v, expected <= %v (WaitDelay leak?)", elapsed, cap)
		}
	case <-time.After(cap):
		t.Fatalf("resolver did not return within %v — WaitDelay is not enforcing a hard ceiling", cap)
	}
}

// TestLoadConfig_SkipsLoginShellWhenLookPathSucceeds proves the laziness
// requirement: if every agent CLI the operator cares about is already
// resolvable via the daemon's PATH (or pinned to an explicit MULTICA_*_PATH),
// the shell-fallback path must not run. We assert this by pointing SHELL at
// a sentinel script that touches a marker file when invoked.
func TestLoadConfig_SkipsLoginShellWhenLookPathSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell not available on Windows")
	}

	// Stage 1: a fake `claude` binary the daemon's bare exec.LookPath
	// definitely sees, so the probe loop never has reason to consult
	// shellResolved.
	pathDir := t.TempDir()
	fakeClaude := filepath.Join(pathDir, "claude")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	// Stage 2: a SHELL that writes a marker file when invoked. If
	// LoadConfig's getShellResolved closure fires, the marker appears.
	shellDir := t.TempDir()
	shellPath := filepath.Join(shellDir, "bash") // pick a name the resolver's allowlist accepts
	marker := filepath.Join(shellDir, "invoked.marker")
	shellBody := "#!/bin/sh\ntouch \"" + marker + "\"\n"
	if err := os.WriteFile(shellPath, []byte(shellBody), 0o755); err != nil {
		t.Fatalf("write sentinel shell: %v", err)
	}

	t.Setenv("PATH", pathDir)
	t.Setenv("SHELL", shellPath)
	// Pin a non-existent agent to a bare name so it would normally trip
	// the fallback — except `claude` already resolves, and the user hasn't
	// configured anything else, so the probe loop should be satisfied
	// after the first probe alone.
	t.Setenv("MULTICA_DAEMON_ID", "11111111-1111-1111-1111-111111111111")

	if _, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:0",
		WorkspacesRoot: t.TempDir(),
	}); err != nil {
		// Some daemon-id / workspace bookkeeping outside our concern may
		// fail in CI; the marker assertion below is what matters either
		// way, so we don't fail on LoadConfig errors directly.
		t.Logf("LoadConfig returned %v (non-fatal for this test)", err)
	}
	// Brief wait for any goroutine the resolver might have leaked. The
	// sync.Once-guarded resolver runs synchronously today, so this should
	// be immediate; the sleep is just to avoid a flake if that ever
	// changes.
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("login shell was invoked even though exec.LookPath found every agent — laziness broken")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error stat-ing marker file: %v", err)
	}
}

func TestLoadConfig_UsesCodexDesktopAppBundleFallback(t *testing.T) {
	pathDir := t.TempDir()
	fakeCodex := filepath.Join(pathDir, "Codex.app", "Contents", "Resources", "codex")
	if err := os.MkdirAll(filepath.Dir(fakeCodex), 0o755); err != nil {
		t.Fatalf("mkdir fake Codex bundle: %v", err)
	}
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake Codex bundle CLI: %v", err)
	}

	oldBundlePaths := codexDesktopAppBundlePaths
	codexDesktopAppBundlePaths = func() []string { return []string{fakeCodex} }
	t.Cleanup(func() { codexDesktopAppBundlePaths = oldBundlePaths })

	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "fish"))
	t.Setenv("MULTICA_DAEMON_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("MULTICA_CODEX_MODEL", "gpt-5")
	pinNonCodexAgentsToMissingPaths(t)

	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:0",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got, ok := cfg.Agents["codex"]
	if !ok {
		t.Fatalf("expected codex agent from Desktop app bundle fallback, got %#v", cfg.Agents)
	}
	if got.Path != fakeCodex {
		t.Fatalf("codex path = %q, want %q", got.Path, fakeCodex)
	}
	if got.Model != "gpt-5" {
		t.Fatalf("codex model = %q, want gpt-5", got.Model)
	}
}

func TestLoadConfig_CodexDesktopFallbackDoesNotOverrideExplicitPath(t *testing.T) {
	pathDir := t.TempDir()
	fakeCodex := filepath.Join(pathDir, "Codex.app", "Contents", "Resources", "codex")
	if err := os.MkdirAll(filepath.Dir(fakeCodex), 0o755); err != nil {
		t.Fatalf("mkdir fake Codex bundle: %v", err)
	}
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake Codex bundle CLI: %v", err)
	}

	oldBundlePaths := codexDesktopAppBundlePaths
	codexDesktopAppBundlePaths = func() []string { return []string{fakeCodex} }
	t.Cleanup(func() { codexDesktopAppBundlePaths = oldBundlePaths })

	t.Setenv("PATH", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "fish"))
	t.Setenv("MULTICA_DAEMON_ID", "11111111-1111-1111-1111-111111111111")
	t.Setenv("MULTICA_CODEX_PATH", filepath.Join(t.TempDir(), "missing-codex"))
	pinNonCodexAgentsToMissingPaths(t)
	fakeClaude := writeFakeExecutable(t, t.TempDir(), "claude")
	t.Setenv("MULTICA_CLAUDE_PATH", fakeClaude)

	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:0",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got, ok := cfg.Agents["codex"]; ok {
		t.Fatalf("explicit missing MULTICA_CODEX_PATH should not fall back to Desktop bundle, got %#v", got)
	}
}

func writeFakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		name += ".bat"
	}
	path := filepath.Join(dir, name)
	var content []byte
	if runtime.GOOS == "windows" {
		content = []byte("@echo off\r\nexit /b 0\r\n")
	} else {
		content = []byte("#!/bin/sh\nexit 0\n")
	}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	return path
}

func pinNonCodexAgentsToMissingPaths(t *testing.T) {
	t.Helper()
	missingDir := t.TempDir()
	for _, name := range []string{
		"MULTICA_CLAUDE_PATH",
		"MULTICA_OPENCODE_PATH",
		"MULTICA_PI_PATH",
		"MULTICA_CURSOR_PATH",
		"MULTICA_KIRO_PATH",
		"MULTICA_GROK_PATH",
	} {
		t.Setenv(name, filepath.Join(missingDir, strings.ToLower(name)))
	}
}

// =============================================================================
// CLI config load fail-soft (missing / malformed config.json)
// =============================================================================

// writeCLIConfigForProfile is a minimal helper for the override tests:
// stages a HOME, writes a config.json under the given profile (empty profile
// = default), and returns the resolved path so tests can assert against it.
func writeCLIConfigForProfile(t *testing.T, profile string, cfg cli.CLIConfig) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := cli.SaveCLIConfigForProfile(cfg, profile); err != nil {
		t.Fatalf("write cli config: %v", err)
	}
}

func clearProxyEnvForTest(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
		"NO_PROXY", "no_proxy",
	} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

func TestApplyProxyConfig_ConfigFallbackAndRaftLoopback(t *testing.T) {
	clearProxyEnvForTest(t)

	applyProxyConfig(&cli.ProxyConfig{
		HTTP:    "http://config-http.internal:8080",
		HTTPS:   "http://config-https.internal:8443",
		NoProxy: ".corp.example,localhost",
	})

	for _, name := range []string{"HTTP_PROXY", "http_proxy"} {
		if got := os.Getenv(name); got != "http://config-http.internal:8080" {
			t.Fatalf("%s = %q", name, got)
		}
	}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		if got := os.Getenv(name); got != "http://config-https.internal:8443" {
			t.Fatalf("%s = %q", name, got)
		}
	}
	const wantNoProxy = "127.0.0.1,localhost,.corp.example"
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		if got := os.Getenv(name); got != wantNoProxy {
			t.Fatalf("%s = %q, want %q", name, got, wantNoProxy)
		}
	}
}

func TestApplyProxyConfig_EnvironmentWinsAndNoProxyUnionsBothCases(t *testing.T) {
	clearProxyEnvForTest(t)
	t.Setenv("HTTP_PROXY", "http://upper-http.internal:8080")
	t.Setenv("http_proxy", "http://lower-http.internal:8080")
	t.Setenv("https_proxy", "http://lower-https.internal:8443")
	t.Setenv("NO_PROXY", "metadata.internal,LOCALHOST")
	t.Setenv("no_proxy", ".svc.cluster.local,metadata.internal")

	applyProxyConfig(&cli.ProxyConfig{
		HTTP:    "http://ignored-config-http.internal:8080",
		HTTPS:   "http://ignored-config-https.internal:8443",
		NoProxy: ".corp.example,localhost",
	})

	for _, name := range []string{"HTTP_PROXY", "http_proxy"} {
		if got := os.Getenv(name); got != "http://upper-http.internal:8080" {
			t.Fatalf("%s = %q, want uppercase env value", name, got)
		}
	}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		if got := os.Getenv(name); got != "http://lower-https.internal:8443" {
			t.Fatalf("%s = %q, want lowercase env fallback", name, got)
		}
	}
	const wantNoProxy = "127.0.0.1,localhost,metadata.internal,.svc.cluster.local,.corp.example"
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		if got := os.Getenv(name); got != wantNoProxy {
			t.Fatalf("%s = %q, want %q", name, got, wantNoProxy)
		}
	}
}

func TestLoadConfig_AppliesProfileProxyConfig(t *testing.T) {
	stageFakeAgent(t)
	clearProxyEnvForTest(t)
	const profile = "proxy-test"
	writeCLIConfigForProfile(t, profile, cli.CLIConfig{
		Proxy: &cli.ProxyConfig{
			HTTP:    "http://profile-proxy.internal:8080",
			NoProxy: ".profile.internal",
		},
	})

	if _, err := LoadConfig(Overrides{Profile: profile}); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, name := range []string{"HTTP_PROXY", "http_proxy"} {
		if got := os.Getenv(name); got != "http://profile-proxy.internal:8080" {
			t.Fatalf("%s = %q", name, got)
		}
	}
	const wantNoProxy = "127.0.0.1,localhost,.profile.internal"
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		if got := os.Getenv(name); got != wantNoProxy {
			t.Fatalf("%s = %q, want %q", name, got, wantNoProxy)
		}
	}
}

func TestLoadConfig_MissingCLIConfigIsNonFatal(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("HOME", t.TempDir())

	if _, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	}); err != nil {
		t.Fatalf("LoadConfig with no config file should not fail: %v", err)
	}
}

// TestLoadConfig_MalformedCLIConfigIsNonFatal verifies the fail-soft contract
// documented inline in LoadConfig: a corrupt config.json must not prevent
// daemon startup. The daemon should log and proceed using env-var-only
// configuration.
func TestLoadConfig_MalformedCLIConfigIsNonFatal(t *testing.T) {
	stageFakeAgent(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	// Write malformed JSON.
	cfgDir := filepath.Join(homeDir, ".multica")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig should not fail on malformed config.json: %v", err)
	}
	// Should also have logged a slog Warn — we don't assert on the log
	// output here (avoids brittle string matching), but the build does
	// make sure log/slog stays imported.
}

// TestLoadConfig_PinnedDaemonIDSkipsFrozenProfileUUIDs guards the sandbox
// snapshot create race: a frozen profiles/*/daemon.id from the source sandbox
// must not enter LegacyDaemonIDs when MULTICA_DAEMON_ID is pinned, or register
// would merge/steal the source runtime row.
func TestLoadConfig_PinnedDaemonIDSkipsFrozenProfileUUIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stageFakeAgent(t)

	foreignID := "22222222-2222-2222-2222-222222222222"
	profileDir := filepath.Join(home, ".multica", "profiles", "sandbox-source")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "daemon.id"), []byte(foreignID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, id := range cfg.LegacyDaemonIDs {
		if id == foreignID {
			t.Fatalf("LegacyDaemonIDs unexpectedly includes frozen source UUID %s: %v", foreignID, cfg.LegacyDaemonIDs)
		}
	}
}

// agentKeys is a tiny helper to make agent-map missing-key error messages
// readable. Returns sorted keys.
func agentKeys(m map[string]AgentEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestLoadConfig_PinnedVersion_Valid tests that a valid release version in
// MULTICA_PINNED_VERSION is accepted and stored in the config.
func TestLoadConfig_PinnedVersion_Valid(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("MULTICA_PINNED_VERSION", "0.3.92")
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PinnedVersion != "0.3.92" {
		t.Fatalf("PinnedVersion = %q, want 0.3.92", cfg.PinnedVersion)
	}
}

// TestLoadConfig_PinnedVersion_Invalid verifies that a non-release version
// string is rejected with a clear error, not silently ignored.
func TestLoadConfig_PinnedVersion_Invalid(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("MULTICA_PINNED_VERSION", "not-a-version")
	_, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for invalid pinned version, got nil")
	}
	if !strings.Contains(err.Error(), "MULTICA_PINNED_VERSION") {
		t.Fatalf("expected error to mention MULTICA_PINNED_VERSION, got: %v", err)
	}
}

// TestLoadConfig_PinnedVersion_EmptyIsNoop verifies that without the env var,
// PinnedVersion is empty (no pin, normal explicit-upgrade behavior).
func TestLoadConfig_PinnedVersion_EmptyIsNoop(t *testing.T) {
	stageFakeAgent(t)
	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:8080",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.PinnedVersion != "" {
		t.Fatalf("PinnedVersion = %q, want empty", cfg.PinnedVersion)
	}
}
