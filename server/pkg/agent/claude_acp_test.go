package agent

import (
	"path/filepath"
	"testing"
)

func TestNewClaudeACPBackendImplementsResidentInterfaces(t *testing.T) {
	t.Parallel()
	b := NewClaudeACPBackend(Config{})
	if _, ok := b.(ResidentRuntimeForceKillable); !ok {
		t.Fatal("ClaudeACPBackend must implement ResidentRuntimeForceKillable")
	}
	if _, ok := b.(ResidentRuntimeLivenessChecker); !ok {
		t.Fatal("ClaudeACPBackend must implement ResidentRuntimeLivenessChecker")
	}
	if err := b.(ResidentRuntimeForceKillable).ForceKill(); err != nil {
		t.Fatalf("ForceKill empty: %v", err)
	}
	alive, known := b.(ResidentRuntimeLivenessChecker).RuntimeAlive()
	if known || alive {
		t.Fatalf("empty process: alive=%v known=%v", alive, known)
	}
}

func TestClaudeCanonicalResidentCapability(t *testing.T) {
	t.Parallel()
	if !Capabilities("claude").CanonicalResident {
		t.Fatal("claude must advertise CanonicalResident after ACP resident PR")
	}
	if !Capabilities("claude").ForceRestart {
		t.Fatal("claude ForceRestart must derive true from resident ForceKill")
	}
}

func TestLooksLikeClaudeACPBinary(t *testing.T) {
	t.Parallel()
	if !looksLikeClaudeACPBinary("claude-agent-acp") {
		t.Fatal("expected claude-agent-acp match")
	}
	if !looksLikeClaudeACPBinary("/opt/bin/claude-code-acp") {
		t.Fatal("expected claude-code-acp match")
	}
	if looksLikeClaudeACPBinary("claude") {
		t.Fatal("main claude CLI must not look like ACP adapter")
	}
}

func TestResolveClaudeACPExecutableRejectsMissing(t *testing.T) {
	t.Parallel()
	_, err := resolveClaudeACPExecutable(Config{
		ExecutablePath: "claude",
		Env:            map[string]string{"PATH": t.TempDir()},
	})
	if err == nil {
		t.Fatal("expected error when adapter missing")
	}
}

func TestResolveClaudeACPExecutableUsesEnvOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude-agent-acp")
	writeTestExecutable(t, bin, []byte("#!/bin/sh\nexit 0\n"))
	path, err := resolveClaudeACPExecutable(Config{
		Env: map[string]string{"MULTICA_CLAUDE_ACP_EXECUTABLE": bin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != bin {
		t.Fatalf("path=%q want %q", path, bin)
	}
}
