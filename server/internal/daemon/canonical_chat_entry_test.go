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
			"MULTICA_SERVER_URL":   "https://example.test",
			"MULTICA_WORKSPACE_ID": workspaceID,
			"MULTICA_AGENT_ID":     agentID,
			"MULTICA_AGENT_NAME":   "agent-a",
			"MULTICA_EXECUTION_ID": turnID,
			"MULTICA_RUN_ID":       turnID,
			"PATH":                 "/usr/bin",
		}
	}

	// Channel/DM deliveries reuse one resident runtime. Different execution
	// workdirs must not enter the fingerprint.
	sharedChat := uuid.NewString()
	runTurn := func(turnID, taskWorkDir, chatSessionID, priorSession string, directed bool) (backend agent.Backend, release func(bool), workDir string) {
		t.Helper()
		task := Task{
			ID:                     turnID,
			WorkspaceID:            workspaceID,
			RuntimeID:              runtimeID,
			ChatSessionID:          chatSessionID,
			ChannelID:              "channel-shared",
			ChatMessage:            "investigate the issue",
			TriggerCommentID:       "comment-" + turnID,
			TriggerCommentContent:  "Please investigate.",
			PriorSessionID:         priorSession,
			RuntimeStateGeneration: 3,
			AuthToken:              "token-a",
		}
		prompt := BuildPrompt(task, "grok", "")
		if !isChatLikeTask(task) || !strings.Contains(prompt, "You are running as a chat assistant") || strings.Contains(prompt, "Your assigned issue ID is:") {
			t.Fatalf("canonical delivery did not select chat semantics:\n%s", prompt)
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
			ChannelID:     "channel-shared",
			Directed:      directed,
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
		// Option A: AGENTS exists after process-create materialize; reuse does not rewrite.
		agentsPath := filepath.Join(turn.WorkDir, "AGENTS.md")
		raw, readErr := os.ReadFile(agentsPath)
		if readErr != nil {
			t.Fatalf("read AGENTS.md on stable cwd: %v", readErr)
		}
		if !strings.Contains(string(raw), "BEGIN MULTICA-RUNTIME") {
			t.Fatalf("AGENTS.md missing Multica runtime block:\n%s", raw)
		}
		return backend, release, turn.WorkDir
	}

	backendA, releaseA, stableWorkDir := runTurn(uuid.NewString(), taskWorkDirA, sharedChat, "provider-session-shared", true)
	userSibling := filepath.Join(stableWorkDir, "user_notes.md")
	if err := os.WriteFile(userSibling, []byte("USER_OWNED_NOTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := backendA.Execute(context.Background(), "first", agent.ExecOptions{}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if got := backendA.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "provider-session-shared" {
		t.Fatalf("same-chat resume = %q, want provider-session-shared", got)
	}
	releaseA(true)

	agentsPath := filepath.Join(stableWorkDir, "AGENTS.md")
	infoA, err := os.Stat(agentsPath)
	if err != nil {
		t.Fatal(err)
	}

	backendB, releaseB, stableWorkDirB := runTurn(uuid.NewString(), taskWorkDirB, sharedChat, "provider-session-shared", true)
	defer releaseB(true)

	if stableWorkDirB != stableWorkDir {
		t.Fatalf("stable workdir drift: %q vs %q", stableWorkDir, stableWorkDirB)
	}
	innerA := backendA.(*canonicalSessionBackend).backend
	innerB := backendB.(*canonicalSessionBackend).backend
	if innerA != innerB {
		t.Fatal("second turn recreated resident backend despite same ChatSessionID context key")
	}
	if got := d.canonicalRuntimes.slotCount(); got != 1 {
		t.Fatalf("slot count = %d, want 1", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("factory counts created=%d closed=%d, want 1/0 (no recreate)", created, closed)
	}

	infoB, err := os.Stat(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !infoA.ModTime().Equal(infoB.ModTime()) {
		t.Fatal("option A violation: AGENTS.md rewritten on resident reuse")
	}
	if raw, err := os.ReadFile(userSibling); err != nil || string(raw) != "USER_OWNED_NOTES" {
		t.Fatalf("user-owned .agent_context sibling not preserved: %v %q", err, raw)
	}
}

func TestTryCanonicalChatBackendRotatesFreshSessionAcrossChatSessions(t *testing.T) {
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
	baseEnv := func(turnID string) map[string]string {
		return map[string]string{
			"MULTICA_SERVER_URL":   "https://example.test",
			"MULTICA_WORKSPACE_ID": workspaceID,
			"MULTICA_AGENT_ID":     agentID,
			"MULTICA_AGENT_NAME":   "agent-a",
			"MULTICA_EXECUTION_ID": turnID,
			"MULTICA_RUN_ID":       turnID,
			"PATH":                 "/usr/bin",
		}
	}
	run := func(chatID, prior string) (agent.Backend, func(bool)) {
		t.Helper()
		task := Task{
			ID:                     uuid.NewString(),
			WorkspaceID:            workspaceID,
			RuntimeID:              runtimeID,
			ChatSessionID:          chatID,
			ChannelID:              "channel-shared",
			ChatMessage:            "hello",
			PriorSessionID:         prior,
			RuntimeStateGeneration: 3,
			AuthToken:              "token-a",
		}
		execOpts := agent.ExecOptions{Cwd: filepath.Join(root, "task-work"), Model: "model-a", ThinkingLevel: "low"}
		taskCtx := execenv.TaskContextForEnv{
			AgentID: agentID, AgentName: "agent-a", ChannelID: "channel-shared", ChatSessionID: chatID, Directed: true,
		}
		backend, release, _, used, err := d.tryCanonicalChatBackend(
			task, "grok", executionProfileFull, agentID, "token-a", bin, baseEnv(task.ID),
			AgentEntry{Path: bin}, agent.Config{}, &execOpts, taskCtx, agentRuntimeTurnTestLogger(),
		)
		if err != nil || !used {
			t.Fatalf("tryCanonicalChatBackend used=%v err=%v", used, err)
		}
		return backend, release
	}

	backendA, releaseA := run("chat-A", "provider-session-A")
	if _, err := backendA.Execute(context.Background(), "a", agent.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := backendA.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "provider-session-A" {
		t.Fatalf("A resume = %q", got)
	}
	releaseA(true)

	// Cross-chat (Frank long-lived colleague): reuse backend, keep Prior.
	backendB, releaseB := run("chat-B", "provider-session-A")
	defer releaseB(true)
	if _, err := backendB.Execute(context.Background(), "b", agent.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := backendB.(*canonicalSessionBackend).backend.(*canonicalRuntimeTestBackend).lastResumeSessionID(); got != "provider-session-A" {
		t.Fatalf("B resume = %q, want Prior retained across chat", got)
	}
	created, closed := probe.counts()
	if created != 1 || closed != 0 {
		t.Fatalf("created=%d closed=%d, want 1/0 (reuse across chat)", created, closed)
	}
	if backendA.(*canonicalSessionBackend).backend != backendB.(*canonicalSessionBackend).backend {
		t.Fatal("cross-chat must reuse resident backend")
	}
}

func TestTryCanonicalChatBackendRejectsMissingGeneration(t *testing.T) {
	d := &Daemon{
		agentRuntimeTurns: newAgentRuntimeTurnCoordinator(Config{WorkspacesRoot: t.TempDir()}, agentRuntimeTurnTestLogger()),
		canonicalRuntimes: newCanonicalAgentRuntimePool(),
	}
	execOpts := agent.ExecOptions{Cwd: "/tmp/task"}
	_, _, _, used, err := d.tryCanonicalChatBackend(
		Task{ChannelID: "channel-1", ChatMessage: "hello", RuntimeStateGeneration: 0, ID: uuid.NewString(), RuntimeID: uuid.NewString()},
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
