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

	sessionStoreLockPollInterval = 20 * time.Millisecond
)

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
