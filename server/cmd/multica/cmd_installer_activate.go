package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

var installerActivateCmd = &cobra.Command{
	Use:    "installer-activate",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		version, _ := cmd.Flags().GetString("version")
		sha256, _ := cmd.Flags().GetString("sha256")
		launcher, _ := cmd.Flags().GetString("launcher")
		if version == "" || sha256 == "" || launcher == "" {
			return fmt.Errorf("version, sha256, and launcher are required")
		}
		candidate, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve installer candidate: %w", err)
		}
		store, err := cli.OpenVersionStore("")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var result cli.LauncherInstallResult
		if err := store.WithMachineMutationLock(ctx, func() error {
			var installErr error
			result, installErr = store.InstallLauncherRelease(ctx, candidate, version, sha256, launcher)
			return installErr
		}); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed %s through VersionStore generation %d at %s\n", result.State.ActiveVersion, result.State.Generation, result.LauncherPath)
		return nil
	},
}

func init() {
	installerActivateCmd.Flags().String("version", "", "expected immutable release version")
	installerActivateCmd.Flags().String("sha256", "", "expected extracted binary SHA-256")
	installerActivateCmd.Flags().String("launcher", "", "stable launcher path")
}
