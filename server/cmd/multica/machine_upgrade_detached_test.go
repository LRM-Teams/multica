package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon"
)

func TestDetachedSuccessorProofRejectsEveryTakeoverIdentityMismatch(t *testing.T) {
	expected := daemon.MachineUpgradeTakeoverProof{
		UpgradeID: "upgrade-1", Generation: "generation-a", DaemonID: "daemon-1",
		PredecessorComputerGeneration: 11, PredecessorVersionStoreGeneration: 7,
		CandidateComputerGeneration: 12, CandidatePID: 1234, TargetVersion: "v10.0.0",
		RuntimeIDs: []string{"runtime-1", "runtime-2"}, WorkspaceIDs: []string{"workspace-1"},
		Phase: "candidate_ready",
	}
	if err := validateDetachedSuccessorProof(expected, expected); err != nil {
		t.Fatalf("exact proof rejected: %v", err)
	}

	tests := map[string]func(*daemon.MachineUpgradeTakeoverProof){
		"operation":                     func(p *daemon.MachineUpgradeTakeoverProof) { p.UpgradeID = "upgrade-stale" },
		"handoff generation":            func(p *daemon.MachineUpgradeTakeoverProof) { p.Generation = "generation-stale" },
		"daemon":                        func(p *daemon.MachineUpgradeTakeoverProof) { p.DaemonID = "daemon-stale" },
		"predecessor Computer":          func(p *daemon.MachineUpgradeTakeoverProof) { p.PredecessorComputerGeneration = 10 },
		"predecessor VersionStore":      func(p *daemon.MachineUpgradeTakeoverProof) { p.PredecessorVersionStoreGeneration = 6 },
		"candidate Computer generation": func(p *daemon.MachineUpgradeTakeoverProof) { p.CandidateComputerGeneration = 13 },
		"candidate pid":                 func(p *daemon.MachineUpgradeTakeoverProof) { p.CandidatePID = 9999 },
		"target version":                func(p *daemon.MachineUpgradeTakeoverProof) { p.TargetVersion = "v10.0.1" },
		"incomplete runtime set":        func(p *daemon.MachineUpgradeTakeoverProof) { p.RuntimeIDs = []string{"runtime-1"} },
		"changed Workspace set":         func(p *daemon.MachineUpgradeTakeoverProof) { p.WorkspaceIDs = []string{"workspace-2"} },
		"not durably committed":         func(p *daemon.MachineUpgradeTakeoverProof) { p.Phase = "handoff" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			observed := expected
			observed.RuntimeIDs = append([]string(nil), expected.RuntimeIDs...)
			observed.WorkspaceIDs = append([]string(nil), expected.WorkspaceIDs...)
			mutate(&observed)
			if err := validateDetachedSuccessorProof(expected, observed); err == nil || !strings.Contains(err.Error(), "detached successor") {
				t.Fatalf("mismatched proof error = %v", err)
			}
		})
	}
}

func TestReadyDetachedSuccessorMismatchTerminatesOnlySpawnedCandidate(t *testing.T) {
	if os.Getenv("MULTICA_TEST_DETACHED_CANDIDATE_SLEEPER") == "1" {
		time.Sleep(time.Minute)
		return
	}
	child := exec.Command(os.Args[0], "-test.run=TestReadyDetachedSuccessorMismatchTerminatesOnlySpawnedCandidate")
	child.Env = append(os.Environ(), "MULTICA_TEST_DETACHED_CANDIDATE_SLEEPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	expected := daemon.MachineUpgradeTakeoverProof{
		UpgradeID: "upgrade-1", Generation: "generation-a", DaemonID: "daemon-1",
		PredecessorComputerGeneration: 11, PredecessorVersionStoreGeneration: 7,
		CandidateComputerGeneration: 12, CandidatePID: child.Process.Pid, TargetVersion: "v10.0.0",
		RuntimeIDs: []string{"runtime-1"}, WorkspaceIDs: []string{"workspace-1"}, Phase: "handoff",
	}
	observed := expected
	observed.DaemonID = "daemon-stale"
	health := map[string]any{
		"status":                   "running",
		"cli_version":              "v10.0.0",
		"machine_upgrade_takeover": observed,
	}
	if err := acceptReadyDetachedCandidate(child, "", "v10.0.0", &expected, expected, health); err == nil {
		t.Fatal("wrong-daemon candidate was accepted")
	}
	if child.ProcessState == nil || child.ProcessState.Success() {
		t.Fatalf("mismatched spawned candidate was not terminated: %+v", child.ProcessState)
	}
}
