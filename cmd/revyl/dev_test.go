package main

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

func TestEnsureWorkerActionSucceeded_LowercaseSuccess(t *testing.T) {
	body := []byte(`{"success":true,"action":"install"}`)
	if err := ensureWorkerActionSucceeded(body, "install"); err != nil {
		t.Fatalf("ensureWorkerActionSucceeded() error = %v, want nil", err)
	}
}

func TestEnsureWorkerActionSucceeded_UppercaseSuccess(t *testing.T) {
	body := []byte(`{"Success":true}`)
	if err := ensureWorkerActionSucceeded(body, "launch"); err != nil {
		t.Fatalf("ensureWorkerActionSucceeded() error = %v, want nil", err)
	}
}

func TestEnsureWorkerActionSucceeded_Failure(t *testing.T) {
	body := []byte(`{"success":false,"action":"open_url","error":"bad scheme"}`)
	err := ensureWorkerActionSucceeded(body, "open_url")
	if err == nil {
		t.Fatal("ensureWorkerActionSucceeded() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "bad scheme") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "bad scheme")
	}
}

func TestEnsureWorkerActionSucceeded_ActionMismatch(t *testing.T) {
	body := []byte(`{"success":true,"action":"launch"}`)
	err := ensureWorkerActionSucceeded(body, "install")
	if err == nil {
		t.Fatal("ensureWorkerActionSucceeded() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), `action="launch"`) && !strings.Contains(err.Error(), `action=\"launch\"`) {
		t.Fatalf("error = %q, want action mismatch", err.Error())
	}
}

func TestEnsureWorkerActionSucceeded_MissingSuccess(t *testing.T) {
	body := []byte(`{"action":"install"}`)
	err := ensureWorkerActionSucceeded(body, "install")
	if err == nil {
		t.Fatal("ensureWorkerActionSucceeded() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "missing success field") {
		t.Fatalf("error = %q, want missing success field", err.Error())
	}
}

func TestExtractInstallBundleID_PrefersBundleID(t *testing.T) {
	body := []byte(`{"bundle_id":"com.example.bundle","package_name":"com.example.package","app_package":"com.example.app"}`)
	got := extractInstallBundleID(body)
	if got != "com.example.bundle" {
		t.Fatalf("extractInstallBundleID() = %q, want %q", got, "com.example.bundle")
	}
}

func TestExtractInstallBundleID_FallsBackToPackageName(t *testing.T) {
	body := []byte(`{"package_name":"com.example.package"}`)
	got := extractInstallBundleID(body)
	if got != "com.example.package" {
		t.Fatalf("extractInstallBundleID() = %q, want %q", got, "com.example.package")
	}
}

func TestExtractInstallBundleID_FallsBackToAppPackage(t *testing.T) {
	body := []byte(`{"app_package":"com.example.app"}`)
	got := extractInstallBundleID(body)
	if got != "com.example.app" {
		t.Fatalf("extractInstallBundleID() = %q, want %q", got, "com.example.app")
	}
}

func TestExtractInstallBundleID_InvalidBody(t *testing.T) {
	body := []byte(`not-json`)
	got := extractInstallBundleID(body)
	if got != "" {
		t.Fatalf("extractInstallBundleID() = %q, want empty string", got)
	}
}

func TestIsUnsupportedWorkerRoute_Matches404Path(t *testing.T) {
	err := &mcppkg.WorkerHTTPError{
		StatusCode: 404,
		Path:       "/open_url",
		Body:       `{"detail":"Not Found"}`,
	}
	if !isUnsupportedWorkerRoute(err, "/open_url") {
		t.Fatal("isUnsupportedWorkerRoute() = false, want true")
	}
}

func TestIsUnsupportedWorkerRoute_UsesErrorsAs(t *testing.T) {
	base := &mcppkg.WorkerHTTPError{
		StatusCode: 404,
		Path:       "/open_url",
		Body:       `{"detail":"Not Found"}`,
	}
	err := fmt.Errorf("outer: %w", base)
	if !isUnsupportedWorkerRoute(err, "/open_url") {
		t.Fatal("isUnsupportedWorkerRoute() = false for wrapped error, want true")
	}
}

func TestIsUnsupportedWorkerRoute_Non404OrDifferentPath(t *testing.T) {
	err := &mcppkg.WorkerHTTPError{
		StatusCode: 500,
		Path:       "/open_url",
		Body:       `{"detail":"boom"}`,
	}
	if isUnsupportedWorkerRoute(err, "/open_url") {
		t.Fatal("isUnsupportedWorkerRoute() = true for non-404, want false")
	}

	err = &mcppkg.WorkerHTTPError{
		StatusCode: 404,
		Path:       "/launch",
		Body:       `{"detail":"Not Found"}`,
	}
	if isUnsupportedWorkerRoute(err, "/open_url") {
		t.Fatal("isUnsupportedWorkerRoute() = true for wrong path, want false")
	}
}

func TestIsContextCanceledError(t *testing.T) {
	if !isContextCanceledError(context.Canceled) {
		t.Fatal("isContextCanceledError(context.Canceled) = false, want true")
	}

	wrapped := fmt.Errorf("wrapped: %w", context.Canceled)
	if !isContextCanceledError(wrapped) {
		t.Fatal("isContextCanceledError(wrapped context.Canceled) = false, want true")
	}

	textOnly := fmt.Errorf("request failed: context canceled")
	if !isContextCanceledError(textOnly) {
		t.Fatal("isContextCanceledError(text-only context canceled) = false, want true")
	}

	if isContextCanceledError(fmt.Errorf("boom")) {
		t.Fatal("isContextCanceledError(non-cancel error) = true, want false")
	}
}

func TestPrintDevReadyFooter_PrintsInteractionShortcuts(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdout(t, func() {
		printDevReadyFooter("https://viewer.example", "nof1://expo-development-client/?url=https%3A%2F%2Ftunnel.example", false)
	})

	for _, expected := range []string{
		"Dev loop ready",
		"Viewer:",
		"Deep Link:",
		"Press Ctrl+C to stop hot reload and release the device",
		"Try device interactions:",
		`revyl device tap --target "Login button"`,
		"revyl device screenshot",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q\noutput:\n%s", expected, output)
		}
	}

	for _, unexpected := range []string{
		"Custom flows without hot reload:",
		"revyl device start --platform",
		"revyl device install --app-url <url>",
	} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("output unexpectedly contains %q\noutput:\n%s", unexpected, output)
		}
	}
}

func TestPrintDevReadyFooter_QuietModeSuppressesInteractionHints(t *testing.T) {
	ui.SetQuietMode(true)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdout(t, func() {
		printDevReadyFooter("https://viewer.example", "nof1://example", false)
	})

	if strings.Contains(output, "Try device interactions:") {
		t.Fatalf("output unexpectedly contains interaction header in quiet mode:\n%s", output)
	}
	if strings.Contains(output, "revyl device tap --target") {
		t.Fatalf("output unexpectedly contains tap shortcut in quiet mode:\n%s", output)
	}
	if strings.Contains(output, "revyl device screenshot") {
		t.Fatalf("output unexpectedly contains screenshot shortcut in quiet mode:\n%s", output)
	}
}

func TestWaitForDevSessionStop_CancelsWhenSessionEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lookup := &fakeDevSessionLookup{}
	lookup.present.Store(true)

	done := make(chan struct{})
	go func() {
		waitForDevSessionStop(ctx, cancel, lookup, 0, 10*time.Millisecond)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	lookup.present.Store(false)

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waitForDevSessionStop did not return after session ended")
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected context to be canceled when session ended")
	}
}

func TestWaitForDevSessionStop_ReturnsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	lookup := &fakeDevSessionLookup{}
	lookup.present.Store(true)

	done := make(chan struct{})
	go func() {
		waitForDevSessionStop(ctx, cancel, lookup, 0, 10*time.Millisecond)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waitForDevSessionStop did not return after context cancellation")
	}
}

func TestIsNoSessionAtIndexError(t *testing.T) {
	if !isNoSessionAtIndexError(fmt.Errorf("no session at index 3"), 3) {
		t.Fatal("expected no-session error match")
	}
	if isNoSessionAtIndexError(fmt.Errorf("backend cancel failed"), 3) {
		t.Fatal("unexpected no-session error match")
	}
}

type fakeDevSessionLookup struct {
	present atomic.Bool
}

func (f *fakeDevSessionLookup) GetSession(index int) *mcppkg.DeviceSession {
	if !f.present.Load() {
		return nil
	}
	return &mcppkg.DeviceSession{Index: index}
}
