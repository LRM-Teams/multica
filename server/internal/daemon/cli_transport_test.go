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

	wantDir := filepath.Join(root, "workspace-1", ".multica", "agents", "agent-1", "runtime", "cli-transport", "run-1")
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

func mustStatMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode()
}
