package main

import (
	"fmt"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/spf13/cobra"
)

var installerActivateCmd = &cobra.Command{
	Use:    "installer-activate",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		launcher, _ := cmd.Flags().GetString("launcher")
		if launcher == "" {
			return fmt.Errorf("launcher is required")
		}
		candidate, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve installer candidate: %w", err)
		}
		if err := cli.SwapExecutable(launcher, candidate); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed to %s\n", launcher)
		return nil
	},
}

func init() {
	installerActivateCmd.Flags().String("version", "", "expected immutable release version")
	installerActivateCmd.Flags().String("sha256", "", "expected extracted binary SHA-256")
	installerActivateCmd.Flags().String("launcher", "", "stable launcher path")
}
