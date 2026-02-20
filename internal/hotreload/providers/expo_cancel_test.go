package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFakeNpx(t *testing.T, sleepSeconds int) string {
	t.Helper()
	dir := t.TempDir()
	npxPath := filepath.Join(dir, "npx")
	content := fmt.Sprintf("#!/bin/sh\n/bin/sleep %d\n", sleepSeconds)
	if err := os.WriteFile(npxPath, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write fake npx: %v", err)
	}
	return dir
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestExpoStart_ContextCancelled_NoDeadlock(t *testing.T) {
	fakeBinDir := writeFakeNpx(t, 5)
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeBinDir+":"+oldPath)

	server := NewExpoDevServer(".", "demo", freeTCPPort(t), false)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- server.Start(ctx)
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
		t.Fatalf("Start did not return after context cancellation (possible deadlock)")
	}
}
