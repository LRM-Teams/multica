package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/util"
)

const detachedPredecessorExitTimeout = 45 * time.Second
const detachedSuccessorReadyTimeout = 45 * time.Second

var spawnDetachedComputerBinary = startDetachedComputerBinary
var spawnDetachedUpgradeCoordinator = startDetachedUpgradeCoordinator
var spawnDetachedRestartCoordinator = startDetachedRestartCoordinator
var probeDetachedSuccessorAttestation = func(profile string) (computer.MachineAttestation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return computer.ProbeMachineAttestation(ctx, computer.ServiceControlEndpoint(computer.RootDir(profile)))
}
var waitForDetachedPredecessors = computer.WaitForMachineUpgradePredecessors
var rollbackDetachedExecutable = cli.RollbackExecutable
var ownedDetachedProcess *exec.Cmd

func startDetachedUpgradeCoordinator(binaryPath, profile string) error {
	if binaryPath == "" {
		return fmt.Errorf("detached upgrade coordinator binary is required")
	}
	return startDetachedCoordinator(binaryPath, profile, computer.ResidentCommand, computer.ResidentUpgradeArg)
}

func startDetachedRestartCoordinator(binaryPath, profile string, handoff computer.ComputerRestartHandoff) error {
	if binaryPath == "" {
		return fmt.Errorf("detached restart coordinator binary is required")
	}
	payload, err := json.Marshal(handoff)
	if err != nil {
		return fmt.Errorf("encode Computer restart handoff: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return startDetachedCoordinator(binaryPath, profile, computer.ResidentCommand, computer.ResidentRestartArg, encoded)
}

func startDetachedCoordinator(binaryPath, profile string, args ...string) error {
	logFile, err := os.OpenFile(computer.LogPath(profile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
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
	return child.Process.Release()
}

// startDetachedComputerBinary launches the target only after the captured
// predecessor service and old Binding children are dead. The incumbent must
// not call this while it is still alive.
func startDetachedComputerBinary(binaryPath, profile, expectedVersion string, handoff computer.PendingMachineUpgradeHandoff) error {
	if binaryPath == "" {
		return fmt.Errorf("detached successor binary is required")
	}
	if strings.TrimSpace(expectedVersion) == "" {
		return fmt.Errorf("detached successor version is required")
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), detachedPredecessorExitTimeout)
	err := waitForDetachedPredecessors(waitCtx, handoff)
	cancelWait()
	if err != nil {
		return fmt.Errorf("wait for Computer upgrade predecessors: %w", err)
	}

	logFile, err := os.OpenFile(computer.LogPath(profile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	args := computer.ResidentArgs(computer.StartOptions{})
	args = append(args, "--source-service-pid", strconv.Itoa(handoff.SourceServicePID))
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
	readyDeadline := time.Now().Add(detachedSuccessorReadyTimeout)
	for time.Now().Before(readyDeadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		health := computer.ProbeHealth(ctx, computer.ServiceControlEndpoint(computer.RootDir(profile)))
		cancel()
		if health["status"] == "running" {
			if err := acceptReadyDetachedCandidate(child, profile, expectedVersion, handoff, health); err != nil {
				return err
			}
			if handoff.KeepOwnedProcess {
				ownedDetachedProcess = child
				return nil
			}
			return child.Process.Release()
		}
		time.Sleep(100 * time.Millisecond)
	}
	terminateDetachedCandidate(child)
	return fmt.Errorf("detached successor did not become ready within %s", detachedSuccessorReadyTimeout)
}

func acceptReadyDetachedCandidate(
	child *exec.Cmd,
	profile, expectedVersion string,
	handoff computer.PendingMachineUpgradeHandoff,
	health map[string]any,
) error {
	actualVersion, _ := health["cliVersion"].(string)
	if !detachedVersionsMatch(actualVersion, expectedVersion) {
		terminateDetachedCandidate(child)
		return fmt.Errorf("detached successor version %q does not match target %q", actualVersion, expectedVersion)
	}
	attestation, err := probeDetachedSuccessorAttestation(profile)
	if err != nil {
		terminateDetachedCandidate(child)
		return fmt.Errorf("detached successor did not answer Computer attestation: %w", err)
	}
	if err := computer.ValidateSuccessorPIDVersion(computer.SuccessorPIDVersion{
		ServicePID: child.Process.Pid, SourceServicePID: handoff.SourceServicePID, ComputerVersion: expectedVersion,
		AcceptedManagedWorkspaceIDs: handoff.AcceptedManagedWorkspaceIDs,
		AcceptedManagedSetRevision:  handoff.AcceptedManagedSetRevision,
	}, attestation); err != nil {
		terminateDetachedCandidate(child)
		return err
	}
	return nil
}

func runComputerUpgradeCoordinator(_ *cobra.Command, _ []string) error {
	util.EnsureHiddenConsole()
	installPath, err := cli.InstallPath()
	if err != nil {
		return err
	}
	return completeDetachedMachineUpgrade("", installPath)
}

func runComputerRestartCoordinator(_ *cobra.Command, args []string) error {
	util.EnsureHiddenConsole()
	payload, err := base64.RawURLEncoding.DecodeString(args[0])
	if err != nil {
		return fmt.Errorf("decode Computer restart handoff: %w", err)
	}
	var handoff computer.ComputerRestartHandoff
	if err := json.Unmarshal(payload, &handoff); err != nil {
		return fmt.Errorf("decode Computer restart handoff: %w", err)
	}
	installPath, err := cli.InstallPath()
	if err != nil {
		return err
	}
	return completeDetachedComputerRestart("", installPath, handoff)
}

func completeDetachedComputerRestart(profile, binaryPath string, handoff computer.ComputerRestartHandoff) error {
	owned := computer.PendingMachineUpgradeHandoff{
		TargetVersion: handoff.Version, SourceServicePID: handoff.SourceServicePID,
		OldRunnerPIDs:               append([]int(nil), handoff.OldBindingPIDs...),
		AcceptedManagedWorkspaceIDs: append([]string(nil), handoff.AcceptedManagedWorkspaceIDs...),
		AcceptedManagedSetRevision:  handoff.AcceptedManagedSetRevision,
		KeepOwnedProcess:            true,
	}
	ownedDetachedProcess = nil
	if err := spawnDetachedComputerBinary(binaryPath, profile, handoff.Version, owned); err != nil {
		return fmt.Errorf("restart detached Computer: %w", err)
	}
	return waitOwnedDetachedProcess()
}

func completeDetachedMachineUpgrade(profile, binaryPath string) error {
	root := computer.RootDir(profile)
	handoff, err := computer.ReadPendingMachineUpgradeHandoff(root)
	if err != nil {
		return err
	}
	if handoff == nil {
		return fmt.Errorf("Computer Machine Upgrade journal is missing")
	}
	owned := *handoff
	owned.KeepOwnedProcess = true
	if err := computer.MarkPendingMachineUpgradePhase(root, computer.MachineUpgradePhaseStartingTarget); err != nil {
		return err
	}
	ownedDetachedProcess = nil
	if err := spawnDetachedComputerBinary(binaryPath, profile, owned.TargetVersion, owned); err != nil {
		if markErr := computer.MarkPendingMachineUpgradePhase(root, computer.MachineUpgradePhaseRollingBack); markErr != nil {
			return fmt.Errorf("start detached Computer successor: %w; mark rollback: %v", err, markErr)
		}
		if rollbackErr := rollbackDetachedExecutable(binaryPath); rollbackErr != nil {
			return fmt.Errorf("start detached Computer successor: %w; restore previous Computer: %v", err, rollbackErr)
		}
		restored := owned
		restored.TargetVersion = owned.FromVersion
		ownedDetachedProcess = nil
		if restoreErr := spawnDetachedComputerBinary(binaryPath, profile, owned.FromVersion, restored); restoreErr != nil {
			return fmt.Errorf("start detached Computer successor: %w; restored previous binary but restart failed: %v", err, restoreErr)
		}
		if finalizeErr := computer.FinalizePendingMachineUpgrade(root); finalizeErr != nil {
			return fmt.Errorf("start detached Computer successor: %w; previous Computer restored; clear journal: %v", err, finalizeErr)
		}
		if waitErr := waitOwnedDetachedProcess(); waitErr != nil {
			return fmt.Errorf("start detached Computer successor: %w; previous Computer restored: %v", err, waitErr)
		}
		return fmt.Errorf("start detached Computer successor: %w; previous Computer restored", err)
	}
	if err := computer.FinalizePendingMachineUpgrade(root); err != nil {
		return err
	}
	return waitOwnedDetachedProcess()
}

func waitOwnedDetachedProcess() error {
	child := ownedDetachedProcess
	ownedDetachedProcess = nil
	if child == nil {
		return nil
	}
	return child.Wait()
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
