package main

import (
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
)

// uuidRegexp matches a canonical UUID (8-4-4-4-12 hex). Shared by CLI resolvers.
var uuidRegexp = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)


// LRM-1049: Autopilot CLI hard-cut. Subcommands remain as stubs so old scripts
// get a clear error instead of a silent no-op.
var autopilotCmd = &cobra.Command{
	Use:   "autopilot",
	Short: "Removed — use multica reminder (LRM-1049)",
	RunE:  runAutopilotRetired,
}

func runAutopilotRetired(cmd *cobra.Command, _ []string) error {
	return fmt.Errorf("autopilot has been removed (LRM-1049); use `multica reminder` for agent self-wake schedules")
}

func init() {
	// Preserve leaf command names so `multica autopilot create` still resolves
	// to the retired error instead of "unknown command".
	for _, use := range []string{
		"list", "get", "create", "update", "delete", "trigger", "runs",
		"trigger-add", "trigger-update", "trigger-delete", "trigger-rotate-url",
	} {
		leaf := &cobra.Command{
			Use:  use,
			RunE: runAutopilotRetired,
			Args: cobra.ArbitraryArgs,
		}
		autopilotCmd.AddCommand(leaf)
	}
}
