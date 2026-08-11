package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

func TestAgentAttachmentRegistryLoadsAndRoundTripsLegacyState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".daemon", reminderAgentStateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	want := reminderAgentState{
		Agents: []reminderAgentResidency{
			{AgentID: "agent-a", RuntimeID: "runtime-a", WorkspaceID: "workspace-a", PlacementGeneration: 4},
			{AgentID: "agent-b", RuntimeID: "runtime-b", WorkspaceID: "workspace-b", PlacementGeneration: 7},
		},
		PlacementHighWatermarks: map[string]int64{"agent-a": 4, "agent-b": 7, "agent-retired": 9},
		RuntimeLifecycleCursors: map[string]int64{"runtime-a": 11, "runtime-b": 13},
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	registry := newLocalAgentAttachmentRegistry(root, nil)
	if got := registry.agents["agent-a"]; got.RuntimeID != "runtime-a" || got.WorkspaceID != "workspace-a" || got.PlacementGeneration != 4 || got.Running != 0 {
		t.Fatalf("loaded agent-a = %+v", got)
	}
	if got := registry.agents["agent-b"]; got.RuntimeID != "runtime-b" || got.WorkspaceID != "workspace-b" || got.PlacementGeneration != 7 || got.Running != 0 {
		t.Fatalf("loaded agent-b = %+v", got)
	}
	if !reflect.DeepEqual(registry.placementHighWatermarks, want.PlacementHighWatermarks) {
		t.Fatalf("loaded tombstone generations = %v, want %v", registry.placementHighWatermarks, want.PlacementHighWatermarks)
	}
	if !reflect.DeepEqual(registry.runtimeLifecycleCursors, want.RuntimeLifecycleCursors) {
		t.Fatalf("loaded lifecycle cursors = %v, want %v", registry.runtimeLifecycleCursors, want.RuntimeLifecycleCursors)
	}

	roundTripRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip reminderAgentState
	if err := json.Unmarshal(roundTripRaw, &roundTrip); err != nil {
		t.Fatalf("decode round-trip state: %v", err)
	}
	if !reflect.DeepEqual(roundTrip, want) {
		t.Fatalf("round-trip state = %+v, want %+v", roundTrip, want)
	}
	assertAgentAttachmentStatePermissions(t, path)
}

func TestAgentAttachmentRegistryCorruptStateRecoversCurrentEmptyFormat(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".daemon", reminderAgentStateFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry := newLocalAgentAttachmentRegistry(root, nil)
	if len(registry.agents) != 0 || len(registry.placementHighWatermarks) != 0 || len(registry.runtimeLifecycleCursors) != 0 {
		t.Fatalf("corrupt state invented Attachment data: agents=%v generations=%v cursors=%v", registry.agents, registry.placementHighWatermarks, registry.runtimeLifecycleCursors)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var recovered reminderAgentState
	if err := json.Unmarshal(raw, &recovered); err != nil {
		t.Fatalf("corrupt state was not replaced with current format: %v", err)
	}
	assertAgentAttachmentStatePermissions(t, path)
}

func TestAgentAttachmentRegistryBootstrapsLocalAgentConfig(t *testing.T) {
	root := t.TempDir()
	workspaceID := uuid.NewString()
	agentID := uuid.NewString()
	config := cachedAgentCredential{AgentID: agentID, RuntimeID: "runtime-bootstrap", WorkspaceID: workspaceID}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(agentworkspace.Root(root, workspaceID, agentID), "runtime", "credentials", "current.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	registry := newLocalAgentAttachmentRegistry(root, nil)
	entry, found := registry.agents[agentID]
	if !found || entry.AgentID != agentID || entry.WorkspaceID != workspaceID || entry.RuntimeID != "runtime-bootstrap" || entry.Running != 0 {
		t.Fatalf("bootstrapped Attachment record = %+v found=%v", entry, found)
	}
	assertAgentAttachmentStatePermissions(t, registry.path)
}

func assertAgentAttachmentStatePermissions(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("Agent Attachment state permissions = %o, want 600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("Agent Attachment state directory permissions = %o, want 700", got)
	}
}
