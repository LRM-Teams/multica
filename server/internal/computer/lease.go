package computer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	residentLockFile     = "resident.lock"
	startLockFile        = "start.lock"
	bindingChildLockFile = "runner.lock"
)

// FileLease holds an OS advisory lock for as long as its file descriptor is
// open. The lock file itself may survive a crash; an unlocked stale file never
// blocks the next process.
type FileLease struct {
	file *os.File
}

func acquireFileLease(ctx context.Context, root, name string) (*FileLease, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockComputerFile(ctx, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &FileLease{file: file}, nil
}

// AcquireResidentLease is the machine-wide singleton ownership gate. The
// resident holds it for its complete process lifetime in addition to owning
// the loopback control port.
func AcquireResidentLease(ctx context.Context, root string) (*FileLease, error) {
	lease, err := acquireFileLease(ctx, root, residentLockFile)
	if err != nil {
		return nil, fmt.Errorf("acquire resident lease: %w", err)
	}
	return lease, nil
}

func acquireStartLease(ctx context.Context, root string) (*FileLease, error) {
	lease, err := acquireFileLease(ctx, root, startLockFile)
	if err != nil {
		return nil, fmt.Errorf("acquire start lease: %w", err)
	}
	return lease, nil
}

// AcquireBindingChildLease is the cross-Host at-most-one execution gate for
// one Workspace Binding. An orphaned child keeps the lease across a Host
// crash, so a successor Host cannot start a second execution owner before the
// orphan detects lease loss and exits. Sibling Workspaces use independent
// lock files.
func AcquireBindingChildLease(ctx context.Context, bindingsRoot, environment, workspaceID string) (*FileLease, error) {
	bindingsRoot = filepath.Clean(bindingsRoot)
	environment = strings.TrimSpace(environment)
	workspaceID = strings.TrimSpace(workspaceID)
	if bindingsRoot == "." || environment == "" || workspaceID == "" || strings.ContainsAny(workspaceID, "/\\") || workspaceID == "." || workspaceID == ".." {
		return nil, errors.New("Binding child lease identity is invalid")
	}
	root := filepath.Join(bindingsRoot, "binding-children", environment, workspaceID)
	lease, err := acquireFileLease(ctx, root, bindingChildLockFile)
	if err != nil {
		return nil, fmt.Errorf("acquire Binding child lease: %w", err)
	}
	return lease, nil
}

// Close releases the advisory lock. It is safe to call more than once.
func (l *FileLease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockComputerFile(l.file)
	err := l.file.Close()
	l.file = nil
	return err
}
