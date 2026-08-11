package daemon

import (
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (r *localAgentAttachmentRegistry) applyLegacyStart(agentID, runtimeID, workspaceID string, generation int64) (bool, bool, error) {
	result, err := (legacyAgentAttachmentAdapter{registry: r}).ApplyStart(protocol.DaemonAgentStartPayload{
		AgentID: agentID, RuntimeID: runtimeID, WorkspaceID: workspaceID,
		PlacementGeneration: generation,
	})
	changed := result.change.Kind == AgentAttachmentAttached || result.change.Kind == AgentAttachmentMoved
	return changed, result.accepted, err
}

func (r *localAgentAttachmentRegistry) applyLegacyStop(agentID, runtimeID string, generation int64) (bool, bool, error) {
	result, err := (legacyAgentAttachmentAdapter{registry: r}).ApplyStop(protocol.DaemonAgentStopPayload{
		AgentID: agentID, RuntimeID: runtimeID, PlacementGeneration: generation,
	})
	return result.change.Kind == AgentAttachmentDetached, result.accepted, err
}

func (r *localAgentAttachmentRegistry) localRecord(agentID string) (localAgentAttachmentRecord, bool) {
	if r == nil {
		return localAgentAttachmentRecord{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.agents[agentID]
	return entry, ok
}

func (r *localAgentAttachmentRegistry) localAgentIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	ids := make([]string, 0, len(r.agents))
	for agentID := range r.agents {
		ids = append(ids, agentID)
	}
	r.mu.Unlock()
	sort.Strings(ids)
	return ids
}

func (r *localAgentAttachmentRegistry) legacyRecoveryCursors() map[string]int64 {
	if r == nil {
		return map[string]int64{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]int64, len(r.runtimeLifecycleCursors))
	for runtimeID, sequence := range r.runtimeLifecycleCursors {
		result[runtimeID] = sequence
	}
	return result
}

func (r *localAgentAttachmentRegistry) advanceLegacyRecovery(cursors map[string]int64) error {
	return (legacyAgentAttachmentAdapter{registry: r}).AdvanceRecovery(cursors)
}

func (r *localAgentAttachmentRegistry) reconcileLegacyRuntimeSet(allowed map[string]bool) (bool, error) {
	allowedRuntimeIDs := make(map[string]struct{}, len(allowed))
	for runtimeID, isAllowed := range allowed {
		if isAllowed && strings.TrimSpace(runtimeID) != "" {
			allowedRuntimeIDs[runtimeID] = struct{}{}
		}
	}
	_, changed, err := r.reconcile(nil, allowedRuntimeIDs, true)
	return changed, err
}
