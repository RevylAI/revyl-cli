//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockedByteCount is the length of the byte range locked at the start of the
// lock file. The range only has to agree across processes, not cover content.
const lockedByteCount = 1

// tryLock takes a non-blocking exclusive LockFileEx lock on file. It reports
// false without an error while another process holds the lock.
func tryLock(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, lockedByteCount, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, lockedByteCount, 0, new(windows.Overlapped))
}
