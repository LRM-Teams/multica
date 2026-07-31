package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var updateDownloadTimeout time.Duration = cli.DefaultUpdateDownloadTimeout

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update multica to the latest version",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().DurationVar(&updateDownloadTimeout, "download-timeout", cli.DefaultUpdateDownloadTimeout, "Maximum time to wait for the release archive download")
}

func runUpdate(_ *cobra.Command, _ []string) error {
	if updateDownloadTimeout <= 0 {
		return fmt.Errorf("download timeout must be greater than zero")
	}

	fmt.Fprintf(os.Stderr, "Current version: %s (commit: %s, built: %s)\n", version, commit, date)

	// Check latest version from the release feed.
	latest, err := cli.FetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check latest version: %v\n", err)
	} else {
		latestVer := strings.TrimPrefix(latest.TagName, "v")
		currentVer := strings.TrimPrefix(version, "v")
		if currentVer == latestVer {
			fmt.Fprintln(os.Stderr, "Already up to date.")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Latest version:  %s\n\n", latest.TagName)
	}

	// Homebrew stays brew-owned until Phase-B formula bootstrap ships.
	// Do not half-migrate brew installs onto self-replace.
	if cli.IsBrewInstall() && cli.IsBrewUpdateConfigured() {
		fmt.Fprintln(os.Stderr, "Updating via Homebrew...")
		output, err := cli.UpdateViaBrew()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", output)
			return fmt.Errorf("brew upgrade failed: %w\nSet MULTICA_BREW_PACKAGE to a valid Multica tap/package or use direct release downloads from %s", err, cli.ReleaseWebURL())
		}
		fmt.Fprintln(os.Stderr, "Update complete.")
		return nil
	}
	if cli.IsBrewInstall() {
		fmt.Fprintln(os.Stderr, "Homebrew install detected, but MULTICA_BREW_PACKAGE is not configured or points at the legacy upstream tap; using VersionStore stage path.")
	}

	// Not installed via brew — stage into VersionStore + offline activate.
	// The old direct self-replace path (UpdateViaDownloadWithTimeout) is
	// retired outright here, not kept as a fallback: Parker's #815 ruling
	// (2026-07-30, #prj-daemon) was combine-not-choose — #1475's manifest
	// discovery stays the source, but every download now lands through
	// stage-then-activate, never overwriting the running binary in place.
	if latest == nil {
		return fmt.Errorf("could not determine latest version; check %s", cli.ReleaseWebURL())
	}
	targetVersion := latest.TagName
	fmt.Fprintf(os.Stderr, "Staging %s into VersionStore (no self-replace)...\n", targetVersion)

	store, err := cli.OpenVersionStore("")
	if err != nil {
		return fmt.Errorf("open version store: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout+30*time.Second)
	defer cancel()

	if cli.IsReleaseVersion(version) {
		if _, err := store.BootstrapActiveFromExecutable(ctx, version); err != nil {
			// Already initialized or non-fatal — continue to stage.
			fmt.Fprintf(os.Stderr, "Note: bootstrap Active: %v\n", err)
		}
	}

	// No daemon connection here, so no server-dispatched override is
	// available — this one-shot CLI process only ever sees env var/default
	// (see cli.DownloadAndStageRelease's serverDispatched doc comment).
	result, err := cli.DownloadAndStageRelease(ctx, store, targetVersion, updateDownloadTimeout, "")
	if err != nil {
		return fmt.Errorf("stage release failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", result.Message)

	state, path, err := store.OfflineActivateStaged(ctx, result.Staged.Version, "cli-update")
	if err != nil {
		return fmt.Errorf("activate staged release failed: %w", err)
	}
	fmt.Fprintf(os.Stderr,
		"Activated %s (generation %d). Binary: %s\nRestart the daemon (or re-exec this binary path) to run the new Active. No self-replace of the old install path.\n",
		state.ActiveVersion,
		state.Generation,
		path,
	)
	return nil
}
