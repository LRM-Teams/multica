package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const machineMutationLockName = "machine-upgrade.lock"

// MachineStateRoot is the per-user Computer state directory (journals,
// upgrade scratch). It is not a version catalog.
func MachineStateRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".local", "share", "multica"), nil
}

// WithMachineMutationLock serializes Computer install/upgrade against the
// machine state directory.
func WithMachineMutationLock(ctx context.Context, fn func() error) error {
	if fn == nil {
		return errors.New("machine mutation callback is required")
	}
	root, err := MachineStateRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(filepath.Join(root, machineMutationLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open machine mutation lock: %w", err)
	}
	defer lockFile.Close()
	if err := lockExclusiveContext(ctx, lockFile); err != nil {
		return fmt.Errorf("lock machine mutation: %w", err)
	}
	defer unlockExclusive(lockFile)
	return fn()
}
