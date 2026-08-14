package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func newTestInboxRegistry(t *testing.T, workspaceID string, ownsRuntime func(string) bool) *InboxRegistry {
	t.Helper()
	registry, err := newInboxRegistry(workspaceID, inboxRegistryDependencies{
		ownsRuntime: ownsRuntime,
		open: func(key InboxKey, runtimeID string) (*MessageCoordinator, error) {
			root := filepath.Join(t.TempDir(), key.WorkspaceID, key.AgentID, runtimeID)
			if err := ensureMulticaAgentRoot(root); err != nil {
				return nil, err
			}
			return NewMessageCoordinator(key, root, func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestInboxRegistryScopesSameAgentIdentityByWorkspace(t *testing.T) {
	first := newTestInboxRegistry(t, "workspace-1", func(runtimeID string) bool { return runtimeID == "runtime-1" })
	second := newTestInboxRegistry(t, "workspace-2", func(runtimeID string) bool { return runtimeID == "runtime-2" })
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	if created, err := first.AcceptStart("agent-1", "runtime-1"); err != nil || !created {
		t.Fatalf("open first Inbox: created=%v err=%v", created, err)
	}
	if created, err := second.AcceptStart("agent-1", "runtime-2"); err != nil || !created {
		t.Fatalf("open second Inbox: created=%v err=%v", created, err)
	}
	firstCoordinator, firstRuntime, firstOK := first.Resolve("agent-1")
	secondCoordinator, secondRuntime, secondOK := second.Resolve("agent-1")
	if !firstOK || !secondOK || firstCoordinator == secondCoordinator {
		t.Fatalf("Workspace-scoped resolution first=%v second=%v", firstOK, secondOK)
	}
	if firstRuntime != "runtime-1" || secondRuntime != "runtime-2" {
		t.Fatalf("runtime scopes = %q/%q", firstRuntime, secondRuntime)
	}
}

func TestInboxRegistryRejectsRuntimeOutsideWorkspace(t *testing.T) {
	registry := newTestInboxRegistry(t, "workspace-1", func(runtimeID string) bool { return runtimeID == "runtime-1" })
	t.Cleanup(registry.Close)
	if created, err := registry.AcceptStart("agent-1", "runtime-2"); err == nil || created {
		t.Fatalf("foreign Runtime opened Inbox: created=%v err=%v", created, err)
	}
	if _, _, ok := registry.Resolve("agent-1"); ok {
		t.Fatal("foreign Runtime Inbox became resolvable")
	}
}

func TestInboxRegistryAcceptedStartReplacesOnlyMatchingAgent(t *testing.T) {
	registry := newTestInboxRegistry(t, "workspace-1", func(string) bool { return true })
	t.Cleanup(registry.Close)
	if _, err := registry.AcceptStart("agent-1", "runtime-1"); err != nil {
		t.Fatal(err)
	}
	previous, _, _ := registry.Resolve("agent-1")
	registry.Remove("agent-1", "runtime-other")
	if current, runtimeID, ok := registry.Resolve("agent-1"); !ok || current != previous || runtimeID != "runtime-1" {
		t.Fatal("non-matching Runtime removed the Inbox")
	}
	if created, err := registry.AcceptStart("agent-1", "runtime-2"); err != nil || !created {
		t.Fatalf("replace moved Inbox: created=%v err=%v", created, err)
	}
	current, runtimeID, ok := registry.Resolve("agent-1")
	if !ok || current == previous || runtimeID != "runtime-2" {
		t.Fatalf("moved Inbox current=%p previous=%p runtime=%q", current, previous, runtimeID)
	}
}
