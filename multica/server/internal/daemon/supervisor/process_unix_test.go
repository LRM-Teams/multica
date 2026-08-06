//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSupervisorGracefulStopTerminatesWorkerProcessGroup(t *testing.T) {
	runDescendantStopScenario(t, false)
}

func TestSupervisorForcedStopTerminatesWorkerProcessGroup(t *testing.T) {
	runDescendantStopScenario(t, true)
}

func runDescendantStopScenario(t *testing.T, ignoreTerm bool) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "worker.sh")
	childPIDPath := filepath.Join(dir, "child.pid")
	termReceiptPath := filepath.Join(dir, "term.received")

	script := `#!/bin/sh
child_pid_path=$1
term_receipt_path=$2
if [ "$3" = "ignore" ]; then
  trap '' TERM
  sh -c 'trap "" TERM; while :; do sleep 1; done' &
else
  sh -c 'trap "exit 0" TERM; while :; do sleep 1; done' &
  trap 'echo received > "$term_receipt_path"; wait "$child" 2>/dev/null; exit 0' TERM
fi
child=$!
echo "$child" > "$child_pid_path"
while :; do
  wait "$child"
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write worker script: %v", err)
	}
	mode := "graceful"
	grace := 500 * time.Millisecond
	if ignoreTerm {
		mode = "ignore"
		grace = 40 * time.Millisecond
	}
	supervisor, err := New(Config{
		LockPath:         filepath.Join(dir, "supervisor.lock"),
		WorkerPath:       "/bin/sh",
		WorkerArgs:       []string{scriptPath, childPIDPath, termReceiptPath, mode},
		InitialBackoff:   10 * time.Millisecond,
		MaxBackoff:       20 * time.Millisecond,
		StableRunWindow:  time.Hour,
		GracefulStopWait: grace,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- supervisor.Run(ctx)
	}()

	childPID := waitForPIDFile(t, childPIDPath)
	running := waitForRunningSnapshot(t, supervisor)
	cancel()
	if err := waitForRun(t, errCh); err != nil {
		t.Fatalf("Run after cancellation: %v", err)
	}

	waitForProcessGone(t, childPID)
	waitForProcessGroupGone(t, running.WorkerPID)
	if ignoreTerm {
		if _, err := os.Stat(termReceiptPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("forced-stop worker unexpectedly handled TERM: %v", err)
		}
		return
	}
	if _, err := os.Stat(termReceiptPath); err != nil {
		t.Fatalf("graceful worker did not record TERM: %v", err)
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for child pid file %s", path)
	return 0
}

func waitForRunningSnapshot(t *testing.T, supervisor *Supervisor) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := supervisor.Snapshot()
		if snapshot.State == StateRunning && snapshot.WorkerPID > 0 {
			return snapshot
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for running supervisor; got %+v", supervisor.Snapshot())
	return Snapshot{}
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still exists after supervisor stopped", pid)
}

func waitForProcessGroupGone(t *testing.T, processGroupID int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-processGroupID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process group %d still exists after supervisor stopped", processGroupID)
}
