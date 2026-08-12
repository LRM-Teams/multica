package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/daemon"
)

const detachedSuccessorPortReleaseTimeout = 5 * time.Second
const detachedSuccessorReadyTimeout = 45 * time.Second
const detachedSuccessorCommitRetryTimeout = 15 * time.Second

var spawnDetachedDaemonBinary = startDetachedDaemonBinary
var requestDetachedSuccessorTakeover = commitDetachedSuccessorTakeover
var probeDetachedSuccessorHealth = func(profile string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return computer.ProbeHealth(ctx, computer.HealthPort(profile))
}

// startDetachedDaemonBinary launches the committed target as the next Computer
// generation only after the machine-wide loopback control port is no longer
// live. It inherits neither a supervisor marker nor the incumbent process
// group, so a failed target cannot keep the old process alive as a hidden
// second owner. The successor must independently bind the port and complete
// normal Machine Upgrade registration/convergence.
func startDetachedDaemonBinary(binaryPath, profile, expectedVersion string, takeoverExpectation *daemon.MachineUpgradeTakeoverProof) error {
	if binaryPath == "" {
		return fmt.Errorf("detached successor binary is required")
	}
	deadline := time.Now().Add(detachedSuccessorPortReleaseTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		live := computer.Alive(computer.ProbeHealth(ctx, computer.HealthPort(profile)))
		cancel()
		if !live {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("incumbent local control port did not release within %s", detachedSuccessorPortReleaseTimeout)
		}
		time.Sleep(50 * time.Millisecond)
	}

	logFile, err := os.OpenFile(computer.LogPath(profile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	generation, err := computer.NewGenerationStore(computer.RootDir(profile)).Next()
	if err != nil {
		return fmt.Errorf("allocate successor Computer generation: %w", err)
	}
	args := computer.ResidentArgs(computer.StartOptions{Generation: generation})
	if takeoverExpectation != nil {
		args = append(args, "--machine-upgrade-detached-candidate")
	}
	child := exec.Command(binaryPath, args...)
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = computer.SysProcAttr(true)
	if err := child.Start(); err != nil {
		if !computer.IsAccessDeniedSpawnErr(err) {
			return err
		}
		child = exec.Command(binaryPath, args...)
		child.Stdout = logFile
		child.Stderr = logFile
		child.SysProcAttr = computer.SysProcAttr(false)
		if err := child.Start(); err != nil {
			return err
		}
	}
	var expectedTakeover daemon.MachineUpgradeTakeoverProof
	if takeoverExpectation != nil {
		expectedTakeover = *takeoverExpectation
		expectedTakeover.WorkspaceIDs = append([]string(nil), takeoverExpectation.WorkspaceIDs...)
		expectedTakeover.CandidateComputerGeneration = generation
		expectedTakeover.CandidatePID = child.Process.Pid
		expectedTakeover.Phase = "takeover_ready"
	}
	// Keep the handle until the candidate itself binds the control port and
	// reaches normal daemon readiness on the exact target. A child that merely
	// started is not a takeover proof. On failure, only this known child is
	// terminated; no PID discovery or name matching is used.
	readyDeadline := time.Now().Add(detachedSuccessorReadyTimeout)
	for time.Now().Before(readyDeadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		health := computer.ProbeHealth(ctx, computer.HealthPort(profile))
		cancel()
		wantStatus := "running"
		if takeoverExpectation != nil {
			wantStatus = "takeover_ready"
		}
		if health["status"] == wantStatus {
			return acceptReadyDetachedCandidate(child, profile, expectedVersion, takeoverExpectation, expectedTakeover, health)
		}
		time.Sleep(100 * time.Millisecond)
	}
	terminateDetachedCandidate(child)
	return fmt.Errorf("detached successor did not become ready within %s", detachedSuccessorReadyTimeout)
}

func acceptReadyDetachedCandidate(
	child *exec.Cmd,
	profile, expectedVersion string,
	takeoverExpectation *daemon.MachineUpgradeTakeoverProof,
	expectedTakeover daemon.MachineUpgradeTakeoverProof,
	health map[string]any,
) error {
	actualVersion, _ := health["cli_version"].(string)
	if expectedVersion != "" && !handoffVersionsMatch(actualVersion, expectedVersion) {
		terminateDetachedCandidate(child)
		return fmt.Errorf("detached successor version %q does not match target %q", actualVersion, expectedVersion)
	}
	if takeoverExpectation == nil {
		return child.Process.Release()
	}
	observed, ok := detachedTakeoverProofFromHealth(health)
	if !ok {
		terminateDetachedCandidate(child)
		return fmt.Errorf("detached successor did not publish takeover proof")
	}
	if err := validateDetachedSuccessorProof(expectedTakeover, observed); err != nil {
		terminateDetachedCandidate(child)
		return err
	}
	committed, err := commitDetachedSuccessorTakeoverVerified(profile, expectedTakeover)
	if err != nil {
		terminateDetachedCandidate(child)
		return err
	}
	expectedTakeover.Phase = committed.Phase
	if err := validateDetachedSuccessorProof(expectedTakeover, committed); err != nil {
		terminateDetachedCandidate(child)
		return err
	}
	return child.Process.Release()
}

// commitDetachedSuccessorTakeoverVerified closes the ambiguous-response
// window around the server generation CAS. A timeout does not prove that the
// commit failed: the candidate may already have durably recorded the result
// while the loopback response was lost. Retry the idempotent command and
// accept only an exact committed proof published by that same child.
func commitDetachedSuccessorTakeoverVerified(profile string, expected daemon.MachineUpgradeTakeoverProof) (daemon.MachineUpgradeTakeoverProof, error) {
	deadline := time.Now().Add(detachedSuccessorCommitRetryTimeout)
	var lastErr error
	for {
		committed, err := requestDetachedSuccessorTakeover(profile, expected)
		if err == nil {
			return committed, nil
		}
		lastErr = err

		health := probeDetachedSuccessorHealth(profile)
		if proof, ok := detachedTakeoverProofFromHealth(health); ok &&
			(proof.Phase == "takeover_committed" || proof.Phase == "candidate_ready") {
			want := expected
			want.Phase = proof.Phase
			if validateDetachedSuccessorProof(want, proof) == nil {
				return proof, nil
			}
		}
		if time.Now().After(deadline) {
			return daemon.MachineUpgradeTakeoverProof{}, fmt.Errorf("detached successor takeover remained unconfirmed after %s: %w", detachedSuccessorCommitRetryTimeout, lastErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func terminateDetachedCandidate(child *exec.Cmd) {
	if child == nil || child.Process == nil {
		return
	}
	_ = child.Process.Kill()
	_ = child.Wait()
}

func detachedTakeoverProofFromHealth(health map[string]any) (daemon.MachineUpgradeTakeoverProof, bool) {
	raw, ok := health["machine_upgrade_takeover"]
	if !ok || raw == nil {
		return daemon.MachineUpgradeTakeoverProof{}, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return daemon.MachineUpgradeTakeoverProof{}, false
	}
	var proof daemon.MachineUpgradeTakeoverProof
	if json.Unmarshal(data, &proof) != nil {
		return daemon.MachineUpgradeTakeoverProof{}, false
	}
	return proof, true
}

func commitDetachedSuccessorTakeover(profile string, expected daemon.MachineUpgradeTakeoverProof) (daemon.MachineUpgradeTakeoverProof, error) {
	token, err := readMachineUpgradeControlToken(profile)
	if err != nil {
		return daemon.MachineUpgradeTakeoverProof{}, fmt.Errorf("read detached takeover control token: %w", err)
	}
	body, err := json.Marshal(expected)
	if err != nil {
		return daemon.MachineUpgradeTakeoverProof{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/machine-upgrade-takeover/commit", computer.HealthPort(profile))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return daemon.MachineUpgradeTakeoverProof{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Control-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return daemon.MachineUpgradeTakeoverProof{}, fmt.Errorf("commit detached successor takeover: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return daemon.MachineUpgradeTakeoverProof{}, fmt.Errorf("commit detached successor takeover: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(message))
	}
	var proof daemon.MachineUpgradeTakeoverProof
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&proof); err != nil {
		return daemon.MachineUpgradeTakeoverProof{}, fmt.Errorf("decode detached successor takeover: %w", err)
	}
	return proof, nil
}

func validateDetachedSuccessorProof(expected, observed daemon.MachineUpgradeTakeoverProof) error {
	if err := daemon.ValidateMachineUpgradeTakeoverProof(expected, observed); err != nil {
		return fmt.Errorf("detached successor does not match committed handoff: %w", err)
	}
	return nil
}
