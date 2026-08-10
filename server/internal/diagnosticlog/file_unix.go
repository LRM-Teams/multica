//go:build !windows

package diagnosticlog

import (
	"os"

	"golang.org/x/sys/unix"
)

func openNoFollow(path string, flags int, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func pathIsUnsafe(_ string, mode os.FileMode) bool {
	return mode&os.ModeSymlink != 0
}
