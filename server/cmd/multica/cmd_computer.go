package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
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
	for _, name := range []string{"profile", "server-url"} {
		if flag := cmd.Flags().Lookup(name); flag != nil && cmd.Flags().Changed(name) {
			return fmt.Errorf("--%s is not supported by the machine-wide Cloud Computer", name)
		}
	}
	return nil
}

var computerStartCmd = &cobra.Command{
	Use:   "start [/<workspace>]",
	Short: "Start the resident Computer",
	Long:  "Start the machine-wide resident Computer that polls for tasks and executes them using local agent CLIs (Claude, Codex).\nRuns detached in the background by default. Use --foreground to run in the current terminal.",
	Args:  optionalWorkspacePath,
	RunE:  runDaemonStart,
}

var computerStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the resident Computer",
	Args:  cobra.NoArgs,
	RunE:  runDaemonStop,
}

var computerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show resident Computer status",
	Args:  cobra.NoArgs,
	RunE:  runDaemonStatus,
}

var computerRestartCmd = &cobra.Command{
	Use:   "restart [/<workspace>]",
	Short: "Restart the resident Computer (stop + start)",
	Args:  optionalWorkspacePath,
	RunE:  runDaemonRestart,
}

var computerLogsCmd = &cobra.Command{
	Use:   "logs [/<workspace>]",
	Short: "Show resident Computer service logs",
	Args:  optionalWorkspacePath,
	RunE:  runDaemonLogs,
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
	Long:  "Upgrade the resident Computer. Production uses stable packages and test uses preview packages; pass --target-version only to install a specific immutable recovery version.",
	Args:  cobra.NoArgs,
	RunE:  runComputerUpgrade,
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
	computerUpgradeCmd.Flags().String("output", "json", "Output format: table or json")
	computerUpgradeCmd.Flags().Bool("wait", false, "Wait for the computer upgrade to complete")

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
	for _, f := range d.FixApplied {
		fmt.Fprintf(os.Stdout, "fixed:        %s\n", f)
	}
	// A disconnected resident is non-zero for automation.
	if !d.Connected && d.Resident != "starting" {
		return fmt.Errorf("Computer is not connected")
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
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	targetVersion, _ := cmd.Flags().GetString("target-version")
	wait, _ := cmd.Flags().GetBool("wait")
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(150*time.Second))
	defer cancel()
	upgrade, err := (&computer.Lifecycle{}).Upgrade(ctx, client, computer.UpgradeOptions{
		TargetVersion: targetVersion,
		Wait:          wait,
	})
	if err != nil {
		return err
	}
	return printComputerUpgrade(cmd, upgrade)
}

func printComputerUpgrade(cmd *cobra.Command, upgrade map[string]any) error {
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, upgrade)
	}
	phase := strVal(upgrade, "phase")
	if phase == "completed" {
		fmt.Printf("Computer upgrade completed: %s\n", strVal(upgrade, "resolved_target"))
	} else if phase == "failed" || phase == "rolled_back" || phase == "timeout" || phase == "cancelled" {
		fmt.Printf("Computer upgrade %s: %s\n", phase, strVal(upgrade, "error_message"))
	} else {
		fmt.Printf("Computer upgrade initiated: %s (phase: %s)\n", strVal(upgrade, "id"), phase)
	}
	return nil
}
