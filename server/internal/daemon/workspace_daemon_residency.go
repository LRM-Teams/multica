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
	launchID        string
	startDispatchID string
	startStopEpoch  uint64
	terminal        bool
	terminalStage   managedRuntimeFailureStage
	terminalReason  string
	terminalDetail  string
	idle            bool
	cooldownUntil   time.Time
}

type agentResidencyStore struct {
	mu      sync.Mutex
	byAgent map[string]agentResidency
	// epochFloor is the tombstone left by clear: the highest startStopEpoch
	// known to belong to an agent's already-cleared launch. It has exactly
	// one entry per agentID this store has ever cleared -- bounded by the
	// WorkspaceDaemon's agent roster, not by how many times an agent has
	// been stopped -- so it cannot grow without limit.
	epochFloor map[string]uint64
	restarts   map[string]context.CancelFunc
	now        func() time.Time
}

const spawnFailCooldown = 15 * time.Second

func newAgentResidencyStore(now func() time.Time) *agentResidencyStore {
	if now == nil {
		now = time.Now
	}
	return &agentResidencyStore{
		byAgent: make(map[string]agentResidency), epochFloor: make(map[string]uint64),
		restarts: make(map[string]context.CancelFunc), now: now,
	}
}

// epochAllowedLocked reports whether a write at startStopEpoch may proceed
// for agentID. It must be called with s.mu held.
//
// Two independent sources of staleness are guarded against:
//   - A live entry: a write older than what the entry already records
//     belongs to a launch already superseded by a newer one for the same
//     agent. Equal is allowed -- rememberLaunch/rememberIdle/rememberFailure
//     for the *same* launch legitimately fire in sequence at the same epoch.
//   - A cleared entry (epochFloor): once Stop clears residency at the
//     stopped launch's own epoch M, any write at or below M belongs to that
//     stopped launch racing in late (its captured epoch can never exceed M).
//     A genuinely new launch always captures an epoch that is *strictly*
//     greater than M, since stop epochs bump only on Stop, never on Start --
//     so ">" here never rejects a legitimate restart.
func (s *agentResidencyStore) epochAllowedLocked(agentID string, startStopEpoch uint64) bool {
	if current, ok := s.byAgent[agentID]; ok && startStopEpoch < current.startStopEpoch {
		return false
	}
	if floor, ok := s.epochFloor[agentID]; ok && startStopEpoch <= floor {
		return false
	}
	return true
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

func (s *agentResidencyStore) rememberLaunch(agentID, runtimeID, launchID, startDispatchID string, startStopEpoch uint64) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.epochAllowedLocked(agentID, startStopEpoch) {
		return
	}
	current := s.byAgent[agentID]
	current.runtimeID = runtimeID
	current.launchID = launchID
	if startDispatchID != "" {
		current.startDispatchID = startDispatchID
	}
	// Record the epoch even though this is a partial update: it is the only
	// write that can happen before rememberIdle/rememberFailure for this
	// launch, so it is what makes clear's tombstone accurate if the launch is
	// stopped before either of those ever runs.
	current.startStopEpoch = startStopEpoch
	s.byAgent[agentID] = current
}

func (s *agentResidencyStore) rememberIdle(agentID, runtimeID, launchID, startDispatchID string, startStopEpoch uint64) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.epochAllowedLocked(agentID, startStopEpoch) {
		return
	}
	current := s.byAgent[agentID]
	s.byAgent[agentID] = agentResidency{
		runtimeID: runtimeID, launchID: launchID, startDispatchID: firstNonEmpty(startDispatchID, current.startDispatchID),
		startStopEpoch: startStopEpoch, idle: true,
	}
}

func (s *agentResidencyStore) rememberFailure(agentID, runtimeID, launchID string, startStopEpoch uint64, stage managedRuntimeFailureStage, reason, detail string) {
	if s == nil || agentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.epochAllowedLocked(agentID, startStopEpoch) {
		return
	}
	current := s.byAgent[agentID]
	res := agentResidency{
		runtimeID: runtimeID, launchID: launchID, startDispatchID: current.startDispatchID,
		startStopEpoch: startStopEpoch,
		terminalStage:  stage, terminalReason: reason, terminalDetail: detail,
	}
	if stage == managedRuntimeFailureSpawn {
		res.idle = true
		res.cooldownUntil = s.now().Add(spawnFailCooldown)
	} else {
		res.terminal = true
	}
	s.byAgent[agentID] = res
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
	// Leave a tombstone at the epoch of the launch being cleared, rather than
	// simply deleting: without it, a write belonging to that exact launch
	// (e.g. provider startup losing its race with this Stop) would find no
	// entry to compare against and would be accepted again, resurrecting the
	// residency Stop just cleared. The tombstone is one uint64 reused across
	// every future clear for this agentID (monotonic max), not one entry per
	// clear, so it cannot grow without bound.
	if current, ok := s.byAgent[agentID]; ok {
		// A plain map read defaults to 0 for a never-seen agentID, which is
		// indistinguishable from "the floor is genuinely 0" -- so the
		// presence check (has) must gate the write, not just the value
		// comparison, or the very first clear at epoch 0 would leave no
		// floor entry at all and silently disable the guard for that agent.
		if existing, has := s.epochFloor[agentID]; !has || current.startStopEpoch > existing {
			s.epochFloor[agentID] = current.startStopEpoch
		}
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
