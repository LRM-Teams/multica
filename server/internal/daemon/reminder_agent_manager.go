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

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const reminderAgentStateFile = "reminder_agents.json"

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

// reminderAgentManager is the daemon-local source of running and idle Agents
// used for Reminder reconnect snapshots. It deliberately does not accept an
// owner inventory from the server. Agents become resident when this daemon
// executes them and remain idle residents across process restarts.
type reminderAgentManager struct {
	mu                      sync.Mutex
	agents                  map[string]reminderAgentResidency
	placementHighWatermarks map[string]int64
	runtimeLifecycleCursors map[string]int64
	root                    string
	path                    string
	logger                  *slog.Logger
	writeState              func(string, []byte) error
}

func newReminderAgentManager(workspacesRoot string, logger *slog.Logger) *reminderAgentManager {
	m := &reminderAgentManager{
		agents:                  make(map[string]reminderAgentResidency),
		placementHighWatermarks: make(map[string]int64),
		runtimeLifecycleCursors: make(map[string]int64),
		root:                    strings.TrimSpace(workspacesRoot),
		logger:                  logger,
		writeState:              writeReminderAgentState,
	}
	if m.root != "" {
		m.path = filepath.Join(m.root, ".daemon", reminderAgentStateFile)
		m.load()
		m.bootstrapFromLocalAgentConfigs()
		if err := m.persistLocked(); err != nil && m.logger != nil {
			m.logger.Warn("persist reminder agent residency failed", "error", err)
		}
	}
	return m
}

func (m *reminderAgentManager) markRunning(agentID, runtimeID, workspaceID string) bool {
	if m == nil || strings.TrimSpace(agentID) == "" {
		return false
	}
	m.mu.Lock()
	previous, existed := m.agents[agentID]
	entry, existed := m.agents[agentID]
	if !existed && m.placementHighWatermarks[agentID] > 0 {
		m.mu.Unlock()
		return false
	}
	entry.AgentID = agentID
	entry.RuntimeID = runtimeID
	entry.WorkspaceID = workspaceID
	entry.Running++
	m.agents[agentID] = entry
	if err := m.persistLocked(); err != nil {
		if existed {
			m.agents[agentID] = previous
		} else {
			delete(m.agents, agentID)
		}
		if m.logger != nil {
			m.logger.Warn("persist running reminder owner failed", "error", err, "agent_id", agentID)
		}
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()
	return !existed
}

func (m *reminderAgentManager) markIdle(agentID string) {
	if m == nil || strings.TrimSpace(agentID) == "" {
		return
	}
	m.mu.Lock()
	entry, ok := m.agents[agentID]
	previous := entry
	if ok && entry.Running > 0 {
		entry.Running--
		m.agents[agentID] = entry
	}
	if err := m.persistLocked(); err != nil {
		if ok {
			m.agents[agentID] = previous
		}
		if m.logger != nil {
			m.logger.Warn("persist idle reminder owner failed", "error", err, "agent_id", agentID)
		}
	}
	m.mu.Unlock()
}

func (m *reminderAgentManager) applyStart(agentID, runtimeID, workspaceID string, generation int64) (bool, bool, error) {
	if m == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(runtimeID) == "" || strings.TrimSpace(workspaceID) == "" || generation < 1 {
		return false, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation < m.placementHighWatermarks[agentID] {
		return false, false, nil
	}
	entry, existed := m.agents[agentID]
	if existed && generation == m.placementHighWatermarks[agentID] && entry.PlacementGeneration == generation && (entry.RuntimeID != runtimeID || entry.WorkspaceID != workspaceID) {
		return false, false, nil
	}
	previousEntry := entry
	previousGeneration, hadGeneration := m.placementHighWatermarks[agentID]
	changed := !existed || entry.RuntimeID != runtimeID || entry.WorkspaceID != workspaceID || entry.PlacementGeneration != generation
	entry.AgentID = agentID
	entry.RuntimeID = runtimeID
	entry.WorkspaceID = workspaceID
	entry.PlacementGeneration = generation
	entry.Running = 0
	m.agents[agentID] = entry
	m.placementHighWatermarks[agentID] = generation
	if err := m.persistLocked(); err != nil {
		if existed {
			m.agents[agentID] = previousEntry
		} else {
			delete(m.agents, agentID)
		}
		if hadGeneration {
			m.placementHighWatermarks[agentID] = previousGeneration
		} else {
			delete(m.placementHighWatermarks, agentID)
		}
		return false, false, err
	}
	return changed, true, nil
}

func (m *reminderAgentManager) residentAgentIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Strings(ids)
	return ids
}

func (m *reminderAgentManager) runtimeResidencies() map[string][]protocol.ReminderRuntimeResidency {
	result := make(map[string][]protocol.ReminderRuntimeResidency)
	if m == nil {
		return result
	}
	m.mu.Lock()
	for _, entry := range m.agents {
		if entry.RuntimeID == "" || entry.AgentID == "" || entry.PlacementGeneration < 1 {
			continue
		}
		result[entry.RuntimeID] = append(result[entry.RuntimeID], protocol.ReminderRuntimeResidency{
			AgentID: entry.AgentID, PlacementGeneration: entry.PlacementGeneration,
		})
	}
	m.mu.Unlock()
	for runtimeID := range result {
		sort.Slice(result[runtimeID], func(i, j int) bool {
			return result[runtimeID][i].AgentID < result[runtimeID][j].AgentID
		})
	}
	return result
}

func (m *reminderAgentManager) get(agentID string) (reminderAgentResidency, bool) {
	if m == nil {
		return reminderAgentResidency{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.agents[agentID]
	return entry, ok
}

func (m *reminderAgentManager) applyStop(agentID, runtimeID string, generation int64) (bool, bool, error) {
	if m == nil || agentID == "" || runtimeID == "" || generation < 1 {
		return false, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation < m.placementHighWatermarks[agentID] {
		return false, false, nil
	}
	entry, ok := m.agents[agentID]
	previousGeneration, hadGeneration := m.placementHighWatermarks[agentID]
	removed := ok && entry.RuntimeID == runtimeID && entry.PlacementGeneration <= generation
	if removed {
		delete(m.agents, agentID)
	}
	m.placementHighWatermarks[agentID] = generation
	if err := m.persistLocked(); err != nil {
		if ok {
			m.agents[agentID] = entry
		}
		if hadGeneration {
			m.placementHighWatermarks[agentID] = previousGeneration
		} else {
			delete(m.placementHighWatermarks, agentID)
		}
		return false, false, err
	}
	return removed, true, nil
}

func (m *reminderAgentManager) lifecycleCursors() map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.runtimeLifecycleCursors))
	for runtimeID, seq := range m.runtimeLifecycleCursors {
		out[runtimeID] = seq
	}
	return out
}

func (m *reminderAgentManager) reconcileRuntimeSet(allowed map[string]bool) (bool, error) {
	if m == nil {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previousAgents := make(map[string]reminderAgentResidency, len(m.agents))
	for agentID, entry := range m.agents {
		previousAgents[agentID] = entry
	}
	previousHighWatermarks := make(map[string]int64, len(m.placementHighWatermarks))
	for agentID, generation := range m.placementHighWatermarks {
		previousHighWatermarks[agentID] = generation
	}
	previousCursors := make(map[string]int64, len(m.runtimeLifecycleCursors))
	for runtimeID, seq := range m.runtimeLifecycleCursors {
		previousCursors[runtimeID] = seq
	}
	changed := false
	for agentID, entry := range m.agents {
		if allowed[entry.RuntimeID] {
			continue
		}
		delete(m.agents, agentID)
		generation := entry.PlacementGeneration
		if generation < 1 {
			generation = 1
		}
		if generation > m.placementHighWatermarks[agentID] {
			m.placementHighWatermarks[agentID] = generation
		}
		changed = true
	}
	for runtimeID := range m.runtimeLifecycleCursors {
		if !allowed[runtimeID] {
			delete(m.runtimeLifecycleCursors, runtimeID)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	if err := m.persistLocked(); err != nil {
		m.agents = previousAgents
		m.placementHighWatermarks = previousHighWatermarks
		m.runtimeLifecycleCursors = previousCursors
		return false, err
	}
	return true, nil
}

func (m *reminderAgentManager) retiredAgentIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0)
	for agentID := range m.placementHighWatermarks {
		if _, active := m.agents[agentID]; !active {
			ids = append(ids, agentID)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *reminderAgentManager) advanceLifecycleCursors(cursors map[string]int64) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	previous := make(map[string]int64, len(m.runtimeLifecycleCursors))
	for runtimeID, seq := range m.runtimeLifecycleCursors {
		previous[runtimeID] = seq
	}
	for runtimeID, seq := range cursors {
		if runtimeID != "" && seq > m.runtimeLifecycleCursors[runtimeID] {
			m.runtimeLifecycleCursors[runtimeID] = seq
		}
	}
	err := m.persistLocked()
	if err != nil {
		m.runtimeLifecycleCursors = previous
	}
	m.mu.Unlock()
	return err
}

func (m *reminderAgentManager) load() {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		if !os.IsNotExist(err) && m.logger != nil {
			m.logger.Warn("load reminder agent residency failed", "error", err)
		}
		return
	}
	var state reminderAgentState
	if err := json.Unmarshal(raw, &state); err != nil {
		if m.logger != nil {
			m.logger.Warn("decode reminder agent residency failed", "error", err)
		}
		return
	}
	for _, entry := range state.Agents {
		if entry.AgentID == "" {
			continue
		}
		entry.Running = 0
		m.agents[entry.AgentID] = entry
	}
	for _, agentID := range state.RemovedAgentIDs {
		if agentID != "" {
			m.placementHighWatermarks[agentID] = 1
		}
	}
	for agentID, generation := range state.PlacementHighWatermarks {
		if agentID != "" && generation > m.placementHighWatermarks[agentID] {
			m.placementHighWatermarks[agentID] = generation
		}
	}
	for runtimeID, seq := range state.RuntimeLifecycleCursors {
		if runtimeID != "" && seq > 0 {
			m.runtimeLifecycleCursors[runtimeID] = seq
		}
	}
}

// bootstrapFromLocalAgentConfigs upgrades an existing daemon without waiting
// for another task to execute. A durable agent credential is daemon-local
// recoverable config and contains the exact workspace/runtime/agent binding
// that produced the local Agent home. Removed owners are tombstoned so stale
// config directories cannot resurrect them after archive or runtime migration.
func (m *reminderAgentManager) bootstrapFromLocalAgentConfigs() {
	if m == nil || m.root == "" {
		return
	}
	workspaces, err := os.ReadDir(m.root)
	if err != nil {
		if !os.IsNotExist(err) && m.logger != nil {
			m.logger.Warn("scan reminder agent configs failed", "error", err)
		}
		return
	}
	for _, workspace := range workspaces {
		if !workspace.IsDir() || strings.HasPrefix(workspace.Name(), ".") {
			continue
		}
		agentsRoot := filepath.Join(m.root, workspace.Name(), ".multica", "agents")
		agents, readErr := os.ReadDir(agentsRoot)
		if readErr != nil {
			continue
		}
		for _, agentDir := range agents {
			if !agentDir.IsDir() {
				continue
			}
			configPath := filepath.Join(agentsRoot, agentDir.Name(), "runtime", "credentials", "current.json")
			raw, readErr := os.ReadFile(configPath)
			if readErr != nil {
				continue
			}
			var config cachedAgentCredential
			if json.Unmarshal(raw, &config) != nil || config.AgentID == "" || config.AgentID != agentDir.Name() || config.WorkspaceID != workspace.Name() {
				continue
			}
			if m.placementHighWatermarks[config.AgentID] > 0 {
				continue
			}
			if _, exists := m.agents[config.AgentID]; exists {
				continue
			}
			m.agents[config.AgentID] = reminderAgentResidency{
				AgentID:     config.AgentID,
				RuntimeID:   config.RuntimeID,
				WorkspaceID: config.WorkspaceID,
			}
		}
	}
}

func (m *reminderAgentManager) persistLocked() error {
	if m.path == "" {
		return nil
	}
	entries := make([]reminderAgentResidency, 0, len(m.agents))
	for _, entry := range m.agents {
		entry.Running = 0
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].AgentID < entries[j].AgentID })
	highWatermarks := make(map[string]int64, len(m.placementHighWatermarks))
	for agentID, generation := range m.placementHighWatermarks {
		highWatermarks[agentID] = generation
	}
	cursors := make(map[string]int64, len(m.runtimeLifecycleCursors))
	for runtimeID, seq := range m.runtimeLifecycleCursors {
		cursors[runtimeID] = seq
	}
	raw, err := json.Marshal(reminderAgentState{Agents: entries, PlacementHighWatermarks: highWatermarks, RuntimeLifecycleCursors: cursors})
	if err != nil {
		return err
	}
	if m.writeState == nil {
		return errors.New("reminder agent state writer is not configured")
	}
	return m.writeState(m.path, raw)
}

func writeReminderAgentState(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Unique temp name: a fixed path+".tmp" races when two daemon processes
	// (crash-loop / overlapping supervise restarts) write the same state file.
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
