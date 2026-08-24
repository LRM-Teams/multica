package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
	logger_pkg "github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
)

// runComputerResident is the CLI bootstrap for the machine-wide Computer Host.
// It wires only internal/computer owners; one Binding's execution is spawned
// separately through computer.BindingRunnerLauncher.
func runComputerResident(cmd *cobra.Command, _ []string) error {
	util.EnsureHiddenConsole()

	profile := ""
	machineConfig, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return fmt.Errorf("read Computer environment: %w", err)
	}
	serviceTarget, err := cli.ResolveServiceTarget(machineConfig)
	if err != nil {
		return fmt.Errorf("resolve Computer environment: %w", err)
	}
	computerID := flagString(cmd, "daemon-id")
	if computerID == "" {
		identity, err := (&computer.Lifecycle{}).Identity()
		if err != nil {
			return fmt.Errorf("resolve machine-wide Computer identity: %w", err)
		}
		computerID = identity
	}
	workspacesRoot, err := computer.ResolveHostWorkspacesRoot()
	if err != nil {
		return err
	}
	bindingsRoot := computer.RootDir("")
	serviceGeneration := uuid.NewString()
	sourceServicePID, err := computer.PendingMachineUpgradeSourceServicePID(bindingsRoot)
	if err != nil {
		return fmt.Errorf("read Computer upgrade predecessor identity: %w", err)
	}
	controlToken, err := computer.EnsureControlToken(profile)
	if err != nil {
		return err
	}
	deviceName := flagString(cmd, "device-name")
	if deviceName == "" {
		deviceName = strings.TrimSpace(os.Getenv("MULTICA_DAEMON_DEVICE_NAME"))
	}
	if deviceName == "" {
		deviceName, _ = os.Hostname()
	}

	ctx, stop := notifyShutdownContext(context.Background())
	defer stop()

	logger := logger_pkg.NewLogger("computer")
	serviceEndpoint := computer.ServiceControlEndpoint(bindingsRoot)
	launcher := computer.BindingRunnerLauncher{
		ComputerID:  computerID,
		Environment: string(serviceTarget.Environment), Profile: profile, ServerBaseURL: serviceTarget.Origin,
		ServiceEndpoint: serviceEndpoint,
		BindingsRoot:    bindingsRoot, WorkspacesRoot: workspacesRoot,
	}
	host, err := computer.NewHost(computer.HostConfig{
		Spawn: launcher.Spawn, ResidentRoot: bindingsRoot, Logger: logger, ControlToken: controlToken,
	})
	if err != nil {
		return err
	}

	// Publish the resident PID for lifecycle commands. Failure remains
	// best-effort so a read-only state directory does not hide the real process.
	lifecycle := &computer.Lifecycle{}
	cleanupPID := func() {}
	if computer.RootDir(profile) != "" {
		cleanup, err := lifecycle.PublishPID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not write PID file: %v\n", err)
		} else {
			cleanupPID = cleanup
		}
	}
	defer cleanupPID()

	bindingStore := computer.NewBindingsStore(bindingsRoot)
	if err := host.RunProcess(ctx, computer.HostProcessConfig{
		ServiceEndpoint: serviceEndpoint, ResidentRoot: bindingsRoot,
		Identity: computer.HostProcessIdentity{
			ComputerID: computerID, ServiceGeneration: serviceGeneration,
			SourceServicePID: sourceServicePID,
			Environment:      string(serviceTarget.Environment),
			Version:          version, ServerURL: serviceTarget.Origin, DeviceName: deviceName,
		},
		ReleaseManifestURL: os.Getenv("MULTICA_RELEASE_MANIFEST_BASE_URL"),
		DesiredWorkspaceIDs: func() ([]string, error) {
			bindings, err := bindingStore.AllActiveForEnvironment(string(serviceTarget.Environment))
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(bindings))
			for _, binding := range bindings {
				ids = append(ids, binding.WorkspaceID)
			}
			return ids, nil
		},
	}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if restartBin := host.RestartBinary(); restartBin != "" {
		if err := bestEffortSyncInstalledServiceUnit(profile, restartBin); err != nil {
			logger.Warn("could not rewrite OS service unit to activated Computer binary",
				"path", restartBin, "error", err)
		}
		if runningUnderSupervision() {
			logger.Info("restarting Computer with updated binary via supervisor handoff", "path", restartBin)
			os.Exit(daemonHandoffExitCode)
		}
		if err := spawnDetachedUpgradeCoordinator(restartBin, profile); err != nil {
			return fmt.Errorf("start detached Computer upgrade coordinator: %w", err)
		}
		logger.Info("started detached Computer upgrade coordinator", "path", restartBin)
	}

	return nil
}
