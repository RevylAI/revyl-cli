package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revyl/cli/internal/hotreload"
)

// TestFlutterAttachPoC is a manual, env-gated end-to-end check. It attaches to
// an already-running Flutter app via our FlutterAttachDevServer and verifies a
// hot reload round-trips. It is skipped unless APP_DIR and VM_SERVICE_URL are
// set, so it never runs in CI.
//
// Usage:
//
//	# 1. In one shell: flutter run -d <device> (note the VM Service URL it prints)
//	# 2. In another:
//	APP_DIR=/tmp/revyl_poc_app \
//	VM_SERVICE_URL=http://127.0.0.1:PORT/TOKEN=/ \
//	  go test ./internal/hotreload/providers/ -run TestFlutterAttachPoC -v -timeout 360s
func TestFlutterAttachPoC(t *testing.T) {
	appDir := os.Getenv("APP_DIR")
	vmURL := os.Getenv("VM_SERVICE_URL")
	if appDir == "" || vmURL == "" {
		t.Skip("set APP_DIR and VM_SERVICE_URL to run the Flutter attach PoC")
	}

	var mu sync.Mutex
	var lines []string
	srv := NewFlutterAttachDevServer(appDir, vmURL)
	if deviceID := os.Getenv("DEVICE_ID"); deviceID != "" {
		srv.SetDeviceID(deviceID)
	}
	srv.SetOutputCallback(func(o hotreload.DevServerOutput) {
		mu.Lock()
		lines = append(lines, o.Line)
		mu.Unlock()
		t.Logf("[attach] %s", o.Line)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	waitForLine(t, &mu, &lines, []string{"key commands", "To hot reload", "Flutter run"}, 120*time.Second)

	if err := srv.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	waitForLine(t, &mu, &lines, []string{"Reloaded", "hot reload"}, 90*time.Second)
	t.Log("phase 1: direct hot reload confirmed through FlutterAttachDevServer")

	// Phase 2: full chain — file change -> FileWatcher -> ReloadDriver -> Reload.
	driver, watcher, err := hotreload.NewFlutterReloadDriver(appDir, srv)
	if err != nil {
		t.Fatalf("NewFlutterReloadDriver: %v", err)
	}
	if err := watcher.Start(ctx); err != nil {
		t.Fatalf("watcher.Start: %v", err)
	}
	defer watcher.Stop()
	go func() { _ = driver.Run(ctx) }()

	mu.Lock()
	mark := len(lines)
	mu.Unlock()

	editMainDart(t, appDir)

	waitForLineAfter(t, &mu, &lines, mark, []string{"Reloaded", "Performing hot reload"}, 90*time.Second)
	t.Log("phase 2: file change -> hot reload confirmed through ReloadDriver")
}

// editMainDart appends a harmless top-level comment to lib/main.dart to trigger
// a file-change event without breaking compilation.
func editMainDart(t *testing.T, appDir string) {
	t.Helper()
	path := filepath.Join(appDir, "lib", "main.dart")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open main.dart: %v", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n// revyl-poc %d\n", time.Now().UnixNano()); err != nil {
		t.Fatalf("write main.dart: %v", err)
	}
}

func waitForLineAfter(t *testing.T, mu *sync.Mutex, lines *[]string, after int, subs []string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		mu.Lock()
		for i := after; i < len(*lines); i++ {
			for _, s := range subs {
				if strings.Contains((*lines)[i], s) {
					mu.Unlock()
					return
				}
			}
		}
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("did not see any of %v after index %d within %s", subs, after, d)
}

func waitForLine(t *testing.T, mu *sync.Mutex, lines *[]string, subs []string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, l := range *lines {
			for _, s := range subs {
				if strings.Contains(l, s) {
					mu.Unlock()
					return
				}
			}
		}
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("did not see any of %v within %s", subs, d)
}
