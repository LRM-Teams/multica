package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

const detachedSuccessorPortReleaseTimeout = 5 * time.Second
const detachedSuccessorReadyTimeout = 45 * time.Second

var spawnDetachedComputerBinary = startDetachedComputerBinary
var probeDetachedSuccessorAttestation = func(profile string) (computer.MachineAttestation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return computer.ProbeMachineAttestation(ctx, computer.ServiceControlEndpoint(computer.RootDir(profile)))
}

// startDetachedComputerBinary launches the committed target as the next Computer
// generation only after the machine-wide loopback control port is no longer
// live. It inherits neither a supervisor marker nor the incumbent process
// group, so a failed target cannot keep the old process alive as a hidden
// second owner. The successor must independently bind the port and complete
// normal Machine Upgrade registration/convergence.
func startDetachedComputerBinary(binaryPath, profile, expectedVersion string) error {
	if binaryPath == "" {
		return fmt.Errorf("detached successor binary is required")
	}
	deadline := time.Now().Add(detachedSuccessorPortReleaseTimeout)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		live := computer.Alive(computer.ProbeHealth(ctx, computer.ServiceControlEndpoint(computer.RootDir(profile))))
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
	args := computer.ResidentArgs(computer.StartOptions{})
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
	// Keep the handle until the candidate itself binds the control port and
	// reaches normal Computer readiness on the exact target. A child that merely
	// started is not a takeover proof. On failure, only this known child is
	// terminated; no PID discovery or name matching is used.
	readyDeadline := time.Now().Add(detachedSuccessorReadyTimeout)
	for time.Now().Before(readyDeadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		health := computer.ProbeHealth(ctx, computer.ServiceControlEndpoint(computer.RootDir(profile)))
		cancel()
		if health["status"] == "running" {
			return acceptReadyDetachedCandidate(child, profile, expectedVersion, health)
		}
		time.Sleep(100 * time.Millisecond)
	}
	terminateDetachedCandidate(child)
	return fmt.Errorf("detached successor did not become ready within %s", detachedSuccessorReadyTimeout)
}

func acceptReadyDetachedCandidate(
	child *exec.Cmd,
	profile, expectedVersion string,
	health map[string]any,
) error {
	actualVersion, _ := health["cliVersion"].(string)
	if expectedVersion != "" && !detachedVersionsMatch(actualVersion, expectedVersion) {
		terminateDetachedCandidate(child)
		return fmt.Errorf("detached successor version %q does not match target %q", actualVersion, expectedVersion)
	}
	attestation, err := probeDetachedSuccessorAttestation(profile)
	if err != nil {
		terminateDetachedCandidate(child)
		return fmt.Errorf("detached successor did not answer Computer attestation: %w", err)
	}
	if err := computer.ValidateSuccessorPIDVersion(computer.SuccessorPIDVersion{
		ServicePID: child.Process.Pid, SourceServicePID: os.Getpid(), ComputerVersion: expectedVersion,
	}, attestation); err != nil {
		terminateDetachedCandidate(child)
		return err
	}
	return child.Process.Release()
}

func terminateDetachedCandidate(child *exec.Cmd) {
	if child == nil || child.Process == nil {
		return
	}
	_ = child.Process.Kill()
	_ = child.Wait()
}

func detachedVersionsMatch(a, b string) bool {
	normalize := func(v string) string {
		return strings.TrimPrefix(strings.TrimSpace(v), "v")
	}
	a, b = normalize(a), normalize(b)
	return a != "" && a == b
}
