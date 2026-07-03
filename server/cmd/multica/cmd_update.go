package main

import (
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

	// Check latest version from GitHub.
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

	// Detect installation method and update accordingly. Homebrew is only used
	// when an explicit package is configured; otherwise direct release download
	// keeps the default source on LRM-Teams/multica instead of the old tap.
	if cli.IsBrewInstall() && cli.IsBrewUpdateConfigured() {
		fmt.Fprintln(os.Stderr, "Updating via Homebrew...")
		output, err := cli.UpdateViaBrew()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", output)
			return fmt.Errorf("brew upgrade failed: %w\nSet MULTICA_BREW_PACKAGE to a valid Multica tap/package or use direct release downloads from %s", err, cli.ReleaseWebURL)
		}
		fmt.Fprintln(os.Stderr, "Update complete.")
		return nil
	}
	if cli.IsBrewInstall() {
		fmt.Fprintln(os.Stderr, "Homebrew install detected, but MULTICA_BREW_PACKAGE is not configured; using direct release download.")
	}

	// Not installed via brew — download binary directly from GitHub Releases.
	if latest == nil {
		return fmt.Errorf("could not determine latest version; check %s/latest", cli.ReleaseWebURL)
	}
	targetVersion := latest.TagName
	fmt.Fprintf(os.Stderr, "Downloading %s from GitHub Releases...\n", targetVersion)
	output, err := cli.UpdateViaDownloadWithTimeout(targetVersion, updateDownloadTimeout)
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\nUpdate complete.\n", output)
	return nil
}
