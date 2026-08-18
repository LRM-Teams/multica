package handler

import (
	"sync"

	"github.com/google/uuid"
)

// agentRestartStore is one in-flight restart on this server process.
// It is not durable and is not replayed after disconnect.
type agentRestartStore struct {
	mu    sync.Mutex
	notes map[string]activeAgentRestartState
}

func newAgentRestartStore() *agentRestartStore {
	return &agentRestartStore{notes: make(map[string]activeAgentRestartState)}
}

func (s *agentRestartStore) begin(state activeAgentRestartState) (activeAgentRestartState, bool) {
	if s == nil || state.agentID == "" {
		return activeAgentRestartState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.notes == nil {
		s.notes = make(map[string]activeAgentRestartState)
	}
	if _, exists := s.notes[state.agentID]; exists {
		return activeAgentRestartState{}, false
	}
	if state.operationID == "" {
		state.operationID = uuid.NewString()
	}
	if state.step == "" {
		state.step = agentRestartStepStopping
	}
	s.notes[state.agentID] = state
	return state, true
}

func (s *agentRestartStore) get(agentID string) (activeAgentRestartState, bool) {
	if s == nil {
		return activeAgentRestartState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.notes[agentID]
	return state, ok
}

func (s *agentRestartStore) update(agentID string, mutate func(*activeAgentRestartState) bool) (activeAgentRestartState, bool) {
	if s == nil || mutate == nil {
		return activeAgentRestartState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.notes[agentID]
	if !ok {
		return activeAgentRestartState{}, false
	}
	if !mutate(&state) {
		return state, false
	}
	s.notes[agentID] = state
	return state, true
}

func (s *agentRestartStore) finish(agentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.notes, agentID)
}

func (s *agentRestartStore) has(agentID string) bool {
	_, ok := s.get(agentID)
	return ok
}

func (s *agentRestartStore) agentIDs() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.notes))
	for agentID := range s.notes {
		out = append(out, agentID)
	}
	return out
}

func (h *Handler) restarts() *agentRestartStore {
	if h == nil {
		return nil
	}
	if h.agentRestarts == nil {
		h.agentRestarts = newAgentRestartStore()
	}
	return h.agentRestarts
}
