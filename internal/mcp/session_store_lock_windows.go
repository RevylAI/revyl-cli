//go:build windows

package mcp

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const lockSessionStoreByteRange = 1

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
