package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ErrBootstrapActiveUnverifiable is returned when the running binary cannot be
// imported as a legal VersionStore release tag (dev/describe builds, non-release).
var ErrBootstrapActiveUnverifiable = errors.New("bootstrap_active_unverifiable")

// BootstrapActiveFromBinary imports an on-disk binary as generation-1 committed
// Active when ActivationState is empty. Idempotent when Active already matches.
//
// Contract (Barry lock successor-2/3): only IsReleaseVersion tags; no bootstrap/…
// path tags. Non-release → typed ErrBootstrapActiveUnverifiable; entry unchanged.
func (s *VersionStore) BootstrapActiveFromBinary(
	ctx context.Context,
	binaryPath string,
	version string,
) (ActivationState, error) {
	if s == nil {
		return ActivationState{}, errors.New("version store is required")
	}
	binaryPath = strings.TrimSpace(binaryPath)
	if binaryPath == "" {
		return ActivationState{}, fmt.Errorf("%w: binary path empty", ErrBootstrapActiveUnverifiable)
	}
	tag, err := normalizeVersionStoreTag(version)
	if err != nil {
		return ActivationState{}, fmt.Errorf("%w: %v", ErrBootstrapActiveUnverifiable, err)
	}

	current, err := s.ReadActivationState()
	if err != nil {
		return ActivationState{}, err
	}
	if current.Generation > 0 {
		if current.ActiveVersion == tag {
			// Already bootstrapped to this release — verify stage still exists.
			if _, err := s.verifyExisting(ctx, tag, ""); err != nil {
				return ActivationState{}, fmt.Errorf(
					"active %s is committed but staged binary missing: %w",
					tag,
					err,
				)
			}
			return current, nil
		}
		// Different committed Active — do not silently overwrite.
		return current, fmt.Errorf(
			"activation already initialized: active=%s generation=%d (wanted bootstrap %s)",
			current.ActiveVersion,
			current.Generation,
			tag,
		)
	}

	data, err := os.ReadFile(binaryPath)
	if err != nil {
		return ActivationState{}, fmt.Errorf("%w: read binary: %v", ErrBootstrapActiveUnverifiable, err)
	}
	if len(data) == 0 {
		return ActivationState{}, fmt.Errorf("%w: empty binary", ErrBootstrapActiveUnverifiable)
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return ActivationState{}, fmt.Errorf("%w: stat binary: %v", ErrBootstrapActiveUnverifiable, err)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}
	digest := bytesSHA256(data)
	if _, err := s.StageBinary(ctx, tag, data, digest, fs.FileMode(mode)); err != nil {
		return ActivationState{}, fmt.Errorf("stage bootstrap binary %s: %w", tag, err)
	}
	// CAS from generation 0 → 1 with Active=tag, Previous empty.
	next, err := s.CompareAndSwapActivation(ctx, 0, tag)
	if err != nil {
		return ActivationState{}, fmt.Errorf("commit bootstrap Active %s: %w", tag, err)
	}
	return next, nil
}

// BootstrapActiveFromExecutable resolves the running process binary and imports
// it when version is a release tag.
func (s *VersionStore) BootstrapActiveFromExecutable(
	ctx context.Context,
	version string,
) (ActivationState, error) {
	exe, err := os.Executable()
	if err != nil {
		return ActivationState{}, fmt.Errorf("%w: resolve executable: %v", ErrBootstrapActiveUnverifiable, err)
	}
	resolved, err := updateTargetPath(exe)
	if err != nil {
		// Fall back to raw executable path.
		resolved = exe
	}
	// Record only a stable installation entrypoint. A successor already
	// running from versions/<tag>/multica must preserve the earlier launcher.
	if !insidePath(s.VersionsRoot(), resolved) {
		if err := s.RememberLauncherPath(resolved); err != nil {
			return ActivationState{}, fmt.Errorf("remember stable launcher: %w", err)
		}
	}
	return s.BootstrapActiveFromBinary(ctx, resolved, version)
}
