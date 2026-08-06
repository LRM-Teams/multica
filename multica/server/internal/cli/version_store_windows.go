//go:build windows

package cli

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func lockExclusiveContext(ctx context.Context, file *os.File) error {
	for {
		var overlapped windows.Overlapped
		err := windows.LockFileEx(
			windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			&overlapped,
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func unlockExclusive(file *os.File) {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		1,
		0,
		&overlapped,
	)
}

func replaceFileAtomic(from, to string) error {
	fromPtr, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPtr, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		fromPtr,
		toPtr,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func syncDirPath(string) error {
	// MoveFileEx(..., MOVEFILE_WRITE_THROUGH) is the durable metadata boundary
	// for the small activation manifest on Windows. Directory handles cannot be
	// opened and synced portably through os.File.
	return nil
}
