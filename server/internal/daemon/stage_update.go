package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

// versionStoreRootFn resolves the VersionStore root. Overridable in tests.
var versionStoreRootFn = cli.DefaultVersionStoreRoot

// downloadAndStageReleaseFn stages a release into the VersionStore without
// self-replacing the process executable. Overridable in tests.
var downloadAndStageReleaseFn = cli.DownloadAndStageRelease

// openVersionStoreFn opens a VersionStore. Overridable in tests.
var openVersionStoreFn = cli.OpenVersionStore

// runStageUpdate downloads targetVersion into the immutable VersionStore.
// It never renames onto the running executable (CUT-T1/T2 prerequisite).
func (d *Daemon) runStageUpdate(targetVersion string) (string, error) {
	if cli.IsBrewInstall() {
		d.logger.Info("Homebrew install detected; daemon stage uses direct LRM release download into VersionStore")
	}
	root, err := versionStoreRootFn()
	if err != nil {
		return "", fmt.Errorf("resolve version store root: %w", err)
	}
	store, err := openVersionStoreFn(root)
	if err != nil {
		return "", fmt.Errorf("open version store: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
	defer cancel()

	// Bootstrap committed Active from the running release when empty so
	// abandon/path A has a generation truth (CUT-T2).
	if cli.IsReleaseVersion(d.cfg.CLIVersion) {
		if _, err := store.BootstrapActiveFromExecutable(ctx, d.cfg.CLIVersion); err != nil {
			// Non-fatal when already initialized to a different tag — stage still ok.
			d.logger.Debug("bootstrap Active before stage", "error", err)
		}
	}

	d.logger.Info("staging CLI release into VersionStore (no self-replace)", "target_version", targetVersion)
	// Same override the release-detection loop already uses
	// already uses (see releaseManifestBaseURLOverride) — without threading
	// it through here too, a machine relying purely on server-dispatch
	// (no local env var set) could see a new version at check time and then
	// silently fall back to the compiled default at download time.
	result, err := downloadAndStageReleaseFn(ctx, store, targetVersion, cli.DefaultUpdateDownloadTimeout, d.releaseManifestBaseURLOverride())
	if err != nil {
		return "", fmt.Errorf("stage release failed: %w", err)
	}
	return result.Message, nil
}

// verifyStagedBinary confirms the target tag is staged and reports the expected version.
func (d *Daemon) verifyStagedBinary(targetVersion, updateOutput string) (string, error) {
	root, err := versionStoreRootFn()
	if err != nil {
		return "", fmt.Errorf("resolve version store root: %w", err)
	}
	store, err := openVersionStoreFn(root)
	if err != nil {
		return "", fmt.Errorf("open version store: %w", err)
	}
	staged, err := store.ResolveStagedVersion(targetVersion)
	if err != nil {
		return "", fmt.Errorf("resolve staged version: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), updatedBinaryVersionCheckTimeout)
	defer cancel()
	if err := cli.VerifyStagedBinaryVersion(ctx, staged.BinaryPath, staged.Version); err != nil {
		msg := fmt.Sprintf(
			"binary_version_mismatch_after_update: staged %s verify failed: %v",
			staged.BinaryPath,
			err,
		)
		if updateSummary := compactUpdateOutput(updateOutput); updateSummary != "" {
			msg += "; updater output: " + updateSummary
		}
		return "", fmt.Errorf("%s", msg)
	}
	return staged.Version, nil
}

// commitStagedActivation activates a staged release via OfflineActivateStaged:
// prepare → candidate_running → real --version probe (+ attempt_id env) →
// candidate_healthy → CAS → committed. Full health+register is still a follow-up;
// we do not mark healthy without a successful candidate binary probe.
func (d *Daemon) commitStagedActivation(ctx context.Context, updateID, targetVersion string) (string, error) {
	if strings.TrimSpace(targetVersion) == "" {
		return "", fmt.Errorf("cannot activate: empty target version")
	}

	root, err := versionStoreRootFn()
	if err != nil {
		return "", err
	}
	store, err := openVersionStoreFn(root)
	if err != nil {
		return "", err
	}
	attemptID := strings.TrimSpace(updateID)
	if attemptID == "" {
		attemptID = uuid.NewString()
	}
	_, path, err := store.OfflineActivateStaged(ctx, targetVersion, attemptID)
	if err != nil {
		return "", err
	}
	return path, nil
}
