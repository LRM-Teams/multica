package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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
	handoff := computer.PendingMachineUpgradeHandoff{
		SourceServicePID: os.Getpid(), TargetVersion: "v10.0.0",
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  "revision-a",
	}
	probeDetachedSuccessorAttestation = func(string) (computer.MachineAttestation, error) {
		return computer.MachineAttestation{
			ServicePID: child.Process.Pid, SourceServicePID: os.Getpid(), ServiceGeneration: "service-1",
			ComputerVersion: "v10.0.0", ManagedWorkspaceIDs: []string{"workspace-a"}, ManagedSetRevision: "revision-a",
		}, nil
	}
	if err := acceptReadyDetachedCandidate(child, "", "v10.0.0", handoff, map[string]any{"cliVersion": "v10.0.0"}); err != nil {
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
	handoff := computer.PendingMachineUpgradeHandoff{SourceServicePID: os.Getpid(), TargetVersion: "v10.0.0"}
	if err := acceptReadyDetachedCandidate(wrongPID, "", "v10.0.0", handoff, map[string]any{"cliVersion": "v10.0.0"}); err == nil {
		t.Fatal("wrong control PID was accepted")
	}
	if wrongPID.ProcessState == nil || wrongPID.ProcessState.Success() {
		t.Fatalf("wrong-PID candidate was not terminated: %+v", wrongPID.ProcessState)
	}

	wrongVersion := startChild("wrong-version")
	probeDetachedSuccessorAttestation = func(string) (computer.MachineAttestation, error) {
		return computer.MachineAttestation{ServicePID: wrongVersion.Process.Pid, ComputerVersion: "v9.9.9"}, nil
	}
	if err := acceptReadyDetachedCandidate(wrongVersion, "", "v10.0.0", handoff, map[string]any{"cliVersion": "v10.0.0"}); err == nil {
		t.Fatal("wrong control version was accepted")
	}
	if wrongVersion.ProcessState == nil || wrongVersion.ProcessState.Success() {
		t.Fatalf("wrong-version candidate was not terminated: %+v", wrongVersion.ProcessState)
	}
}

func TestCompleteDetachedMachineUpgradeRollsBackWithFromVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	if err := computer.WritePendingMachineUpgradeHandoffForTest(computer.RootDir(""), computer.PendingMachineUpgradeHandoff{
		RequestID: "upgrade-a", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		SourceServicePID:            101,
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"},
		AcceptedManagedSetRevision:  "revision-a",
	}); err != nil {
		t.Fatal(err)
	}

	originalSpawn := spawnDetachedComputerBinary
	originalRollback := rollbackDetachedExecutable
	t.Cleanup(func() {
		spawnDetachedComputerBinary = originalSpawn
		rollbackDetachedExecutable = originalRollback
	})
	var versions []string
	spawnDetachedComputerBinary = func(_, _, expectedVersion string, handoff computer.PendingMachineUpgradeHandoff) error {
		versions = append(versions, expectedVersion)
		if expectedVersion == "v2.0.0" {
			if handoff.TargetVersion != "v2.0.0" {
				t.Fatalf("target handoff = %+v", handoff)
			}
			return fmt.Errorf("target failed")
		}
		if expectedVersion != "v1.0.0" || handoff.TargetVersion != "v1.0.0" {
			t.Fatalf("rollback spawn version=%q handoff=%+v", expectedVersion, handoff)
		}
		return nil
	}
	var rolledBack string
	rollbackDetachedExecutable = func(current string) error {
		rolledBack = current
		return nil
	}
	err := completeDetachedMachineUpgrade("", "/tmp/active-computer")
	if err == nil || !strings.Contains(err.Error(), "previous Computer restored") {
		t.Fatalf("complete = %v", err)
	}
	if rolledBack != "/tmp/active-computer" {
		t.Fatalf("rollback path = %q", rolledBack)
	}
	if len(versions) != 2 || versions[0] != "v2.0.0" || versions[1] != "v1.0.0" {
		t.Fatalf("spawn versions = %v", versions)
	}
}

func TestCompleteDetachedMachineUpgradeKeepsOwnedSuccessor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	if err := computer.WritePendingMachineUpgradeHandoffForTest(computer.RootDir(""), computer.PendingMachineUpgradeHandoff{
		RequestID: "upgrade-a", FromVersion: "v1.0.0", TargetVersion: "v2.0.0",
		SourceServicePID: 101, AcceptedManagedSetRevision: "revision-a",
	}); err != nil {
		t.Fatal(err)
	}
	originalSpawn := spawnDetachedComputerBinary
	t.Cleanup(func() { spawnDetachedComputerBinary = originalSpawn })
	var waited bool
	spawnDetachedComputerBinary = func(_, _, expectedVersion string, handoff computer.PendingMachineUpgradeHandoff) error {
		if expectedVersion != "v2.0.0" {
			t.Fatalf("expected target spawn, got %q", expectedVersion)
		}
		if !handoff.KeepOwnedProcess {
			t.Fatal("coordinator must keep the successor process")
		}
		waited = true
		return nil
	}
	if err := completeDetachedMachineUpgrade("", "/tmp/active-computer"); err != nil {
		t.Fatal(err)
	}
	if !waited {
		t.Fatal("coordinator did not wait on the successor it spawned")
	}
	got, err := computer.ReadPendingMachineUpgradeHandoff(computer.RootDir(""))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("successful coordinator must finalize journal = %+v", got)
	}
}

func TestCompleteDetachedComputerRestartDoesNotRequireUpgradeJournal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	originalSpawn := spawnDetachedComputerBinary
	t.Cleanup(func() { spawnDetachedComputerBinary = originalSpawn })

	called := false
	spawnDetachedComputerBinary = func(binaryPath, profile, expectedVersion string, handoff computer.PendingMachineUpgradeHandoff) error {
		called = true
		if binaryPath != "/tmp/current-computer" || profile != "" || expectedVersion != "alpha.8" {
			t.Fatalf("restart spawn path=%q profile=%q version=%q", binaryPath, profile, expectedVersion)
		}
		if handoff.SourceServicePID != 101 || len(handoff.OldRunnerPIDs) != 1 || handoff.OldRunnerPIDs[0] != 202 {
			t.Fatalf("restart predecessor handoff = %+v", handoff)
		}
		if !handoff.KeepOwnedProcess {
			t.Fatal("restart coordinator must own the successor")
		}
		return nil
	}
	err := completeDetachedComputerRestart("", "/tmp/current-computer", computer.ComputerRestartHandoff{
		Version: "alpha.8", SourceServicePID: 101, OldBindingPIDs: []int{202},
		AcceptedManagedWorkspaceIDs: []string{"workspace-a"}, AcceptedManagedSetRevision: "revision-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("same-binary restart did not spawn a successor")
	}
	journal, err := computer.ReadPendingMachineUpgradeHandoff(computer.RootDir(""))
	if err != nil {
		t.Fatal(err)
	}
	if journal != nil {
		t.Fatalf("same-binary restart created an upgrade journal: %+v", journal)
	}
}
