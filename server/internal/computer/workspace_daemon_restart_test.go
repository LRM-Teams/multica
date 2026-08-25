package computer

import (
	"testing"
	"time"
)

func TestWorkspaceDaemonRestartBacksOffThenDegrades(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	restart := &workspaceDaemonRestart{}

	restart.recordExit(now, WorkspaceDaemonExitCrash)
	if restart.canStart(now) {
		t.Fatal("crash must wait for backoff")
	}
	if !restart.canStart(now.Add(WorkspaceDaemonRestartBackoff)) {
		t.Fatal("crash must be restartable after backoff")
	}

	restart.recordExit(now.Add(time.Second), WorkspaceDaemonExitCrash)
	restart.recordExit(now.Add(2*time.Second), WorkspaceDaemonExitCrash)
	if !restart.degraded || restart.canStart(now.Add(time.Hour)) {
		t.Fatal("three crashes in the window must stop automatic restart")
	}
}

func TestWorkspaceDaemonRestartHandlesUnlinkedAndGracefulExit(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	restart := &workspaceDaemonRestart{}

	restart.recordExit(now, WorkspaceDaemonExitUnlinked)
	if !restart.degraded || restart.canStart(now) {
		t.Fatal("unlinked WorkspaceDaemon must not restart")
	}

	restart.recordExit(now, WorkspaceDaemonExitGraceful)
	if restart.degraded || !restart.canStart(now) {
		t.Fatal("graceful exit must permit a later requested start")
	}
}
