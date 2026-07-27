package daemon

import (
	"sync"
	"time"
)

// DefaultInboundWatchdog is the Raft-aligned silence threshold before the
// daemon-ws connection sends a liveness probe and, if still silent, forces
// reconnect. Locked at 70s for production defaults and unit tests that assert
// the default; operators may override via MULTICA_DAEMON_INBOUND_WATCHDOG.
// Set the env to 0 to disable.
const DefaultInboundWatchdog = 70 * time.Second

// inboundWatchdogAction is the pure decision produced by inboundWatchdogState.Tick.
type inboundWatchdogAction int

const (
	inboundWatchdogNone inboundWatchdogAction = iota
	inboundWatchdogProbe
	inboundWatchdogTerminate
)

// inboundWatchdogState is a pure two-phase silence detector:
//
//  1. After `interval` with no inbound frames → Probe (send liveness probe).
//  2. After another `interval` still without inbound since the probe → Terminate.
//
// Any inbound frame resets both phases. interval <= 0 disables the detector.
type inboundWatchdogState struct {
	mu          sync.Mutex
	lastInbound time.Time
	probeSentAt time.Time // zero means no probe outstanding
}

func newInboundWatchdogState(now time.Time) *inboundWatchdogState {
	return &inboundWatchdogState{lastInbound: now}
}

func (s *inboundWatchdogState) onInbound(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastInbound = now
	s.probeSentAt = time.Time{}
}

// tick evaluates silence at `now`. Call periodically (e.g. every second or
// interval/10). Safe for concurrent use with onInbound.
func (s *inboundWatchdogState) tick(now time.Time, interval time.Duration) inboundWatchdogAction {
	if interval <= 0 {
		return inboundWatchdogNone
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.Sub(s.lastInbound) < interval {
		return inboundWatchdogNone
	}
	if s.probeSentAt.IsZero() {
		s.probeSentAt = now
		return inboundWatchdogProbe
	}
	if now.Sub(s.probeSentAt) >= interval {
		return inboundWatchdogTerminate
	}
	return inboundWatchdogNone
}
