package hotreload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}
	return path
}

func writeSleepScript(t *testing.T, seconds int) string {
	t.Helper()
	// exec replaces the shell with sleep so SIGKILL from context cancellation
	// terminates the process directly (no orphaned child holding the pipe open).
	return writeScript(t, fmt.Sprintf("#!/bin/sh\nexec sleep %d\n", seconds))
}

func TestStartTunnel_ContextCancelled_NoDeadlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

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
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\nexit 0\n")
	tunnel := NewTunnelManager(scriptPath, nil)

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

func TestStartTunnel_StderrCapturedInError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\necho 'ERR some cloudflared error' >&2\necho 'ERR another line' >&2\nexit 1\n")
	tunnel := NewTunnelManager(scriptPath, nil)

	_, err := tunnel.StartTunnel(context.Background(), 8081)
	if err == nil {
		t.Fatal("expected error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "ERR some cloudflared error") {
		t.Errorf("error should contain stderr output, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "ERR another line") {
		t.Errorf("error should contain all stderr lines, got: %s", errMsg)
	}
}

func TestStartTunnel_EmptyStderrShowsNoOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\nexit 0\n")
	tunnel := NewTunnelManager(scriptPath, nil)

	_, err := tunnel.StartTunnel(context.Background(), 8081)
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("expected 'no output' in error for silent exit, got: %s", err.Error())
	}
}

func TestStartTunnel_StderrBufferCapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("echo 'line %d' >&2", i))
	}
	script := "#!/bin/sh\n" + strings.Join(lines, "\n") + "\nexit 1\n"
	scriptPath := writeScript(t, script)

	tunnel := NewTunnelManager(scriptPath, nil)

	_, err := tunnel.StartTunnel(context.Background(), 8081)
	if err == nil {
		t.Fatal("expected error")
	}

	if !strings.Contains(err.Error(), "line 29") {
		t.Errorf("error should contain most recent line, got: %s", err.Error())
	}
	if strings.Contains(err.Error(), "line 0") {
		t.Errorf("error should have dropped oldest lines beyond buffer, got: %s", err.Error())
	}
}

func TestDefaultTunnelConfig(t *testing.T) {
	cfg := DefaultTunnelConfig()
	if cfg.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts=3, got %d", cfg.MaxAttempts)
	}
	if cfg.BaseDelay != 2*time.Second {
		t.Errorf("expected BaseDelay=2s, got %s", cfg.BaseDelay)
	}
	if cfg.URLTimeout != 30*time.Second {
		t.Errorf("expected URLTimeout=30s, got %s", cfg.URLTimeout)
	}
}

func TestStartTunnelWithRetry_SucceedsFirstAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\necho 'https://test-tunnel.trycloudflare.com' >&2\nsleep 30\n")
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond, URLTimeout: 5 * time.Second})

	info, err := tunnel.StartTunnelWithRetry(context.Background(), 8081)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	defer tunnel.Stop()

	if info.PublicURL != "https://test-tunnel.trycloudflare.com" {
		t.Errorf("unexpected URL: %s", info.PublicURL)
	}
	if !tunnel.IsRunning() {
		t.Error("tunnel should be running after successful start")
	}
}

func TestStartTunnelWithRetry_RetriesOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	// Script that fails first two calls then succeeds.
	// Uses a counter file to track attempts.
	dir := t.TempDir()
	counterFile := filepath.Join(dir, "counter")
	os.WriteFile(counterFile, []byte("0"), 0o644)

	scriptContent := fmt.Sprintf(`#!/bin/sh
COUNTER=$(cat %s)
COUNTER=$((COUNTER + 1))
echo "$COUNTER" > %s
if [ "$COUNTER" -lt 3 ]; then
  echo "ERR attempt $COUNTER failed" >&2
  exit 1
fi
echo "https://retry-success.trycloudflare.com" >&2
sleep 30
`, counterFile, counterFile)

	scriptPath := writeScript(t, scriptContent)
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond, URLTimeout: 5 * time.Second})

	var logMessages []string
	tunnel.SetLogCallback(func(msg string) { logMessages = append(logMessages, msg) })

	info, err := tunnel.StartTunnelWithRetry(context.Background(), 8081)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	defer tunnel.Stop()

	if info.PublicURL != "https://retry-success.trycloudflare.com" {
		t.Errorf("unexpected URL: %s", info.PublicURL)
	}

	foundRetryLog := false
	for _, msg := range logMessages {
		if strings.Contains(msg, "attempt 1/3 failed") || strings.Contains(msg, "attempt 2/3 failed") {
			foundRetryLog = true
			break
		}
	}
	if !foundRetryLog {
		t.Errorf("expected retry log messages, got: %v", logMessages)
	}
}

func TestStartTunnelWithRetry_AllAttemptsFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\necho 'ERR permanent failure' >&2\nexit 1\n")
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 2, BaseDelay: 10 * time.Millisecond, URLTimeout: 5 * time.Second})

	_, err := tunnel.StartTunnelWithRetry(context.Background(), 8081)
	if err == nil {
		t.Fatal("expected error after all attempts fail")
	}
	if !strings.Contains(err.Error(), "after 2 attempts") {
		t.Errorf("error should mention attempt count, got: %s", err.Error())
	}
}

func TestStartTunnelWithRetry_ContextCancelledDuringRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\necho 'ERR fail' >&2\nexit 1\n")
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 5, BaseDelay: 500 * time.Millisecond, URLTimeout: 5 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := tunnel.StartTunnelWithRetry(ctx, 8081)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline error, got: %v", err)
	}
}

func TestStartTunnelWithRetry_EmitsEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	dir := t.TempDir()
	counterFile := filepath.Join(dir, "counter")
	os.WriteFile(counterFile, []byte("0"), 0o644)

	scriptContent := fmt.Sprintf(`#!/bin/sh
COUNTER=$(cat %s)
COUNTER=$((COUNTER + 1))
echo "$COUNTER" > %s
if [ "$COUNTER" -lt 2 ]; then
  echo "ERR fail" >&2
  exit 1
fi
echo "https://event-test.trycloudflare.com" >&2
sleep 30
`, counterFile, counterFile)

	scriptPath := writeScript(t, scriptContent)
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond, URLTimeout: 5 * time.Second})

	info, err := tunnel.StartTunnelWithRetry(context.Background(), 8081)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer tunnel.Stop()

	if info.PublicURL != "https://event-test.trycloudflare.com" {
		t.Errorf("unexpected URL: %s", info.PublicURL)
	}

	// Drain events
	var events []TunnelEvent
	for {
		select {
		case ev := <-tunnel.Events():
			events = append(events, ev)
		default:
			goto done
		}
	}
done:

	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Type != TunnelEventConnected {
		t.Errorf("last event should be Connected, got type %d", lastEvent.Type)
	}
}

func TestHealthMonitor_DetectsDisconnectAndReconnects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	// Script that prints the URL then exits shortly after (simulates tunnel crash).
	// On subsequent runs, it stays alive.
	dir := t.TempDir()
	counterFile := filepath.Join(dir, "counter")
	os.WriteFile(counterFile, []byte("0"), 0o644)

	scriptContent := fmt.Sprintf(`#!/bin/sh
COUNTER=$(cat %s)
COUNTER=$((COUNTER + 1))
echo "$COUNTER" > %s
echo "https://health-$COUNTER.trycloudflare.com" >&2
if [ "$COUNTER" -eq 1 ]; then
  sleep 0.5
  exit 1
fi
sleep 30
`, counterFile, counterFile)

	scriptPath := writeScript(t, scriptContent)
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 3, BaseDelay: 50 * time.Millisecond, URLTimeout: 5 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	info, err := tunnel.StartTunnelWithRetry(ctx, 8081)
	if err != nil {
		t.Fatalf("initial start failed: %v", err)
	}
	if info.PublicURL != "https://health-1.trycloudflare.com" {
		t.Errorf("unexpected initial URL: %s", info.PublicURL)
	}

	tunnel.StartHealthMonitor(ctx)

	// Wait for the reconnection event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-tunnel.Events():
			if ev.Type == TunnelEventReconnected {
				if !strings.Contains(ev.URL, "health-") {
					t.Errorf("reconnected URL unexpected: %s", ev.URL)
				}
				tunnel.Stop()
				return
			}
		case <-timeout:
			tunnel.Stop()
			t.Fatal("timed out waiting for reconnection event")
		}
	}
}

func TestHealthMonitor_StoppedByStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\necho 'https://stop-test.trycloudflare.com' >&2\nsleep 30\n")
	tunnel := NewTunnelManager(scriptPath, nil)

	ctx := context.Background()
	info, err := tunnel.StartTunnel(ctx, 8081)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if info.PublicURL != "https://stop-test.trycloudflare.com" {
		t.Errorf("unexpected URL: %s", info.PublicURL)
	}

	tunnel.StartHealthMonitor(ctx)
	tunnel.Stop()

	if tunnel.IsRunning() {
		t.Error("tunnel should not be running after Stop")
	}
}

func TestSetConfig_OverridesDefaults(t *testing.T) {
	tunnel := NewTunnelManager("/nonexistent", nil)

	custom := TunnelConfig{
		MaxAttempts: 5,
		BaseDelay:   100 * time.Millisecond,
		URLTimeout:  10 * time.Second,
	}
	tunnel.SetConfig(custom)

	tunnel.mu.Lock()
	cfg := tunnel.config
	tunnel.mu.Unlock()

	if cfg.MaxAttempts != 5 {
		t.Errorf("expected MaxAttempts=5, got %d", cfg.MaxAttempts)
	}
	if cfg.BaseDelay != 100*time.Millisecond {
		t.Errorf("expected BaseDelay=100ms, got %s", cfg.BaseDelay)
	}
}

func TestStartTunnelWithRetry_StopDuringBackoff_AbortsRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	scriptPath := writeScript(t, "#!/bin/sh\necho 'ERR fail' >&2\nexit 1\n")
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 5, BaseDelay: 200 * time.Millisecond, URLTimeout: 5 * time.Second})

	retryDone := make(chan error, 1)
	go func() {
		_, err := tunnel.StartTunnelWithRetry(context.Background(), 8081)
		retryDone <- err
	}()

	// Let the first attempt fail and the backoff sleep begin, then call Stop.
	time.Sleep(100 * time.Millisecond)
	tunnel.Stop()

	select {
	case err := <-retryDone:
		if err == nil {
			t.Fatal("expected error after Stop during retry")
		}
		if !strings.Contains(err.Error(), "stopped") {
			t.Errorf("expected 'stopped' in error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartTunnelWithRetry did not return after Stop (possible deadlock)")
	}

	if tunnel.IsRunning() {
		t.Error("tunnel should not be running after Stop")
	}
}

func TestURLTimeout_Configurable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only process script test; skipped on windows")
	}

	// Use exec to replace shell with sleep so context cancellation (SIGKILL) terminates it immediately
	scriptPath := writeScript(t, "#!/bin/sh\nexec sleep 30\n")
	tunnel := NewTunnelManager(scriptPath, nil)
	tunnel.SetConfig(TunnelConfig{MaxAttempts: 1, BaseDelay: 10 * time.Millisecond, URLTimeout: 500 * time.Millisecond})

	start := time.Now()
	_, err := tunnel.StartTunnel(context.Background(), 8081)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("timeout took too long (%s), expected ~500ms", elapsed)
	}
}
