//go:build !windows

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockSessionStoreFile(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}

func unlockSessionStoreFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
