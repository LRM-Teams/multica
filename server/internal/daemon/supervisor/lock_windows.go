//go:build windows

package supervisor

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errProfileLocked = errors.New("supervisor profile lock is held")

func lockProfileFile(file *os.File) error {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errProfileLocked
	}
	return err
}

func unlockProfileFile(file *os.File) {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		1,
		0,
		&overlapped,
	)
}
