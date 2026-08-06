//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"syscall"
)

var errProfileLocked = errors.New("supervisor profile lock is held")

func lockProfileFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return errProfileLocked
	}
	return err
}

func unlockProfileFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
