package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/turntransport"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// debugFlag is bound to the persistent --debug flag and, when set, makes
// FormatError emit the full original error chain instead of just the
// user-facing message.
var debugFlag bool

var rootCmd = &cobra.Command{
	Use:           "multica",
	Short:         "Multica CLI — local agent runtime and management tool",
	Long:          "Work seamlessly with Multica from the command line.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)\ngo: %s, os/arch: %s/%s", version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	rootCmd.SetVersionTemplate("multica {{.Version}}\n")

	// Tag every CLI HTTP request with this binary's build version so the
	// server can split logs/metrics by client version.
	cli.ClientVersion = version

	rootCmd.PersistentFlags().String("server-url", "", "Multica server URL (env: MULTICA_SERVER_URL)")
	rootCmd.PersistentFlags().String("workspace-id", "", "Workspace ID (env: MULTICA_WORKSPACE_ID)")
	rootCmd.PersistentFlags().String("profile", "", "Configuration profile name (e.g. dev) — isolates config and daemon state")
	// Kept parseable for one compatibility cycle by non-Computer management
	// commands, but retired from public help and rejected by setup/computer.
	_ = rootCmd.PersistentFlags().MarkHidden("server-url")
	_ = rootCmd.PersistentFlags().MarkHidden("profile")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Print full error details on failure (env: MULTICA_DEBUG)")

	// Core commands
	issueCmd.GroupID = groupCore
	projectCmd.GroupID = groupCore
	labelCmd.GroupID = groupCore
	workspaceCmd.GroupID = groupCore
	skillCmd.GroupID = groupCore
	memoryCmd.GroupID = groupCore
	messageCmd.GroupID = groupCore
	channelCmd.GroupID = groupCore
	goalCmd.GroupID = groupCore
	threadCmd.GroupID = groupCore
	reminderCmd.GroupID = groupCore
	actionCmd.GroupID = groupCore
	migrationCmd.GroupID = groupCore
	workLeaseCmd.GroupID = groupCore

	// Runtime commands
	computerCmd.GroupID = groupRuntime
	daemonCmd.GroupID = groupRuntime
	runtimeCmd.GroupID = groupRuntime

	// Additional commands
	authCmd.GroupID = groupAdditional
	userCmd.GroupID = groupAdditional
	loginCmd.GroupID = groupAdditional
	setupCmd.GroupID = groupAdditional
	attachmentCmd.GroupID = groupAdditional
	configCmd.GroupID = groupAdditional
	updateCmd.GroupID = groupAdditional
	versionCmd.GroupID = groupAdditional

	rootCmd.AddCommand(issueCmd)
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(researchCmd)
	rootCmd.AddCommand(labelCmd)
	rootCmd.AddCommand(workspaceCmd)
	rootCmd.AddCommand(skillCmd)
	rootCmd.AddCommand(memoryCmd)
	rootCmd.AddCommand(messageCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(computerCmd)
	rootCmd.AddCommand(runtimeCmd)
	rootCmd.AddCommand(authCmd)
	rootCmd.AddCommand(userCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(attachmentCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(channelCmd)
	rootCmd.AddCommand(goalCmd)
	rootCmd.AddCommand(threadCmd)
	rootCmd.AddCommand(reminderCmd)
	rootCmd.AddCommand(actionCmd)
	rootCmd.AddCommand(migrationCmd)
	rootCmd.AddCommand(workLeaseCmd)
	rootCmd.AddCommand(stickerCmd)
	rootCmd.AddCommand(sandboxdCmd)

	initHelp(rootCmd)
}

func main() {
	if err := turntransport.ApplyFromEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "agent transport unavailable: %v\n", err)
		os.Exit(1)
	}
	cli.CleanupStaleUpdateArtifacts()
	if err := rootCmd.Execute(); err != nil {
		if err != errSilent {
			fmt.Fprintln(os.Stderr, cli.FormatError(err, debugFlag))
		}
		os.Exit(cli.ExitCodeFor(err))
	}
}
