package daemon

import (
	"context"
	"sync"
	"time"
)

// agentResidency is the Raft 1.0.16 no-process continuation: what to do with a
// Delivery after APM no longer has a live launch. It is not a Message ledger.
type agentResidency struct {
	runtimeID       string
	agentInstanceID string
	terminal        bool
	terminalStage   managedRuntimeFailureStage
	terminalReason  string
	terminalDetail  string
	idle            bool
	cooldownUntil   time.Time
}

type agentResidencyStore struct {
	mu       sync.Mutex
	byAgent  map[string]agentResidency
	restarts map[string]context.CancelFunc
	now      func() time.Time
}

const spawnFailCooldown = 15 * time.Second

func newAgentResidencyStore(now func() time.Time) *agentResidencyStore {
	if now == nil {
		now = time.Now
	}
	return &agentResidencyStore{
		byAgent:  make(map[string]agentResidency),
		restarts: make(map[string]context.CancelFunc), now: now,
	}
}

func (s *agentResidencyStore) get(agentID string) (agentResidency, bool) {
	if s == nil {
		return agentResidency{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.byAgent[agentID]
	return res, ok
}

func (s *agentResidencyStore) rememberLaunch(agentID, runtimeID, agentInstanceID string) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.byAgent[agentID]
	current.runtimeID = runtimeID
	current.agentInstanceID = agentInstanceID
	s.byAgent[agentID] = current
}

func (s *agentResidencyStore) rememberIdle(agentID, runtimeID, agentInstanceID string) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.byAgent[agentID]; !ok || current.agentInstanceID != agentInstanceID {
		return
	}
	s.byAgent[agentID] = agentResidency{
		runtimeID: runtimeID, agentInstanceID: agentInstanceID,
		idle: true,
	}
}

func (s *agentResidencyStore) rememberFailure(agentID, runtimeID, agentInstanceID string, stage managedRuntimeFailureStage, reason, detail string) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.byAgent[agentID]; !ok || current.agentInstanceID != agentInstanceID {
		return
	}
	res := agentResidency{
		runtimeID: runtimeID, agentInstanceID: agentInstanceID,
		terminalStage: stage, terminalReason: reason, terminalDetail: detail,
	}
	if stage == managedRuntimeFailureSpawn {
		res.idle = true
		res.cooldownUntil = s.now().Add(spawnFailCooldown)
	} else {
		res.terminal = true
	}
	s.byAgent[agentID] = res
}

func (s *agentResidencyStore) replaceIdleInstance(agentID, runtimeID, previousAgentInstanceID, agentInstanceID string) bool {
	if s == nil || previousAgentInstanceID == "" || agentInstanceID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.byAgent[agentID]
	if !ok || current.runtimeID != runtimeID || current.agentInstanceID != previousAgentInstanceID || !current.idle {
		return false
	}
	current.agentInstanceID = agentInstanceID
	s.byAgent[agentID] = current
	return true
}

func (s *agentResidencyStore) beginRestart(parent context.Context, agentID string) (context.Context, bool) {
	if s == nil || agentID == "" {
		return parent, false
	}
	if parent == nil {
		parent = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.restarts[agentID]; busy {
		return parent, false
	}
	ctx, cancel := context.WithCancel(parent)
	s.restarts[agentID] = cancel
	return ctx, true
}

func (s *agentResidencyStore) endRestart(agentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.restarts[agentID]; ok {
		cancel()
		delete(s.restarts, agentID)
	}
}

func (s *agentResidencyStore) clear(agentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.restarts[agentID]; ok {
		cancel()
		delete(s.restarts, agentID)
	}
	delete(s.byAgent, agentID)
}

func (s *agentResidencyStore) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for agentID, cancel := range s.restarts {
		cancel()
		delete(s.restarts, agentID)
	}
}

func (r agentResidency) coolingDown(now time.Time) bool {
	return !r.cooldownUntil.IsZero() && now.Before(r.cooldownUntil)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
