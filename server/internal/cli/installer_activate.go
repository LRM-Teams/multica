package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LauncherInstallResult records the shared installer/VersionStore outcome.
// The launcher remains the stable public path while Active/Previous retain
// immutable release binaries for Computer handoff and rollback.
type LauncherInstallResult struct {
	State        ActivationState
	Staged       StagedVersion
	LauncherPath string
}

// InstallLauncherRelease activates an already-downloaded, checksum-verified
// release through the same VersionStore used by Computer upgrade, then
// atomically refreshes the stable CLI launcher. It is called by the hidden
// installer bridge in the downloaded candidate binary.
func (s *VersionStore) InstallLauncherRelease(
	ctx context.Context,
	candidatePath string,
	version string,
	expectedSHA256 string,
	launcherPath string,
) (LauncherInstallResult, error) {
	if s == nil {
		return LauncherInstallResult{}, fmt.Errorf("version store is required")
	}
	tag, err := normalizeVersionStoreTag(version)
	if err != nil {
		return LauncherInstallResult{}, err
	}
	expectedDigest, err := normalizeSHA256(expectedSHA256)
	if err != nil {
		return LauncherInstallResult{}, err
	}
	candidatePath = filepath.Clean(strings.TrimSpace(candidatePath))
	launcherPath = filepath.Clean(strings.TrimSpace(launcherPath))
	if candidatePath == "." || candidatePath == "" {
		return LauncherInstallResult{}, fmt.Errorf("candidate path is required")
	}
	if launcherPath == "." || launcherPath == "" || !filepath.IsAbs(launcherPath) {
		return LauncherInstallResult{}, fmt.Errorf("stable launcher path must be absolute")
	}
	if insidePath(s.VersionsRoot(), launcherPath) {
		return LauncherInstallResult{}, fmt.Errorf("stable launcher path must not be inside the version store")
	}
	if remembered, ok, err := s.LauncherPath(); err == nil && ok && remembered != launcherPath {
		return LauncherInstallResult{}, fmt.Errorf("stable launcher is already %s; refusing to replace it with %s", remembered, launcherPath)
	} else if err != nil {
		return LauncherInstallResult{}, err
	}

	candidate, err := os.ReadFile(candidatePath)
	if err != nil {
		return LauncherInstallResult{}, fmt.Errorf("read installer candidate: %w", err)
	}
	if actual := bytesSHA256(candidate); actual != expectedDigest {
		return LauncherInstallResult{}, fmt.Errorf("installer candidate checksum mismatch: expected %s, got %s", expectedDigest, actual)
	}
	info, err := os.Stat(candidatePath)
	if err != nil {
		return LauncherInstallResult{}, fmt.Errorf("stat installer candidate: %w", err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}

	// Import a pre-VersionStore release launcher as the retained predecessor
	// when its own --version output is provable. Development/unknown binaries
	// are never fabricated into release history.
	before, err := s.ReadActivationState()
	if err != nil {
		return LauncherInstallResult{}, err
	}
	if before.Generation == 0 {
		if _, statErr := os.Stat(launcherPath); statErr == nil {
			if previousTag, ok := installedReleaseVersion(ctx, launcherPath); ok {
				if err := s.RememberLauncherPath(launcherPath); err != nil {
					return LauncherInstallResult{}, err
				}
				if _, err := s.BootstrapActiveFromBinary(ctx, launcherPath, previousTag); err != nil {
					return LauncherInstallResult{}, fmt.Errorf("bootstrap installed release: %w", err)
				}
				before, err = s.ReadActivationState()
				if err != nil {
					return LauncherInstallResult{}, err
				}
			}
		}
	}

	staged, err := s.StageBinary(ctx, tag, candidate, expectedDigest, fs.FileMode(mode))
	if err != nil {
		return LauncherInstallResult{}, fmt.Errorf("stage installer release: %w", err)
	}
	state := before
	activated := before.ActiveVersion != tag
	if activated {
		// A verified explicit install is also the recovery path when a locally
		// modified current Active can no longer be retained as a safe rollback.
		// Ordinary daemon/config upgrades remain fail-closed through the public
		// OfflineActivateStaged path.
		state, _, err = s.offlineActivateStaged(ctx, tag, "", true)
		if err != nil {
			return LauncherInstallResult{}, fmt.Errorf("activate installer release: %w", err)
		}
	}

	if err := replaceLauncherAtomic(launcherPath, candidate, fs.FileMode(mode)); err != nil {
		if activated && before.ActiveVersion != "" {
			_, _, _ = s.RollbackToPreviousActive(ctx, "")
		}
		return LauncherInstallResult{}, fmt.Errorf("replace stable launcher: %w", err)
	}
	if err := s.RememberLauncherPath(launcherPath); err != nil {
		return LauncherInstallResult{}, fmt.Errorf("remember stable launcher: %w", err)
	}
	return LauncherInstallResult{State: state, Staged: staged, LauncherPath: launcherPath}, nil
}

func installedReleaseVersion(ctx context.Context, binaryPath string) (string, bool) {
	cmd := exec.CommandContext(ctx, binaryPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 2 || fields[0] != "multica" {
		return "", false
	}
	tag, err := normalizeVersionStoreTag(fields[1])
	return tag, err == nil
}

func replaceLauncherAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".multica-launcher-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replaceFileAtomic(tempPath, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirPath(dir)
}
