package mcp

import (
	"fmt"
	"os"
	"time"
)

const (
	// sessionStoreLockTimeout bounds how long a writer waits for the session
	// store lock. The lock is only ever held across a small read-merge-write, so
	// exceeding this means a peer is wedged rather than merely busy, and failing
	// loudly beats hanging a device command forever.
	sessionStoreLockTimeout = 5 * time.Second

	// sessionStoreLockPollInterval spaces non-blocking acquisition attempts.
	sessionStoreLockPollInterval = 20 * time.Millisecond
)

// lockSessionStore takes an exclusive cross-process lock guarding
// device-sessions.json, waiting up to sessionStoreLockTimeout.
//
// The lock must live on a sidecar path rather than on the data file itself:
// the data file is replaced by rename, which swaps the inode out from under any
// lock held on it, so two writers could each hold a "lock" on different inodes.
//
// Parameters:
//   - lockPath: Sidecar lock file path, created if absent.
//
// Returns:
//   - func(): Releases the lock and closes the file. Safe to call once.
//   - error: If the lock file cannot be opened, or the timeout elapses while a
//     peer holds the lock.
func lockSessionStore(lockPath string) (func(), error) {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session store lock %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(sessionStoreLockTimeout)
	for {
		locked, lockErr := tryLockSessionStoreFile(file)
		if lockErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock session store %s: %w", lockPath, lockErr)
		}
		if locked {
			return func() {
				_ = unlockSessionStoreFile(file)
				_ = file.Close()
			}, nil
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf(
				"timed out after %s waiting for the session store lock %s; another revyl process may be stuck",
				sessionStoreLockTimeout, lockPath,
			)
		}
		time.Sleep(sessionStoreLockPollInterval)
	}
}
