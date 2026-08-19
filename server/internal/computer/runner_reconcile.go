package computer

import (
	"time"

	"github.com/google/uuid"
)

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
// startIdentity is generated before each spawn. Ready and exit observations
// must carry that exact identity, so stale process events cannot mutate the
// replacement slot.
type RunnerRecord struct {
	Lifecycle    RunnerLifecycle
	BackoffUntil time.Time
	ExternalPID  int

	startIdentity string
	child         bool
	crashes       []time.Time
}

func (r *RunnerRecord) StartIdentity() string {
	if r == nil {
		return ""
	}
	return r.startIdentity
}

func (r *RunnerRecord) HasChild() bool {
	return r != nil && r.child
}

func (r *RunnerRecord) CanSpawn(wanted bool, now time.Time) bool {
	if r == nil {
		return wanted
	}
	if !wanted || r.child || r.ExternalPID > 0 {
		return false
	}
	if r.Lifecycle != RunnerLifecycleCrashed && r.Lifecycle != RunnerLifecycleStopped {
		return false
	}
	return !now.Before(r.BackoffUntil)
}

// AdoptExternalPID records a still-live runner that a successor Host found
// through pidfile evidence. Raft 1.0.17 does the same: adopt instead of
// spawning a second child, and do not kill that pid on Host shutdown.
func (r *RunnerRecord) AdoptExternalPID(pid int) {
	if r == nil || pid < 1 {
		return
	}
	r.child = false
	r.ExternalPID = pid
	r.Lifecycle = RunnerLifecycleRunning
	r.BackoffUntil = time.Time{}
}

// ClearExternalPIDIfDead drops a previously adopted runner after it exits so
// the next reconcile may spawn a replacement.
func (r *RunnerRecord) ClearExternalPIDIfDead(alive bool) bool {
	if r == nil || r.ExternalPID < 1 || alive {
		return false
	}
	r.ExternalPID = 0
	if r.Lifecycle == RunnerLifecycleRunning {
		r.Lifecycle = RunnerLifecycleStopped
	}
	return true
}

func (r *RunnerRecord) ObserveSpawn() string {
	if r == nil {
		return ""
	}
	r.startIdentity = uuid.NewString()
	r.child = true
	r.Lifecycle = RunnerLifecycleStarting
	r.BackoffUntil = time.Time{}
	return r.startIdentity
}

func (r *RunnerRecord) ObserveReady(startIdentity string) bool {
	if r == nil || !r.child || r.startIdentity != startIdentity || r.Lifecycle != RunnerLifecycleStarting {
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
