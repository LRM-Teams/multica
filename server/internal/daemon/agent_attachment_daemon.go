package daemon

import (
	"sort"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) attachmentRegistry() AgentAttachmentRegistry {
	if d == nil {
		return nil
	}
	if d.agentAttachments != nil {
		return d.agentAttachments
	}
	if d.reminderAgents != nil {
		return d.reminderAgents.localAgentAttachmentRegistry
	}
	return nil
}

func (d *Daemon) attachmentRuntimeSets() []AgentAttachmentRuntimeSet {
	registry := d.attachmentRegistry()
	workspaces := make(map[string][]string)
	if registry != nil {
		for _, workspaceID := range registry.WorkspaceIDs() {
			workspaces[workspaceID] = nil
		}
	}
	if d == nil {
		return nil
	}
	d.mu.Lock()
	for workspaceID := range d.workspaces {
		if _, exists := workspaces[workspaceID]; !exists {
			workspaces[workspaceID] = nil
		}
	}
	for runtimeID, runtime := range d.runtimeIndex {
		workspaces[runtime.WorkspaceID] = append(workspaces[runtime.WorkspaceID], runtimeID)
	}
	d.mu.Unlock()
	workspaceIDs := make([]string, 0, len(workspaces))
	for workspaceID := range workspaces {
		if workspaceID != "" {
			workspaceIDs = append(workspaceIDs, workspaceID)
		}
	}
	sort.Strings(workspaceIDs)
	result := make([]AgentAttachmentRuntimeSet, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		runtimeIDs := workspaces[workspaceID]
		sort.Strings(runtimeIDs)
		result = append(result, AgentAttachmentRuntimeSet{WorkspaceID: workspaceID, RuntimeIDs: runtimeIDs})
	}
	return result
}

func (d *Daemon) currentAttachments() []AgentAttachment {
	registry := d.attachmentRegistry()
	if registry == nil {
		return nil
	}
	attachments := make([]AgentAttachment, 0)
	for _, runtimeSet := range d.attachmentRuntimeSets() {
		allowed := runtimeSet.runtimeIDs()
		for _, attachment := range registry.List(runtimeSet.WorkspaceID) {
			if _, ok := allowed[attachment.RuntimeID]; ok {
				attachments = append(attachments, attachment)
			}
		}
	}
	sort.Slice(attachments, func(i, j int) bool { return attachments[i].AgentID < attachments[j].AgentID })
	return attachments
}

func (d *Daemon) currentAttachmentForAgent(agentID string) (AgentAttachment, bool) {
	for _, attachment := range d.currentAttachments() {
		if attachment.AgentID == agentID {
			return attachment, true
		}
	}
	return AgentAttachment{}, false
}

func (d *Daemon) currentAttachmentForRuntimeAgent(runtimeID, agentID string) (AgentAttachment, bool) {
	if d == nil {
		return AgentAttachment{}, false
	}
	d.mu.Lock()
	runtime, known := d.runtimeIndex[runtimeID]
	d.mu.Unlock()
	registry := d.attachmentRegistry()
	if !known || registry == nil {
		return AgentAttachment{}, false
	}
	attachment, found := registry.Resolve(runtime.WorkspaceID, agentID)
	return attachment, found && attachment.RuntimeID == runtimeID
}

func (d *Daemon) reminderRuntimeResidencies() map[string][]protocol.ReminderRuntimeResidency {
	result := make(map[string][]protocol.ReminderRuntimeResidency)
	for _, attachment := range d.currentAttachments() {
		result[attachment.RuntimeID] = append(result[attachment.RuntimeID], protocol.ReminderRuntimeResidency{
			AgentID: attachment.AgentID, PlacementGeneration: int64(attachment.AttachmentGeneration),
		})
	}
	for runtimeID := range result {
		sort.Slice(result[runtimeID], func(i, j int) bool { return result[runtimeID][i].AgentID < result[runtimeID][j].AgentID })
	}
	return result
}

func (d *Daemon) attachmentRecoveryCursors() map[string]int64 {
	result := make(map[string]int64)
	registry := d.attachmentRegistry()
	if registry == nil {
		return result
	}
	for _, runtimeSet := range d.attachmentRuntimeSets() {
		state, err := registry.RecoveryState(runtimeSet)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("read Agent Attachment recovery state failed", "workspace_id", runtimeSet.WorkspaceID, "reason", "invalid_runtime_set", "error", err)
			}
			continue
		}
		for _, cursor := range state.Cursors {
			result[cursor.RuntimeID] = int64(cursor.LifecycleSeq)
		}
	}
	return result
}
