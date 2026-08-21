package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCursorMcpConfigWritesManagedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := json.RawMessage(`{"mcpServers":{"figma":{"command":"npx","args":["-y","@nexus2520/figma-mcp-server"],"env":{"FIGMA_ACCESS_TOKEN":"figd_test"}}}}`)
	if err := ensureCursorMcpConfig(dir, raw, nil); err != nil {
		t.Fatalf("ensureCursorMcpConfig: %v", err)
	}
	mcpPath := filepath.Join(dir, cursorMcpRelPath)
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read mcp.json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse mcp.json: %v", err)
	}
	servers, ok := parsed["mcpServers"].(map[string]any)
	if !ok || servers["figma"] == nil {
		t.Fatalf("mcp.json missing figma server: %s", data)
	}
	info, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("stat mcp.json: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mcp.json mode = %o, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, cursorMcpOwnedMarker)); err != nil {
		t.Fatalf("owned marker missing: %v", err)
	}
}

func TestEnsureCursorMcpConfigClearsOnlyOwnedFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	raw := json.RawMessage(`{"mcpServers":{"figma":{"command":"npx"}}}`)
	if err := ensureCursorMcpConfig(dir, raw, nil); err != nil {
		t.Fatalf("write managed: %v", err)
	}
	if err := ensureCursorMcpConfig(dir, nil, nil); err != nil {
		t.Fatalf("clear managed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cursorMcpRelPath)); !os.IsNotExist(err) {
		t.Fatalf("managed mcp.json still present after clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cursorMcpOwnedMarker)); !os.IsNotExist(err) {
		t.Fatalf("owned marker still present after clear: %v", err)
	}
}

func TestEnsureCursorMcpConfigLeavesUserFileAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cursorDir := filepath.Join(dir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o700); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(dir, cursorMcpRelPath)
	if err := os.WriteFile(userPath, []byte(`{"mcpServers":{"user":{"command":"echo"}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureCursorMcpConfig(dir, nil, nil); err != nil {
		t.Fatalf("clear without marker: %v", err)
	}
	data, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatalf("user mcp.json removed unexpectedly: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("user mcp.json corrupted: %s", data)
	}
}

func TestEnsureCursorMcpConfigRequiresCwdWhenManaged(t *testing.T) {
	t.Parallel()
	err := ensureCursorMcpConfig("", json.RawMessage(`{"mcpServers":{}}`), nil)
	if err == nil {
		t.Fatal("expected error when cwd empty and mcp_config managed")
	}
}

func TestBuildCursorArgsApproveMcpsWhenManaged(t *testing.T) {
	t.Parallel()
	args := buildCursorArgs("hi", ExecOptions{
		Cwd:       "/tmp/ws",
		McpConfig: json.RawMessage(`{"mcpServers":{"figma":{"command":"npx"}}}`),
	}, nil)
	if !cursorTestHasArg(args, "--approve-mcps") {
		t.Fatalf("expected --approve-mcps, got %v", args)
	}
	if !cursorTestHasArg(args, "--workspace") {
		t.Fatalf("expected --workspace, got %v", args)
	}
}

func TestBuildCursorArgsNoApproveMcpsWithoutManaged(t *testing.T) {
	t.Parallel()
	args := buildCursorArgs("hi", ExecOptions{Cwd: "/tmp/ws"}, nil)
	if cursorTestHasArg(args, "--approve-mcps") {
		t.Fatalf("did not expect --approve-mcps, got %v", args)
	}
}

func TestBuildPiArgsIgnoresMcpConfig(t *testing.T) {
	t.Parallel()
	// Pi has no MCP support (pi 0.84.2) and does not understand --mcp-config;
	// passing it makes pi exit 1 immediately with "Unknown option: --mcp-config"
	// (LRM-1598). The pi backend must not emit the flag even when an MCP config
	// path was provided.
	args := buildPiArgs("", "/tmp/s.jsonl", ExecOptions{
		piMcpConfigPath: "/tmp/mcp.json",
	}, nil)
	if cursorTestHasArgPair(args, "--mcp-config", "/tmp/mcp.json") {
		t.Fatalf("pi must not pass --mcp-config, got %v", args)
	}
	rpcArgs := buildPiRPCArgs("/tmp/s.jsonl", ExecOptions{piMcpConfigPath: "/tmp/mcp.json"}, nil)
	if cursorTestHasArgPair(rpcArgs, "--mcp-config", "/tmp/mcp.json") {
		t.Fatalf("pi RPC must not pass --mcp-config, got %v", rpcArgs)
	}
}

func TestBuildPiArgsCollapsesDefaultModelSentinel(t *testing.T) {
	t.Parallel()
	// The research fleet persists literal "default" as the model sentinel. pi
	// has no model named "default" — `--model default` makes pi exit 1 with
	// "Model \"default\" not found" (LRM-1598). Both builders must collapse it
	// to no --model flag so pi chooses its own default.
	for _, sentinel := range []string{"", "default", "  default  "} {
		args := buildPiArgs("", "/tmp/s.jsonl", ExecOptions{Model: sentinel}, nil)
		if cursorTestHasArgPair(args, "--model", sentinel) || cursorTestHasArg(args, "--model") {
			t.Fatalf("expected no --model for sentinel %q, got %v", sentinel, args)
		}
		rpcArgs := buildPiRPCArgs("/tmp/s.jsonl", ExecOptions{Model: sentinel}, nil)
		if cursorTestHasArg(rpcArgs, "--model") {
			t.Fatalf("expected no RPC --model for sentinel %q, got %v", sentinel, rpcArgs)
		}
	}
}

func cursorTestHasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func cursorTestHasArgPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
