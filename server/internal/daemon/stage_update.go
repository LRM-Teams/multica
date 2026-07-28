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
	result, err := downloadAndStageReleaseFn(ctx, store, targetVersion, cli.DefaultUpdateDownloadTimeout)
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

// commitStagedActivation performs the thin activate path after stage+verify:
// prepare journal → CAS Active to candidate → journal committed.
// Returns the staged binary path for re-exec. Full candidate health/register
// (design §3.3 steps 4–5) is a follow-up; this keeps Active immutable until CAS.
func (d *Daemon) commitStagedActivation(ctx context.Context, updateID, updateOutput string) (string, error) {
	// Target version is best-effort recovered from observation or updateOutput.
	target := ""
	if d.updateObservation != nil {
		target = strings.TrimSpace(d.updateObservation.Snapshot().TargetVersion)
	}
	if target == "" {
		// Fall back: parse "Staged vX.Y.Z" style messages from StageReleaseBytes.
		target = parseStagedVersionFromOutput(updateOutput)
	}
	if target == "" {
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
	staged, err := store.ResolveStagedVersion(target)
	if err != nil {
		return "", err
	}
	// Ensure staged binary still verifies.
	if err := cli.VerifyStagedBinaryVersion(ctx, staged.BinaryPath, staged.Version); err != nil {
		return "", fmt.Errorf("pre-CAS verify: %w", err)
	}

	state, err := store.ReadActivationState()
	if err != nil {
		return "", err
	}
	attemptID := strings.TrimSpace(updateID)
	if attemptID == "" {
		attemptID = uuid.NewString()
	}
	if _, err := store.PrepareActivationAttempt(
		attemptID,
		state.Generation,
		state.ActiveVersion,
		staged.Version,
	); err != nil {
		return "", fmt.Errorf("prepare journal: %w", err)
	}
	// Thin path: skip real candidate_running process; advance to healthy then CAS.
	if _, err := store.AdvanceActivationPhase(attemptID, cli.ActivationPhaseCandidateRunning, ""); err != nil {
		_, _ = store.AdvanceActivationPhase(attemptID, cli.ActivationPhaseAborted, "activate_phase_failed")
		return "", err
	}
	if _, err := store.AdvanceActivationPhase(attemptID, cli.ActivationPhaseCandidateHealthy, ""); err != nil {
		_, _ = store.AdvanceActivationPhase(attemptID, cli.ActivationPhaseAborted, "activate_phase_failed")
		return "", err
	}
	next, err := store.CompareAndSwapActivation(ctx, state.Generation, staged.Version)
	if err != nil {
		_, _ = store.AdvanceActivationPhase(attemptID, cli.ActivationPhaseAborted, "activation_cas_failed")
		return "", fmt.Errorf("CAS Active: %w", err)
	}
	if _, err := store.AdvanceActivationPhase(attemptID, cli.ActivationPhaseCommitted, ""); err != nil {
		// CAS already rotated Active — mark committed best-effort; do not roll back Active.
		d.logger.Warn("journal commit phase after successful CAS failed", "error", err, "generation", next.Generation)
	}
	return staged.BinaryPath, nil
}

func parseStagedVersionFromOutput(output string) string {
	// StageReleaseBytes message: "Staged v0.3.78 into version store at ..."
	fields := strings.Fields(output)
	for i, f := range fields {
		if f == "Staged" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
