package daemon

import (
	"context"
	"fmt"
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
	d.stagedUpgradePath = path
	return fmt.Sprintf("Staged %s at %s; PATH unchanged", cli.NormalizeReleaseTag(targetVersion), path), nil
}

func (d *Daemon) verifyStagedBinary(targetVersion, stagedPath string) (string, error) {
	path := strings.TrimSpace(stagedPath)
	if path == "" {
		path = strings.TrimSpace(d.stagedUpgradePath)
	}
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
	staged := strings.TrimSpace(d.stagedUpgradePath)
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
