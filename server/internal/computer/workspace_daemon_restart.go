package computer

import "time"

const (
	WorkspaceDaemonReconcileInterval = 5 * time.Second
	WorkspaceDaemonRestartBackoff    = 2 * time.Second
	WorkspaceDaemonCrashWindow       = 60 * time.Second
	WorkspaceDaemonCrashLimit        = 3
)

type WorkspaceDaemonStatus string

const (
	WorkspaceDaemonStopped  WorkspaceDaemonStatus = "stopped"
	WorkspaceDaemonStarting WorkspaceDaemonStatus = "starting"
	WorkspaceDaemonRunning  WorkspaceDaemonStatus = "running"
	WorkspaceDaemonCrashed  WorkspaceDaemonStatus = "crashed"
	WorkspaceDaemonDegraded WorkspaceDaemonStatus = "degraded"
)

type WorkspaceDaemonExitClass string

const (
	WorkspaceDaemonExitGraceful WorkspaceDaemonExitClass = "graceful"
	WorkspaceDaemonExitCrash    WorkspaceDaemonExitClass = "crash"
	WorkspaceDaemonExitUnlinked WorkspaceDaemonExitClass = "unlinked"
)

type workspaceDaemonRestart struct {
	notBefore time.Time
	crashes   []time.Time
	degraded  bool
}

func (restart *workspaceDaemonRestart) canStart(now time.Time) bool {
	return restart != nil && !restart.degraded && !now.Before(restart.notBefore)
}

func (restart *workspaceDaemonRestart) recordExit(now time.Time, class WorkspaceDaemonExitClass) {
	if restart == nil {
		return
	}
	switch class {
	case WorkspaceDaemonExitUnlinked:
		restart.degraded = true
		restart.notBefore = time.Time{}
	case WorkspaceDaemonExitCrash:
		restart.crashes = appendRecentCrash(restart.crashes, now)
		restart.degraded = len(restart.crashes) >= WorkspaceDaemonCrashLimit
		if restart.degraded {
			restart.notBefore = time.Time{}
			return
		}
		restart.notBefore = now.Add(WorkspaceDaemonRestartBackoff)
	default:
		restart.degraded = false
		restart.notBefore = time.Time{}
	}
}

func appendRecentCrash(crashes []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-WorkspaceDaemonCrashWindow)
	kept := crashes[:0]
	for _, at := range crashes {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return append(kept, now)
}
