//go:build windows

package diagnosticlog

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openNoFollow(path string, flags int, perm os.FileMode) (*os.File, error) {
	if attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path)); err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf("diagnostic path is a reparse point: %s", path)
	}
	return os.OpenFile(path, flags, perm)
}

func pathIsUnsafe(path string, mode os.FileMode) bool {
	if mode&os.ModeSymlink != 0 {
		return true
	}
	attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
	return err == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func lockFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
}

func unlockFile(file *os.File) {
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}
