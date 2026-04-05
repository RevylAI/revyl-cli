package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
	if !strings.Contains(err.Error(), "unexpected response") {
		t.Fatalf("error = %q, want unexpected response", err.Error())
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

	output := captureStdoutAndStderr(t, func() {
		printDevReadyFooter("https://viewer.example", "https://app.revyl.ai/tests/report?sessionId=test-session", "nof1://expo-development-client/?url=https%3A%2F%2Ftunnel.example", false)
	})

	for _, expected := range []string{
		"Dev loop ready",
		"Viewer:",
		"Report:",
		"Deep Link:",
		"[r] rebuild native + reinstall",
		"[q] quit",
		"In a new terminal, try:",
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
		printDevReadyFooter("https://viewer.example", "https://app.revyl.ai/tests/report?sessionId=test-session", "nof1://example", false)
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

func TestIsNoSessionAtIndexError(t *testing.T) {
	if !isNoSessionAtIndexError(fmt.Errorf("no session at index 3"), 3) {
		t.Fatal("expected no-session error match")
	}
	if isNoSessionAtIndexError(fmt.Errorf("backend cancel failed"), 3) {
		t.Fatal("unexpected no-session error match")
	}
}

func TestStartDevSessionWithProgress_PrintsTimedHintsForSlowProvisioning(t *testing.T) {
	starter := &fakeDevSessionStarter{
		delay:   70 * time.Millisecond,
		index:   2,
		session: &mcppkg.DeviceSession{Index: 2, Platform: "ios"},
	}
	recorder := &devSessionProgressRecorder{}

	idx, session, err := startDevSessionWithProgress(
		context.Background(),
		starter,
		mcppkg.StartSessionOptions{Platform: "ios"},
		20*time.Millisecond,
		recorder.hooks(),
	)
	if err != nil {
		t.Fatalf("startDevSessionWithProgress() error = %v, want nil", err)
	}
	if idx != 2 {
		t.Fatalf("index = %d, want %d", idx, 2)
	}
	if session == nil || session.Index != 2 {
		t.Fatalf("session = %#v, want index 2 session", session)
	}

	startCalls, stopCalls, infoMessages := recorder.snapshot()
	if startCalls < 2 {
		t.Fatalf("start spinner calls = %d, want >= 2 (initial + after at least one hint)", startCalls)
	}
	if stopCalls < 2 {
		t.Fatalf("stop spinner calls = %d, want >= 2 (hint + deferred final stop)", stopCalls)
	}
	if len(infoMessages) == 0 {
		t.Fatal("expected at least one timed provisioning hint")
	}
	if !strings.Contains(infoMessages[0], "Still provisioning device...") {
		t.Fatalf("first hint = %q, want provisioning hint", infoMessages[0])
	}
}

func TestStartDevSessionWithProgress_FastSuccessSkipsTimedHints(t *testing.T) {
	starter := &fakeDevSessionStarter{
		delay:   5 * time.Millisecond,
		index:   1,
		session: &mcppkg.DeviceSession{Index: 1, Platform: "android"},
	}
	recorder := &devSessionProgressRecorder{}

	idx, session, err := startDevSessionWithProgress(
		context.Background(),
		starter,
		mcppkg.StartSessionOptions{Platform: "android"},
		80*time.Millisecond,
		recorder.hooks(),
	)
	if err != nil {
		t.Fatalf("startDevSessionWithProgress() error = %v, want nil", err)
	}
	if idx != 1 || session == nil || session.Index != 1 {
		t.Fatalf("got index=%d session=%#v, want index=1 with session", idx, session)
	}

	startCalls, stopCalls, infoMessages := recorder.snapshot()
	if startCalls != 1 {
		t.Fatalf("start spinner calls = %d, want 1", startCalls)
	}
	if stopCalls != 1 {
		t.Fatalf("stop spinner calls = %d, want 1", stopCalls)
	}
	if len(infoMessages) != 0 {
		t.Fatalf("timed hints = %v, want none for fast success", infoMessages)
	}
}

func TestStartDevSessionWithProgress_ReturnsStartError(t *testing.T) {
	starter := &fakeDevSessionStarter{
		delay: 30 * time.Millisecond,
		err:   fmt.Errorf("backend unavailable"),
	}
	recorder := &devSessionProgressRecorder{}

	_, _, err := startDevSessionWithProgress(
		context.Background(),
		starter,
		mcppkg.StartSessionOptions{Platform: "ios"},
		100*time.Millisecond,
		recorder.hooks(),
	)
	if err == nil {
		t.Fatal("startDevSessionWithProgress() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "backend unavailable") {
		t.Fatalf("error = %q, want backend unavailable", err.Error())
	}

	startCalls, stopCalls, _ := recorder.snapshot()
	if startCalls != 1 {
		t.Fatalf("start spinner calls = %d, want 1", startCalls)
	}
	if stopCalls != 1 {
		t.Fatalf("stop spinner calls = %d, want 1", stopCalls)
	}
}

func TestStartDevSessionWithProgress_ReturnsContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	starter := &fakeDevSessionStarter{
		waitForCancel: true,
	}
	recorder := &devSessionProgressRecorder{}

	done := make(chan error, 1)
	go func() {
		_, _, err := startDevSessionWithProgress(
			ctx,
			starter,
			mcppkg.StartSessionOptions{Platform: "ios"},
			10*time.Millisecond,
			recorder.hooks(),
		)
		done <- err
	}()

	time.AfterFunc(25*time.Millisecond, cancel)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("startDevSessionWithProgress() error = nil, want context canceled")
		}
		if err != context.Canceled {
			t.Fatalf("error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startDevSessionWithProgress did not return after cancellation")
	}
}

type fakeDevSessionStarter struct {
	delay         time.Duration
	index         int
	session       *mcppkg.DeviceSession
	err           error
	waitForCancel bool
}

func (f *fakeDevSessionStarter) StartSession(
	ctx context.Context,
	opts mcppkg.StartSessionOptions,
) (int, *mcppkg.DeviceSession, error) {
	_ = opts
	if f.waitForCancel {
		<-ctx.Done()
		return -1, nil, ctx.Err()
	}
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return -1, nil, ctx.Err()
		case <-timer.C:
		}
	}
	if f.err != nil {
		return -1, nil, f.err
	}
	return f.index, f.session, nil
}

type devSessionProgressRecorder struct {
	mu           sync.Mutex
	startCalls   int
	stopCalls    int
	infoMessages []string
}

func (r *devSessionProgressRecorder) hooks() *devSessionProgressHooks {
	return &devSessionProgressHooks{
		startSpinner: func(message string) {
			_ = message
			r.mu.Lock()
			defer r.mu.Unlock()
			r.startCalls++
		},
		stopSpinner: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.stopCalls++
		},
		printInfo: func(format string, args ...interface{}) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.infoMessages = append(r.infoMessages, fmt.Sprintf(format, args...))
		},
	}
}

func (r *devSessionProgressRecorder) snapshot() (int, int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	msgs := make([]string, len(r.infoMessages))
	copy(msgs, r.infoMessages)
	return r.startCalls, r.stopCalls, msgs
}
