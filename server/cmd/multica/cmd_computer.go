package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
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
		computerMode = true
		return nil
	},
}

var computerStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the resident Computer",
	Long:  "Start the machine-wide resident Computer that polls for tasks and executes them using local agent CLIs (Claude, Codex).\nRuns detached in the background by default. Use --foreground to run in the current terminal.",
	Args:  cobra.NoArgs,
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
	Use:   "restart",
	Short: "Restart the resident Computer (stop + start)",
	Args:  cobra.NoArgs,
	RunE:  runDaemonRestart,
}

var computerLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show resident Computer service logs",
	Args:  cobra.NoArgs,
	RunE:  runDaemonLogs,
}

var computerDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose the Computer (read-only evidence)",
	Args:  cobra.NoArgs,
	RunE:  runComputerDoctor,
}

var computerUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade this computer",
	Long:  "Upgrade the daemon on this computer. By default it requests the latest version; pass --target-version to install a specific version.",
	Args:  cobra.NoArgs,
	RunE:  runComputerUpgrade,
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
}

func runComputerDoctor(cmd *cobra.Command, _ []string) error {
	lc := &computer.Lifecycle{Profile: ""}
	d := lc.Diagnose()
	if fix, _ := cmd.Flags().GetBool("fix"); fix {
		d = lc.Fix(d)
	}
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, d)
	}
	fmt.Fprintf(os.Stdout, "identity:     %s (%s)\n", d.IdentityState, orDash(d.ComputerID))
	fmt.Fprintf(os.Stdout, "resident:     %s\n", d.Resident)
	fmt.Fprintf(os.Stdout, "bindings:     %d\n", d.Bindings)
	fmt.Fprintf(os.Stdout, "connected:    %v\n", d.Connected)
	fmt.Fprintf(os.Stdout, "canonical:    %s\n", d.CanonicalHost)
	for _, f := range d.FixApplied {
		fmt.Fprintf(os.Stdout, "fixed:        %s\n", f)
	}
	// A disconnected resident is non-zero for automation.
	if !d.Connected && d.Resident != "starting" {
		return fmt.Errorf("Computer is not connected")
	}
	return nil
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

	identity, err := (&computer.Lifecycle{Profile: resolveProfile(cmd)}).Identity()
	if err != nil {
		return fmt.Errorf("resolve local computer identity: %w", err)
	}
	targetVersion, _ := cmd.Flags().GetString("target-version")
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(150*time.Second))
	defer cancel()

	body := map[string]any{"target_version": strings.TrimSpace(targetVersion), "request_id": uuid.NewString()}
	var upgrade map[string]any
	if err := client.PostJSON(ctx, "/api/daemons/"+identity+"/upgrades", body, &upgrade); err != nil {
		return fmt.Errorf("create computer upgrade: %w", err)
	}

	wait, _ := cmd.Flags().GetBool("wait")
	if !wait {
		return printComputerUpgrade(cmd, upgrade)
	}
	upgradeID := strVal(upgrade, "id")
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for computer upgrade (last phase: %s)", strVal(upgrade, "phase"))
		case <-time.After(2 * time.Second):
		}
		if err := client.GetJSON(ctx, "/api/daemons/"+identity+"/upgrades/"+upgradeID, &upgrade); err != nil {
			return fmt.Errorf("get computer upgrade status: %w", err)
		}
		phase := strVal(upgrade, "phase")
		if phase != "completed" && phase != "failed" && phase != "rolled_back" && phase != "timeout" && phase != "cancelled" {
			continue
		}
		return printComputerUpgrade(cmd, upgrade)
	}
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
