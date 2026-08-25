package handler

import (
	"sync"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type runnerObservationScope struct {
	workspaceID      string
	daemonID         string
	daemonInstanceID string
}

type runnerObservedAgent struct {
	workspaceID      string
	agentID          string
	runtimeID        string
	status           string
	daemonID         string
	daemonInstanceID string
	sessionID        string
}

// runnerObservationStore is the live Computer process, not a cache of a
// table. Presence, Activity authorization, and restart stop/session look
// here. A new server process starts empty and waits for the current socket
// to report again.
type runnerObservationStore struct {
	mu    sync.Mutex
	notes map[runnerObservationScope]map[string]runnerObservedAgent
}

func newRunnerObservationStore() *runnerObservationStore {
	return &runnerObservationStore{notes: make(map[runnerObservationScope]map[string]runnerObservedAgent)}
}

func (s *runnerObservationStore) acceptStatus(workspaceID, daemonID, daemonInstanceID, agentID, runtimeID, status string) bool {
	if s == nil || agentID == "" || daemonID == "" || daemonInstanceID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.findLocked(workspaceID, agentID)
	sameRuntime := ok && current.daemonID == daemonID && current.daemonInstanceID == daemonInstanceID && current.runtimeID == runtimeID
	if sameRuntime && current.runtimeID != "" {
		runtimeID = current.runtimeID
	}
	sessionID := ""
	if sameRuntime {
		sessionID = current.sessionID
	}
	s.evictAgentLocked(workspaceID, agentID)
	s.writeLocked(runnerObservedAgent{
		workspaceID: workspaceID, agentID: agentID, runtimeID: runtimeID,
		status: status, daemonID: daemonID,
		daemonInstanceID: daemonInstanceID, sessionID: sessionID,
	})
	return true
}

func (s *runnerObservationStore) acceptStartAck(workspaceID, daemonID, daemonInstanceID, agentID, runtimeID string) (string, bool) {
	if s == nil || agentID == "" || daemonID == "" || daemonInstanceID == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.findLocked(workspaceID, agentID)
	sameRuntime := ok && current.daemonID == daemonID && current.daemonInstanceID == daemonInstanceID && current.runtimeID == runtimeID
	status := "accepted"
	if sameRuntime && current.status == protocol.AgentStatusActive {
		status = protocol.AgentStatusActive
	}
	if sameRuntime && current.runtimeID != "" {
		runtimeID = current.runtimeID
	}
	sessionID := ""
	if sameRuntime {
		sessionID = current.sessionID
	}
	s.evictAgentLocked(workspaceID, agentID)
	s.writeLocked(runnerObservedAgent{
		workspaceID: workspaceID, agentID: agentID, runtimeID: runtimeID,
		status: status, daemonID: daemonID,
		daemonInstanceID: daemonInstanceID, sessionID: sessionID,
	})
	return status, true
}

func (s *runnerObservationStore) acceptSession(workspaceID, daemonID, daemonInstanceID, agentID, sessionID string) bool {
	if s == nil || agentID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.findLocked(workspaceID, agentID)
	if !ok || current.daemonID != daemonID || current.daemonInstanceID != daemonInstanceID {
		return false
	}
	if current.status != "accepted" && current.status != protocol.AgentStatusActive {
		return false
	}
	current.sessionID = sessionID
	s.writeLocked(current)
	return true
}

func (s *runnerObservationStore) putStatus(workspaceID, daemonID, daemonInstanceID, agentID, runtimeID, status string) {
	s.acceptStatus(workspaceID, daemonID, daemonInstanceID, agentID, runtimeID, status)
}

func (s *runnerObservationStore) putSession(workspaceID, daemonID, daemonInstanceID, agentID, sessionID string) {
	s.acceptSession(workspaceID, daemonID, daemonInstanceID, agentID, sessionID)
}

func (s *runnerObservationStore) deactivate(workspaceID, daemonID, daemonInstanceID, agentID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.findLocked(workspaceID, agentID)
	if !ok || current.daemonID != daemonID || current.daemonInstanceID != daemonInstanceID {
		return false
	}
	if current.status != protocol.AgentStatusActive {
		return false
	}
	current.status = protocol.AgentStatusInactive
	s.writeLocked(current)
	return true
}

func (s *runnerObservationStore) get(workspaceID, agentID string) (runnerObservedAgent, bool) {
	if s == nil {
		return runnerObservedAgent{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findLocked(workspaceID, agentID)
}

func (s *runnerObservationStore) listWorkspace(workspaceID string) []runnerObservedAgent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runnerObservedAgent, 0)
	for scope, agents := range s.notes {
		if scope.workspaceID != workspaceID {
			continue
		}
		for _, obs := range agents {
			out = append(out, obs)
		}
	}
	return out
}

func (s *runnerObservationStore) listInstance(workspaceID, daemonID, daemonInstanceID string) []runnerObservedAgent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	agents := s.notes[runnerObservationScope{workspaceID: workspaceID, daemonID: daemonID, daemonInstanceID: daemonInstanceID}]
	out := make([]runnerObservedAgent, 0, len(agents))
	for _, obs := range agents {
		out = append(out, obs)
	}
	return out
}

func (s *runnerObservationStore) forgetInstance(workspaceID, daemonID, daemonInstanceID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.notes, runnerObservationScope{workspaceID: workspaceID, daemonID: daemonID, daemonInstanceID: daemonInstanceID})
}

func (s *runnerObservationStore) forgetOtherInstances(workspaceID, daemonID, keepInstanceID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for scope := range s.notes {
		if scope.workspaceID == workspaceID && scope.daemonID == daemonID && scope.daemonInstanceID != keepInstanceID {
			delete(s.notes, scope)
		}
	}
}

func (s *runnerObservationStore) findLocked(workspaceID, agentID string) (runnerObservedAgent, bool) {
	for scope, agents := range s.notes {
		if scope.workspaceID != workspaceID {
			continue
		}
		if obs, ok := agents[agentID]; ok {
			return obs, true
		}
	}
	return runnerObservedAgent{}, false
}

func (s *runnerObservationStore) evictAgentLocked(workspaceID, agentID string) {
	for scope, agents := range s.notes {
		if scope.workspaceID != workspaceID {
			continue
		}
		delete(agents, agentID)
		if len(agents) == 0 {
			delete(s.notes, scope)
		}
	}
}

func (s *runnerObservationStore) writeLocked(obs runnerObservedAgent) {
	if s.notes == nil {
		s.notes = make(map[runnerObservationScope]map[string]runnerObservedAgent)
	}
	scope := runnerObservationScope{workspaceID: obs.workspaceID, daemonID: obs.daemonID, daemonInstanceID: obs.daemonInstanceID}
	agents := s.notes[scope]
	if agents == nil {
		agents = make(map[string]runnerObservedAgent)
		s.notes[scope] = agents
	}
	agents[obs.agentID] = obs
}

func (h *Handler) observations() *runnerObservationStore {
	if h == nil {
		return nil
	}
	if h.runnerObservations == nil {
		h.runnerObservations = newRunnerObservationStore()
	}
	return h.runnerObservations
}
