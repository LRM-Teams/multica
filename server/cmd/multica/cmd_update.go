package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

var updateDownloadTimeout time.Duration = cli.DefaultUpdateDownloadTimeout
var updateRequestID string

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update multica to the latest version",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().DurationVar(&updateDownloadTimeout, "download-timeout", cli.DefaultUpdateDownloadTimeout, "Maximum time to wait for the release archive download")
	updateCmd.Flags().StringVar(&updateRequestID, "request-id", "", "Idempotency key for a live Machine Upgrade request")
}

func runUpdate(cmd *cobra.Command, _ []string) error {
	if updateDownloadTimeout <= 0 {
		return fmt.Errorf("download timeout must be greater than zero")
	}

	fmt.Fprintf(os.Stderr, "Current version: %s (commit: %s, built: %s)\n", version, commit, date)

	// Check latest version from the release feed.
	latest, err := cli.FetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not check latest version: %v\n", err)
	} else {
		if routed, err := requestLiveMachineUpgrade(cmd, latest.TagName); err != nil {
			return err
		} else if routed {
			return nil
		}
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

	var activatedVersion string
	var activatedGeneration uint64
	var activatedPath string
	err = store.WithMachineMutationLock(ctx, func() error {
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
			return fmt.Errorf("stage release: %w", err)
		}
		fmt.Fprintf(os.Stderr, "%s\n", result.Message)

		state, path, err := store.OfflineActivateStaged(ctx, result.Staged.Version, "cli-update")
		if err != nil {
			return fmt.Errorf("activate staged release: %w", err)
		}
		activatedVersion = state.ActiveVersion
		activatedGeneration = state.Generation
		activatedPath = path
		return nil
	})
	if err != nil {
		return fmt.Errorf("offline machine upgrade: %w", err)
	}
	fmt.Fprintf(os.Stderr,
		"Installed %s for the next daemon start (generation %d). Binary: %s\nNo running successor was proven; start or restart the daemon to use this Active release.\n",
		activatedVersion,
		activatedGeneration,
		activatedPath,
	)
	return nil
}

// requestLiveMachineUpgrade routes an explicit local request through the
// server's canonical machine operation whenever the profile's daemon is live.
// The unauthenticated health endpoint is liveness-only: no upgrade mutation is
// exposed there. A stale or unreachable PID file is treated conservatively as
// a live-but-uncontrollable daemon so this command cannot mutate Active behind
// a potentially running owner.
func requestLiveMachineUpgrade(cmd *cobra.Command, targetVersion string) (bool, error) {
	profile := resolveProfile(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	health := computer.ProbeHealth(ctx, computer.HealthPort(profile))
	cancel()
	if computer.Alive(health) {
		daemonID, _ := health["daemon_id"].(string)
		daemonID = strings.TrimSpace(daemonID)
		if daemonID == "" {
			return true, fmt.Errorf("upgrade_service_unreachable: live daemon did not prove its machine identity")
		}
		controlToken, err := readMachineUpgradeControlToken(profile)
		if err != nil {
			return true, fmt.Errorf("upgrade_service_unreachable: read owner control credential: %w", err)
		}
		requestID := strings.TrimSpace(updateRequestID)
		if requestID == "" {
			requestID = fmt.Sprintf("local-%d", time.Now().UnixNano())
		}
		var operation map[string]any
		requestCtx, requestCancel := context.WithTimeout(context.Background(), cli.AtLeastAPITimeout(30*time.Second))
		body, marshalErr := json.Marshal(map[string]string{"request_id": requestID, "target_version": targetVersion})
		if marshalErr != nil {
			requestCancel()
			return true, fmt.Errorf("upgrade_service_unreachable: encode owner request: %w", marshalErr)
		}
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/machine-upgrades", computer.HealthPort(profile)), bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Multica-Control-Token", controlToken)
			response, requestErr := (&http.Client{Timeout: cli.AtLeastAPITimeout(30 * time.Second)}).Do(req)
			if requestErr != nil {
				err = requestErr
			} else {
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
					if response.StatusCode == http.StatusConflict {
						requestCancel()
						return true, fmt.Errorf("machine upgrade request rejected: %s", strings.TrimSpace(string(message)))
					}
					err = fmt.Errorf("local control returned %s: %s", response.Status, strings.TrimSpace(string(message)))
				} else {
					err = json.NewDecoder(response.Body).Decode(&operation)
				}
			}
		}
		requestCancel()
		if err != nil {
			return true, fmt.Errorf("upgrade_service_unreachable: request machine upgrade through live owner: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Machine Upgrade requested: %s (target %s). The live daemon owns staging and handoff.\n", strVal(operation, "id"), targetVersion)
		return true, nil
	}
	if _, err := os.Stat(computer.PIDPath(profile)); err == nil {
		return true, fmt.Errorf("upgrade_service_unreachable: daemon PID state exists but its local control surface is unavailable; refusing offline activation")
	}
	return false, nil
}
