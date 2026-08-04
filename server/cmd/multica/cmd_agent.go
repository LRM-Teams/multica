package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// agentCmd is intentionally a leaf with no subcommands.
// Product (Frank/Parker 2026-08-04): remove Multica agent management CLI;
// align with Raft (list via workspace info; manage via Web UI / action cards).
var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Removed — use workspace info --agents and the Web UI",
	Long: `The multica agent * management CLI has been removed.

List agents:
  multica workspace info --agents
  multica workspace info --agents --output json

Create / edit / archive agents:
  Use the Multica Web UI (or agent:create action cards for hiring).

Self-profile CLI (align raft profile) may return later.`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("multica agent CLI was removed; list with: multica workspace info --agents\nManage agents in the Web UI")
	},
}
