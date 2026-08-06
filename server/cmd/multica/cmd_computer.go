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
	"github.com/multica-ai/multica/server/internal/daemon"
)

var computerCmd = &cobra.Command{
	Use:   "computer",
	Short: "Manage this computer",
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
	computerCmd.AddCommand(computerUpgradeCmd)
}

func runComputerUpgrade(cmd *cobra.Command, _ []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	daemonID, err := daemon.EnsureDaemonID(resolveProfile(cmd))
	if err != nil {
		return fmt.Errorf("resolve local computer identity: %w", err)
	}
	targetVersion, _ := cmd.Flags().GetString("target-version")
	ctx, cancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(150*time.Second))
	defer cancel()

	body := map[string]any{"target_version": strings.TrimSpace(targetVersion), "request_id": uuid.NewString()}
	var upgrade map[string]any
	if err := client.PostJSON(ctx, "/api/daemons/"+daemonID+"/upgrades", body, &upgrade); err != nil {
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
		if err := client.GetJSON(ctx, "/api/daemons/"+daemonID+"/upgrades/"+upgradeID, &upgrade); err != nil {
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
