package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

var errInboxRegistryClosed = errors.New("Inbox registry is closed")

type inboxCoordinatorFactory func(InboxKey, string) (*MessageCoordinator, error)

type inboxAttachmentResolver interface {
	Resolve(string, string) (AgentAttachment, bool)
}

type inboxRegistryEntry struct {
	runtimeID   string
	coordinator *MessageCoordinator
}

type inboxRegistryDependencies struct {
	attachments inboxAttachmentResolver
	ownsRuntime func(string) bool
	open        inboxCoordinatorFactory
	logger      *slog.Logger
}

// InboxRegistry owns every in-memory Message coordinator for one immutable
// Workspace Runner scope. AgentAttachmentRegistry remains the authority for
// whether an Inbox may be opened and which Runtime it belongs to.
type InboxRegistry struct {
	workspaceID string
	attachments inboxAttachmentResolver
	ownsRuntime func(string) bool
	open        inboxCoordinatorFactory
	logger      *slog.Logger

	mu      sync.RWMutex
	inboxes map[string]inboxRegistryEntry
	closed  bool
}

func newInboxRegistry(workspaceID string, dependencies inboxRegistryDependencies) (*InboxRegistry, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("Inbox registry Workspace identity is required")
	}
	if dependencies.attachments == nil || dependencies.ownsRuntime == nil || dependencies.open == nil {
		return nil, errors.New("Inbox registry Attachment authority, Runtime ownership, and coordinator factory are required")
	}
	return &InboxRegistry{
		workspaceID: workspaceID,
		attachments: dependencies.attachments,
		ownsRuntime: dependencies.ownsRuntime,
		open:        dependencies.open,
		logger:      dependencies.logger,
		inboxes:     make(map[string]inboxRegistryEntry),
	}, nil
}

// Ensure opens or replaces one Inbox only after resolving its current durable
// Attachment inside this registry's fixed Workspace scope.
func (registry *InboxRegistry) Ensure(agentID string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if registry == nil || agentID == "" {
		return false, errors.New("Inbox registry and Agent identity are required")
	}
	attachment, ok := registry.attachments.Resolve(registry.workspaceID, agentID)
	if !ok {
		return false, fmt.Errorf("no durable Agent Attachment for %q in Workspace %q", agentID, registry.workspaceID)
	}
	if !registry.ownsRuntime(attachment.RuntimeID) {
		return false, fmt.Errorf("durable Agent Attachment for %q is not owned by Workspace %q", agentID, registry.workspaceID)
	}

	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return false, errInboxRegistryClosed
	}
	previous, exists := registry.inboxes[agentID]
	if exists && previous.runtimeID == attachment.RuntimeID && previous.coordinator != nil {
		registry.mu.Unlock()
		return false, nil
	}
	coordinator, err := registry.open(
		InboxKey{WorkspaceID: registry.workspaceID, AgentID: agentID},
		attachment.RuntimeID,
	)
	if err != nil {
		registry.mu.Unlock()
		return false, err
	}
	// Attachment may move while local files are opened. Re-resolve before the
	// coordinator becomes visible so a stale Runtime never wins that race.
	current, currentOK := registry.attachments.Resolve(registry.workspaceID, agentID)
	if !currentOK || current.RuntimeID != attachment.RuntimeID || !registry.ownsRuntime(current.RuntimeID) {
		registry.mu.Unlock()
		coordinator.Close()
		return false, fmt.Errorf("Agent Attachment changed while opening Inbox %q in Workspace %q", agentID, registry.workspaceID)
	}
	registry.inboxes[agentID] = inboxRegistryEntry{runtimeID: attachment.RuntimeID, coordinator: coordinator}
	registry.mu.Unlock()
	if exists && previous.coordinator != nil {
		previous.coordinator.Close()
	}
	registry.log("workspace Runner Inbox opened", agentID, attachment.RuntimeID, map[bool]string{true: "replaced", false: "created"}[exists])
	return true, nil
}

func (registry *InboxRegistry) Resolve(agentID string) (*MessageCoordinator, string, bool) {
	if registry == nil {
		return nil, "", false
	}
	agentID = strings.TrimSpace(agentID)
	registry.mu.RLock()
	entry, ok := registry.inboxes[agentID]
	closed := registry.closed
	registry.mu.RUnlock()
	if closed || !ok || entry.coordinator == nil {
		return nil, "", false
	}
	return entry.coordinator, entry.runtimeID, true
}

func (registry *InboxRegistry) Remove(agentID, runtimeID string) {
	if registry == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	runtimeID = strings.TrimSpace(runtimeID)
	registry.mu.Lock()
	entry, ok := registry.inboxes[agentID]
	if ok && (runtimeID == "" || entry.runtimeID == runtimeID) {
		delete(registry.inboxes, agentID)
	} else {
		ok = false
	}
	registry.mu.Unlock()
	if ok && entry.coordinator != nil {
		entry.coordinator.Close()
		registry.log("workspace Runner Inbox closed", agentID, entry.runtimeID, "removed")
	}
}

// BeginRecovery snapshots only this Workspace Runner's Inboxes. A reconnect
// therefore cannot fan recovery out to another Workspace.
func (registry *InboxRegistry) BeginRecovery(send func(protocol.AgentRecoveryRequest) error) {
	if registry == nil || send == nil {
		return
	}
	registry.mu.RLock()
	agentIDs := make([]string, 0, len(registry.inboxes))
	coordinators := make(map[string]*MessageCoordinator, len(registry.inboxes))
	for agentID, entry := range registry.inboxes {
		if entry.coordinator != nil {
			agentIDs = append(agentIDs, agentID)
			coordinators[agentID] = entry.coordinator
		}
	}
	registry.mu.RUnlock()
	sort.Strings(agentIDs)
	for _, agentID := range agentIDs {
		if err := send(coordinators[agentID].BeginRecovery(agentID, 100)); err != nil && registry.logger != nil {
			registry.logger.Warn("workspace Runner Inbox recovery request failed", "error", err, "workspace_id", registry.workspaceID, "agent_id", agentID, "reason", "runner_connection_write_failed")
		}
	}
}

func (registry *InboxRegistry) snapshot() map[string]inboxRegistryEntry {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make(map[string]inboxRegistryEntry, len(registry.inboxes))
	for agentID, entry := range registry.inboxes {
		result[agentID] = entry
	}
	return result
}

func (registry *InboxRegistry) Close() {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	if registry.closed {
		registry.mu.Unlock()
		return
	}
	registry.closed = true
	inboxes := registry.inboxes
	registry.inboxes = nil
	registry.mu.Unlock()
	for agentID, entry := range inboxes {
		if entry.coordinator != nil {
			entry.coordinator.Close()
			registry.log("workspace Runner Inbox closed", agentID, entry.runtimeID, "runner_closed")
		}
	}
}

func (registry *InboxRegistry) log(message, agentID, runtimeID, outcome string) {
	if registry.logger == nil {
		return
	}
	registry.logger.Debug(message,
		"workspace_id", registry.workspaceID,
		"agent_id", agentID,
		"runtime_id", runtimeID,
		"outcome", outcome,
	)
}
