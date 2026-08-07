package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
)

const detachedSuccessorPortReleaseTimeout = 5 * time.Second
const detachedSuccessorReadyTimeout = 45 * time.Second

var spawnDetachedDaemonBinary = startDetachedDaemonBinary

// startDetachedDaemonBinary launches the committed target as the next daemon
// generation only after this profile's loopback control port is no longer
// live. It inherits neither a supervisor marker nor the incumbent process
// group, so a failed target cannot keep the old process alive as a hidden
// second owner. The successor must independently bind the port and complete
// normal Machine Upgrade registration/convergence.
func startDetachedDaemonBinary(binaryPath, profile, expectedVersion string) error {
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
	args := []string{"daemon", "start", "--foreground"}
	if profile != "" {
		args = append(args, "--profile", profile)
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
	// Keep the handle until the candidate itself binds the control port and
	// reaches normal daemon readiness on the exact target. A child that merely
	// started is not a takeover proof. On failure, only this known child is
	// terminated; no PID discovery or name matching is used.
	readyDeadline := time.Now().Add(detachedSuccessorReadyTimeout)
	for time.Now().Before(readyDeadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		health := computer.ProbeHealth(ctx, computer.HealthPort(profile))
		cancel()
		if health["status"] == "running" {
			actual, _ := health["cli_version"].(string)
			if expectedVersion == "" || handoffVersionsMatch(actual, expectedVersion) {
				return child.Process.Release()
			}
			_ = child.Process.Kill()
			_, _ = child.Process.Wait()
			return fmt.Errorf("detached successor version %q does not match target %q", actual, expectedVersion)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = child.Process.Kill()
	_, _ = child.Process.Wait()
	return fmt.Errorf("detached successor did not become ready within %s", detachedSuccessorReadyTimeout)
}
