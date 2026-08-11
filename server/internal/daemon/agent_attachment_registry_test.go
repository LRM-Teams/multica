package daemon

import (
	"errors"
	"reflect"
	"testing"
)

func TestAgentAttachmentRegistryGenerationMatrix(t *testing.T) {
	registry := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(t.TempDir(), nil))
	workspaceID := "workspace-a"

	assertApply := func(want AgentAttachmentChangeKind, event AgentAttachmentEvent) {
		t.Helper()
		change, err := registry.Apply(workspaceID, event)
		if err != nil {
			t.Fatalf("Apply(%+v): %v", event, err)
		}
		if change.Kind != want {
			t.Fatalf("Apply(%+v) kind = %q, want %q (change=%+v)", event, change.Kind, want, change)
		}
	}
	event := func(kind AgentAttachmentEventKind, runtimeID string, generation, seq int64) AgentAttachmentEvent {
		return AgentAttachmentEvent{
			Kind: kind, AgentID: "agent-a", RuntimeID: runtimeID,
			AttachmentGeneration: AttachmentGeneration(generation), LifecycleSeq: AttachmentLifecycleSequence(seq),
		}
	}

	assertApply(AgentAttachmentAttached, event(AgentAttachmentEventAttach, "runtime-a", 1, 1))
	assertApply(AgentAttachmentMoved, event(AgentAttachmentEventAttach, "runtime-b", 2, 1))
	assertApply(AgentAttachmentUnchanged, event(AgentAttachmentEventAttach, "runtime-b", 2, 2))
	assertApply(AgentAttachmentUnchanged, event(AgentAttachmentEventAttach, "runtime-c", 2, 1))
	assertApply(AgentAttachmentUnchanged, event(AgentAttachmentEventDetach, "runtime-a", 1, 2))
	assertApply(AgentAttachmentMoved, event(AgentAttachmentEventAttach, "runtime-a", 3, 3))
	assertApply(AgentAttachmentUnchanged, event(AgentAttachmentEventDetach, "runtime-b", 2, 3))

	attachment, found := registry.Resolve(workspaceID, "agent-a")
	if !found || attachment.RuntimeID != "runtime-a" || attachment.AttachmentGeneration != 3 {
		t.Fatalf("final Attachment = %+v found=%v", attachment, found)
	}
	if _, found := registry.Resolve("workspace-b", "agent-a"); found {
		t.Fatal("Attachment resolved through the wrong Workspace")
	}
}

func TestAgentAttachmentRegistryTombstoneSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	registry := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(root, nil))
	workspaceID := "workspace-a"
	apply := func(kind AgentAttachmentEventKind, generation, seq int64) AgentAttachmentChange {
		t.Helper()
		change, err := registry.Apply(workspaceID, AgentAttachmentEvent{
			Kind: kind, AgentID: "agent-a", RuntimeID: "runtime-a",
			AttachmentGeneration: AttachmentGeneration(generation), LifecycleSeq: AttachmentLifecycleSequence(seq),
		})
		if err != nil {
			t.Fatal(err)
		}
		return change
	}

	if change := apply(AgentAttachmentEventAttach, 4, 1); change.Kind != AgentAttachmentAttached {
		t.Fatalf("initial change = %+v", change)
	}
	if change := apply(AgentAttachmentEventDetach, 5, 2); change.Kind != AgentAttachmentDetached {
		t.Fatalf("detach change = %+v", change)
	}

	registry = newLocalAgentAttachmentRegistry(root, nil)
	if _, found := registry.Resolve(workspaceID, "agent-a"); found {
		t.Fatal("detached Agent resurrected after restart")
	}
	if change := apply(AgentAttachmentEventAttach, 4, 3); change.Kind != AgentAttachmentUnchanged {
		t.Fatalf("stale attach change = %+v", change)
	}
	if _, found := registry.Resolve(workspaceID, "agent-a"); found {
		t.Fatal("stale attach crossed the durable tombstone")
	}
	if change := apply(AgentAttachmentEventAttach, 6, 4); change.Kind != AgentAttachmentAttached {
		t.Fatalf("new attach change = %+v", change)
	}
}

func TestAgentAttachmentRegistryAcceptsSameGenerationMoveAfterDetach(t *testing.T) {
	registry := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(t.TempDir(), nil))
	workspaceID := "workspace-a"
	apply := func(kind AgentAttachmentEventKind, runtimeID string, generation, seq int64) AgentAttachmentChange {
		t.Helper()
		change, err := registry.Apply(workspaceID, AgentAttachmentEvent{
			Kind: kind, AgentID: "agent-a", RuntimeID: runtimeID,
			AttachmentGeneration: AttachmentGeneration(generation), LifecycleSeq: AttachmentLifecycleSequence(seq),
		})
		if err != nil {
			t.Fatal(err)
		}
		return change
	}
	if change := apply(AgentAttachmentEventAttach, "runtime-a", 1, 1); change.Kind != AgentAttachmentAttached {
		t.Fatalf("initial attach = %+v", change)
	}
	if change := apply(AgentAttachmentEventDetach, "runtime-a", 2, 2); change.Kind != AgentAttachmentDetached {
		t.Fatalf("move detach = %+v", change)
	}
	if change := apply(AgentAttachmentEventAttach, "runtime-b", 2, 1); change.Kind != AgentAttachmentAttached {
		t.Fatalf("move attach = %+v", change)
	}
	attachment, found := registry.Resolve(workspaceID, "agent-a")
	if !found || attachment.RuntimeID != "runtime-b" || attachment.AttachmentGeneration != 2 {
		t.Fatalf("moved Attachment = %+v found=%v", attachment, found)
	}
}

func TestAgentAttachmentRegistryRejectsCrossWorkspaceApply(t *testing.T) {
	registry := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(t.TempDir(), nil))
	event := AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-a", RuntimeID: "runtime-a",
		AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	if _, err := registry.Apply("workspace-a", event); err != nil {
		t.Fatal(err)
	}
	event.RuntimeID = "runtime-b"
	event.AttachmentGeneration = 2
	if _, err := registry.Apply("workspace-b", event); err == nil {
		t.Fatal("Apply accepted an Agent already attached through another Workspace")
	}
	attachment, found := registry.Resolve("workspace-a", "agent-a")
	if !found || attachment.RuntimeID != "runtime-a" || attachment.AttachmentGeneration != 1 {
		t.Fatalf("cross-Workspace Apply changed Attachment = %+v found=%v", attachment, found)
	}
}

func TestTaskObservationCannotRewriteGenerationBearingAttachment(t *testing.T) {
	registry := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	if _, err := registry.Apply("workspace-a", AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-a", RuntimeID: "runtime-a",
		AttachmentGeneration: 4, LifecycleSeq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if created, observed := registry.observeTaskStarted("agent-a", "runtime-b", "workspace-b"); created || observed {
		t.Fatal("task observation reported a generation-bearing Attachment as new")
	}
	attachment, found := registry.Resolve("workspace-a", "agent-a")
	if !found || attachment.RuntimeID != "runtime-a" || attachment.AttachmentGeneration != 4 {
		t.Fatalf("task observation rewrote Attachment = %+v found=%v", attachment, found)
	}
	if _, found := registry.Resolve("workspace-b", "agent-a"); found {
		t.Fatal("task observation moved Attachment across Workspace")
	}
}

func TestAgentAttachmentRegistryReconcilesOnlyExplicitWorkspaceRuntimeSet(t *testing.T) {
	registry := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(t.TempDir(), nil))
	attach := func(workspaceID, agentID, runtimeID string) {
		t.Helper()
		if _, err := registry.Apply(workspaceID, AgentAttachmentEvent{
			Kind: AgentAttachmentEventAttach, AgentID: agentID, RuntimeID: runtimeID,
			AttachmentGeneration: 1, LifecycleSeq: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	attach("workspace-a", "agent-old", "runtime-old")
	attach("workspace-a", "agent-keep", "runtime-keep")
	attach("workspace-b", "agent-other", "runtime-other")

	changes, err := registry.Reconcile(AgentAttachmentRuntimeSet{
		WorkspaceID: "workspace-a", RuntimeIDs: []string{"runtime-keep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Kind != AgentAttachmentDetached || changes[0].Previous.AgentID != "agent-old" {
		t.Fatalf("reconcile changes = %+v", changes)
	}
	if _, found := registry.Resolve("workspace-a", "agent-old"); found {
		t.Fatal("disallowed Runtime Attachment remains")
	}
	if attachment, found := registry.Resolve("workspace-a", "agent-keep"); !found || attachment.RuntimeID != "runtime-keep" {
		t.Fatalf("allowed Attachment = %+v found=%v", attachment, found)
	}
	if attachment, found := registry.Resolve("workspace-b", "agent-other"); !found || attachment.RuntimeID != "runtime-other" {
		t.Fatalf("other Workspace Attachment = %+v found=%v", attachment, found)
	}
}

func TestAgentAttachmentRegistryRecoveryCursorIsScopedMonotonicAndDurable(t *testing.T) {
	root := t.TempDir()
	scope := AgentAttachmentRuntimeSet{WorkspaceID: "workspace-a", RuntimeIDs: []string{"runtime-a", "runtime-b"}}
	registry := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(root, nil))

	if err := registry.AdvanceRecovery(scope, []AgentAttachmentRecoveryCursor{
		{RuntimeID: "runtime-a", LifecycleSeq: 7},
		{RuntimeID: "runtime-b", LifecycleSeq: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.AdvanceRecovery(scope, []AgentAttachmentRecoveryCursor{
		{RuntimeID: "runtime-a", LifecycleSeq: 6},
		{RuntimeID: "runtime-b", LifecycleSeq: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.AdvanceRecovery(scope, []AgentAttachmentRecoveryCursor{
		{RuntimeID: "runtime-outside", LifecycleSeq: 9},
	}); err == nil {
		t.Fatal("AdvanceRecovery accepted a Runtime outside the explicit set")
	}

	registry = newLocalAgentAttachmentRegistry(root, nil)
	state, err := registry.RecoveryState(scope)
	if err != nil {
		t.Fatal(err)
	}
	want := AgentAttachmentRecoveryState{
		WorkspaceID: "workspace-a",
		Cursors: []AgentAttachmentRecoveryCursor{
			{RuntimeID: "runtime-a", LifecycleSeq: 7},
			{RuntimeID: "runtime-b", LifecycleSeq: 3},
		},
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("RecoveryState() = %+v, want %+v", state, want)
	}
}

func TestAgentAttachmentRegistryApplyRollsBackStateAndCursorTogether(t *testing.T) {
	root := t.TempDir()
	concrete := newLocalAgentAttachmentRegistry(root, nil)
	concrete.writeState = func(string, []byte) error { return errors.New("injected Attachment state failure") }
	registry := AgentAttachmentRegistry(concrete)
	event := AgentAttachmentEvent{
		Kind: AgentAttachmentEventAttach, AgentID: "agent-a", RuntimeID: "runtime-a",
		AttachmentGeneration: 1, LifecycleSeq: 5,
	}
	if change, err := registry.Apply("workspace-a", event); err == nil || change.Kind != AgentAttachmentUnchanged {
		t.Fatalf("failed Apply() = change %+v err %v", change, err)
	}
	if _, found := registry.Resolve("workspace-a", "agent-a"); found {
		t.Fatal("failed Apply leaked in-memory Attachment")
	}
	state, err := registry.RecoveryState(AgentAttachmentRuntimeSet{WorkspaceID: "workspace-a", RuntimeIDs: []string{"runtime-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cursors) != 1 || state.Cursors[0].LifecycleSeq != 0 {
		t.Fatalf("failed Apply leaked cursor: %+v", state)
	}
	reloaded := AgentAttachmentRegistry(newLocalAgentAttachmentRegistry(root, nil))
	if _, found := reloaded.Resolve("workspace-a", "agent-a"); found {
		t.Fatal("failed Apply leaked durable Attachment")
	}
	state, err = reloaded.RecoveryState(AgentAttachmentRuntimeSet{WorkspaceID: "workspace-a", RuntimeIDs: []string{"runtime-a"}})
	if err != nil || len(state.Cursors) != 1 || state.Cursors[0].LifecycleSeq != 0 {
		t.Fatalf("reloaded failed Apply cursor = %+v err=%v", state, err)
	}
}
