package main

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

func TestAcceptReadyDetachedComputerRequiresExactControlPIDAndVersion(t *testing.T) {
	if os.Getenv("MULTICA_TEST_DETACHED_CANDIDATE_SLEEPER") == "1" {
		time.Sleep(time.Minute)
		return
	}
	child := exec.Command(os.Args[0], "-test.run=TestAcceptReadyDetachedComputerRequiresExactControlPIDAndVersion")
	child.Env = append(os.Environ(), "MULTICA_TEST_DETACHED_CANDIDATE_SLEEPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })

	originalProbe := probeDetachedSuccessorAttestation
	t.Cleanup(func() { probeDetachedSuccessorAttestation = originalProbe })
	probeDetachedSuccessorAttestation = func(string) (computer.MachineAttestation, error) {
		return computer.MachineAttestation{ServicePID: child.Process.Pid, ComputerVersion: "v10.0.0"}, nil
	}
	if err := acceptReadyDetachedCandidate(child, "", "v10.0.0", map[string]any{"cli_version": "v10.0.0"}); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptReadyDetachedComputerRejectsWrongControlPIDOrVersion(t *testing.T) {
	if os.Getenv("MULTICA_TEST_DETACHED_CANDIDATE_SLEEPER") == "1" {
		time.Sleep(time.Minute)
		return
	}
	startChild := func(name string) *exec.Cmd {
		child := exec.Command(os.Args[0], "-test.run=TestAcceptReadyDetachedComputerRejectsWrongControlPIDOrVersion")
		child.Env = append(os.Environ(), "MULTICA_TEST_DETACHED_CANDIDATE_SLEEPER=1")
		if err := child.Start(); err != nil {
			t.Fatalf("start %s child: %v", name, err)
		}
		return child
	}

	originalProbe := probeDetachedSuccessorAttestation
	t.Cleanup(func() { probeDetachedSuccessorAttestation = originalProbe })
	wrongPID := startChild("wrong-pid")
	probeDetachedSuccessorAttestation = func(string) (computer.MachineAttestation, error) {
		return computer.MachineAttestation{ServicePID: wrongPID.Process.Pid + 1, ComputerVersion: "v10.0.0"}, nil
	}
	if err := acceptReadyDetachedCandidate(wrongPID, "", "v10.0.0", map[string]any{"cli_version": "v10.0.0"}); err == nil {
		t.Fatal("wrong control PID was accepted")
	}
	if wrongPID.ProcessState == nil || wrongPID.ProcessState.Success() {
		t.Fatalf("wrong-PID candidate was not terminated: %+v", wrongPID.ProcessState)
	}

	wrongVersion := startChild("wrong-version")
	probeDetachedSuccessorAttestation = func(string) (computer.MachineAttestation, error) {
		return computer.MachineAttestation{ServicePID: wrongVersion.Process.Pid, ComputerVersion: "v9.9.9"}, nil
	}
	if err := acceptReadyDetachedCandidate(wrongVersion, "", "v10.0.0", map[string]any{"cli_version": "v10.0.0"}); err == nil {
		t.Fatal("wrong control version was accepted")
	}
	if wrongVersion.ProcessState == nil || wrongVersion.ProcessState.Success() {
		t.Fatalf("wrong-version candidate was not terminated: %+v", wrongVersion.ProcessState)
	}
}
