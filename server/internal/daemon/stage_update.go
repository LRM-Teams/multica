package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

var machineStateRootFn = cli.MachineStateRoot

// versionStoreRootFn is the journal/state-dir hook used by existing tests.
var versionStoreRootFn = func() (string, error) { return machineStateRootFn() }

var stageReleaseFn = func(targetVersion string, downloadTimeout time.Duration, serverDispatched string) (string, error) {
	return cli.StageReleaseScratch(targetVersion, downloadTimeout, serverDispatched)
}

func (d *Daemon) runStageUpdate(targetVersion string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
	defer cancel()
	_ = ctx
	path, err := stageReleaseFn(targetVersion, cli.DefaultUpdateDownloadTimeout, d.releaseManifestBaseURLOverride())
	if err != nil {
		return "", fmt.Errorf("stage release failed: %w", err)
	}
	resolved := resolveStagedBinaryFile(path)
	if resolved == "" {
		return "", fmt.Errorf("stage release did not produce a regular file: %q", path)
	}
	d.stagedUpgradePath = resolved
	return fmt.Sprintf("Staged %s at %s; PATH unchanged", cli.NormalizeReleaseTag(targetVersion), resolved), nil
}

// resolveStagedBinaryFile keeps only paths that are regular files. The
// runStageUpdate status sentence is not a path and must never be exec'd.
func resolveStagedBinaryFile(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

func (d *Daemon) verifyStagedBinary(targetVersion, stagedPath string) (string, error) {
	path := resolveStagedBinaryFile(d.stagedUpgradePath, stagedPath)
	if path == "" {
		return "", fmt.Errorf("staged binary path is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), updatedBinaryVersionCheckTimeout)
	defer cancel()
	if err := cli.VerifyStagedBinaryVersion(ctx, path, cli.NormalizeReleaseTag(targetVersion)); err != nil {
		return "", fmt.Errorf("binary_version_mismatch_after_update: staged %s verify failed: %v", path, err)
	}
	return cli.NormalizeReleaseTag(targetVersion), nil
}

func (d *Daemon) commitStagedActivation(_ context.Context, _, targetVersion string) (string, error) {
	if strings.TrimSpace(targetVersion) == "" {
		return "", fmt.Errorf("cannot activate: empty target version")
	}
	installPath, err := cli.InstallPath()
	if err != nil {
		return "", err
	}
	staged := resolveStagedBinaryFile(d.stagedUpgradePath)
	if staged == "" || staged == installPath {
		path, err := cli.StageReleaseScratch(targetVersion, cli.DefaultUpdateDownloadTimeout, d.releaseManifestBaseURLOverride())
		if err != nil {
			return "", err
		}
		staged = path
	}
	if err := cli.SwapExecutable(installPath, staged); err != nil {
		return "", fmt.Errorf("swap PATH computer: %w", err)
	}
	d.stagedUpgradePath = ""
	return installPath, nil
}

func (d *Daemon) refreshStableLauncher() error {
	return nil
}
