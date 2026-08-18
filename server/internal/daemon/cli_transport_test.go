package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareTaskCLITransportWritesPerRunWrapperAndTokenFile(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "multica-real")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}

	wrapperDir, tokenFile, err := prepareTaskCLITransport(
		Config{WorkspacesRoot: root},
		"workspace-1",
		"agent-1",
		"run-1",
		bin,
		"task-token-secret",
	)
	if err != nil {
		t.Fatalf("prepareTaskCLITransport: %v", err)
	}

	wantDir := filepath.Join(workspaceStateRoot(root, "workspace-1"), "cli-transport", "agent-1", "run-1")
	if wrapperDir != wantDir {
		t.Fatalf("wrapperDir = %q, want %q", wrapperDir, wantDir)
	}
	if tokenFile != filepath.Join(wantDir, "token") {
		t.Fatalf("tokenFile = %q", tokenFile)
	}

	token, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if string(token) != "task-token-secret" {
		t.Fatalf("token file = %q", token)
	}
	if runtime.GOOS != "windows" {
		if mode := mustStatMode(t, tokenFile).Perm(); mode != 0o600 {
			t.Fatalf("token file mode = %o, want 0600", mode)
		}
	}

	wrapper := filepath.Join(wrapperDir, "multica")
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"unset MULTICA_TOKEN",
		"export MULTICA_TOKEN_FILE=",
		tokenFile,
		bin,
		"exec ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("wrapper missing %q:\n%s", want, text)
		}
	}
	if runtime.GOOS != "windows" {
		if mode := mustStatMode(t, wrapper).Perm(); mode != 0o700 {
			t.Fatalf("wrapper mode = %o, want 0700", mode)
		}
	}
}

func TestPrepareTaskCLITransportRejectsPathTraversal(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "multica")
	for _, tc := range []struct {
		workspaceID string
		agentID     string
		runID       string
	}{
		{workspaceID: "../workspace", agentID: "agent-1", runID: "run-1"},
		{workspaceID: "workspace-1", agentID: "../agent", runID: "run-1"},
		{workspaceID: "workspace-1", agentID: "agent-1", runID: "../run"},
	} {
		if _, _, err := prepareTaskCLITransport(Config{WorkspacesRoot: t.TempDir()}, tc.workspaceID, tc.agentID, tc.runID, bin, "token"); err == nil {
			t.Fatalf("path traversal was accepted for %#v", tc)
		}
	}
}

func TestPrepareStableAgentCLITransportUsesAgentScopedFixedPath(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "multica-real")
	transport, err := prepareStableAgentCLITransport(
		Config{WorkspacesRoot: root},
		"workspace-1",
		"agent-1",
		bin,
	)
	if err != nil {
		t.Fatalf("prepareStableAgentCLITransport: %v", err)
	}

	wantRoot := filepath.Join(workspaceStateRoot(root, "workspace-1"), "cli-transport", "agent-1")
	if transport.Root() != wantRoot {
		t.Fatalf("transport root = %q, want %q", transport.Root(), wantRoot)
	}
	if got, want := transport.WrapperPath(), filepath.Join(wantRoot, "bin", "multica"); got != want {
		t.Fatalf("wrapper path = %q, want %q", got, want)
	}
}

func TestSplitAgentProcessEnvironmentRemovesTurnIdentity(t *testing.T) {
	stable, current, err := splitAgentProcessEnvironment(map[string]string{
		"MULTICA_AGENT_ID":                       "agent-1",
		"MULTICA_WORKSPACE_ID":                   "workspace-1",
		"MULTICA_TASK_ID":                        "task-1",
		"MULTICA_AGENT_INBOX_LEASE_TOKEN":        "lease-1",
		"MULTICA_QUICK_CREATE_SOURCE_MESSAGE_ID": "message-1",
	})
	if err != nil {
		t.Fatalf("splitAgentProcessEnvironment: %v", err)
	}
	if stable["MULTICA_AGENT_ID"] != "agent-1" || stable["MULTICA_WORKSPACE_ID"] != "workspace-1" {
		t.Fatalf("stable environment = %#v", stable)
	}
	if _, ok := stable["MULTICA_TASK_ID"]; ok {
		t.Fatal("stable environment contains task id")
	}
	if current["MULTICA_TASK_ID"] != "task-1" || current["MULTICA_AGENT_INBOX_LEASE_TOKEN"] != "lease-1" {
		t.Fatalf("current-turn environment = %#v", current)
	}
}

func mustStatMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode()
}

// TestWindowsCLIWrapperBodyIsCmdNotShell verifies the Windows per-task CLI
// wrapper is a cmd.exe batch that clears and sets the token env and calls the
// real exe — never a bare extensionless #!/bin/sh shim (the popup root cause).
func TestWindowsCLIWrapperBodyIsCmdNotShell(t *testing.T) {
	body := windowsCLIWrapperBody(`C:\agent\cli-transport\run-1\token`, `C:\multica\multica.exe`)

	if strings.HasPrefix(body, "#!") {
		t.Fatalf("windows wrapper starts with shebang: %q", body)
	}
	for _, want := range []string{
		"@echo off",
		`set "MULTICA_TOKEN="`,
		`set "MULTICA_TOKEN_FILE=C:\agent\cli-transport\run-1\token"`,
		`call "C:\multica\multica.exe" %*`,
		"exit /b %ERRORLEVEL%",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("windows wrapper missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, "#/bin/sh") {
		t.Fatalf("windows wrapper contains shell shebang: %q", body)
	}
}
