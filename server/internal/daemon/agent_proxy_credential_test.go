package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentProxyCLITransportPinsAuthenticatedLaunchContext(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	key := InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}

	transport, err := d.prepareAgentProxyCLITransport(
		key,
		"runtime-1",
		filepath.Join(root, "bin", "multica"),
	)
	if err != nil {
		t.Fatalf("prepare Agent Proxy CLI transport: %v", err)
	}

	if info, err := os.Stat(filepath.Dir(transport.tokenFile)); err != nil {
		t.Fatalf("stat Agent Proxy credential directory: %v", err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("Agent Proxy credential directory mode = %o, want 700", info.Mode().Perm())
	}
	if info, err := os.Stat(transport.tokenFile); err != nil {
		t.Fatalf("stat Agent Proxy token file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("Agent Proxy token file mode = %o, want 600", info.Mode().Perm())
	}
	token, err := os.ReadFile(transport.tokenFile)
	if err != nil {
		t.Fatalf("read Agent Proxy token file: %v", err)
	}
	if strings.TrimSpace(string(token)) == "" {
		t.Fatal("Agent Proxy token file is empty")
	}

	wrapper, err := os.ReadFile(transport.wrapperPath)
	if err != nil {
		t.Fatalf("read Agent Proxy CLI wrapper: %v", err)
	}
	wrapperText := string(wrapper)
	for _, expected := range []string{
		"MULTICA_AGENT_ID",
		"MULTICA_WORKSPACE_ID",
		AgentProxyURLEnv,
		AgentProxyTokenFileEnv,
	} {
		if !strings.Contains(wrapperText, expected) {
			t.Fatalf("Agent Proxy CLI wrapper omitted %q: %s", expected, wrapperText)
		}
	}
	if strings.Contains(wrapperText, strings.TrimSpace(string(token))) {
		t.Fatal("Agent Proxy CLI wrapper contains the raw token")
	}

	credential, err := d.authenticateAgentProxyToken(strings.TrimSpace(string(token)))
	if err != nil {
		t.Fatalf("authenticate Agent Proxy token: %v", err)
	}
	if credential.Inbox != key || credential.RuntimeID != "runtime-1" {
		t.Fatalf("authenticated Agent Proxy context = %+v", credential)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("close Agent Proxy CLI transport: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(transport.tokenFile)); !os.IsNotExist(err) {
		t.Fatalf("Agent Proxy credential directory survived cleanup: %v", err)
	}
	if _, err := d.authenticateAgentProxyToken(strings.TrimSpace(string(token))); err == nil {
		t.Fatal("revoked Agent Proxy token still authenticates")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("idempotent Agent Proxy CLI transport close: %v", err)
	}
}
