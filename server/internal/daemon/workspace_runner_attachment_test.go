package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/pkg/agent"
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
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 3,
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

func TestWorkspaceRunnerAttachmentAttachPersistsOwnershipWithoutLaunchingLifecycle(t *testing.T) {
	root := t.TempDir()
	d := New(Config{WorkspacesRoot: root}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	payload := protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	receipt, err := runner.applyAttachmentAttach(payload)
	if err != nil || receipt != protocol.WorkspaceRunnerAgentAttachedPayload(payload) {
		t.Fatalf("apply Attachment attach receipt=%+v err=%v", receipt, err)
	}
	attachment, found := d.attachmentRegistry().Resolve(workspaceID, agentID)
	if !found || attachment.RuntimeID != runtimeID || attachment.AttachmentGeneration != 1 {
		t.Fatalf("durable Attachment=%+v found=%v", attachment, found)
	}
	if _, _, found := runner.inboxes.Resolve(agentID); found {
		t.Fatal("Attachment created an Inbox before agent:start")
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
		AgentID: "agent-1", RuntimeID: "runtime-other", AttachmentGeneration: 1, LifecycleSeq: 1,
	})
	if err == nil {
		t.Fatal("cross-Workspace Runtime Attachment was accepted")
	}
	if _, _, found := runner.inboxes.Resolve("agent-1"); found {
		t.Fatal("cross-Workspace Runtime Attachment opened an Inbox")
	}
}

func TestWorkspaceRunnerManagedStartCreatesInboxWithoutAttachment(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	start := protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "start-1", StartDispatchID: "start-1" + "-dispatch"}
	ack, status, session, err := runner.startManagedAgent(context.Background(), start)
	if err != nil {
		t.Fatalf("server-authorized Agent start: %v", err)
	}
	if ack.AgentID != agentID || ack.LaunchID == "" || status.LaunchID != ack.LaunchID || session.LaunchID != ack.LaunchID {
		t.Fatalf("managed start result ack=%+v status=%+v session=%+v", ack, status, session)
	}
}

func TestWorkspaceRunnerProviderSpawnFailureReportsInactiveAndOffline(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-cursor", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error {
		return fmt.Errorf("spawn cursor: executable unavailable")
	}
	var activities []protocol.AgentActivityPayload
	runner.activity.AttachTransport(func(payload protocol.AgentActivityPayload) { activities = append(activities, payload) })
	start := protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "dispatch-1"}
	_, status, _, err := runner.startManagedAgent(context.Background(), start)
	if err == nil {
		t.Fatal("provider spawn failure was accepted")
	}
	if status.Status != protocol.AgentStatusInactive {
		t.Fatalf("spawn failure status = %+v, want inactive", status)
	}
	if len(activities) != 1 || activities[0].Snapshot.ActivityKind != protocol.ActivityKindOffline {
		t.Fatalf("spawn failure Activity = %+v, want Offline", activities)
	}
}

func TestWorkspaceRunnerManagedStartEmitsStartingActivity(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	var activities []protocol.AgentActivityPayload
	runner.activity.AttachTransport(func(payload protocol.AgentActivityPayload) { activities = append(activities, payload) })
	start := protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "dispatch-1"}
	if _, _, _, err := runner.startManagedAgent(context.Background(), start); err != nil {
		t.Fatalf("managed start: %v", err)
	}
	if len(activities) == 0 {
		t.Fatal("managed start produced no Activity")
	}
	got := activities[len(activities)-1]
	if got.Snapshot.ActivityKind != protocol.ActivityKindWorking || got.Snapshot.DetailKind != "starting" {
		t.Fatalf("spawn Activity = kind=%q detail=%q, want working/starting", got.Snapshot.ActivityKind, got.Snapshot.DetailKind)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("starting entries = %d, want 1", len(got.Entries))
	}
	var body protocol.AgentActivityNarrativeBody
	if err := json.Unmarshal(got.Entries[0].Body, &body); err != nil {
		t.Fatalf("decode starting narrative: %v", err)
	}
	if body.Text != "Starting…" {
		t.Fatalf("starting text = %q, want %q", body.Text, "Starting…")
	}
}

func TestWorkspaceRunnerManagedStartWaitsForCapacityBeforeProvider(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir(), MaxAgentProcesses: 1}, nil)
	workspaceID := "workspace-1"
	for _, runtimeID := range []string{"runtime-1", "runtime-2"} {
		d.mu.Lock()
		d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
		d.mu.Unlock()
	}
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	providerStarted := make(chan string, 2)
	runner.ensureResidentRuntime = func(_ context.Context, agentID, _ string, _ *agent.PiRunIdentity) error {
		providerStarted <- agentID
		return nil
	}
	first := protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1", StartDispatchID: "launch-1" + "-dispatch"}
	if _, _, _, err := runner.startManagedAgent(context.Background(), first); err != nil {
		t.Fatalf("start first Agent: %v", err)
	}
	if got := <-providerStarted; got != first.AgentID {
		t.Fatalf("first provider Agent = %q", got)
	}

	second := protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-2", RuntimeID: "runtime-2", LaunchID: "launch-2", StartDispatchID: "launch-2" + "-dispatch"}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := runner.startManagedAgent(context.Background(), second)
		done <- err
	}()
	select {
	case got := <-providerStarted:
		t.Fatalf("queued provider started before admission: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: first.AgentID, LaunchID: first.LaunchID}); err != nil {
		t.Fatalf("release first Agent capacity: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("promoted Agent start: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("promoted Agent did not start")
	}
	if got := <-providerStarted; got != second.AgentID {
		t.Fatalf("promoted provider Agent = %q", got)
	}
}

func TestWorkspaceRunnerManagedStopRejectsStaleLaunchID(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	if _, err := runner.applyAttachmentAttach(protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatalf("attach Agent before start: %v", err)
	}
	first, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "start-1", StartDispatchID: "start-1" + "-dispatch"})
	if err != nil {
		t.Fatalf("start initial managed Agent: %v", err)
	}
	if err := runner.processes.Stop(agentProcessCallback{AgentID: agentID, LaunchID: first.LaunchID}); err != nil {
		t.Fatalf("stop initial managed launch: %v", err)
	}
	replacement, _, _, err := runner.startManagedAgent(context.Background(), protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "start-2", StartDispatchID: "start-2" + "-dispatch"})
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

func TestWorkspaceRunnerManagedStartRejectsRuntimeMoveWithoutStopAndKeepsInbox(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, agentID := "workspace-1", "agent-1"
	for _, runtimeID := range []string{"runtime-old", "runtime-new"} {
		d.mu.Lock()
		d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
		d.mu.Unlock()
	}
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	old := protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: "runtime-old", LaunchID: "launch-old", StartDispatchID: "launch-old" + "-dispatch"}
	if _, _, _, err := runner.startManagedAgent(context.Background(), old); err != nil {
		t.Fatalf("start old Runtime: %v", err)
	}
	if _, err := runner.registerManagedAgentStart(protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: "runtime-new", LaunchID: "launch-new", StartDispatchID: "launch-new" + "-dispatch"}); err == nil {
		t.Fatal("runtime move without stop was accepted")
	}
	_, runtimeID, ok := runner.inboxes.Resolve(agentID)
	if !ok || runtimeID != old.RuntimeID {
		t.Fatalf("rejected runtime move changed Inbox: runtime=%q exists=%v", runtimeID, ok)
	}
}

func TestWorkspaceRunnerManagedStopClosesProviderAndInbox(t *testing.T) {
	d := New(Config{WorkspacesRoot: t.TempDir()}, nil)
	workspaceID, runtimeID, agentID := "workspace-1", "runtime-1", "agent-1"
	d.mu.Lock()
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.mu.Unlock()
	runner, _ := attachTestWorkspaceRunner(t, d, workspaceID, nil)
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	backend := &canonicalRuntimeTestBackend{}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	start := protocol.WorkspaceRunnerAgentStartPayload{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "launch-1", StartDispatchID: "launch-1" + "-dispatch"}
	if _, _, _, err := runner.startManagedAgent(context.Background(), start); err != nil {
		t.Fatalf("start managed Agent: %v", err)
	}
	status, err := runner.stopManagedAgent(protocol.WorkspaceRunnerAgentStopPayload{AgentID: agentID, LaunchID: start.LaunchID})
	if err != nil {
		t.Fatalf("stop managed Agent: %v", err)
	}
	if status.Status != protocol.AgentStatusInactive || backend.forceKillCount() != 0 {
		t.Fatalf("stop status=%+v force_kills=%d", status, backend.forceKillCount())
	}
	if _, _, found := runner.inboxes.Resolve(agentID); found {
		t.Fatal("managed stop retained Agent Inbox")
	}
	if _, found := runner.processes.Snapshot(agentID); found {
		t.Fatal("managed stop retained APM launch")
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
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	if _, err := runner.applyAttachmentAttach(attach); err != nil {
		t.Fatalf("apply Attachment attach: %v", err)
	}
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID, LaunchID: "start-1", StartDispatchID: "start-1" + "-dispatch", ReadinessPolicy: agentRuntimeReadinessFirstEvent}); err != nil {
		t.Fatalf("start managed Agent for detach: %v", err)
	}
	detach := protocol.WorkspaceRunnerAgentDetachPayload{
		AgentID: agentID, RuntimeID: runtimeID, AttachmentGeneration: 1, LifecycleSeq: 2,
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
	reattach.AttachmentGeneration, reattach.LifecycleSeq = 2, 3
	if _, err := runner.applyAttachmentAttach(reattach); err != nil {
		t.Fatalf("reattach did not recover preserved Inbox state: %v", err)
	}
	stale := detach
	stale.LifecycleSeq = 4
	if _, err := runner.applyAttachmentDetach(stale); err != nil {
		t.Fatalf("stale detach did not converge harmlessly: %v", err)
	}
	attachment, found := d.attachmentRegistry().Resolve(workspaceID, agentID)
	if !found || attachment.AttachmentGeneration != 2 || attachment.RuntimeID != runtimeID {
		t.Fatalf("stale detach removed newer Attachment: %+v found=%v", attachment, found)
	}
	if _, _, found := runner.inboxes.Resolve(agentID); found {
		t.Fatal("reattach created an Inbox before agent:start")
	}
}
