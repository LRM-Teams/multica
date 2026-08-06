package agent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestForceKillProcessTreatsAlreadyReapedAsSuccess pins the CI failure mode
// on TestPiRPCBackendForceKillDuringInitialAckActuallyKillsNotHang: ForceKill
// closes stdin, the hung child exits, Execute()'s Wait() reaps it, then Kill
// returns os.ErrProcessDone. That must be success — the process is already
// dead — not an error that forceInvalidateSession would surface as a failed
// restart.
func TestForceKillProcessTreatsAlreadyReapedAsSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hung")
	writeTestExecutable(t, path, []byte(`#!/bin/sh
while IFS= read -r line; do
  :
done
`))

	cmd := exec.Command(path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Let the child enter its read loop before we close stdin.
	time.Sleep(50 * time.Millisecond)
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		// Shell exits 0 on EOF; anything else means the fixture broke.
		t.Fatalf("Wait after stdin close: %v", err)
	}
	if err := forceKillProcess(cmd.Process); err != nil {
		t.Fatalf("forceKillProcess after Wait returned %v, want nil (os.ErrProcessDone must be treated as success)", err)
	}
	// Confirm raw Kill still surfaces ErrProcessDone so this test fails for
	// the right reason if the helper is gutted to always return nil without
	// calling Kill.
	if err := cmd.Process.Kill(); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("raw Kill after Wait = %v, want os.ErrProcessDone (fixture/reaper assumptions broken)", err)
	}
}
