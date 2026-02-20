package hotreload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSleepScript(t *testing.T, seconds int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sleep-script.sh")
	content := fmt.Sprintf("#!/bin/sh\n/bin/sleep %d\n", seconds)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write sleep script: %v", err)
	}
	return path
}

func TestStartTunnel_ContextCancelled_NoDeadlock(t *testing.T) {
	scriptPath := writeSleepScript(t, 30)
	tunnel := NewTunnelManager(scriptPath, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := tunnel.StartTunnel(ctx, 8081)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected context cancellation error")
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected cancellation error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("StartTunnel did not return after context cancellation (possible deadlock)")
	}

	if tunnel.IsRunning() {
		t.Fatalf("tunnel should not be running after cancellation")
	}
}

func TestStartTunnel_ProcessExitsWithoutURL_NoDeadlock(t *testing.T) {
	tunnel := NewTunnelManager("/bin/true", nil)

	done := make(chan error, 1)
	go func() {
		_, err := tunnel.StartTunnel(context.Background(), 8081)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error when process exits without URL")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("StartTunnel did not return when process exited early (possible deadlock)")
	}
}
