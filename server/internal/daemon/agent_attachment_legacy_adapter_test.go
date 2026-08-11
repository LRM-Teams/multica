package daemon

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestLegacyAgentAttachmentAdapterInheritsRegistryFencing(t *testing.T) {
	registry := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	adapter := legacyAgentAttachmentAdapter{registry: registry}

	result, err := adapter.ApplyStart(protocol.DaemonAgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-a", WorkspaceID: "workspace-a",
		PlacementGeneration: 1,
	})
	if err != nil || !result.accepted || result.change.Kind != AgentAttachmentAttached {
		t.Fatalf("live start = %+v err=%v", result, err)
	}
	result, err = adapter.ApplyStart(protocol.DaemonAgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-a", WorkspaceID: "workspace-a",
		PlacementGeneration: 1, LifecycleSeq: 1, Replay: true,
	})
	if err != nil || !result.accepted || result.change.Kind != AgentAttachmentUnchanged {
		t.Fatalf("duplicate replay start = %+v err=%v", result, err)
	}
	result, err = adapter.ApplyStart(protocol.DaemonAgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-a", WorkspaceID: "workspace-a",
		PlacementGeneration: 2, LifecycleSeq: 1, Replay: true,
	})
	if err != nil || result.accepted || result.change.Kind != AgentAttachmentUnchanged {
		t.Fatalf("conflicting duplicate replay start = %+v err=%v", result, err)
	}
	result, err = adapter.ApplyStart(protocol.DaemonAgentStartPayload{
		AgentID: "agent-a", RuntimeID: "runtime-b", WorkspaceID: "workspace-a",
		PlacementGeneration: 2, LifecycleSeq: 1, Replay: true,
	})
	if err != nil || !result.accepted || result.change.Kind != AgentAttachmentMoved {
		t.Fatalf("replay move = %+v err=%v", result, err)
	}
	result, err = adapter.ApplyStop(protocol.DaemonAgentStopPayload{
		AgentID: "agent-a", RuntimeID: "runtime-a",
		PlacementGeneration: 1, LifecycleSeq: 2, Replay: true,
	})
	if err != nil || result.accepted || result.change.Kind != AgentAttachmentUnchanged {
		t.Fatalf("stale replay stop = %+v err=%v", result, err)
	}

	attachment, found := registry.Resolve("workspace-a", "agent-a")
	if !found || attachment.RuntimeID != "runtime-b" || attachment.AttachmentGeneration != 2 {
		t.Fatalf("final Attachment = %+v found=%v", attachment, found)
	}
	state, err := registry.RecoveryState(AgentAttachmentRuntimeSet{
		WorkspaceID: "workspace-a", RuntimeIDs: []string{"runtime-a", "runtime-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCursors := []AgentAttachmentRecoveryCursor{
		{RuntimeID: "runtime-a", LifecycleSeq: 2},
		{RuntimeID: "runtime-b", LifecycleSeq: 1},
	}
	if !reflect.DeepEqual(state.Cursors, wantCursors) {
		t.Fatalf("legacy replay cursors = %+v, want %+v", state.Cursors, wantCursors)
	}
}

func TestLegacyAgentAttachmentAdapterLiveAndReplayConverge(t *testing.T) {
	apply := func(t *testing.T, lifecycleSeq int64, replay bool) AgentAttachment {
		t.Helper()
		registry := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
		adapter := legacyAgentAttachmentAdapter{registry: registry}
		result, err := adapter.ApplyStart(protocol.DaemonAgentStartPayload{
			AgentID: "agent-a", RuntimeID: "runtime-a", WorkspaceID: "workspace-a",
			PlacementGeneration: 4, LifecycleSeq: lifecycleSeq, Replay: replay,
		})
		if err != nil || !result.accepted || result.change.Kind != AgentAttachmentAttached {
			t.Fatalf("ApplyStart() = %+v err=%v", result, err)
		}
		attachment, found := registry.Resolve("workspace-a", "agent-a")
		if !found {
			t.Fatal("Attachment not resolved")
		}
		return attachment
	}
	live := apply(t, 0, false)
	replay := apply(t, 9, true)
	if !reflect.DeepEqual(live, replay) {
		t.Fatalf("live Attachment = %+v, replay Attachment = %+v", live, replay)
	}
}

func TestWakeProductionDoesNotMutateLegacyPlacementFacade(t *testing.T) {
	for _, path := range []string{"wakeup.go", "daemon.go"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"reminderAgents.applyStart(",
			"reminderAgents.applyStop(",
			"reminderAgents.advanceLifecycleCursors(",
			"reminderAgents.agents",
			"placementHighWatermarks",
			"runtimeLifecycleCursors",
		} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("%s directly calls legacy placement facade %q", path, forbidden)
			}
		}
	}
	raw, err := os.ReadFile("workspace_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, notYetActive := range []string{"case protocol.EventAgentAttach", "case protocol.EventAgentDetach"} {
		if strings.Contains(string(raw), notYetActive) {
			t.Fatalf("Workspace Runner Attachment event activated before its cutover ticket: %q", notYetActive)
		}
	}
}
