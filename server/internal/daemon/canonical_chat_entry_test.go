package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/pkg/agent"
)

func TestTryCanonicalChatBackendReusesResidentSlotAcrossTaskWorkdirs(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "multica")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	runtimeID := uuid.NewString()
	probe := &canonicalRuntimeFactoryProbe{}

	d := &Daemon{
		cfg:                          Config{WorkspacesRoot: root},
		logger:                       agentRuntimeTurnTestLogger(),
		agentRuntimeTurns:            newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: root}, agentRuntimeTurnTestLogger()),
		canonicalRuntimes:            newCanonicalAgentRuntimePool(),
		canonicalChatFactoryOverride: probe.factory,
	}

	// Per-task cloud workdirs differ; canonical identity must ignore them.
	taskWorkDirA := filepath.Join(root, workspaceID, "task-aaaa", "workdir")
	taskWorkDirB := filepath.Join(root, workspaceID, "task-bbbb", "workdir")
	for _, dir := range []string{taskWorkDirA, taskWorkDirB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	baseEnv := func(turnID string) map[string]string {
		return map[string]string{
			"MULTICA_SERVER_URL":              "https://example.test",
			"MULTICA_WORKSPACE_ID":            workspaceID,
			"MULTICA_AGENT_ID":                agentID,
			"MULTICA_AGENT_NAME":              "agent-a",
			"MULTICA_TASK_ID":                 turnID,
			"MULTICA_RUN_ID":                  turnID,
			"MULTICA_AGENT_INBOX_LEASE_TOKEN": "lease-a",
			"PATH":                            "/usr/bin",
		}
	}

	runTurn := func(turnID, taskWorkDir string) (backend agent.Backend, release func(bool), workDir string) {
		t.Helper()
		task := Task{
			ID:                     turnID,
			WorkspaceID:            workspaceID,
			RuntimeID:              runtimeID,
			ChatSessionID:          uuid.NewString(), // different chat surfaces
			PriorSessionID:         "provider-session-shared",
			RuntimeStateGeneration: 3,
			AuthToken:              "token-a",
		}
		execOpts := agent.ExecOptions{
			Cwd:           taskWorkDir, // deliberately unstable per-task path
			Model:         "model-a",
			ThinkingLevel: "low",
		}
		backend, release, turn, used, err := d.tryCanonicalChatBackend(
			task,
			"grok",
			executionProfileFull,
			agentID,
			"token-a",
			bin,
			baseEnv(turnID),
			AgentEntry{Path: bin},
			agent.Config{},
			&execOpts,
			agentRuntimeTurnTestLogger(),
		)
		if err != nil {
			t.Fatalf("tryCanonicalChatBackend: %v", err)
		}
		if !used || backend == nil || release == nil || turn == nil {
			t.Fatalf("used=%v backend=%v release=%v turn=%v", used, backend != nil, release != nil, turn != nil)
		}
		if execOpts.Cwd != turn.WorkDir {
			t.Fatalf("exec cwd = %q, want stable turn workdir %q", execOpts.Cwd, turn.WorkDir)
		}
		if execOpts.Cwd == taskWorkDir {
			t.Fatalf("exec cwd still per-task path %q", taskWorkDir)
		}
		return backend, release, turn.WorkDir
	}

	turnAID := uuid.NewString()
	backendA, releaseA, stableWorkDir := runTurn(turnAID, taskWorkDirA)
	if _, err := backendA.Execute(context.Background(), "first", agent.ExecOptions{}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	releaseA(true)

	turnBID := uuid.NewString()
	backendB, releaseB, stableWorkDirB := runTurn(turnBID, taskWorkDirB)
	defer releaseB(true)

	if stableWorkDirB != stableWorkDir {
		t.Fatalf("stable workdir drift: %q vs %q", stableWorkDir, stableWorkDirB)
	}
	// Unwrap canonical session wrapper to compare resident backends.
	innerA := backendA.(*canonicalSessionBackend).backend
	innerB := backendB.(*canonicalSessionBackend).backend
	if innerA != innerB {
		t.Fatal("second turn recreated resident backend despite same agent×runtime and stable turn.WorkDir")
	}
	if got := d.canonicalRuntimes.slotCount(); got != 1 {
		t.Fatalf("slot count = %d, want 1", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("factory counts created=%d closed=%d, want 1/0 (no recreate)", created, closed)
	}
}

func TestTryCanonicalChatBackendRejectsMissingGeneration(t *testing.T) {
	d := &Daemon{
		agentRuntimeTurns: newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: t.TempDir()}, agentRuntimeTurnTestLogger()),
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
	}
	execOpts := agent.ExecOptions{Cwd: "/tmp/task"}
	_, _, _, used, err := d.tryCanonicalChatBackend(
		Task{ChatSessionID: uuid.NewString(), RuntimeStateGeneration: 0, ID: uuid.NewString(), RuntimeID: uuid.NewString()},
		"grok",
		executionProfileFull,
		uuid.NewString(),
		"token",
		"/bin/true",
		map[string]string{},
		AgentEntry{Path: "/bin/true"},
		agent.Config{},
		&execOpts,
		nil,
	)
	if err == nil || used {
		t.Fatalf("want fail-closed missing generation, used=%v err=%v", used, err)
	}
}
