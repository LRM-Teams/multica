package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/daemon"
	logger_pkg "github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
)

var computerCmd = &cobra.Command{
	Use:   "computer",
	Short: "Manage this computer",
	// The Computer is machine-wide: Workspace selectors are scoping only and
	// never select a profile or a second resident.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := rejectRetiredComputerFlags(cmd); err != nil {
			return err
		}
		computerMode = true
		return nil
	},
}

func rejectRetiredComputerFlags(cmd *cobra.Command) error {
	if err := rejectRetiredComputerProfileFlag(cmd); err != nil {
		return err
	}
	if flag := cmd.Flags().Lookup("server-url"); flag != nil && cmd.Flags().Changed("server-url") {
		return fmt.Errorf("--server-url is not supported by the machine-wide Cloud Computer")
	}
	return nil
}

func rejectRetiredComputerProfileFlag(cmd *cobra.Command) error {
	if flag := cmd.Flags().Lookup("profile"); flag != nil && cmd.Flags().Changed("profile") {
		return fmt.Errorf("--profile is not supported by the machine-wide Cloud Computer")
	}
	return nil
}

var computerRunCmd = &cobra.Command{
	Use:    computer.ResidentServiceArg,
	Hidden: true,
	Short:  "Run the Computer",
	Args:   cobra.NoArgs,
	RunE:   run,
}

// run starts the machine-wide Computer. ComputerCore owns the Computer
// lifecycle, while DaemonCore owns WorkspaceDaemon processes for active bindings.
func run(cmd *cobra.Command, _ []string) error {
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
	workspacesRoot, err := computer.ResolveComputerWorkspacesRoot()
	if err != nil {
		return err
	}
	bindingsRoot := computer.RootDir("")
	serviceGeneration := uuid.NewString()
	sourceServicePID, err := computer.PendingMachineUpgradeSourceServicePID(bindingsRoot)
	if err != nil {
		return fmt.Errorf("read Computer upgrade predecessor identity: %w", err)
	}
	if explicitSourcePID, flagErr := cmd.Flags().GetInt("source-service-pid"); flagErr == nil && explicitSourcePID > 0 {
		sourceServicePID = explicitSourcePID
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
	launcher := computer.WorkspaceDaemonLauncher{
		ComputerID:  computerID,
		Environment: string(serviceTarget.Environment), Profile: profile, ServerBaseURL: serviceTarget.Origin,
		ServiceEndpoint: serviceEndpoint,
		BindingsRoot:    bindingsRoot, WorkspacesRoot: workspacesRoot,
	}
	computerCore, err := computer.NewComputerCore(computer.ComputerCoreConfig{
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
	if err := computerCore.Run(ctx, computer.ComputerProcessConfig{
		ServiceEndpoint: serviceEndpoint, ResidentRoot: bindingsRoot,
		Identity: computer.ComputerIdentity{
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
	if restartPlan := computerCore.RestartPlan(); restartPlan.BinaryPath != "" {
		restartBin := restartPlan.BinaryPath
		if err := bestEffortSyncInstalledServiceUnit(profile, restartBin); err != nil {
			logger.Warn("could not rewrite OS service unit to activated Computer binary",
				"path", restartBin, "error", err)
		}
		if runningUnderSupervision() {
			logger.Info("restarting Computer with updated binary via process supervisor handoff", "path", restartBin)
			os.Exit(daemonHandoffExitCode)
		}
		if handoff := restartPlan.CurrentBinaryHandoff; handoff != nil {
			if err := spawnDetachedRestartCoordinator(restartBin, profile, *handoff); err != nil {
				return fmt.Errorf("start detached Computer restart coordinator: %w", err)
			}
			logger.Info("started detached Computer restart coordinator", "path", restartBin)
			return nil
		}
		if err := spawnDetachedUpgradeCoordinator(restartBin, profile); err != nil {
			return fmt.Errorf("start detached Computer upgrade coordinator: %w", err)
		}
		logger.Info("started detached Computer upgrade coordinator", "path", restartBin)
	}

	return nil
}

var computerWorkspaceDaemonCmd = &cobra.Command{
	Use:    computer.WorkspaceDaemonArg,
	Hidden: true,
	Short:  "Run one WorkspaceDaemon process",
	Args:   cobra.NoArgs,
	RunE:   runWorkspaceDaemonCommand,
}

var computerUpgradeCoordinatorCmd = &cobra.Command{
	Use:    computer.ResidentUpgradeArg,
	Hidden: true,
	Short:  "Run the detached Machine Upgrade coordinator",
	Args:   cobra.NoArgs,
	RunE:   runComputerUpgradeCoordinator,
}

var computerRestartCoordinatorCmd = &cobra.Command{
	Use:    computer.ResidentRestartArg + " <handoff>",
	Hidden: true,
	Short:  "Run the detached current-binary restart coordinator",
	Args:   cobra.ExactArgs(1),
	RunE:   runComputerRestartCoordinator,
}

var computerStartCmd = &cobra.Command{
	Use:   "start [/<workspace>]",
	Short: "Start the resident Computer",
	Long:  "Start the machine-wide resident Computer that polls for tasks and executes them using local agent CLIs (Claude, Codex).\nRuns detached in the background by default. Use --foreground to run in the current terminal.",
	Args:  optionalWorkspacePath,
	RunE:  runComputerStart,
}

var computerStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the resident Computer",
	Args:  cobra.NoArgs,
	RunE:  runComputerStop,
}

var computerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show resident Computer status",
	Args:  cobra.NoArgs,
	RunE:  runComputerStatus,
}

var computerRestartCmd = &cobra.Command{
	Use:   "restart [/<workspace>]",
	Short: "Restart the resident Computer (stop + start)",
	Args:  optionalWorkspacePath,
	RunE:  runComputerRestart,
}

var computerLogsCmd = &cobra.Command{
	Use:   "logs [/<workspace>]",
	Short: "Show resident Computer service logs",
	Args:  optionalWorkspacePath,
	RunE:  runComputerLogs,
}

var computerDoctorCmd = &cobra.Command{
	Use:   "doctor [/<workspace>]",
	Short: "Diagnose the Computer (read-only evidence)",
	Args:  optionalWorkspacePath,
	RunE:  runComputerDoctor,
}

var computerUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade this computer",
	Long: `Upgrade the resident Computer through its single machine owner.

When the Computer is running, the command asks that live owner to download,
verify, activate, hand off, and converge the upgrade. It waits by default and
shows each real phase until the successor reconnects; use --no-wait to return
after submission. When no resident exists, it installs the verified release
for the next Computer start under the same machine lock. A live but unreachable
resident fails closed without changing the Active release.

Production uses stable packages and test uses preview packages. Pass
--target-version only to install a specific immutable recovery version.`,
	Args: cobra.NoArgs,
	RunE: runComputerUpgrade,
}

var computerUpgradeServiceEndpoint = func(profile string) string {
	return computer.ServiceControlEndpoint(computer.RootDir(profile))
}

var computerIdentityCmd = &cobra.Command{
	Use:   "identity",
	Short: "Inspect or explicitly resolve Computer identity evidence",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return cli.PrintJSON(os.Stdout, computer.NewIdentityStore(computer.RootDir("")).Peek(""))
	},
}

var computerIdentityAdoptCmd = &cobra.Command{
	Use:   "adopt <computer-id>",
	Short: "Adopt a preserved legacy Computer identity",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if err := requireComputerStoppedForIdentityChange(); err != nil {
			return err
		}
		result, err := computer.NewIdentityStore(computer.RootDir("")).Adopt(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Adopted Computer identity %s. Preserved legacy evidence was not deleted.\n", result.ID)
		return nil
	},
}

var computerIdentityFreshCmd = &cobra.Command{
	Use:   "fresh",
	Short: "Explicitly create a new Computer identity while preserving legacy evidence",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := requireComputerStoppedForIdentityChange(); err != nil {
			return err
		}
		result, err := computer.NewIdentityStore(computer.RootDir("")).CreateFresh()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Computer identity is %s. Preserved legacy evidence was not deleted.\n", result.ID)
		return nil
	},
}

func init() {
	computerUpgradeCmd.Flags().String("target-version", "", "Target version to install (default: latest)")
	computerUpgradeCmd.Flags().Bool("no-wait", false, "Submit a live upgrade without waiting for restart and reconnection")

	// Machine-wide flags used by the lifecycle commands. These live on the
	// parent so both start and restart see them; resolveProfile is forced to
	// "" in computer mode, so --profile is deliberately NOT exposed.
	f := computerStartCmd.Flags()
	f.Bool("foreground", false, "Run in the foreground instead of background")
	f.String("daemon-id", "", "Unique daemon identifier (env: MULTICA_DAEMON_ID)")
	f.String("device-name", "", "Human-readable device name (env: MULTICA_DAEMON_DEVICE_NAME)")
	f.String("runtime-name", "", "Runtime display name (env: MULTICA_AGENT_RUNTIME_NAME)")
	f.Duration("poll-interval", 0, "Task poll interval (env: MULTICA_DAEMON_POLL_INTERVAL)")
	f.Duration("heartbeat-interval", 0, "Heartbeat interval (env: MULTICA_DAEMON_HEARTBEAT_INTERVAL)")
	f.Duration("agent-timeout", 0, "Absolute per-task wall-clock cap; 0 = no cap, rely on the watchdogs (env: MULTICA_AGENT_TIMEOUT)")
	f.Duration("codex-semantic-inactivity-timeout", 0, "Codex semantic inactivity timeout (env: MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT)")

	computerStatusCmd.Flags().String("output", "table", "Output format: table or json")
	computerLogsCmd.Flags().BoolP("follow", "f", false, "Follow log output")
	computerLogsCmd.Flags().IntP("lines", "n", 50, "Number of lines to show")
	computerDoctorCmd.Flags().Bool("fix", false, "Apply only provably safe stale-state cleanup")
	computerDoctorCmd.Flags().String("output", "table", "Output format: table or json")

	addRunFlags(computerRunCmd)
	computerRunCmd.Flags().Int("source-service-pid", 0, "Predecessor Computer service PID")
	computerWorkspaceDaemonCmd.Flags().String("workspace-id", "", "Workspace identity")
	_ = computerWorkspaceDaemonCmd.MarkFlagRequired("workspace-id")

	computerCmd.AddCommand(computerRunCmd)
	computerCmd.AddCommand(computerWorkspaceDaemonCmd)
	computerCmd.AddCommand(computerUpgradeCoordinatorCmd)
	computerCmd.AddCommand(computerRestartCoordinatorCmd)
	computerSuperviseCmd.Hidden = true
	computerCmd.AddCommand(computerSuperviseCmd)
	computerCmd.AddCommand(computerStartCmd)
	computerCmd.AddCommand(computerStopCmd)
	computerCmd.AddCommand(computerStatusCmd)
	computerCmd.AddCommand(computerRestartCmd)
	computerCmd.AddCommand(computerLogsCmd)
	computerCmd.AddCommand(computerDoctorCmd)
	computerCmd.AddCommand(computerUpgradeCmd)
	computerIdentityCmd.AddCommand(computerIdentityAdoptCmd)
	computerIdentityCmd.AddCommand(computerIdentityFreshCmd)
	computerCmd.AddCommand(computerIdentityCmd)
}

func runWorkspaceDaemonCommand(cmd *cobra.Command, _ []string) error {
	workspaceID, _ := cmd.Flags().GetString("workspace-id")
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("workspace-id is required")
	}
	bootstrap, err := computer.ReadWorkspaceDaemonBootstrap(os.Stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(workspaceID) != bootstrap.WorkspaceID {
		return fmt.Errorf("workspace-id %q does not match WorkspaceDaemon bootstrap %q", workspaceID, bootstrap.WorkspaceID)
	}
	ctx, stop := notifyShutdownContext(context.Background())
	defer stop()
	return runWorkspaceDaemon(ctx, bootstrap, func(ready computer.WorkspaceDaemonReady) error {
		return computer.WriteWorkspaceDaemonReady(os.Stdout, ready)
	})
}

func runWorkspaceDaemon(ctx context.Context, bootstrap computer.WorkspaceDaemonBootstrap, publishReady func(computer.WorkspaceDaemonReady) error) error {
	cfg, err := daemon.LoadConfig(daemon.Overrides{
		ServerURL:      bootstrap.ServerBaseURL,
		WorkspacesRoot: bootstrap.WorkspacesRoot,
		DaemonID:       bootstrap.ComputerID,
		Profile:        bootstrap.Profile,
	})
	if err != nil {
		return err
	}
	cfg.CLIVersion = version
	cfg.Environment = bootstrap.Environment
	cfg.ServerBaseURL = bootstrap.ServerBaseURL
	cfg.DaemonID = bootstrap.ComputerID
	cfg.BindingsRoot = bootstrap.BindingsRoot
	cfg.WorkspacesRoot = bootstrap.WorkspacesRoot
	controlToken, err := computer.ReadControlToken(bootstrap.Profile)
	if err != nil {
		return fmt.Errorf("read ComputerCore control token: %w", err)
	}
	cfg.LocalControlToken = controlToken
	logger := logger_pkg.NewLogger("workspace-daemon").With("workspace_id", bootstrap.WorkspaceID)
	return daemon.RunWorkspaceDaemonProcess(ctx, daemon.WorkspaceDaemonProcessConfig{
		Daemon: cfg, Bootstrap: bootstrap, Logger: logger, PublishReady: publishReady,
	})
}

func addRunFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("daemon-id", "", "Unique daemon identifier (env: MULTICA_DAEMON_ID)")
	f.String("device-name", "", "Human-readable device name (env: MULTICA_DAEMON_DEVICE_NAME)")
	f.String("runtime-name", "", "Runtime display name (env: MULTICA_AGENT_RUNTIME_NAME)")
	f.Duration("poll-interval", 0, "Task poll interval (env: MULTICA_DAEMON_POLL_INTERVAL)")
	f.Duration("heartbeat-interval", 0, "Heartbeat interval (env: MULTICA_DAEMON_HEARTBEAT_INTERVAL)")
	f.Duration("agent-timeout", 0, "Absolute per-task wall-clock cap; 0 = no cap, rely on the watchdogs (env: MULTICA_AGENT_TIMEOUT)")
	f.Duration("codex-semantic-inactivity-timeout", 0, "Codex semantic inactivity timeout (env: MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT)")
}

func requireComputerStoppedForIdentityChange() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if computer.Alive((&computer.Lifecycle{}).Health(ctx)) {
		return fmt.Errorf("stop the Computer before changing its identity")
	}
	return nil
}

func runComputerDoctor(cmd *cobra.Command, args []string) error {
	binding, selected, err := resolveWorkspaceBinding(args)
	if err != nil {
		return err
	}
	lc := &computer.Lifecycle{}
	d := lc.Diagnose()
	if selected {
		d.SelectedWorkspaceID = binding.WorkspaceID
		d.SelectedWorkspaceSlug = binding.WorkspaceSlug
		d.SelectedConnectionActive = binding.Active
	}
	if fix, _ := cmd.Flags().GetBool("fix"); fix {
		d = lc.Fix(d)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, d)
	}
	fmt.Fprintf(os.Stdout, "identity:     %s (%s)\n", d.IdentityState, orDash(d.ComputerID))
	fmt.Fprintf(os.Stdout, "resident:     %s\n", d.Resident)
	fmt.Fprintf(os.Stdout, "connections:  %d\n", d.WorkspaceConnections)
	fmt.Fprintf(os.Stdout, "connected:    %v\n", d.Connected)
	fmt.Fprintf(os.Stdout, "environment:  %s\n", orDash(d.Environment))
	fmt.Fprintf(os.Stdout, "service:      %s\n", orDash(d.ServiceOrigin))
	fmt.Fprintf(os.Stdout, "package:      %s\n", orDash(d.PackageSource))
	fmt.Fprintf(os.Stdout, "resident env: %s\n", orDash(d.ResidentEnvironment))
	fmt.Fprintf(os.Stdout, "resident svc: %s\n", orDash(d.ResidentServiceOrigin))
	fmt.Fprintf(os.Stdout, "resident pkg: %s\n", orDash(d.ResidentPackageSource))
	fmt.Fprintf(os.Stdout, "config drift: %v\n", d.ConfigurationDrift)
	fmt.Fprintf(os.Stdout, "canonical:    %s\n", d.CanonicalHost)
	for _, candidate := range d.LegacyIdentityCandidates {
		fmt.Fprintf(os.Stdout, "legacy id:    %s (preserved; explicit choice required)\n", candidate)
	}
	for _, workspaceDaemon := range d.WorkspaceDaemons {
		fmt.Fprintf(os.Stdout, "workspace daemon: workspace=%s pid=%d alive=%v owned=%v\n", workspaceDaemon.WorkspaceID, workspaceDaemon.PID, workspaceDaemon.Alive, workspaceDaemon.Owned)
	}
	for _, f := range d.FixApplied {
		fmt.Fprintf(os.Stdout, "fixed:        %s\n", f)
	}
	for _, workspaceDaemon := range d.UnownedLive {
		fmt.Fprintf(os.Stdout, "degraded:     WorkspaceDaemon for %s (pid %d) is alive but not owned by this Computer; run `multica computer restart`\n", workspaceDaemon.WorkspaceID, workspaceDaemon.PID)
	}
	// A disconnected resident is non-zero for automation.
	if !d.Connected && d.Resident != "starting" {
		return fmt.Errorf("Computer is not connected")
	}
	if len(d.UnownedLive) > 0 {
		return fmt.Errorf("Computer has %d WorkspaceDaemon process(es) alive but not owned by this Computer; run `multica computer restart`", len(d.UnownedLive))
	}
	return nil
}

// optionalWorkspacePath accepts an immutable-id or display-slug selector only
// in the public /workspace form. It scopes readiness/diagnostics/log filtering;
// it never selects a profile or changes which Bindings the resident restores.
func optionalWorkspacePath(_ *cobra.Command, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("accepts at most one Workspace selector")
	}
	if len(args) == 1 && (!strings.HasPrefix(args[0], "/") || len(args[0]) == 1) {
		return fmt.Errorf("Workspace selector must be /<workspace-id-or-slug>")
	}
	return nil
}

func resolveWorkspaceBinding(args []string) (computer.WorkspaceBinding, bool, error) {
	if len(args) == 0 {
		return computer.WorkspaceBinding{}, false, nil
	}
	selector := strings.TrimPrefix(args[0], "/")
	cfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return computer.WorkspaceBinding{}, true, fmt.Errorf("load current service environment: %w", err)
	}
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return computer.WorkspaceBinding{}, true, err
	}
	bindings, err := computer.NewBindingsStore(computer.RootDir("")).AllActiveForEnvironment(string(target.Environment))
	if err != nil {
		return computer.WorkspaceBinding{}, true, fmt.Errorf("load Computer Workspace connections: %w", err)
	}
	var matched []computer.WorkspaceBinding
	for _, binding := range bindings {
		if binding.WorkspaceID == selector || binding.WorkspaceSlug == selector {
			matched = append(matched, binding)
		}
	}
	if len(matched) == 0 {
		return computer.WorkspaceBinding{}, true, fmt.Errorf("Workspace %q is not connected; run `multica setup /%s`", selector, selector)
	}
	if len(matched) > 1 {
		return computer.WorkspaceBinding{}, true, fmt.Errorf("Workspace selector %q is ambiguous; use the immutable Workspace id", selector)
	}
	return matched[0], true, nil
}

func orDash(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func runComputerUpgrade(cmd *cobra.Command, _ []string) error {
	targetVersion, _ := cmd.Flags().GetString("target-version")
	noWait, _ := cmd.Flags().GetBool("no-wait")
	displayTarget := strings.TrimSpace(targetVersion)
	if displayTarget == "" {
		displayTarget = "latest"
	}
	endpoint := computerUpgradeServiceEndpoint("")
	probeCtx, probeCancel := context.WithTimeout(cmd.Context(), 2*time.Second)
	initialHealth := (&computer.Lifecycle{ServiceEndpoint: endpoint}).Health(probeCtx)
	probeCancel()
	currentVersion := version
	if computer.Alive(initialHealth) {
		currentVersion = healthValue(initialHealth, "cliVersion")
	}
	display := newComputerUpgradeDisplay(os.Stdout, currentVersion, displayTarget, stdoutIsInteractive())
	display.update("requesting", "Resolving release and contacting Computer")

	ctx, cancel := context.WithTimeout(cmd.Context(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
	defer cancel()
	type upgradeOutcome struct {
		result computer.UpgradeResult
		err    error
	}
	outcomeCh := make(chan upgradeOutcome, 1)
	go func() {
		result, err := (&computer.Lifecycle{ServiceEndpoint: endpoint}).Upgrade(ctx, computer.UpgradeOptions{
			TargetVersion: targetVersion, CreateLiveIntent: createComputerUpgradeHumanIntent,
		})
		outcomeCh <- upgradeOutcome{result: result, err: err}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var outcome upgradeOutcome
	waiting := true
	for waiting {
		select {
		case outcome = <-outcomeCh:
			waiting = false
		case <-ticker.C:
			display.update("requesting", "Resolving release and contacting Computer")
		case <-ctx.Done():
			display.clear()
			return ctx.Err()
		}
	}
	if outcome.err != nil {
		display.clear()
		return outcome.err
	}
	upgrade := outcome.result
	if upgrade.Route == computer.UpgradeRouteLive {
		requestID := strVal(upgrade.Operation, "request_id")
		if noWait {
			display.accepted(requestID, upgrade.ResolvedTarget)
			return nil
		}
		display.update("accepted", "Waiting for Computer to begin")
		watchCtx, watchCancel := context.WithTimeout(cmd.Context(), computerUpgradeWatchTimeout)
		defer watchCancel()
		result, err := watchComputerUpgrade(watchCtx, endpoint, requestID, initialHealth, display)
		if err != nil {
			display.clear()
			return err
		}
		display.success(result)
		return nil
	}
	display.installed(upgrade.ActiveVersion, upgrade.BinaryPath, upgrade.AlreadyCurrent)
	return nil
}

func createComputerUpgradeHumanIntent(ctx context.Context, computerID, requestID, targetVersion string) (map[string]any, error) {
	cfg, err := cli.LoadCLIConfigForProfile("")
	if err != nil {
		return nil, fmt.Errorf("load human session: %w", err)
	}
	target, err := cli.ResolveServiceTarget(cfg)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("human session is missing; run `multica login`")
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" {
		return nil, fmt.Errorf("human session has no Workspace context; run `multica setup /<workspace>`")
	}
	client := cli.NewAPIClient(target.Origin, cfg.WorkspaceID, cfg.Token)
	var operation map[string]any
	path := fmt.Sprintf("/api/daemons/%s/upgrades", url.PathEscape(strings.TrimSpace(computerID)))
	if err := client.PostJSON(ctx, path, map[string]string{
		"request_id": requestID, "target_version": targetVersion,
	}, &operation); err != nil {
		return nil, err
	}
	return operation, nil
}
