package daemon

import (
	"testing"
	"time"
)

// TestResidentCrashBackoffTrackerEscalatesBackoffThenTerminal pins the
// retry-cap contract task #42②'s spec requires: crashes within the window
// escalate through the backoff schedule, and once the count exceeds the cap
// the runtime is flagged terminal (auto-recovery stops) instead of retrying
// forever.
func TestResidentCrashBackoffTrackerEscalatesBackoffThenTerminal(t *testing.T) {
	tracker := newResidentCrashBackoffTracker(10*time.Minute, 3)
	base := time.Unix(1000, 0)

	attempt, backoff, terminal := tracker.recordCrash("agent-a", "runtime-a", base)
	if attempt != 1 || backoff != 5*time.Second || terminal {
		t.Fatalf("1st crash = attempt %d backoff %v terminal %v, want 1 5s false", attempt, backoff, terminal)
	}
	attempt, backoff, terminal = tracker.recordCrash("agent-a", "runtime-a", base.Add(1*time.Minute))
	if attempt != 2 || backoff != 15*time.Second || terminal {
		t.Fatalf("2nd crash = attempt %d backoff %v terminal %v, want 2 15s false", attempt, backoff, terminal)
	}
	attempt, backoff, terminal = tracker.recordCrash("agent-a", "runtime-a", base.Add(2*time.Minute))
	if attempt != 3 || backoff != 60*time.Second || terminal {
		t.Fatalf("3rd crash = attempt %d backoff %v terminal %v, want 3 60s false", attempt, backoff, terminal)
	}
	if tracker.isTerminal("agent-a", "runtime-a") {
		t.Fatal("should not be terminal at exactly the cap")
	}

	// 4th crash within the window exceeds the cap of 3.
	attempt, _, terminal = tracker.recordCrash("agent-a", "runtime-a", base.Add(3*time.Minute))
	if attempt != 4 || !terminal {
		t.Fatalf("4th crash = attempt %d terminal %v, want 4 true", attempt, terminal)
	}
	if !tracker.isTerminal("agent-a", "runtime-a") {
		t.Fatal("expected isTerminal=true after exceeding retry cap")
	}
}

// TestResidentCrashBackoffTrackerPrunesOldCrashesOutsideWindow ensures crash
// history outside the rolling window does not count against the cap — a
// runtime that crashed a long time ago and has been fine since must not be
// permanently penalized.
func TestResidentCrashBackoffTrackerPrunesOldCrashesOutsideWindow(t *testing.T) {
	tracker := newResidentCrashBackoffTracker(10*time.Minute, 2)
	base := time.Unix(2000, 0)

	tracker.recordCrash("agent-b", "runtime-b", base)
	tracker.recordCrash("agent-b", "runtime-b", base.Add(1*time.Minute))
	if _, _, terminal := tracker.recordCrash("agent-b", "runtime-b", base.Add(2*time.Minute)); !terminal {
		t.Fatal("expected terminal after 3 crashes within a 2-crash cap")
	}

	// A 4th crash long after the window has fully elapsed should only see
	// itself — the first three are pruned, so this is attempt 1 again.
	attempt, _, terminal := tracker.recordCrash("agent-b", "runtime-b", base.Add(30*time.Minute))
	if attempt != 1 || terminal {
		t.Fatalf("crash outside window = attempt %d terminal %v, want 1 false", attempt, terminal)
	}
}

// TestResidentCrashBackoffTrackerClearResetsAfterManualRestart pins the
// #42① integration point: a manual restart must give the runtime a fresh
// retry budget instead of leaving stale crash history that immediately
// re-trips the cap.
func TestResidentCrashBackoffTrackerClearResetsAfterManualRestart(t *testing.T) {
	tracker := newResidentCrashBackoffTracker(10*time.Minute, 1)
	base := time.Unix(3000, 0)

	tracker.recordCrash("agent-c", "runtime-c", base)
	if _, _, terminal := tracker.recordCrash("agent-c", "runtime-c", base.Add(time.Second)); !terminal {
		t.Fatal("expected terminal after exceeding cap of 1")
	}
	if !tracker.isTerminal("agent-c", "runtime-c") {
		t.Fatal("expected isTerminal=true before clear")
	}

	tracker.clear("agent-c", "runtime-c")
	if tracker.isTerminal("agent-c", "runtime-c") {
		t.Fatal("expected isTerminal=false immediately after clear")
	}
	attempt, _, terminal := tracker.recordCrash("agent-c", "runtime-c", base.Add(time.Hour))
	if attempt != 1 || terminal {
		t.Fatalf("crash after clear = attempt %d terminal %v, want 1 false", attempt, terminal)
	}
}

// TestResidentCrashBackoffTrackerIsolatesDifferentRuntimes ensures one
// agent×runtime's crash-loop does not spuriously flag an unrelated slot.
func TestResidentCrashBackoffTrackerIsolatesDifferentRuntimes(t *testing.T) {
	tracker := newResidentCrashBackoffTracker(10*time.Minute, 1)
	base := time.Unix(4000, 0)

	tracker.recordCrash("agent-d", "runtime-d", base)
	tracker.recordCrash("agent-d", "runtime-d", base.Add(time.Second))
	if !tracker.isTerminal("agent-d", "runtime-d") {
		t.Fatal("expected agent-d/runtime-d terminal")
	}
	if tracker.isTerminal("agent-e", "runtime-e") {
		t.Fatal("unrelated agent-e/runtime-e must not be affected")
	}
}
