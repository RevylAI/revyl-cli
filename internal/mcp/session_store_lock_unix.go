//go:build !windows

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockSessionStoreFile attempts a non-blocking exclusive flock.
//
// Parameters:
//   - file: The open lock file.
//
// Returns:
//   - bool: True when the lock was acquired.
//   - error: Only for failures other than "already held by a peer", which is
//     reported as a false acquisition so the caller can retry.
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

// unlockSessionStoreFile releases the flock held on file.
func unlockSessionStoreFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
