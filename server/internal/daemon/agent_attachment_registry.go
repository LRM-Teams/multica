package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

const agentAttachmentStateFile = "reminder_agents.json"

// These wire structs deliberately retain the existing reminder_agents.json
// field names. Agent Attachment extraction must not require a state migration.
type localAgentAttachmentState struct {
	Agents                  []localAgentAttachmentRecord `json:"agents"`
	RemovedAgentIDs         []string                     `json:"removed_agent_ids,omitempty"`
	PlacementHighWatermarks map[string]int64             `json:"placement_high_watermarks,omitempty"`
	RuntimeLifecycleCursors map[string]int64             `json:"runtime_lifecycle_cursors,omitempty"`
}

type localAgentAttachmentRecord struct {
	AgentID             string `json:"agent_id"`
	RuntimeID           string `json:"runtime_id"`
	WorkspaceID         string `json:"workspace_id"`
	PlacementGeneration int64  `json:"placement_generation"`
	ActiveTasks         int    `json:"-"`
}

// localAgentAttachmentRegistry owns durable Attachment persistence, fencing,
// recovery cursors, and compatibility bootstrap from pre-Attachment state.
type localAgentAttachmentRegistry struct {
	mu                      sync.Mutex
	agents                  map[string]localAgentAttachmentRecord
	placementHighWatermarks map[string]int64
	runtimeLifecycleCursors map[string]int64
	root                    string
	workspacesRoot          string
	workspaceScope          string
	path                    string
	logger                  *slog.Logger
	writeState              func(string, []byte) error
}

func newLocalAgentAttachmentRegistry(workspacesRoot string, logger *slog.Logger) *localAgentAttachmentRegistry {
	return newLocalAgentAttachmentRegistryAt(workspacesRoot, workspacesRoot, logger)
}

func newLocalAgentAttachmentRegistryAt(stateRoot, workspacesRoot string, logger *slog.Logger) *localAgentAttachmentRegistry {
	return newLocalAgentAttachmentRegistryAtScope(stateRoot, workspacesRoot, "", logger)
}

func newLocalAgentAttachmentRegistryForBinding(stateRoot, workspacesRoot, workspaceID string, logger *slog.Logger) *localAgentAttachmentRegistry {
	return newLocalAgentAttachmentRegistryAtScope(stateRoot, workspacesRoot, workspaceID, logger)
}

func newLocalAgentAttachmentRegistryAtScope(stateRoot, workspacesRoot, workspaceID string, logger *slog.Logger) *localAgentAttachmentRegistry {
	registry := &localAgentAttachmentRegistry{
		agents:                  make(map[string]localAgentAttachmentRecord),
		placementHighWatermarks: make(map[string]int64),
		runtimeLifecycleCursors: make(map[string]int64),
		root:                    strings.TrimSpace(stateRoot),
		workspacesRoot:          strings.TrimSpace(workspacesRoot),
		workspaceScope:          strings.TrimSpace(workspaceID),
		logger:                  logger,
		writeState:              writeDaemonStateAtomically,
	}
	if registry.root != "" {
		registry.path = filepath.Join(registry.root, ".daemon", agentAttachmentStateFile)
		registry.load()
		registry.bootstrapFromLocalAgentConfigs()
		if err := registry.persistLocked(); err != nil && registry.logger != nil {
			registry.logger.Warn("persist Agent Attachment registry failed", "error", err)
		}
	}
	return registry
}

// observeTaskStarted preserves the pre-Attachment bootstrap behavior for a
// daemon that executes work before receiving a generation-bearing lifecycle
// event. Generation-zero records remain provisional: Resolve and List exclude
// them, so they cannot authorize restart recovery.
func (r *localAgentAttachmentRegistry) observeTaskStarted(agentID, runtimeID, workspaceID string) (bool, bool) {
	if r == nil || strings.TrimSpace(agentID) == "" {
		return false, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, existed := r.agents[agentID]
	entry := previous
	if !existed && r.placementHighWatermarks[agentID] > 0 {
		return false, false
	}
	if existed && entry.PlacementGeneration > 0 && (entry.RuntimeID != runtimeID || entry.WorkspaceID != workspaceID) {
		if r.logger != nil {
			r.logger.Warn("provisional task observation rejected outside Agent Attachment", "workspace_id", workspaceID, "runtime_id", runtimeID, "agent_id", agentID, "attachment_workspace_id", entry.WorkspaceID, "attachment_runtime_id", entry.RuntimeID, "attachment_generation", entry.PlacementGeneration, "reason", "attachment_identity_mismatch")
		}
		return false, false
	}
	entry.AgentID = agentID
	entry.RuntimeID = runtimeID
	entry.WorkspaceID = workspaceID
	entry.ActiveTasks++
	r.agents[agentID] = entry
	if err := r.persistLocked(); err != nil {
		if existed {
			r.agents[agentID] = previous
		} else {
			delete(r.agents, agentID)
		}
		if r.logger != nil {
			r.logger.Warn("persist provisional Agent Attachment task observation failed", "workspace_id", workspaceID, "runtime_id", runtimeID, "agent_id", agentID, "reason", "persist_failed", "error", err)
		}
		return false, false
	}
	return !existed, true
}

func (r *localAgentAttachmentRegistry) observeTaskFinished(agentID string) {
	if r == nil || strings.TrimSpace(agentID) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.agents[agentID]
	previous := entry
	if ok && entry.ActiveTasks > 0 {
		entry.ActiveTasks--
		r.agents[agentID] = entry
	}
	if err := r.persistLocked(); err != nil {
		if ok {
			r.agents[agentID] = previous
		}
		if r.logger != nil {
			r.logger.Warn("persist provisional Agent Attachment task completion failed", "workspace_id", entry.WorkspaceID, "runtime_id", entry.RuntimeID, "agent_id", agentID, "reason", "persist_failed", "error", err)
		}
	}
}

type agentAttachmentApplyResult struct {
	change   AgentAttachmentChange
	accepted bool
	reason   string
}

func (r *localAgentAttachmentRegistry) Apply(workspaceID string, event AgentAttachmentEvent) (AgentAttachmentChange, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return AgentAttachmentChange{}, errors.New("Agent Attachment Workspace scope is required")
	}
	if err := event.Validate(); err != nil {
		return AgentAttachmentChange{}, err
	}
	result, err := r.applyEvent(workspaceID, event, true, true)
	return result.change, err
}

// applyEvent is the single generation, tombstone, and lifecycle cursor
// implementation behind the formal registry. Test fixtures may disable cursor
// tracking when seeding historical local state, but production callers always
// use the scoped registry contract.
func (r *localAgentAttachmentRegistry) applyEvent(
	workspaceID string,
	event AgentAttachmentEvent,
	trackLifecycle bool,
	strictWorkspace bool,
) (agentAttachmentApplyResult, error) {
	result := agentAttachmentApplyResult{
		change: AgentAttachmentChange{Kind: AgentAttachmentUnchanged},
		reason: "unchanged",
	}
	if r == nil {
		return result, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, existed := r.agents[event.AgentID]
	if strictWorkspace && existed && entry.WorkspaceID != workspaceID {
		err := fmt.Errorf("Agent Attachment %s belongs to Workspace %s, not %s", event.AgentID, entry.WorkspaceID, workspaceID)
		result.reason = "workspace_conflict"
		r.logApplyLocked(workspaceID, event, result, err)
		return result, err
	}
	if !strictWorkspace && workspaceID == "" && existed {
		workspaceID = entry.WorkspaceID
	}

	previousCursor := r.runtimeLifecycleCursors[event.RuntimeID]
	if trackLifecycle && int64(event.LifecycleSeq) <= previousCursor {
		result.reason = "duplicate_lifecycle_sequence"
		if event.Kind == AgentAttachmentEventAttach && existed && entry.WorkspaceID == workspaceID && entry.RuntimeID == event.RuntimeID && entry.PlacementGeneration == int64(event.AttachmentGeneration) {
			result.accepted = true
			result.change.Current = attachmentFromRecord(entry)
		}
		r.logApplyLocked(workspaceID, event, result, nil)
		return result, nil
	}

	previousEntry := entry
	previousGeneration, hadGeneration := r.placementHighWatermarks[event.AgentID]
	stateChanged := false
	generation := int64(event.AttachmentGeneration)
	highWatermark := previousGeneration
	current := attachmentFromRecord(entry)

	switch event.Kind {
	case AgentAttachmentEventAttach:
		switch {
		case generation < highWatermark:
			result.reason = "stale_generation"
		case generation == highWatermark && existed:
			if entry.RuntimeID != event.RuntimeID || entry.WorkspaceID != workspaceID || entry.PlacementGeneration != generation {
				result.reason = "generation_conflict"
			} else {
				result.accepted = true
				result.reason = "idempotent_attach"
				result.change.Current = current
			}
		default:
			result.accepted = true
			result.change.Previous = current
			result.change.Current = AgentAttachment{
				WorkspaceID: workspaceID, AgentID: event.AgentID, RuntimeID: event.RuntimeID,
				AttachmentGeneration: event.AttachmentGeneration,
			}
			if existed {
				result.change.Kind = AgentAttachmentMoved
				result.reason = "moved"
			} else if generation == highWatermark {
				result.change.Kind = AgentAttachmentAttached
				result.reason = "same_generation_move_attach"
			} else {
				result.change.Kind = AgentAttachmentAttached
				result.reason = "attached"
			}
			r.agents[event.AgentID] = localAgentAttachmentRecord{
				AgentID: event.AgentID, RuntimeID: event.RuntimeID, WorkspaceID: workspaceID,
				PlacementGeneration: generation,
			}
			r.placementHighWatermarks[event.AgentID] = generation
			stateChanged = true
		}

	case AgentAttachmentEventDetach:
		switch {
		case generation < highWatermark:
			result.reason = "stale_generation"
		case generation == highWatermark && !existed:
			result.accepted = true
			result.reason = "idempotent_detach"
		case generation == highWatermark && entry.RuntimeID != event.RuntimeID:
			result.accepted = true
			result.reason = "different_runtime"
			result.change.Current = current
		default:
			result.accepted = true
			if generation > highWatermark {
				r.placementHighWatermarks[event.AgentID] = generation
				stateChanged = true
			}
			if existed && entry.RuntimeID == event.RuntimeID && entry.WorkspaceID == workspaceID && entry.PlacementGeneration <= generation {
				delete(r.agents, event.AgentID)
				result.change.Kind = AgentAttachmentDetached
				result.change.Previous = current
				result.reason = "detached"
				stateChanged = true
			} else {
				result.change.Current = current
				result.reason = "different_runtime"
			}
		}
	}

	cursorChanged := false
	if trackLifecycle {
		r.runtimeLifecycleCursors[event.RuntimeID] = int64(event.LifecycleSeq)
		cursorChanged = true
	}
	if !stateChanged && !cursorChanged {
		r.logApplyLocked(workspaceID, event, result, nil)
		return result, nil
	}
	if err := r.persistLocked(); err != nil {
		if existed {
			r.agents[event.AgentID] = previousEntry
		} else {
			delete(r.agents, event.AgentID)
		}
		if hadGeneration {
			r.placementHighWatermarks[event.AgentID] = previousGeneration
		} else {
			delete(r.placementHighWatermarks, event.AgentID)
		}
		if trackLifecycle {
			if previousCursor > 0 {
				r.runtimeLifecycleCursors[event.RuntimeID] = previousCursor
			} else {
				delete(r.runtimeLifecycleCursors, event.RuntimeID)
			}
		}
		result.reason = "persist_failed"
		r.logApplyLocked(workspaceID, event, result, err)
		return agentAttachmentApplyResult{change: AgentAttachmentChange{Kind: AgentAttachmentUnchanged}, reason: result.reason}, err
	}
	r.logApplyLocked(workspaceID, event, result, nil)
	return result, nil
}

func (r *localAgentAttachmentRegistry) Resolve(workspaceID, agentID string) (AgentAttachment, bool) {
	if r == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(agentID) == "" {
		return AgentAttachment{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, found := r.agents[agentID]
	if !found || entry.WorkspaceID != workspaceID || entry.PlacementGeneration < 1 {
		return AgentAttachment{}, false
	}
	return attachmentFromRecord(entry), true
}

func (r *localAgentAttachmentRegistry) List(workspaceID string) []AgentAttachment {
	if r == nil || strings.TrimSpace(workspaceID) == "" {
		return nil
	}
	r.mu.Lock()
	attachments := make([]AgentAttachment, 0)
	for _, entry := range r.agents {
		if entry.WorkspaceID == workspaceID && entry.PlacementGeneration > 0 {
			attachments = append(attachments, attachmentFromRecord(entry))
		}
	}
	r.mu.Unlock()
	sort.Slice(attachments, func(i, j int) bool { return attachments[i].AgentID < attachments[j].AgentID })
	return attachments
}

func (r *localAgentAttachmentRegistry) WorkspaceIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	workspaces := make(map[string]struct{})
	for _, entry := range r.agents {
		if entry.WorkspaceID != "" {
			workspaces[entry.WorkspaceID] = struct{}{}
		}
	}
	r.mu.Unlock()
	result := make([]string, 0, len(workspaces))
	for workspaceID := range workspaces {
		result = append(result, workspaceID)
	}
	sort.Strings(result)
	return result
}

func (r *localAgentAttachmentRegistry) DetachedAgentIDs() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	ids := make([]string, 0)
	for agentID := range r.placementHighWatermarks {
		if _, attached := r.agents[agentID]; !attached {
			ids = append(ids, agentID)
		}
	}
	r.mu.Unlock()
	sort.Strings(ids)
	return ids
}

func (r *localAgentAttachmentRegistry) RecoveryState(runtimeSet AgentAttachmentRuntimeSet) (AgentAttachmentRecoveryState, error) {
	if err := runtimeSet.Validate(); err != nil {
		return AgentAttachmentRecoveryState{}, err
	}
	if r == nil {
		return AgentAttachmentRecoveryState{WorkspaceID: runtimeSet.WorkspaceID}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateRuntimeSetWorkspaceLocked(runtimeSet); err != nil {
		return AgentAttachmentRecoveryState{}, err
	}
	runtimeIDs := append([]string(nil), runtimeSet.RuntimeIDs...)
	sort.Strings(runtimeIDs)
	state := AgentAttachmentRecoveryState{WorkspaceID: runtimeSet.WorkspaceID, Cursors: make([]AgentAttachmentRecoveryCursor, 0, len(runtimeIDs))}
	for _, runtimeID := range runtimeIDs {
		state.Cursors = append(state.Cursors, AgentAttachmentRecoveryCursor{
			RuntimeID: runtimeID, LifecycleSeq: AttachmentLifecycleSequence(r.runtimeLifecycleCursors[runtimeID]),
		})
	}
	return state, nil
}

func (r *localAgentAttachmentRegistry) AdvanceRecovery(runtimeSet AgentAttachmentRuntimeSet, cursors []AgentAttachmentRecoveryCursor) error {
	if err := runtimeSet.Validate(); err != nil {
		return err
	}
	allowed := runtimeSet.runtimeIDs()
	for _, cursor := range cursors {
		if err := cursor.Validate(); err != nil {
			return err
		}
		if _, ok := allowed[cursor.RuntimeID]; !ok {
			return fmt.Errorf("Agent Attachment recovery Runtime %s is outside Workspace %s Runtime set", cursor.RuntimeID, runtimeSet.WorkspaceID)
		}
	}
	cursorMap := make(map[string]int64, len(cursors))
	for _, cursor := range cursors {
		cursorMap[cursor.RuntimeID] = int64(cursor.LifecycleSeq)
	}
	return r.advanceRecovery(&runtimeSet, cursorMap)
}

func (r *localAgentAttachmentRegistry) advanceRecovery(runtimeSet *AgentAttachmentRuntimeSet, cursors map[string]int64) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if runtimeSet != nil {
		if err := r.validateRuntimeSetWorkspaceLocked(*runtimeSet); err != nil {
			return err
		}
	}
	previous := cloneInt64Map(r.runtimeLifecycleCursors)
	changed := false
	advanced := make([]AgentAttachmentRecoveryCursor, 0, len(cursors))
	for runtimeID, seq := range cursors {
		if strings.TrimSpace(runtimeID) != "" && seq > r.runtimeLifecycleCursors[runtimeID] {
			r.runtimeLifecycleCursors[runtimeID] = seq
			changed = true
			advanced = append(advanced, AgentAttachmentRecoveryCursor{RuntimeID: runtimeID, LifecycleSeq: AttachmentLifecycleSequence(seq)})
		}
	}
	if !changed {
		return nil
	}
	if err := r.persistLocked(); err != nil {
		r.runtimeLifecycleCursors = previous
		if r.logger != nil {
			r.logger.Error("Agent Attachment recovery cursor persist failed", "reason", "persist_failed", "error", err)
		}
		return err
	}
	if r.logger != nil {
		sort.Slice(advanced, func(i, j int) bool { return advanced[i].RuntimeID < advanced[j].RuntimeID })
		workspaceID := ""
		if runtimeSet != nil {
			workspaceID = runtimeSet.WorkspaceID
		}
		for _, cursor := range advanced {
			r.logger.Debug("Agent Attachment recovery cursor advanced", "workspace_id", workspaceID, "runtime_id", cursor.RuntimeID, "lifecycle_seq", cursor.LifecycleSeq, "outcome", "advanced", "reason", "replay_committed")
		}
	}
	return nil
}

func (r *localAgentAttachmentRegistry) Reconcile(runtimeSet AgentAttachmentRuntimeSet) ([]AgentAttachmentChange, error) {
	if err := runtimeSet.Validate(); err != nil {
		return nil, err
	}
	allowed := runtimeSet.runtimeIDs()
	changes, _, err := r.reconcile(&runtimeSet, allowed, false)
	return changes, err
}

func (r *localAgentAttachmentRegistry) reconcile(
	runtimeSet *AgentAttachmentRuntimeSet,
	allowed map[string]struct{},
	removeAllDisallowedCursors bool,
) ([]AgentAttachmentChange, bool, error) {
	if r == nil {
		return nil, false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if runtimeSet != nil {
		if err := r.validateRuntimeSetWorkspaceLocked(*runtimeSet); err != nil {
			return nil, false, err
		}
	}
	previousAgents := cloneAttachmentRecordMap(r.agents)
	previousHighWatermarks := cloneInt64Map(r.placementHighWatermarks)
	previousCursors := cloneInt64Map(r.runtimeLifecycleCursors)
	changes := make([]AgentAttachmentChange, 0)
	removedRuntimeIDs := make(map[string]struct{})
	for agentID, entry := range r.agents {
		if runtimeSet != nil && entry.WorkspaceID != runtimeSet.WorkspaceID {
			continue
		}
		if _, ok := allowed[entry.RuntimeID]; ok {
			continue
		}
		delete(r.agents, agentID)
		generation := entry.PlacementGeneration
		if generation < 1 {
			generation = 1
		}
		if generation > r.placementHighWatermarks[agentID] {
			r.placementHighWatermarks[agentID] = generation
		}
		removedRuntimeIDs[entry.RuntimeID] = struct{}{}
		previous := attachmentFromRecord(entry)
		previous.AttachmentGeneration = AttachmentGeneration(generation)
		changes = append(changes, AgentAttachmentChange{
			Kind: AgentAttachmentDetached, Previous: previous,
		})
	}
	cursorChanged := false
	if removeAllDisallowedCursors {
		for runtimeID := range r.runtimeLifecycleCursors {
			if _, ok := allowed[runtimeID]; !ok {
				delete(r.runtimeLifecycleCursors, runtimeID)
				cursorChanged = true
			}
		}
	} else {
		for runtimeID := range removedRuntimeIDs {
			if _, exists := r.runtimeLifecycleCursors[runtimeID]; exists {
				delete(r.runtimeLifecycleCursors, runtimeID)
				cursorChanged = true
			}
		}
	}
	changed := len(changes) > 0 || cursorChanged
	if !changed {
		return nil, false, nil
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Previous.AgentID < changes[j].Previous.AgentID })
	if err := r.persistLocked(); err != nil {
		r.agents = previousAgents
		r.placementHighWatermarks = previousHighWatermarks
		r.runtimeLifecycleCursors = previousCursors
		if r.logger != nil {
			workspaceID := ""
			if runtimeSet != nil {
				workspaceID = runtimeSet.WorkspaceID
			}
			r.logger.Error("Agent Attachment reconcile persist failed", "workspace_id", workspaceID, "outcome", "failed", "reason", "persist_failed", "error", err)
		}
		return nil, false, err
	}
	if r.logger != nil {
		for _, change := range changes {
			workspaceID := change.Previous.WorkspaceID
			if runtimeSet != nil {
				workspaceID = runtimeSet.WorkspaceID
			}
			r.logger.Info("Agent Attachment reconciled", "workspace_id", workspaceID, "agent_id", change.Previous.AgentID, "runtime_id", change.Previous.RuntimeID, "attachment_generation", change.Previous.AttachmentGeneration, "outcome", change.Kind, "reason", "runtime_not_allowed")
		}
	}
	return changes, true, nil
}

func (r *localAgentAttachmentRegistry) validateRuntimeSetWorkspaceLocked(runtimeSet AgentAttachmentRuntimeSet) error {
	allowed := runtimeSet.runtimeIDs()
	for _, entry := range r.agents {
		if _, ok := allowed[entry.RuntimeID]; ok && entry.WorkspaceID != runtimeSet.WorkspaceID {
			return fmt.Errorf("Agent Attachment Runtime %s belongs to Workspace %s, not %s", entry.RuntimeID, entry.WorkspaceID, runtimeSet.WorkspaceID)
		}
	}
	return nil
}

func (r *localAgentAttachmentRegistry) logApplyLocked(workspaceID string, event AgentAttachmentEvent, result agentAttachmentApplyResult, applyErr error) {
	if r.logger == nil {
		return
	}
	args := []any{
		"workspace_id", workspaceID,
		"agent_id", event.AgentID,
		"runtime_id", event.RuntimeID,
		"event_kind", event.Kind,
		"attachment_generation", event.AttachmentGeneration,
		"lifecycle_seq", event.LifecycleSeq,
		"outcome", result.change.Kind,
		"reason", result.reason,
	}
	if applyErr != nil {
		args = append(args, "error", applyErr)
		r.logger.Error("Agent Attachment apply failed", args...)
		return
	}
	if result.change.Kind == AgentAttachmentUnchanged {
		r.logger.Debug("Agent Attachment apply unchanged", args...)
		return
	}
	r.logger.Info("Agent Attachment applied", args...)
}

func attachmentFromRecord(entry localAgentAttachmentRecord) AgentAttachment {
	return AgentAttachment{
		WorkspaceID: entry.WorkspaceID, AgentID: entry.AgentID, RuntimeID: entry.RuntimeID,
		AttachmentGeneration: AttachmentGeneration(entry.PlacementGeneration),
	}
}

func cloneAttachmentRecordMap(source map[string]localAgentAttachmentRecord) map[string]localAgentAttachmentRecord {
	clone := make(map[string]localAgentAttachmentRecord, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneInt64Map(source map[string]int64) map[string]int64 {
	clone := make(map[string]int64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func (r *localAgentAttachmentRegistry) load() {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		if !os.IsNotExist(err) && r.logger != nil {
			r.logger.Warn("load Agent Attachment registry failed", "error", err)
		}
		return
	}
	var state localAgentAttachmentState
	if err := json.Unmarshal(raw, &state); err != nil {
		if r.logger != nil {
			r.logger.Warn("decode Agent Attachment registry failed", "error", err)
		}
		return
	}
	for _, entry := range state.Agents {
		if entry.AgentID == "" {
			continue
		}
		entry.ActiveTasks = 0
		r.agents[entry.AgentID] = entry
		if entry.PlacementGeneration > r.placementHighWatermarks[entry.AgentID] {
			r.placementHighWatermarks[entry.AgentID] = entry.PlacementGeneration
		}
	}
	for _, agentID := range state.RemovedAgentIDs {
		if agentID != "" {
			r.placementHighWatermarks[agentID] = 1
		}
	}
	for agentID, generation := range state.PlacementHighWatermarks {
		if agentID != "" && generation > r.placementHighWatermarks[agentID] {
			r.placementHighWatermarks[agentID] = generation
		}
	}
	for runtimeID, seq := range state.RuntimeLifecycleCursors {
		if runtimeID != "" && seq > 0 {
			r.runtimeLifecycleCursors[runtimeID] = seq
		}
	}
}

// bootstrapFromLocalAgentConfigs upgrades an existing daemon without waiting
// for another task to execute. Removed Attachments remain fenced so stale
// credential directories cannot resurrect them after detach or migration.
func (r *localAgentAttachmentRegistry) bootstrapFromLocalAgentConfigs() {
	if r == nil || r.workspacesRoot == "" {
		return
	}
	if r.workspaceScope != "" {
		r.bootstrapWorkspaceAgentConfigs(r.workspaceScope)
		return
	}
	workspaces, err := os.ReadDir(r.workspacesRoot)
	if err != nil {
		if !os.IsNotExist(err) && r.logger != nil {
			r.logger.Warn("scan Agent Attachment bootstrap configs failed", "error", err)
		}
		return
	}
	for _, workspace := range workspaces {
		if !workspace.IsDir() || !isCanonicalUUIDDirName(workspace.Name()) {
			continue
		}
		r.bootstrapWorkspaceAgentConfigs(workspace.Name())
	}
}

func (r *localAgentAttachmentRegistry) bootstrapWorkspaceAgentConfigs(workspaceID string) {
	if r == nil || !isCanonicalUUIDDirName(workspaceID) {
		return
	}
	agentsRoot := agentworkspace.AgentsDir(r.workspacesRoot, workspaceID)
	agents, readErr := os.ReadDir(agentsRoot)
	if readErr != nil {
		return
	}
	for _, agentDir := range agents {
		if !agentDir.IsDir() {
			continue
		}
		configPath := filepath.Join(agentworkspace.Root(r.workspacesRoot, workspaceID, agentDir.Name()), "runtime", "credentials", "current.json")
		raw, readErr := os.ReadFile(configPath)
		if readErr != nil {
			continue
		}
		var config cachedAgentCredential
		if json.Unmarshal(raw, &config) != nil || config.AgentID == "" || config.AgentID != agentDir.Name() || config.WorkspaceID != workspaceID {
			continue
		}
		if r.placementHighWatermarks[config.AgentID] > 0 {
			continue
		}
		if _, exists := r.agents[config.AgentID]; exists {
			continue
		}
		r.agents[config.AgentID] = localAgentAttachmentRecord{
			AgentID: config.AgentID, RuntimeID: config.RuntimeID, WorkspaceID: config.WorkspaceID,
		}
	}
}

func (r *localAgentAttachmentRegistry) persistLocked() error {
	if r.path == "" {
		return nil
	}
	entries := make([]localAgentAttachmentRecord, 0, len(r.agents))
	for _, entry := range r.agents {
		entry.ActiveTasks = 0
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].AgentID < entries[j].AgentID })
	highWatermarks := make(map[string]int64, len(r.placementHighWatermarks))
	for agentID, generation := range r.placementHighWatermarks {
		highWatermarks[agentID] = generation
	}
	cursors := make(map[string]int64, len(r.runtimeLifecycleCursors))
	for runtimeID, seq := range r.runtimeLifecycleCursors {
		cursors[runtimeID] = seq
	}
	raw, err := json.Marshal(localAgentAttachmentState{
		Agents: entries, PlacementHighWatermarks: highWatermarks, RuntimeLifecycleCursors: cursors,
	})
	if err != nil {
		return err
	}
	if r.writeState == nil {
		return errors.New("Agent Attachment registry state writer is not configured")
	}
	return r.writeState(r.path, raw)
}

func writeDaemonStateAtomically(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
