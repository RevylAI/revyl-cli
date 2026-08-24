//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerTimeoutTerminatesUnixProcessGroup(t *testing.T) {
	workDir := t.TempDir()
	markerPath := filepath.Join(workDir, "orphaned-child")
	err := NewRunner(workDir).RunContext(context.Background(), "(sleep 0.3; touch orphaned-child) & wait", RunOptions{
		Timeout: 50 * time.Millisecond,
	}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunContext() error = %v, want context.DeadlineExceeded", err)
	}

	time.Sleep(500 * time.Millisecond)
	if _, statErr := os.Stat(markerPath); statErr == nil {
		t.Fatal("child process survived build command timeout")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat marker after timeout: %v", statErr)
	}
}
