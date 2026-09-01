package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReleaseCleanupResult reports release files removed after a successor has
// passed activation checks. The active launcher is never part of the cleanup.
type ReleaseCleanupResult struct {
	PreviousExecutable bool
	LegacyVersions     bool
	UpgradeStaging     bool
}

// CleanupInstalledReleaseResidue removes rollback and staging files only after
// the caller has accepted the new Computer process. Keeping this separate from
// SwapExecutable preserves current.prev until rollback is no longer possible.
func CleanupInstalledReleaseResidue(installPath string) (ReleaseCleanupResult, error) {
	installPath = filepath.Clean(strings.TrimSpace(installPath))
	if installPath == "." || installPath == "" || !filepath.IsAbs(installPath) {
		return ReleaseCleanupResult{}, errors.New("installed executable path must be absolute")
	}
	info, err := os.Stat(installPath)
	if err != nil {
		return ReleaseCleanupResult{}, fmt.Errorf("stat installed executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ReleaseCleanupResult{}, errors.New("installed executable must be a regular file")
	}

	root, err := MachineStateRoot()
	if err != nil {
		return ReleaseCleanupResult{}, err
	}
	root = filepath.Clean(root)
	versionsRoot := filepath.Join(root, "versions")
	stagingRoot := filepath.Join(root, "upgrade-staging")
	if pathInside(versionsRoot, installPath) || pathInside(stagingRoot, installPath) {
		return ReleaseCleanupResult{}, errors.New("installed executable must be outside release residue directories")
	}

	result := ReleaseCleanupResult{}
	var cleanupErrors []error
	if removed, removeErr := removeReleaseResiduePath(prevPath(installPath), false); removeErr != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove previous executable: %w", removeErr))
	} else {
		result.PreviousExecutable = removed
	}
	if removed, removeErr := removeReleaseResiduePath(versionsRoot, true); removeErr != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove legacy versions: %w", removeErr))
	} else {
		result.LegacyVersions = removed
	}
	if removed, removeErr := removeReleaseResiduePath(stagingRoot, true); removeErr != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove upgrade staging: %w", removeErr))
	} else {
		result.UpgradeStaging = removed
	}
	return result, errors.Join(cleanupErrors...)
}

func removeReleaseResiduePath(path string, recursive bool) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if recursive {
		if err := os.RemoveAll(path); err != nil {
			return false, err
		}
	} else if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func pathInside(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
