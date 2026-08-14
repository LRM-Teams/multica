package computer

import "time"

const (
	// RunnerReconcileInterval is Raft Computer's RECONCILE_INTERVAL_MS.
	RunnerReconcileInterval = 5 * time.Second
	// RunnerRestartBackoff is Raft Computer's CHILD_RESTART_BACKOFF_MS.
	RunnerRestartBackoff = 2 * time.Second
	// RunnerCrashWindow is Raft Computer's CRASH_WINDOW_MS.
	RunnerCrashWindow = 60 * time.Second
	// RunnerDegradedThreshold is Raft Computer's DEGRADED_THRESHOLD.
	RunnerDegradedThreshold = 3
)

type RunnerLifecycle string

const (
	RunnerLifecycleStopped  RunnerLifecycle = "stopped"
	RunnerLifecycleStarting RunnerLifecycle = "starting"
	RunnerLifecycleRunning  RunnerLifecycle = "running"
	RunnerLifecycleCrashed  RunnerLifecycle = "crashed"
	RunnerLifecycleDegraded RunnerLifecycle = "degraded"
)

type RunnerExitClass string

const (
	RunnerExitGraceful RunnerExitClass = "graceful"
	RunnerExitCrash    RunnerExitClass = "crash"
	RunnerExitUnlinked RunnerExitClass = "unlinked"
)

// RunnerRecord is one Binding's desired-vs-actual slot on the Computer host.
// BindingRunner is the OS-child adapter; CanSpawn / ObserveExit stay shared.
//
// generation increments on each ObserveSpawn. It is the host-side equivalent
// of Raft Computer's inactive_process_generation fence (current !== process):
// an exit from a previous supervise generation must not mutate the live slot.
type RunnerRecord struct {
	Lifecycle    RunnerLifecycle
	BackoffUntil time.Time

	generation int64
	child      bool
	crashes    []time.Time
}

func (r *RunnerRecord) Generation() int64 {
	if r == nil {
		return 0
	}
	return r.generation
}

func (r *RunnerRecord) HasChild() bool {
	return r != nil && r.child
}

func (r *RunnerRecord) CanSpawn(wanted bool, now time.Time) bool {
	if r == nil {
		return wanted
	}
	if !wanted || r.child {
		return false
	}
	if r.Lifecycle != RunnerLifecycleCrashed && r.Lifecycle != RunnerLifecycleStopped {
		return false
	}
	return !now.Before(r.BackoffUntil)
}

func (r *RunnerRecord) ObserveSpawn() {
	if r == nil {
		return
	}
	r.generation++
	r.child = true
	r.Lifecycle = RunnerLifecycleStarting
	r.BackoffUntil = time.Time{}
}

func (r *RunnerRecord) ObserveReady(generation int64) bool {
	if r == nil || !r.child || r.generation != generation || r.Lifecycle != RunnerLifecycleStarting {
		return false
	}
	r.Lifecycle = RunnerLifecycleRunning
	return true
}

func (r *RunnerRecord) ObserveExit(now time.Time, class RunnerExitClass) {
	if r == nil {
		return
	}
	r.child = false
	switch class {
	case RunnerExitUnlinked:
		r.Lifecycle = RunnerLifecycleDegraded
		r.BackoffUntil = time.Time{}
	case RunnerExitCrash:
		r.crashes = appendCrash(r.crashes, now)
		if crashBudgetBreached(r.crashes, now) {
			r.Lifecycle = RunnerLifecycleDegraded
			r.BackoffUntil = time.Time{}
			return
		}
		r.Lifecycle = RunnerLifecycleCrashed
		r.BackoffUntil = now.Add(RunnerRestartBackoff)
	default:
		r.Lifecycle = RunnerLifecycleStopped
		r.BackoffUntil = time.Time{}
	}
}

func appendCrash(crashes []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-RunnerCrashWindow)
	kept := crashes[:0]
	for _, at := range crashes {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return append(kept, now)
}

func crashBudgetBreached(crashes []time.Time, now time.Time) bool {
	cutoff := now.Add(-RunnerCrashWindow)
	n := 0
	for _, at := range crashes {
		if at.After(cutoff) {
			n++
		}
	}
	return n >= RunnerDegradedThreshold
}
