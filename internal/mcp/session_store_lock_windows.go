//go:build windows

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockSessionStoreByteRange is the single-byte range locked on the sidecar file.
// The range only has to be consistent across processes, not cover real content.
const lockSessionStoreByteRange = 1

// tryLockSessionStoreFile attempts a non-blocking exclusive LockFileEx.
//
// Parameters:
//   - file: The open lock file.
//
// Returns:
//   - bool: True when the lock was acquired.
//   - error: Only for failures other than "already held by a peer", which is
//     reported as a false acquisition so the caller can retry.
func tryLockSessionStoreFile(file *os.File) (bool, error) {
	overlapped := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		lockSessionStoreByteRange,
		0,
		overlapped,
	)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION), errors.Is(err, windows.ERROR_IO_PENDING):
		return false, nil
	default:
		return false, err
	}
}

// unlockSessionStoreFile releases the byte-range lock held on file.
func unlockSessionStoreFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		lockSessionStoreByteRange,
		0,
		overlapped,
	)
}
