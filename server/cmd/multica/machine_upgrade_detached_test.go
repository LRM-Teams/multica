package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/daemon"
)

func TestDetachedTakeoverLostResponseUsesExactDurableCandidateProof(t *testing.T) {
	expected := daemon.MachineUpgradeTakeoverProof{
		UpgradeID: "upgrade-1", Generation: "generation-a", ComputerID: "daemon-1",
		PredecessorComputerGeneration: 11, PredecessorVersionStoreGeneration: 7,
		CandidateComputerGeneration: 12, CandidatePID: 1234, TargetVersion: "v10.0.0",
		WorkspaceIDs: []string{"workspace-1"}, Phase: "takeover_ready",
	}
	committed := expected
	committed.Phase = "takeover_committed"

	originalRequest := requestDetachedSuccessorTakeover
	originalProbe := probeDetachedSuccessorHealth
	t.Cleanup(func() {
		requestDetachedSuccessorTakeover = originalRequest
		probeDetachedSuccessorHealth = originalProbe
	})
	requestDetachedSuccessorTakeover = func(string, daemon.MachineUpgradeTakeoverProof) (daemon.MachineUpgradeTakeoverProof, error) {
		return daemon.MachineUpgradeTakeoverProof{}, errors.New("lost loopback response")
	}
	probeDetachedSuccessorHealth = func(string) map[string]any {
		return map[string]any{"machine_upgrade_takeover": committed}
	}

	got, err := commitDetachedSuccessorTakeoverVerified("test", expected)
	if err != nil {
		t.Fatalf("durably committed candidate was rejected after response loss: %v", err)
	}
	if got.Phase != "takeover_committed" || got.CandidatePID != expected.CandidatePID {
		t.Fatalf("committed proof = %+v", got)
	}
}

func TestDetachedSuccessorProofRejectsEveryTakeoverIdentityMismatch(t *testing.T) {
	expected := daemon.MachineUpgradeTakeoverProof{
		UpgradeID: "upgrade-1", Generation: "generation-a", ComputerID: "daemon-1",
		PredecessorComputerGeneration: 11, PredecessorVersionStoreGeneration: 7,
		CandidateComputerGeneration: 12, CandidatePID: 1234, TargetVersion: "v10.0.0",
		WorkspaceIDs: []string{"workspace-1"},
		Phase:        "takeover_committed",
	}
	if err := validateDetachedSuccessorProof(expected, expected); err != nil {
		t.Fatalf("exact proof rejected: %v", err)
	}

	tests := map[string]func(*daemon.MachineUpgradeTakeoverProof){
		"operation":                     func(p *daemon.MachineUpgradeTakeoverProof) { p.UpgradeID = "upgrade-stale" },
		"handoff generation":            func(p *daemon.MachineUpgradeTakeoverProof) { p.Generation = "generation-stale" },
		"Computer":                      func(p *daemon.MachineUpgradeTakeoverProof) { p.ComputerID = "computer-stale" },
		"predecessor Computer":          func(p *daemon.MachineUpgradeTakeoverProof) { p.PredecessorComputerGeneration = 10 },
		"predecessor VersionStore":      func(p *daemon.MachineUpgradeTakeoverProof) { p.PredecessorVersionStoreGeneration = 6 },
		"candidate Computer generation": func(p *daemon.MachineUpgradeTakeoverProof) { p.CandidateComputerGeneration = 13 },
		"candidate pid":                 func(p *daemon.MachineUpgradeTakeoverProof) { p.CandidatePID = 9999 },
		"target version":                func(p *daemon.MachineUpgradeTakeoverProof) { p.TargetVersion = "v10.0.1" },
		"changed Workspace set":         func(p *daemon.MachineUpgradeTakeoverProof) { p.WorkspaceIDs = []string{"workspace-2"} },
		"not durably committed":         func(p *daemon.MachineUpgradeTakeoverProof) { p.Phase = "takeover_ready" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			observed := expected
			observed.WorkspaceIDs = append([]string(nil), expected.WorkspaceIDs...)
			mutate(&observed)
			if err := validateDetachedSuccessorProof(expected, observed); err == nil || !strings.Contains(err.Error(), "detached successor") {
				t.Fatalf("mismatched proof error = %v", err)
			}
		})
	}
}

func TestMachineUpgradeTakeoverProtocolIsBoundToCandidateGeneration(t *testing.T) {
	value := machineUpgradeTakeoverProtocolValue(12)
	if got := machineUpgradeTakeoverProtocolForGeneration(value, 12); got != daemon.MachineUpgradeTakeoverProtocolV2 {
		t.Fatalf("matching protocol = %q", got)
	}
	if got := machineUpgradeTakeoverProtocolForGeneration(value, 13); got != "" {
		t.Fatalf("inherited stale protocol = %q, want legacy", got)
	}
	if got := machineUpgradeTakeoverProtocolForGeneration(string(daemon.MachineUpgradeTakeoverProtocolV2), 12); got != "" {
		t.Fatalf("unbound protocol = %q, want legacy", got)
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
		UpgradeID: "upgrade-1", Generation: "generation-a", ComputerID: "daemon-1",
		PredecessorComputerGeneration: 11, PredecessorVersionStoreGeneration: 7,
		CandidateComputerGeneration: 12, CandidatePID: child.Process.Pid, TargetVersion: "v10.0.0",
		WorkspaceIDs: []string{"workspace-1"}, Phase: "takeover_ready",
	}
	observed := expected
	observed.ComputerID = "computer-stale"
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
