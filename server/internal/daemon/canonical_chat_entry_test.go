package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/daemon/execenv"
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

	// issueMarker is task-scoped and appears as a literal in
	// .agent_context/issue_context.md (ChatSessionID does not appear in AGENTS.md).
	runTurn := func(turnID, taskWorkDir, chatSessionID, issueMarker string) (backend agent.Backend, release func(bool), workDir string) {
		t.Helper()
		task := Task{
			ID:                     turnID,
			WorkspaceID:            workspaceID,
			RuntimeID:              runtimeID,
			ChatSessionID:          chatSessionID,
			PriorSessionID:         "provider-session-shared",
			RuntimeStateGeneration: 3,
			AuthToken:              "token-a",
		}
		execOpts := agent.ExecOptions{
			Cwd:           taskWorkDir,
			Model:         "model-a",
			ThinkingLevel: "low",
		}
		taskCtx := execenv.TaskContextForEnv{
			AgentID:       agentID,
			AgentName:     "agent-a",
			ChatSessionID: chatSessionID,
			ChannelID:     "channel-" + turnID[:8],
			IssueID:       issueMarker,
			Directed:      true,
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
			taskCtx,
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
		// Tier B: provider-readable runtime brief on stable cwd.
		agentsPath := filepath.Join(turn.WorkDir, "AGENTS.md")
		raw, readErr := os.ReadFile(agentsPath)
		if readErr != nil {
			t.Fatalf("read AGENTS.md on stable cwd: %v", readErr)
		}
		if !strings.Contains(string(raw), "BEGIN MULTICA-RUNTIME") {
			t.Fatalf("AGENTS.md missing Multica runtime block:\n%s", raw)
		}
		// Tier B: task-scoped sidecar with this turn's marker.
		ctxPath := filepath.Join(turn.WorkDir, ".agent_context", "issue_context.md")
		ctxRaw, ctxErr := os.ReadFile(ctxPath)
		if ctxErr != nil {
			t.Fatalf("expected .agent_context/issue_context.md on stable cwd: %v", ctxErr)
		}
		if !strings.Contains(string(ctxRaw), issueMarker) {
			t.Fatalf("issue_context.md missing this-turn marker %q:\n%s", issueMarker, ctxRaw)
		}
		return backend, release, turn.WorkDir
	}

	issueA := "issue-marker-task-A-" + uuid.NewString()
	backendA, releaseA, stableWorkDir := runTurn(uuid.NewString(), taskWorkDirA, uuid.NewString(), issueA)
	// Stamp a poison residual that must not survive into turn B if materialize
	// only appended without clearing .agent_context (Tier C).
	poison := filepath.Join(stableWorkDir, ".agent_context", "poison_task_A.md")
	if err := os.WriteFile(poison, []byte("TASK_A_SECRET_SHOULD_NOT_LEAK"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := backendA.Execute(context.Background(), "first", agent.ExecOptions{}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	releaseA(true)

	issueB := "issue-marker-task-B-" + uuid.NewString()
	backendB, releaseB, stableWorkDirB := runTurn(uuid.NewString(), taskWorkDirB, uuid.NewString(), issueB)
	defer releaseB(true)

	if stableWorkDirB != stableWorkDir {
		t.Fatalf("stable workdir drift: %q vs %q", stableWorkDir, stableWorkDirB)
	}
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

	// Tier C residual clear: poison + task A facts gone; task B facts present.
	if _, err := os.Stat(poison); !os.IsNotExist(err) {
		t.Fatalf("task A poison residual still present under stable cwd: %v", err)
	}
	ctxB, err := os.ReadFile(filepath.Join(stableWorkDir, ".agent_context", "issue_context.md"))
	if err != nil {
		t.Fatal(err)
	}
	ctxText := string(ctxB)
	if strings.Contains(ctxText, issueA) {
		t.Fatalf("task A issue marker still present after turn B materialize:\n%s", ctxText)
	}
	if !strings.Contains(ctxText, issueB) {
		t.Fatalf("task B issue marker missing after turn B materialize:\n%s", ctxText)
	}
	if strings.Contains(ctxText, "TASK_A_SECRET_SHOULD_NOT_LEAK") {
		t.Fatal("task A secret leaked into issue_context after turn B")
	}
	agentsRaw, err := os.ReadFile(filepath.Join(stableWorkDir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentsRaw), "BEGIN MULTICA-RUNTIME") {
		t.Fatal("turn B AGENTS.md missing Multica runtime block")
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
		execenv.TaskContextForEnv{},
		nil,
	)
	if err == nil || used {
		t.Fatalf("want fail-closed missing generation, used=%v err=%v", used, err)
	}
}
