package daemon

import (
	"os"
	"testing"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerAttachmentAttachPersistsInboxWithoutLaunchingProcess(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	payload := protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "attach-1",
	}
	receipt, err := runner.applyAttachmentAttach(payload)
	if err != nil || receipt != protocol.WorkspaceRunnerAgentAttachedPayload(payload) {
		t.Fatalf("apply Attachment attach receipt=%+v err=%v", receipt, err)
	}
	attachment, found := d.attachmentRegistry().Resolve(workspaceID, agentID)
	if !found || attachment.RuntimeID != runtimeID || attachment.AttachmentGeneration != 1 {
		t.Fatalf("durable Attachment=%+v found=%v", attachment, found)
	}
	if _, inboxRuntime, found := runner.inboxes.Resolve(agentID); !found || inboxRuntime != runtimeID {
		t.Fatalf("Attachment Inbox found=%v runtime=%q", found, inboxRuntime)
	}
	if _, err := os.Stat(agentworkspace.Root(root, workspaceID, agentID)); err != nil {
		t.Fatalf("Attachment AgentRoot was not created: %v", err)
	}
	if _, found := runner.processes.Snapshot(agentID); found {
		t.Fatal("Attachment attach started a managed process")
	}
	duplicate, err := runner.applyAttachmentAttach(payload)
	if err != nil || duplicate != receipt {
		t.Fatalf("duplicate Attachment attach receipt=%+v err=%v", duplicate, err)
	}
}

func TestWorkspaceRunnerAttachmentAttachRejectsWrongRuntimeBeforeInboxCreation(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-other"] = Runtime{ID: "runtime-other", WorkspaceID: "workspace-other"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	_, err := runner.applyAttachmentAttach(protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-other", AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "wrong-runtime",
	})
	if err == nil {
		t.Fatal("cross-Workspace Runtime Attachment was accepted")
	}
	if _, _, found := runner.inboxes.Resolve("agent-1"); found {
		t.Fatal("cross-Workspace Runtime Attachment opened an Inbox")
	}
}
