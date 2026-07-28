//go:build windows

package cli

import (
	"os"
	"path/filepath"
)

// oldBinarySuffix is appended to the previous executable while a new one is
// being installed. Windows refuses to overwrite a running .exe but allows
// renaming it. Retained only so CleanupStaleUpdateArtifacts can still find
// and reclaim `.old` files left behind by machines that updated before #815
// retired the self-replace path this suffix was created for.
const oldBinarySuffix = ".old"

// CleanupStaleUpdateArtifacts removes leftover `.old` binaries from previous
// updates. Windows can't delete a running .exe, so a prior update may have
// left one behind; once the user restarts, this call reclaims the space.
func CleanupStaleUpdateArtifacts() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	_ = os.Remove(exePath + oldBinarySuffix)
}
