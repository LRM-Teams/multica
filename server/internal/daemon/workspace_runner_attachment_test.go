package daemon

import (
	"os"
	"reflect"
	"testing"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerAttachmentReplayUsesExactWorkspaceRuntimeSet(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-2"] = Runtime{ID: "runtime-2", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-other"] = Runtime{ID: "runtime-other", WorkspaceID: "workspace-other"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)
	if _, err := runner.applyAttachmentAttach(protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 3, CorrelationID: "attach-1",
	}); err != nil {
		t.Fatalf("seed Attachment replay cursor: %v", err)
	}

	runtimeSet := runner.attachmentRuntimeSet()
	request, err := runner.attachmentReplayRequest(runtimeSet)
	if err != nil {
		t.Fatalf("attachmentReplayRequest(): %v", err)
	}
	if want := map[string]int64{"runtime-1": 3, "runtime-2": 0}; !reflect.DeepEqual(request.RuntimeCursors, want) {
		t.Fatalf("Attachment replay request cursors = %v, want %v", request.RuntimeCursors, want)
	}

	end := protocol.WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: map[string]int64{"runtime-1": 5, "runtime-2": 7}}
	ack, err := runner.completeAttachmentReplay(runtimeSet, end)
	if err != nil {
		t.Fatalf("completeAttachmentReplay(): %v", err)
	}
	if !reflect.DeepEqual(ack.RuntimeCursors, end.RuntimeCursors) {
		t.Fatalf("Attachment replay ack cursors = %v, want %v", ack.RuntimeCursors, end.RuntimeCursors)
	}
	request, err = runner.attachmentReplayRequest(runtimeSet)
	if err != nil {
		t.Fatalf("attachmentReplayRequest() after completion: %v", err)
	}
	if !reflect.DeepEqual(request.RuntimeCursors, end.RuntimeCursors) {
		t.Fatalf("persisted Attachment replay cursors = %v, want %v", request.RuntimeCursors, end.RuntimeCursors)
	}
}

func TestWorkspaceRunnerAttachmentReplayRejectsIncompleteOrCrossWorkspaceEnd(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-2"] = Runtime{ID: "runtime-2", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-other"] = Runtime{ID: "runtime-other", WorkspaceID: "workspace-other"}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-1", nil)

	invalid := []protocol.WorkspaceRunnerAttachmentReplayEnd{
		{RuntimeCursors: map[string]int64{"runtime-1": 1}},
		{RuntimeCursors: map[string]int64{"runtime-1": 1, "runtime-other": 1}},
	}
	for _, end := range invalid {
		if _, err := runner.completeAttachmentReplay(runner.attachmentRuntimeSet(), end); err == nil {
			t.Fatalf("accepted invalid Attachment replay end: %+v", end)
		}
	}
	request, err := runner.attachmentReplayRequest(runner.attachmentRuntimeSet())
	if err != nil {
		t.Fatalf("attachmentReplayRequest(): %v", err)
	}
	if want := map[string]int64{"runtime-1": 0, "runtime-2": 0}; !reflect.DeepEqual(request.RuntimeCursors, want) {
		t.Fatalf("invalid replay end changed cursors = %v, want %v", request.RuntimeCursors, want)
	}
}

func TestWorkspaceRunnerAttachmentReplaySupportsWorkspaceWithoutRuntimes(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	runner, _ := attachTestWorkspaceRunner(t, d, "workspace-empty", nil)
	runtimeSet := runner.attachmentRuntimeSet()
	request, err := runner.attachmentReplayRequest(runtimeSet)
	if err != nil {
		t.Fatalf("attachmentReplayRequest(): %v", err)
	}
	if len(request.RuntimeCursors) != 0 {
		t.Fatalf("zero-Runtime replay request cursors = %v", request.RuntimeCursors)
	}
	ack, err := runner.completeAttachmentReplay(runtimeSet, protocol.WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: map[string]int64{}})
	if err != nil {
		t.Fatalf("complete zero-Runtime replay: %v", err)
	}
	if len(ack.RuntimeCursors) != 0 {
		t.Fatalf("zero-Runtime replay ack cursors = %v", ack.RuntimeCursors)
	}
}

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

func TestWorkspaceRunnerManagedStartRequiresMatchingAttachment(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	start := protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, StartDispatchID: "start-1"}
	if _, _, _, err := runner.startManagedAgent(start); err == nil {
		t.Fatal("unattached Agent start was accepted")
	}
	if _, found := runner.processes.Snapshot(agentID); found {
		t.Fatal("unattached Agent start created a managed launch")
	}
	if _, err := runner.applyAttachmentAttach(protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "attach-1",
	}); err != nil {
		t.Fatalf("attach Agent before start: %v", err)
	}
	ack, status, session, err := runner.startManagedAgent(start)
	if err != nil {
		t.Fatalf("attached Agent start: %v", err)
	}
	if ack.AgentID != agentID || ack.LaunchID == "" || status.LaunchID != ack.LaunchID || session.LaunchID != ack.LaunchID {
		t.Fatalf("managed start result ack=%+v status=%+v session=%+v", ack, status, session)
	}
}

func TestWorkspaceRunnerManagedStopRejectsStaleLaunchID(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	if _, err := runner.applyAttachmentAttach(protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "attach-1",
	}); err != nil {
		t.Fatalf("attach Agent before start: %v", err)
	}
	first, _, _, err := runner.startManagedAgent(protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, StartDispatchID: "start-1"})
	if err != nil {
		t.Fatalf("start initial managed Agent: %v", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: agentID, LaunchID: first.LaunchID}); err != nil {
		t.Fatalf("stop initial managed launch: %v", err)
	}
	replacement, _, _, err := runner.startManagedAgent(protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, StartDispatchID: "start-2"})
	if err != nil {
		t.Fatalf("start replacement managed Agent: %v", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: agentID, LaunchID: first.LaunchID}); err == nil {
		t.Fatal("stale stop accepted after replacement launch")
	}
	current, found := runner.processes.Snapshot(agentID)
	if !found || current.LaunchID != replacement.LaunchID {
		t.Fatalf("stale stop changed replacement launch: found=%v current=%+v replacement=%+v", found, current, replacement)
	}
}

func TestWorkspaceRunnerAttachmentDetachTearsDownOnlyMatchingVolatileState(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	attach := protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1, CorrelationID: "attach-1",
	}
	if _, err := runner.applyAttachmentAttach(attach); err != nil {
		t.Fatalf("apply Attachment attach: %v", err)
	}
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID, StartDispatchID: "start-1", ReadinessPolicy: agentRuntimeReadinessFirstEvent}); err != nil {
		t.Fatalf("start managed Agent for detach: %v", err)
	}
	detach := protocol.WorkspaceRunnerAgentDetachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 2, CorrelationID: "detach-1",
	}
	receipt, err := runner.applyAttachmentDetach(detach)
	if err != nil || receipt != protocol.WorkspaceRunnerAgentDetachedPayload(detach) {
		t.Fatalf("apply Attachment detach receipt=%+v err=%v", receipt, err)
	}
	if _, found := d.attachmentRegistry().Resolve(workspaceID, agentID); found {
		t.Fatal("detach retained durable Attachment")
	}
	if _, _, found := runner.inboxes.Resolve(agentID); found {
		t.Fatal("detach retained in-memory Inbox")
	}
	if _, found := runner.processes.Snapshot(agentID); found {
		t.Fatal("detach retained managed launch")
	}
	if _, err := os.Stat(agentworkspace.Root(root, workspaceID, agentID)); err != nil {
		t.Fatalf("detach removed durable AgentRoot: %v", err)
	}
	if _, err := runner.applyAttachmentDetach(detach); err != nil {
		t.Fatalf("duplicate detach did not converge: %v", err)
	}
	reattach := attach
	reattach.AttachmentGeneration, reattach.LifecycleSeq, reattach.CorrelationID = 2, 3, "attach-2"
	if _, err := runner.applyAttachmentAttach(reattach); err != nil {
		t.Fatalf("reattach did not recover preserved Inbox state: %v", err)
	}
	stale := detach
	stale.LifecycleSeq, stale.CorrelationID = 4, "stale-detach-1"
	if _, err := runner.applyAttachmentDetach(stale); err != nil {
		t.Fatalf("stale detach did not converge harmlessly: %v", err)
	}
	attachment, found := d.attachmentRegistry().Resolve(workspaceID, agentID)
	if !found || attachment.AttachmentGeneration != 2 || attachment.RuntimeID != runtimeID {
		t.Fatalf("stale detach removed newer Attachment: %+v found=%v", attachment, found)
	}
	if _, inboxRuntime, found := runner.inboxes.Resolve(agentID); !found || inboxRuntime != runtimeID {
		t.Fatalf("reattach did not recover Inbox: found=%v runtime=%q", found, inboxRuntime)
	}
}
