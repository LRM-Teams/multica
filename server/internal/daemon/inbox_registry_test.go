package daemon

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type testInboxAttachmentResolver struct {
	mu          sync.Mutex
	attachments map[InboxKey]AgentAttachment
}

func (resolver *testInboxAttachmentResolver) Resolve(workspaceID, agentID string) (AgentAttachment, bool) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	attachment, ok := resolver.attachments[InboxKey{WorkspaceID: workspaceID, AgentID: agentID}]
	return attachment, ok
}

func (resolver *testInboxAttachmentResolver) set(attachment AgentAttachment) {
	resolver.mu.Lock()
	resolver.attachments[InboxKey{WorkspaceID: attachment.WorkspaceID, AgentID: attachment.AgentID}] = attachment
	resolver.mu.Unlock()
}

func newTestInboxRegistry(t *testing.T, workspaceID string, resolver inboxAttachmentResolver) *InboxRegistry {
	t.Helper()
	registry, err := newInboxRegistry(workspaceID, inboxRegistryDependencies{
		attachments: resolver,
		ownsRuntime: func(string) bool { return true },
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
	resolver := &testInboxAttachmentResolver{attachments: map[InboxKey]AgentAttachment{}}
	resolver.set(AgentAttachment{WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1})
	resolver.set(AgentAttachment{WorkspaceID: "workspace-2", AgentID: "agent-1", RuntimeID: "runtime-2", AttachmentGeneration: 1})
	first := newTestInboxRegistry(t, "workspace-1", resolver)
	second := newTestInboxRegistry(t, "workspace-2", resolver)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	if created, err := first.Ensure("agent-1"); err != nil || !created {
		t.Fatalf("open first Inbox: created=%v err=%v", created, err)
	}
	if created, err := second.Ensure("agent-1"); err != nil || !created {
		t.Fatalf("open second Inbox: created=%v err=%v", created, err)
	}
	firstCoordinator, firstRuntime, firstOK := first.Resolve("agent-1")
	secondCoordinator, secondRuntime, secondOK := second.Resolve("agent-1")
	if !firstOK || !secondOK || firstCoordinator == secondCoordinator {
		t.Fatalf("Workspace-scoped resolution first=%v second=%v", firstOK, secondOK)
	}
	if !firstCoordinator.hasInboxKey(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}) || firstRuntime != "runtime-1" {
		t.Fatalf("first Inbox resolved outside Workspace: runtime=%q", firstRuntime)
	}
	if !secondCoordinator.hasInboxKey(InboxKey{WorkspaceID: "workspace-2", AgentID: "agent-1"}) || secondRuntime != "runtime-2" {
		t.Fatalf("second Inbox resolved outside Workspace: runtime=%q", secondRuntime)
	}
}

func TestInboxRegistryRejectsCreationWithoutScopedAttachmentOrRuntime(t *testing.T) {
	resolver := &testInboxAttachmentResolver{attachments: map[InboxKey]AgentAttachment{}}
	resolver.set(AgentAttachment{WorkspaceID: "workspace-2", AgentID: "agent-1", RuntimeID: "runtime-2", AttachmentGeneration: 1})
	registry := newTestInboxRegistry(t, "workspace-1", resolver)
	t.Cleanup(registry.Close)

	if created, err := registry.Ensure("agent-1"); err == nil || created {
		t.Fatalf("cross-Workspace Attachment opened Inbox: created=%v err=%v", created, err)
	}
	if _, _, ok := registry.Resolve("agent-1"); ok {
		t.Fatal("cross-Workspace Inbox became resolvable")
	}
}

func TestInboxRegistryReconnectRecoveryIsWorkspaceScoped(t *testing.T) {
	resolver := &testInboxAttachmentResolver{attachments: map[InboxKey]AgentAttachment{}}
	resolver.set(AgentAttachment{WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1})
	resolver.set(AgentAttachment{WorkspaceID: "workspace-2", AgentID: "agent-2", RuntimeID: "runtime-2", AttachmentGeneration: 1})
	first := newTestInboxRegistry(t, "workspace-1", resolver)
	second := newTestInboxRegistry(t, "workspace-2", resolver)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	if _, err := first.Ensure("agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Ensure("agent-2"); err != nil {
		t.Fatal(err)
	}

	var recovered []string
	first.BeginRecovery(func(request protocol.AgentRecoveryRequest) error {
		recovered = append(recovered, request.AgentID)
		return nil
	})
	if want := []string{"agent-1"}; !reflect.DeepEqual(recovered, want) {
		t.Fatalf("Workspace reconnect recovered Agents %v, want %v", recovered, want)
	}
}

func TestInboxRegistryCloseDoesNotCloseSiblingWorkspace(t *testing.T) {
	resolver := &testInboxAttachmentResolver{attachments: map[InboxKey]AgentAttachment{}}
	resolver.set(AgentAttachment{WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1})
	resolver.set(AgentAttachment{WorkspaceID: "workspace-2", AgentID: "agent-2", RuntimeID: "runtime-2", AttachmentGeneration: 1})
	first := newTestInboxRegistry(t, "workspace-1", resolver)
	second := newTestInboxRegistry(t, "workspace-2", resolver)
	t.Cleanup(second.Close)
	if _, err := first.Ensure("agent-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Ensure("agent-2"); err != nil {
		t.Fatal(err)
	}
	firstCoordinator, _, _ := first.Resolve("agent-1")
	secondCoordinator, _, _ := second.Resolve("agent-2")

	first.Close()
	if firstCoordinator.hasInboxKey(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}) {
		t.Fatal("closed Runner retained an open Inbox")
	}
	if !secondCoordinator.hasInboxKey(InboxKey{WorkspaceID: "workspace-2", AgentID: "agent-2"}) {
		t.Fatal("closing one Runner closed a sibling Workspace Inbox")
	}
	if _, _, ok := second.Resolve("agent-2"); !ok {
		t.Fatal("sibling Workspace Inbox became unavailable")
	}
}

func TestInboxRegistryAttachmentMoveReplacesOnlyMatchingRuntime(t *testing.T) {
	resolver := &testInboxAttachmentResolver{attachments: map[InboxKey]AgentAttachment{}}
	resolver.set(AgentAttachment{WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1})
	registry := newTestInboxRegistry(t, "workspace-1", resolver)
	t.Cleanup(registry.Close)
	if _, err := registry.Ensure("agent-1"); err != nil {
		t.Fatal(err)
	}
	previous, _, _ := registry.Resolve("agent-1")
	registry.Remove("agent-1", "runtime-other")
	if current, runtimeID, ok := registry.Resolve("agent-1"); !ok || current != previous || runtimeID != "runtime-1" {
		t.Fatal("non-matching Runtime removed the Inbox")
	}

	resolver.set(AgentAttachment{WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-2", AttachmentGeneration: 2})
	if created, err := registry.Ensure("agent-1"); err != nil || !created {
		t.Fatalf("replace moved Inbox: created=%v err=%v", created, err)
	}
	current, runtimeID, ok := registry.Resolve("agent-1")
	if !ok || current == previous || runtimeID != "runtime-2" {
		t.Fatalf("moved Inbox current=%p previous=%p runtime=%q", current, previous, runtimeID)
	}
	if previous.hasInboxKey(InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}) {
		t.Fatal("replaced coordinator remained open")
	}
}
