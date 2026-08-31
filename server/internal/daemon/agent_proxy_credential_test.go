package daemon

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAgentProxyRegistrationPinsAndRevokesServerCredential(t *testing.T) {
	root := t.TempDir()
	var forwarded, revoked bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/reminders/list":
			forwarded = true
			if got := r.Header.Get("Authorization"); got != "Bearer sk_agent_launch" {
				t.Errorf("forwarded Authorization = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/api/daemon/runtimes/runtime-1/agents/agent-1/credentials/credential-1/revoke":
			revoked = true
			if got := r.Header.Get("Authorization"); got != "Bearer mdt_runtime" {
				t.Errorf("revoke Authorization = %q", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: root, ServerBaseURL: upstream.URL, HealthPort: 19514}
	launchCredential, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Prefix: "sk_agent_lau", Token: "sk_agent_launch",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken("runtime-1", "mdt_runtime", time.Now().Add(time.Hour))
	d := &Daemon{cfg: cfg, client: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	transport, err := d.prepareAgentProxyCLITransport(
		InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"},
		"runtime-1", "launch-1", filepath.Join(root, "bin", "multica"), launchCredential,
	)
	if err != nil {
		t.Fatal(err)
	}
	rawToken, err := os.ReadFile(transport.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-2", AgentID: "agent-1", Prefix: "sk_agent_new", Token: "sk_agent_replacement",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/reminders/list", strings.NewReader(`{}`))
	req.Header.Set(AgentProxyAuthHeader, "Bearer "+strings.TrimSpace(string(rawToken)))
	rec := httptest.NewRecorder()
	d.authenticateAgentProxyRequest(d.credentialProxyAgentAPIHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !forwarded {
		t.Fatalf("Proxy response = %d, forwarded=%v", rec.Code, forwarded)
	}
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("launch server credential was not revoked")
	}
	if cached, ok := readCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", time.Now()); !ok || cached.CredentialID != "credential-2" {
		t.Fatalf("replacement cache was removed: ok=%v credential=%+v", ok, cached)
	}
}

func TestAgentProxyCLITransportPinsAuthenticatedLaunchContext(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	key := InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}

	transport, err := d.prepareAgentProxyCLITransport(
		key,
		"runtime-1",
		"launch-1",
		filepath.Join(root, "bin", "multica"),
	)
	if err != nil {
		t.Fatalf("prepare Agent Proxy CLI transport: %v", err)
	}
	wantRoot := filepath.Join(workspaceStateRoot(root, "workspace-1"), "cli-transport", "agent-1", "launch-1")
	if transport.root != wantRoot {
		t.Fatalf("Agent Proxy wrapper root = %q, want %q", transport.root, wantRoot)
	}
	wantTokenFile := filepath.Join(workspaceStateRoot(root, "workspace-1"), "agent-proxy-tokens", "agent-1", "launch-1.token")
	if transport.tokenFile != wantTokenFile {
		t.Fatalf("Agent Proxy token file = %q, want %q", transport.tokenFile, wantTokenFile)
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
		"unset " + AgentProxyCLIWrapperEnv,
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
	if _, err := os.Stat(transport.tokenFile); !os.IsNotExist(err) {
		t.Fatalf("Agent Proxy token file survived cleanup: %v", err)
	}
	if _, err := d.authenticateAgentProxyToken(strings.TrimSpace(string(token))); err == nil {
		t.Fatal("revoked Agent Proxy token still authenticates")
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("idempotent Agent Proxy CLI transport close: %v", err)
	}
}

func TestAgentProxyRequestAuthenticationPinsLaunchIdentity(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	transport, err := d.prepareAgentProxyCLITransport(
		InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"},
		"runtime-1", "launch-1", filepath.Join(root, "bin", "multica"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	rawToken, err := os.ReadFile(transport.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	handler := d.authenticateAgentProxyRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-ID") != "agent-1" || r.Header.Get("X-Workspace-ID") != "workspace-1" {
			t.Errorf("identity headers = %q/%q", r.Header.Get("X-Agent-ID"), r.Header.Get("X-Workspace-ID"))
		}
		if r.Header.Get(AgentProxyAuthHeader) != "" {
			t.Error("local credential leaked past authentication boundary")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	for name, tc := range map[string]struct {
		token string
		want  int
	}{
		"forged": {token: "mpt_forged", want: http.StatusUnauthorized},
		"valid":  {token: strings.TrimSpace(string(rawToken)), want: http.StatusNoContent},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			req.Header.Set(AgentProxyAuthHeader, "Bearer "+tc.token)
			req.Header.Set("X-Agent-ID", "agent-forged")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestAgentProxyCLITransportRejectsDuplicateLaunchWithoutReplacingToken(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	key := InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}
	bin := filepath.Join(root, "bin", "multica")
	first, err := d.prepareAgentProxyCLITransport(key, "runtime-1", "launch-1", bin)
	if err != nil {
		t.Fatalf("prepare first transport: %v", err)
	}
	defer func() { _ = first.Close() }()
	firstToken, err := os.ReadFile(first.tokenFile)
	if err != nil {
		t.Fatalf("read first token: %v", err)
	}
	if _, err := d.prepareAgentProxyCLITransport(key, "runtime-1", "launch-1", bin); err == nil {
		t.Fatal("duplicate launch should be rejected")
	}
	after, err := os.ReadFile(first.tokenFile)
	if err != nil {
		t.Fatalf("read token after duplicate: %v", err)
	}
	if string(after) != string(firstToken) {
		t.Fatal("duplicate launch replaced the original token")
	}
}

func TestAgentProxyCLITransportRejectsPathTraversal(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, key := range []InboxKey{
		{WorkspaceID: "../workspace", AgentID: "agent-1"},
		{WorkspaceID: "workspace-1", AgentID: "../agent"},
	} {
		if _, err := d.prepareAgentProxyCLITransport(key, "runtime-1", "launch-1", "/tmp/multica"); err == nil {
			t.Fatalf("path traversal key %#v was accepted", key)
		}
	}
	if _, err := d.prepareAgentProxyCLITransport(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", "../launch", "/tmp/multica"); err == nil {
		t.Fatal("launch path traversal was accepted")
	}
}

func TestAgentProxyCLIWrapperPreservesExistingTaskCredentialTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper execution test")
	}
	root := t.TempDir()
	realBinary := filepath.Join(root, "real-multica")
	if err := os.WriteFile(realBinary, []byte("#!/bin/sh\nprintf 'agent=%s\\nworkspace=%s\\nproxy=%s\\nproxy_token_file=%s\\ntask_token_file=%s\\nwrapper_forward=%s\\n' \"$MULTICA_AGENT_ID\" \"$MULTICA_WORKSPACE_ID\" \"$MULTICA_AGENT_PROXY_URL\" \"$MULTICA_AGENT_PROXY_TOKEN_FILE\" \"$MULTICA_TOKEN_FILE\" \"$MULTICA_AGENT_CLI_WRAPPER\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	d := New(Config{WorkspacesRoot: root, HealthPort: 19514}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	transport, err := d.prepareAgentProxyCLITransport(
		InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"},
		"runtime-1",
		"launch-1",
		realBinary,
	)
	if err != nil {
		t.Fatalf("prepare Agent Proxy CLI transport: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })

	command := exec.Command(transport.wrapperPath)
	command.Env = append(os.Environ(),
		"MULTICA_TOKEN_FILE=/task/credential.token",
		AgentProxyCLIWrapperEnv+"=/stale/wrapper",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute Agent Proxy wrapper: %v: %s", err, output)
	}
	text := string(output)
	for _, expected := range []string{
		"agent=agent-1",
		"workspace=workspace-1",
		"proxy=http://127.0.0.1:19514",
		"proxy_token_file=" + transport.tokenFile,
		"task_token_file=/task/credential.token",
		"wrapper_forward=\n",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Agent Proxy wrapper output omitted %q: %s", expected, text)
		}
	}
}
