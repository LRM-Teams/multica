package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestRunTask_RefusesTurnWhenAgentWorkspaceOverCapacity is the direct
// regression test for task #94: the agent's own tool calls write to
// .multica/agents/<id> directly during a turn (bash/edit tools operating on
// MULTICA_AGENT_ROOT), completely outside any daemon-mediated write path, so
// there is no per-write byte to intercept. The only enforcement point is
// turn-start: seed the workspace already over its cap, then confirm runTask
// refuses to start the turn at all (a distinct, named FailureReason) rather
// than proceeding into agent execution.
func TestRunTask_RefusesTurnWhenAgentWorkspaceOverCapacity(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	cfg := Config{
		WorkspacesRoot:           workspacesRoot,
		AgentWorkspaceQuotaBytes: 10, // tiny cap, trivially exceeded below
		Agents: map[string]AgentEntry{
			"claude": {Path: filepath.Join(t.TempDir(), "unused-agent-binary")},
		},
	}

	const workspaceID = "ws-quota"
	const agentID = "agent-quota"
	agentRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		t.Fatalf("seed agent root: %v", err)
	}
	// Well over the 10-byte cap.
	if err := os.WriteFile(filepath.Join(agentRoot, "notes.md"), []byte("more than ten bytes of content"), 0o644); err != nil {
		t.Fatalf("seed oversized file: %v", err)
	}

	d := &Daemon{
		client:         NewClient("http://unused.invalid"),
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:     make(map[string]*workspaceState),
		runtimeIndex:   map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots: make(map[string]int),
		cfg:            cfg,
	}

	result, err := d.runTask(context.Background(), canonicalInboxTaskForTest(Task{
		ID:          "task-over-quota",
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-over-quota",
		AgentID:     agentID,
		Agent:       &AgentData{ID: agentID, Name: "test-agent"},
	}), "claude", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("runTask error = %v, want fail-closed TaskResult (nil error)", err)
	}
	if result.Status != "failed" || result.FailureReason != "agent_workspace_over_capacity" {
		t.Fatalf("result = %+v, want Status=failed FailureReason=agent_workspace_over_capacity", result)
	}
}

// TestRunTask_AllowsTurnWhenAgentWorkspaceUnderCapacity is the companion
// negative case: a workspace under an explicit positive cap must not be
// blocked by this gate.
func TestRunTask_AllowsTurnWhenAgentWorkspaceUnderCapacity(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	cfg := Config{
		WorkspacesRoot:           workspacesRoot,
		AgentWorkspaceQuotaBytes: 2 << 30, // explicit cap; tiny seed stays under it
		Agents: map[string]AgentEntry{
			"claude": {Path: filepath.Join(t.TempDir(), "unused-agent-binary")},
		},
	}

	const workspaceID = "ws-under-quota"
	const agentID = "agent-under-quota"
	agentRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		t.Fatalf("seed agent root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "notes.md"), []byte("tiny"), 0o644); err != nil {
		t.Fatalf("seed small file: %v", err)
	}

	d := &Daemon{
		client:         NewClient("http://unused.invalid"),
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:     make(map[string]*workspaceState),
		runtimeIndex:   map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots: make(map[string]int),
		cfg:            cfg,
	}

	// This workspace is under capacity, so runTask proceeds past the quota
	// gate into real agent execution against a nonexistent binary — which
	// eventually fails on its own via a much longer readiness/retry path.
	// A short ctx timeout forces that unrelated failure to surface fast;
	// the only thing under test here is that the quota gate did not fire.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, _ := d.runTask(ctx, canonicalInboxTaskForTest(Task{
		ID:          "task-under-quota",
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-under-quota",
		AgentID:     agentID,
		Agent:       &AgentData{ID: agentID, Name: "test-agent"},
	}), "claude", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if result.FailureReason == "agent_workspace_over_capacity" {
		t.Fatalf("result = %+v, quota gate must not fire when usage is far under the cap", result)
	}
}

// TestRunTask_AllowsTurnWhenAgentWorkspaceQuotaDisabled covers LRM-1047:
// default / explicit 0 means unlimited — a large workspace must not be refused.
func TestRunTask_AllowsTurnWhenAgentWorkspaceQuotaDisabled(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	cfg := Config{
		WorkspacesRoot:           workspacesRoot,
		AgentWorkspaceQuotaBytes: 0, // unlimited
		Agents: map[string]AgentEntry{
			"claude": {Path: filepath.Join(t.TempDir(), "unused-agent-binary")},
		},
	}

	const workspaceID = "ws-unlimited-quota"
	const agentID = "agent-unlimited-quota"
	agentRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		t.Fatalf("seed agent root: %v", err)
	}
	// Larger than the historical 2GiB default would have allowed… if we
	// actually wrote 2GiB+ here the test would be slow; instead seed a
	// few MiB which is already enough to prove we are not on a 10-byte
	// test cap, and pair with quota=0 (the production default).
	big := make([]byte, 4<<20) // 4 MiB
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(agentRoot, "fat.bin"), big, 0o644); err != nil {
		t.Fatalf("seed large file: %v", err)
	}

	d := &Daemon{
		client:         NewClient("http://unused.invalid"),
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		workspaces:     make(map[string]*workspaceState),
		runtimeIndex:   map[string]Runtime{"rt-1": {ID: "rt-1", Provider: "claude"}},
		activeEnvRoots: make(map[string]int),
		cfg:            cfg,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, _ := d.runTask(ctx, canonicalInboxTaskForTest(Task{
		ID:          "task-unlimited-quota",
		WorkspaceID: workspaceID,
		RuntimeID:   "rt-1",
		IssueID:     "issue-unlimited-quota",
		AgentID:     agentID,
		Agent:       &AgentData{ID: agentID, Name: "test-agent"},
	}), "claude", 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if result.FailureReason == "agent_workspace_over_capacity" {
		t.Fatalf("result = %+v, quota gate must not fire when AgentWorkspaceQuotaBytes=0 (unlimited)", result)
	}
}

// writeFileRequestResult sends req through handleWriteFileRequest and
// decodes the response frame it sends back.
func writeFileRequestResult(t *testing.T, d *Daemon, req protocol.WriteWorkdirFileRequestPayload) protocol.WriteWorkdirFileResponsePayload {
	t.Helper()
	writes := make(chan []byte, 1)
	d.handleWriteFileRequest(req, writes)
	var msg protocol.Message
	select {
	case frame := <-writes:
		if err := json.Unmarshal(frame, &msg); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
	default:
		t.Fatal("handleWriteFileRequest sent no response frame")
	}
	var resp protocol.WriteWorkdirFileResponsePayload
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("unmarshal response payload: %v", err)
	}
	return resp
}

// TestHandleWriteFileRequest_RefusesGrowingEditWhenAgentWorkspaceOverCapacity
// covers the secondary, lower-value enforcement point: the RPC that lets a
// human edit an existing file in the Workspace tab. It cannot create new
// files (writeWorkdirTextFile only edits files that already exist, under a
// 256KB cap — see its own Missing/TooLarge checks), but once the workspace
// is already over capacity, a write that would make the file (and so the
// workspace) BIGGER must still be refused.
func TestHandleWriteFileRequest_RefusesGrowingEditWhenAgentWorkspaceOverCapacity(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	cfg := Config{WorkspacesRoot: workspacesRoot, AgentWorkspaceQuotaBytes: 10}

	const workspaceID = "ws-rpc-quota-grow"
	const agentID = "agent-rpc-quota-grow"
	agentRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		t.Fatalf("seed agent root: %v", err)
	}
	existingFile := "existing.md"
	if err := os.WriteFile(filepath.Join(agentRoot, existingFile), []byte("already-over-cap"), 0o644); err != nil {
		t.Fatalf("seed oversized existing file: %v", err)
	}

	d := &Daemon{cfg: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resp := writeFileRequestResult(t, d, protocol.WriteWorkdirFileRequestPayload{
		RequestID: "req-grow",
		RelPath:   filepath.ToSlash(filepath.Join(workspaceID, ".multica", "agents", agentID)),
		FilePath:  existingFile,
		Content:   "this new content is longer than the sixteen-byte original file",
	})
	if resp.Error == "" {
		t.Fatalf("resp = %+v, want a non-empty Error refusing a write that grows the file while over capacity", resp)
	}
}

// TestHandleWriteFileRequest_AllowsShrinkingEditWhenAgentWorkspaceOverCapacity
// is the recovery-path regression test Alice's review caught missing: the
// turn-start gate blocks every turn for an over-capacity agent, including
// one that might otherwise clean up its own workspace, so an owner/admin
// editing a file SMALLER via this RPC is the one remaining way to shrink a
// single file back down (short of handleDeleteDirRequest nuking the whole
// directory). An earlier version of this check refused every write once
// over cap, which would have blocked this exact recovery action.
func TestHandleWriteFileRequest_AllowsShrinkingEditWhenAgentWorkspaceOverCapacity(t *testing.T) {
	t.Parallel()

	workspacesRoot := t.TempDir()
	cfg := Config{WorkspacesRoot: workspacesRoot, AgentWorkspaceQuotaBytes: 10}

	const workspaceID = "ws-rpc-quota-shrink"
	const agentID = "agent-rpc-quota-shrink"
	agentRoot := multicaAgentRoot(cfg, workspaceID, agentID)
	if err := os.MkdirAll(agentRoot, 0o755); err != nil {
		t.Fatalf("seed agent root: %v", err)
	}
	existingFile := "existing.md"
	if err := os.WriteFile(filepath.Join(agentRoot, existingFile), []byte("this file alone is already over the ten-byte cap"), 0o644); err != nil {
		t.Fatalf("seed oversized existing file: %v", err)
	}
	// A second, untouched file — large enough that even after shrinking
	// existingFile below, the workspace TOTAL remains well over cap. Per
	// Parker's own worked example (used=1000, quota=10, shrinking one file
	// from 900 to 800 still leaves the total at 900): the shrink must
	// still succeed even though the resulting total is still over
	// capacity — recovery is a multi-step process, not a single edit that
	// must land back under quota in one shot.
	if err := os.WriteFile(filepath.Join(agentRoot, "other.md"), []byte("this other file also keeps the total over cap even after the edit below"), 0o644); err != nil {
		t.Fatalf("seed second oversized file: %v", err)
	}

	d := &Daemon{cfg: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	resp := writeFileRequestResult(t, d, protocol.WriteWorkdirFileRequestPayload{
		RequestID: "req-shrink",
		RelPath:   filepath.ToSlash(filepath.Join(workspaceID, ".multica", "agents", agentID)),
		FilePath:  existingFile,
		Content:   "short",
	})
	if resp.Error != "" {
		t.Fatalf("resp = %+v, want no error — a write that shrinks the file must be allowed even while the workspace TOTAL remains over capacity afterward", resp)
	}
	if postEditTotal := dirSize(agentRoot); postEditTotal < cfg.AgentWorkspaceQuotaBytes {
		t.Fatalf("test setup bug: post-edit total %d dropped under the %d cap — this no longer exercises the still-over-cap case", postEditTotal, cfg.AgentWorkspaceQuotaBytes)
	}
}
