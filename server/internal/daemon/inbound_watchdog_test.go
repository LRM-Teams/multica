package daemon

import (
	"testing"
	"time"
)

func TestInboundWatchdogDefaultIsRaft70s(t *testing.T) {
	if DefaultInboundWatchdog != 70*time.Second {
		t.Fatalf("DefaultInboundWatchdog = %v, want 70s (Raft-aligned lock)", DefaultInboundWatchdog)
	}
}

func TestInboundWatchdogStateSequence(t *testing.T) {
	const interval = 70 * time.Second
	start := time.Unix(1_700_000_000, 0)
	s := newInboundWatchdogState(start)

	// Healthy traffic: inbound before interval → no action.
	if got := s.tick(start.Add(30*time.Second), interval); got != inboundWatchdogNone {
		t.Fatalf("early tick = %v, want none", got)
	}
	s.onInbound(start.Add(40 * time.Second))
	if got := s.tick(start.Add(100*time.Second), interval); got != inboundWatchdogNone {
		// 100-40 = 60s < 70s
		t.Fatalf("after inbound tick = %v, want none", got)
	}

	// Silence reaches interval → probe once.
	if got := s.tick(start.Add(40*time.Second+interval), interval); got != inboundWatchdogProbe {
		t.Fatalf("silence tick = %v, want probe", got)
	}
	// Second tick before second interval elapses → still none (probe already sent).
	if got := s.tick(start.Add(40*time.Second+interval+time.Second), interval); got != inboundWatchdogNone {
		t.Fatalf("mid-probe tick = %v, want none", got)
	}

	// Still silent after another full interval from probe → terminate.
	if got := s.tick(start.Add(40*time.Second+2*interval), interval); got != inboundWatchdogTerminate {
		t.Fatalf("post-probe silence = %v, want terminate", got)
	}
}

func TestInboundWatchdogProbeThenRecover(t *testing.T) {
	const interval = 70 * time.Second
	start := time.Unix(1_700_000_000, 0)
	s := newInboundWatchdogState(start)

	if got := s.tick(start.Add(interval), interval); got != inboundWatchdogProbe {
		t.Fatalf("want probe, got %v", got)
	}
	// Server responds after probe.
	s.onInbound(start.Add(interval + 5*time.Second))
	// Must not terminate; clock advances past original probe window.
	if got := s.tick(start.Add(interval+5*time.Second+interval-time.Second), interval); got != inboundWatchdogNone {
		t.Fatalf("after recovery = %v, want none", got)
	}
	// New silence cycle starts from recovery inbound.
	if got := s.tick(start.Add(interval+5*time.Second+interval), interval); got != inboundWatchdogProbe {
		t.Fatalf("second silence cycle = %v, want probe", got)
	}
}

func TestInboundWatchdogDisabledWhenIntervalZero(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	s := newInboundWatchdogState(start)
	if got := s.tick(start.Add(24*time.Hour), 0); got != inboundWatchdogNone {
		t.Fatalf("disabled watchdog = %v, want none", got)
	}
}
