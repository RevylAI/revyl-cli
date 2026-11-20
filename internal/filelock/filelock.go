// Package filelock takes exclusive cross-process locks on lock files, so the
// CLI and MCP processes that share a project's .revyl state serialize their
// updates to it.
package filelock

import (
	"context"
	"os"
	"time"
)

// retryInterval spaces non-blocking lock attempts while another process holds
// the lock.
const retryInterval = 25 * time.Millisecond

// Acquire creates the lock file at path with mode 0600 when it is missing and
// takes an exclusive lock on it, waiting until ctx ends. Lock a file of its
// own, never the data it guards: replacing that data by rename would move the
// lock along with the old file. Call the returned function once to release
// the lock.
func Acquire(ctx context.Context, path string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// #nosec G304 -- callers construct this path from the local project root and a fixed lock name.
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	retry := time.NewTicker(retryInterval)
	defer retry.Stop()
	for {
		locked, err := tryLock(lock)
		if err != nil {
			_ = lock.Close()
			return nil, err
		}
		if locked {
			return func() {
				_ = unlock(lock)
				_ = lock.Close()
			}, nil
		}
		select {
		case <-ctx.Done():
			_ = lock.Close()
			return nil, ctx.Err()
		case <-retry.C:
		}
	}
}
