package daemon

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/agentworkspace"
)

const reminderAgentStateFile = "reminder_agents.json"

// These wire structs deliberately retain the existing reminder_agents.json
// field names. Agent Attachment extraction must not require a state migration.
type reminderAgentState struct {
	Agents                  []reminderAgentResidency `json:"agents"`
	RemovedAgentIDs         []string                 `json:"removed_agent_ids,omitempty"`
	PlacementHighWatermarks map[string]int64         `json:"placement_high_watermarks,omitempty"`
	RuntimeLifecycleCursors map[string]int64         `json:"runtime_lifecycle_cursors,omitempty"`
}

type reminderAgentResidency struct {
	AgentID             string `json:"agent_id"`
	RuntimeID           string `json:"runtime_id"`
	WorkspaceID         string `json:"workspace_id"`
	PlacementGeneration int64  `json:"placement_generation"`
	Running             int    `json:"-"`
}

// localAgentAttachmentRegistry owns the durable state that predates the
// Agent Attachment vocabulary. The reminderAgentManager facade remains during
// caller migration, but persistence and local bootstrap no longer belong to
// Reminder projection code.
type localAgentAttachmentRegistry struct {
	mu                      sync.Mutex
	agents                  map[string]reminderAgentResidency
	placementHighWatermarks map[string]int64
	runtimeLifecycleCursors map[string]int64
	root                    string
	path                    string
	logger                  *slog.Logger
	writeState              func(string, []byte) error
}

func newLocalAgentAttachmentRegistry(workspacesRoot string, logger *slog.Logger) *localAgentAttachmentRegistry {
	registry := &localAgentAttachmentRegistry{
		agents:                  make(map[string]reminderAgentResidency),
		placementHighWatermarks: make(map[string]int64),
		runtimeLifecycleCursors: make(map[string]int64),
		root:                    strings.TrimSpace(workspacesRoot),
		logger:                  logger,
		writeState:              writeReminderAgentState,
	}
	if registry.root != "" {
		registry.path = filepath.Join(registry.root, ".daemon", reminderAgentStateFile)
		registry.load()
		registry.bootstrapFromLocalAgentConfigs()
		if err := registry.persistLocked(); err != nil && registry.logger != nil {
			registry.logger.Warn("persist Agent Attachment registry failed", "error", err)
		}
	}
	return registry
}

func (r *localAgentAttachmentRegistry) load() {
	raw, err := os.ReadFile(r.path)
	if err != nil {
		if !os.IsNotExist(err) && r.logger != nil {
			r.logger.Warn("load Agent Attachment registry failed", "error", err)
		}
		return
	}
	var state reminderAgentState
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
		entry.Running = 0
		r.agents[entry.AgentID] = entry
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
	if r == nil || r.root == "" {
		return
	}
	workspaces, err := os.ReadDir(r.root)
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
		agentsRoot := agentworkspace.AgentsDir(r.root, workspace.Name())
		agents, readErr := os.ReadDir(agentsRoot)
		if readErr != nil {
			continue
		}
		for _, agentDir := range agents {
			if !agentDir.IsDir() {
				continue
			}
			configPath := filepath.Join(agentworkspace.Root(r.root, workspace.Name(), agentDir.Name()), "runtime", "credentials", "current.json")
			raw, readErr := os.ReadFile(configPath)
			if readErr != nil {
				continue
			}
			var config cachedAgentCredential
			if json.Unmarshal(raw, &config) != nil || config.AgentID == "" || config.AgentID != agentDir.Name() || config.WorkspaceID != workspace.Name() {
				continue
			}
			if r.placementHighWatermarks[config.AgentID] > 0 {
				continue
			}
			if _, exists := r.agents[config.AgentID]; exists {
				continue
			}
			r.agents[config.AgentID] = reminderAgentResidency{
				AgentID: config.AgentID, RuntimeID: config.RuntimeID, WorkspaceID: config.WorkspaceID,
			}
		}
	}
}

func (r *localAgentAttachmentRegistry) persistLocked() error {
	if r.path == "" {
		return nil
	}
	entries := make([]reminderAgentResidency, 0, len(r.agents))
	for _, entry := range r.agents {
		entry.Running = 0
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
	raw, err := json.Marshal(reminderAgentState{
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

func writeReminderAgentState(path string, raw []byte) error {
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
