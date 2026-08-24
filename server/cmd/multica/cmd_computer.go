package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/daemon"
	logger_pkg "github.com/multica-ai/multica/server/internal/logger"
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

var computerServiceCmd = &cobra.Command{
	Use:    computer.ResidentServiceArg,
	Hidden: true,
	Short:  "Run the Computer resident process",
	Args:   cobra.NoArgs,
	RunE:   runComputerResident,
}

var computerRunnerCmd = &cobra.Command{
	Use:    computer.ResidentRunnerArg,
	Hidden: true,
	Short:  "Run one Workspace Binding child",
	Args:   cobra.NoArgs,
	RunE:   runComputerBindingRunner,
}

var computerUpgradeCoordinatorCmd = &cobra.Command{
	Use:    computer.ResidentUpgradeArg,
	Hidden: true,
	Short:  "Run the detached Machine Upgrade coordinator",
	Args:   cobra.NoArgs,
	RunE:   runComputerUpgradeCoordinator,
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
verify, activate, hand off, and converge the upgrade. When no resident exists,
it installs the verified release for the next Computer start under the same
machine lock. A live but unreachable resident fails closed without changing
the Active release.

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

	addComputerResidentFlags(computerServiceCmd)
	computerRunnerCmd.Flags().String("workspace-id", "", "Workspace Binding identity")
	_ = computerRunnerCmd.MarkFlagRequired("workspace-id")

	computerCmd.AddCommand(computerServiceCmd)
	computerCmd.AddCommand(computerRunnerCmd)
	computerCmd.AddCommand(computerUpgradeCoordinatorCmd)
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

func runComputerBindingRunner(cmd *cobra.Command, _ []string) error {
	workspaceID, _ := cmd.Flags().GetString("workspace-id")
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("workspace-id is required")
	}
	bootstrap, err := computer.ReadBindingChildBootstrap(os.Stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(workspaceID) != bootstrap.WorkspaceID {
		return fmt.Errorf("workspace-id %q does not match Binding child bootstrap %q", workspaceID, bootstrap.WorkspaceID)
	}
	ctx, stop := notifyShutdownContext(context.Background())
	defer stop()
	return runBindingChild(ctx, bootstrap, func(ready computer.BindingChildReady) error {
		return computer.WriteBindingChildReady(os.Stdout, ready)
	})
}

func runBindingChild(ctx context.Context, bootstrap computer.BindingChildBootstrap, publishReady func(computer.BindingChildReady) error) error {
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
	logger := logger_pkg.NewLogger("runner").With("workspace_id", bootstrap.WorkspaceID)
	return daemon.RunBindingChild(ctx, daemon.BindingChildRunConfig{
		Daemon: cfg, Bootstrap: bootstrap, Logger: logger, PublishReady: publishReady,
	})
}

func addComputerResidentFlags(cmd *cobra.Command) {
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
	for _, runner := range d.Runners {
		fmt.Fprintf(os.Stdout, "runner:       workspace=%s pid=%d alive=%v owned=%v\n", runner.WorkspaceID, runner.PID, runner.Alive, runner.Owned)
	}
	for _, f := range d.FixApplied {
		fmt.Fprintf(os.Stdout, "fixed:        %s\n", f)
	}
	for _, runner := range d.UnownedLive {
		fmt.Fprintf(os.Stdout, "degraded:     workspace %s Binding Runner (pid %d) is alive but not owned by this Computer; run `multica computer restart`\n", runner.WorkspaceID, runner.PID)
	}
	// A disconnected resident is non-zero for automation.
	if !d.Connected && d.Resident != "starting" {
		return fmt.Errorf("Computer is not connected")
	}
	if len(d.UnownedLive) > 0 {
		return fmt.Errorf("Computer has %d Binding Runner(s) alive but not owned by this Computer; run `multica computer restart`", len(d.UnownedLive))
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
	ctx, cancel := context.WithTimeout(cmd.Context(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
	defer cancel()
	upgrade, err := (&computer.Lifecycle{ServiceEndpoint: computerUpgradeServiceEndpoint("")}).Upgrade(ctx, computer.UpgradeOptions{
		TargetVersion:    targetVersion,
		CreateLiveIntent: createComputerUpgradeHumanIntent,
	})
	if err != nil {
		return err
	}
	if upgrade.Route == computer.UpgradeRouteLive {
		fmt.Fprintf(os.Stdout,
			"Computer upgrade accepted: %s (target %s). The live Computer owns download, verification, handoff, and convergence.\n",
			strVal(upgrade.Operation, "request_id"),
			upgrade.ResolvedTarget,
		)
		return nil
	}
	if upgrade.AlreadyCurrent {
		fmt.Fprintf(os.Stdout,
			"%s is already Active for the next Computer start (generation %d). Binary: %s\nNo running successor was proven.\n",
			upgrade.ActiveVersion,
			upgrade.Generation,
			upgrade.BinaryPath,
		)
		return nil
	}
	fmt.Fprintf(os.Stdout,
		"Installed %s for the next Computer start (generation %d). Binary: %s\nNo running successor was proven; run `multica computer start` to use this Active release.\n",
		upgrade.ActiveVersion,
		upgrade.Generation,
		upgrade.BinaryPath,
	)
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
