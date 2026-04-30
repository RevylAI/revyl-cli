package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
	"github.com/revyl/cli/internal/ui"
)

func TestShouldAttemptHotReloadAutoSetup(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.ProjectConfig
		want bool
	}{
		{
			name: "expo with build platforms still attempts auto setup",
			cfg: &config.ProjectConfig{
				Build: config.BuildConfig{
					System: "Expo",
					Platforms: map[string]config.BuildPlatform{
						"ios": {},
					},
				},
			},
			want: true,
		},
		{
			name: "react native with build platforms still attempts auto setup",
			cfg: &config.ProjectConfig{
				Build: config.BuildConfig{
					System: "React Native",
					Platforms: map[string]config.BuildPlatform{
						"ios": {},
					},
				},
			},
			want: true,
		},
		{
			name: "gradle uses rebuild-only loop",
			cfg: &config.ProjectConfig{
				Build: config.BuildConfig{
					System: "Gradle (Android)",
					Platforms: map[string]config.BuildPlatform{
						"android": {},
					},
				},
			},
			want: false,
		},
		{
			name: "no build platforms still attempts auto setup",
			cfg: &config.ProjectConfig{
				Build: config.BuildConfig{
					System: "Unknown",
				},
			},
			want: true,
		},
		{
			name: "existing hot reload config skips auto setup",
			cfg: &config.ProjectConfig{
				HotReload: config.HotReloadConfig{
					Providers: map[string]*config.ProviderConfig{
						"expo": {},
					},
				},
				Build: config.BuildConfig{
					System: "Expo",
					Platforms: map[string]config.BuildPlatform{
						"ios": {},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldAttemptHotReloadAutoSetup(tt.cfg); got != tt.want {
				t.Fatalf("shouldAttemptHotReloadAutoSetup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegisterDevStartFlagsLaunchVarParsesRepeatedValues(t *testing.T) {
	orig := devStartLaunchVars
	t.Cleanup(func() { devStartLaunchVars = orig })

	devStartLaunchVars = nil
	cmd := &cobra.Command{Use: "dev-test"}
	registerDevStartFlags(cmd)

	if err := cmd.ParseFlags([]string{"--launch-var", "API_URL", "--launch-var", "DEBUG"}); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	want := []string{"API_URL", "DEBUG"}
	if fmt.Sprint(devStartLaunchVars) != fmt.Sprint(want) {
		t.Fatalf("devStartLaunchVars = %v, want %v", devStartLaunchVars, want)
	}
}

func TestWithDevStartLaunchVarsCopiesLaunchVarsIntoStartOptions(t *testing.T) {
	orig := devStartLaunchVars
	t.Cleanup(func() { devStartLaunchVars = orig })

	devStartLaunchVars = []string{"REVYL_AUTH_BYPASS_ENABLED", "REVYL_AUTH_BYPASS_TOKEN"}
	opts := withDevStartLaunchVars(mcppkg.StartSessionOptions{Platform: "ios"})

	want := []string{"REVYL_AUTH_BYPASS_ENABLED", "REVYL_AUTH_BYPASS_TOKEN"}
	if fmt.Sprint(opts.LaunchVars) != fmt.Sprint(want) {
		t.Fatalf("LaunchVars = %v, want %v", opts.LaunchVars, want)
	}

	devStartLaunchVars[0] = "MUTATED"
	if opts.LaunchVars[0] != "REVYL_AUTH_BYPASS_ENABLED" {
		t.Fatalf("LaunchVars shared backing array; got %v", opts.LaunchVars)
	}
}

func TestFormatDevActionDebugPayloadShowsOpenURL(t *testing.T) {
	got := formatDevActionDebugPayload(map[string]string{
		"url": "myapp://expo-development-client/?url=https%3A%2F%2Frelay.example",
	})

	if !strings.Contains(got, "url=myapp://expo-development-client/?url=https%3A%2F%2Frelay.example") {
		t.Fatalf("payload = %q, want full open_url value", got)
	}
}

func TestFormatDevActionDebugPayloadMasksInstallAppURL(t *testing.T) {
	got := formatDevActionDebugPayload(map[string]string{
		"app_url":   "https://storage.example/app.ipa?X-Amz-Signature=secret",
		"bundle_id": "com.example.app",
	})

	if strings.Contains(got, "secret") {
		t.Fatalf("payload = %q, leaked presigned query", got)
	}
	if !strings.Contains(got, "bundle_id=com.example.app") {
		t.Fatalf("payload = %q, want bundle id", got)
	}
}

func TestDevStartLaunchVarsReachSessionStartOptions(t *testing.T) {
	orig := devStartLaunchVars
	t.Cleanup(func() { devStartLaunchVars = orig })

	devStartLaunchVars = nil
	cmd := &cobra.Command{Use: "dev-test"}
	registerDevStartFlags(cmd)
	if err := cmd.ParseFlags([]string{"--launch-var", "A", "--launch-var", "B"}); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}

	starter := &fakeDevSessionStarter{
		index:   1,
		session: &mcppkg.DeviceSession{Index: 1, Platform: "ios"},
	}
	recorder := &devSessionProgressRecorder{}

	_, _, err := startDevSessionWithProgress(
		context.Background(),
		starter,
		withDevStartLaunchVars(mcppkg.StartSessionOptions{Platform: "ios"}),
		80*time.Millisecond,
		recorder.hooks(),
	)
	if err != nil {
		t.Fatalf("startDevSessionWithProgress() error = %v", err)
	}

	want := []string{"A", "B"}
	if fmt.Sprint(starter.opts.LaunchVars) != fmt.Sprint(want) {
		t.Fatalf("StartSession LaunchVars = %v, want %v", starter.opts.LaunchVars, want)
	}
}

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
		printDevReadyFooter("https://viewer.example", "nof1://expo-development-client/?url=https%3A%2F%2Ftunnel.example", false, false, false, "default", 0)
	})

	for _, expected := range []string{
		"Dev loop ready",
		"Viewer:",
		"Deep Link:",
		"[r] rebuild native + reinstall",
		"[q] quit",
		"context: default",
		"Manage the session:",
		"revyl dev status",
		"revyl dev rebuild",
		"revyl dev test run <name>",
		"revyl dev attach default",
		"Interact with the device:",
		`revyl device tap --target "Login button" -s 0`,
		"# AI-grounded tap",
		`revyl device instruction "log in and verify" -s 0`,
		"revyl device screenshot -s 0",
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

func TestPrintDevReadyFooter_SessionIndexInCommandHints(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdoutAndStderr(t, func() {
		printDevReadyFooter("https://viewer.example", "nof1://example", false, false, false, "ios-main", 2)
	})

	for _, expected := range []string{
		"context: ios-main",
		"revyl dev attach ios-main",
		"-s 2",
		`revyl device tap --target "Login button" -s 2`,
		`revyl device instruction "log in and verify" -s 2`,
		"revyl device screenshot -s 2",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output missing %q\noutput:\n%s", expected, output)
		}
	}
}

func TestPrintDevReadyFooter_QuietModeSuppressesInteractionHints(t *testing.T) {
	ui.SetQuietMode(true)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdout(t, func() {
		printDevReadyFooter("https://viewer.example", "nof1://example", false, false, false, "default", 0)
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

func TestPrintDevReadyFooter_BareRN_ShowsTunnelURLInsteadOfDeepLink(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdoutAndStderr(t, func() {
		printDevReadyFooter("https://viewer.example", "https://abc-def.trycloudflare.com", false, true, false, "default", 0)
	})

	if !strings.Contains(output, "Tunnel URL:") {
		t.Fatalf("output missing 'Tunnel URL:' for bare RN footer\noutput:\n%s", output)
	}
	if strings.Contains(output, "Deep Link:") {
		t.Fatalf("output unexpectedly contains 'Deep Link:' for bare RN footer\noutput:\n%s", output)
	}
}

func TestPrintDevReadyFooter_Expo_ShowsDeepLink(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdoutAndStderr(t, func() {
		printDevReadyFooter("https://viewer.example", "nof1://expo-development-client/?url=https%3A%2F%2Ftunnel.example", false, false, false, "default", 0)
	})

	if !strings.Contains(output, "Deep Link:") {
		t.Fatalf("output missing 'Deep Link:' for Expo footer\noutput:\n%s", output)
	}
	if strings.Contains(output, "Tunnel URL:") {
		t.Fatalf("output unexpectedly contains 'Tunnel URL:' for Expo footer\noutput:\n%s", output)
	}
}

func TestPrintRebuildLoopControls_WithKeybinds(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdoutAndStderr(t, func() {
		printRebuildLoopControls(true, false)
	})

	if !strings.Contains(output, "[r] rebuild + reinstall") {
		t.Fatalf("output missing keybinding rebuild hint:\n%s", output)
	}
	if !strings.Contains(output, "[q] quit") {
		t.Fatalf("output missing quit hint:\n%s", output)
	}
	if strings.Contains(output, "revyl dev rebuild") {
		t.Fatalf("output unexpectedly contains non-TTY rebuild hint:\n%s", output)
	}
}

func TestPrintRebuildLoopControls_WithoutKeybinds(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	output := captureStdoutAndStderr(t, func() {
		printRebuildLoopControls(false, false)
	})

	if !strings.Contains(output, "Trigger rebuild: revyl dev rebuild") {
		t.Fatalf("output missing non-TTY rebuild hint:\n%s", output)
	}
	if !strings.Contains(output, "Stop session:    Ctrl+C") {
		t.Fatalf("output missing Ctrl+C hint:\n%s", output)
	}
	if strings.Contains(output, "[r] rebuild + reinstall") {
		t.Fatalf("output unexpectedly contains TTY keybinding hint:\n%s", output)
	}
}

func TestResolveRebuildLoopPlatform_UsesExplicitPlatformKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"custom-ios": {
					Command: "xcodebuild",
					Output:  "build/*.app",
				},
			},
		},
	}

	platformKey, devicePlatform, err := resolveRebuildLoopPlatform(cfg, "ios", "custom-ios", false)
	if err != nil {
		t.Fatalf("resolveRebuildLoopPlatform() error = %v, want nil", err)
	}
	if platformKey != "custom-ios" {
		t.Fatalf("platformKey = %q, want %q", platformKey, "custom-ios")
	}
	if devicePlatform != "ios" {
		t.Fatalf("devicePlatform = %q, want %q", devicePlatform, "ios")
	}
}

func TestResolveRebuildLoopPlatform_RejectsMismatchedExplicitPlatform(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"ios-dev": {
					Command: "xcodebuild",
					Output:  "build/*.app",
				},
			},
		},
	}

	_, _, err := resolveRebuildLoopPlatform(cfg, "android", "ios-dev", true)
	if err == nil {
		t.Fatal("resolveRebuildLoopPlatform() error = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), "ios build") {
		t.Fatalf("error = %q, want ios build mismatch", err.Error())
	}
}

func TestResolveRebuildLoopPlatform_RejectsAmbiguousKeysWithoutPlatformKey(t *testing.T) {
	cfg := &config.ProjectConfig{
		Build: config.BuildConfig{
			Platforms: map[string]config.BuildPlatform{
				"simulator": {
					Command: "xcodebuild",
					Output:  "build/*.app",
				},
				"device": {
					Command: "./gradlew assembleDebug",
					Output:  "app-debug.apk",
				},
			},
		},
	}

	_, _, err := resolveRebuildLoopPlatform(cfg, "ios", "", false)
	if err == nil {
		t.Fatal("resolveRebuildLoopPlatform() error = nil, want ambiguity error")
	}
	if !strings.Contains(err.Error(), "Use --platform-key to choose one") {
		t.Fatalf("error = %q, want explicit platform-key guidance", err.Error())
	}
}

func TestFormatBuildVersionLabel_PrefersVersionAndID(t *testing.T) {
	label := formatBuildVersionLabel(&api.BuildVersion{
		ID:      "ver_123",
		Version: "1.2.3-ios",
	})
	if label != "1.2.3-ios (ver_123)" {
		t.Fatalf("formatBuildVersionLabel() = %q, want %q", label, "1.2.3-ios (ver_123)")
	}
}

func TestTryLaunchInstalledApp_WarnsWithResolvedIdentifier(t *testing.T) {
	ui.SetQuietMode(false)
	t.Cleanup(func() {
		ui.SetQuietMode(false)
	})

	requester := &fakeWorkerSessionRequester{
		err: fmt.Errorf("launch route unavailable"),
	}
	output := captureStdoutAndStderr(t, func() {
		tryLaunchInstalledApp(context.Background(), requester, 7, "android", "com.example.app", "", "")
	})

	if !strings.Contains(output, "launch failed") {
		t.Fatalf("output missing launch failure warning:\n%s", output)
	}
	if !strings.Contains(output, "Package name com.example.app") {
		t.Fatalf("output missing resolved package identifier:\n%s", output)
	}
	if requester.path != "/launch" {
		t.Fatalf("path = %q, want /launch", requester.path)
	}
	if requester.sessionIndex != 7 {
		t.Fatalf("sessionIndex = %d, want 7", requester.sessionIndex)
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
	opts          mcppkg.StartSessionOptions
}

func (f *fakeDevSessionStarter) StartSession(
	ctx context.Context,
	opts mcppkg.StartSessionOptions,
) (int, *mcppkg.DeviceSession, error) {
	f.opts = opts
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

type fakeWorkerSessionRequester struct {
	sessionIndex int
	path         string
	body         map[string]string
	resp         []byte
	err          error
}

func (f *fakeWorkerSessionRequester) WorkerRequestForSession(
	ctx context.Context,
	sessionIndex int,
	path string,
	body interface{},
) ([]byte, error) {
	_ = ctx
	f.sessionIndex = sessionIndex
	f.path = path
	if payload, ok := body.(map[string]string); ok {
		f.body = payload
	}
	return f.resp, f.err
}

// ---------------------------------------------------------------------------
// Delta push / status file tests
// ---------------------------------------------------------------------------

func TestWriteDevStatus_Success(t *testing.T) {
	dir := t.TempDir()
	statusPath := dir + "/status.json"

	session := &mcppkg.DeviceSession{
		SessionID: "sess-123",
		ViewerURL: "https://app.revyl.ai/device/sess-123",
	}
	viewerURL := devSessionViewerURL(session, false)

	result := devRebuildResult{
		elapsed:       15 * time.Second,
		buildDuration: 14 * time.Second,
		pushDuration:  1 * time.Second,
		usedDelta:     true,
		dataPreserved: true,
		filesChanged:  2,
		manifest:      &build.AppManifest{Hash: "abc"},
	}

	writeDevStatus(
		statusPath,
		session,
		viewerURL,
		"https://hr-abc.revyl.ai",
		"myapp://expo-development-client/?url=https://hr-abc.revyl.ai",
		"relay",
		"ios",
		3,
		true,
		result,
	)

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var ds devStatus
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if ds.State != "idle" {
		t.Fatalf("expected state=idle, got %s", ds.State)
	}
	if ds.SessionID != "sess-123" {
		t.Fatalf("expected session_id=sess-123, got %s", ds.SessionID)
	}
	if ds.ViewerURL != "https://app.revyl.ai/sessions/sess-123" {
		t.Fatalf("expected viewer_url=%s, got %s", "https://app.revyl.ai/sessions/sess-123", ds.ViewerURL)
	}
	if ds.TunnelURL != "https://hr-abc.revyl.ai" {
		t.Fatalf("expected tunnel_url=https://hr-abc.revyl.ai, got %s", ds.TunnelURL)
	}
	if ds.DeepLinkURL != "myapp://expo-development-client/?url=https://hr-abc.revyl.ai" {
		t.Fatalf("expected deep_link_url to be persisted, got %s", ds.DeepLinkURL)
	}
	if ds.Transport != "relay" {
		t.Fatalf("expected transport=relay, got %s", ds.Transport)
	}
	if ds.RebuildCount != 3 {
		t.Fatalf("expected rebuild_count=3, got %d", ds.RebuildCount)
	}
	if ds.LastRebuild == nil {
		t.Fatal("expected last_rebuild to be non-nil")
	}
	if ds.LastRebuild.Status != "success" {
		t.Fatalf("expected status=success, got %s", ds.LastRebuild.Status)
	}
	if ds.LastRebuild.PushMode != "delta" {
		t.Fatalf("expected push_mode=delta, got %s", ds.LastRebuild.PushMode)
	}
	if !ds.LastRebuild.DataPreserved {
		t.Fatal("expected data_preserved=true")
	}
	if ds.LastRebuild.FilesChanged != 2 {
		t.Fatalf("expected files_changed=2, got %d", ds.LastRebuild.FilesChanged)
	}
}

func TestWriteDevStatus_BuildFailure(t *testing.T) {
	dir := t.TempDir()
	statusPath := dir + "/status.json"

	result := devRebuildResult{
		buildErr:      fmt.Errorf("exit code 65"),
		elapsed:       8 * time.Second,
		buildDuration: 8 * time.Second,
		buildErrors: []build.BuildError{
			{File: "LoginView.swift", Line: 42, Column: 15, Severity: "error", Message: "type mismatch"},
		},
	}

	writeDevStatus(statusPath, nil, "", "", "", "", "ios", 1, false, result)

	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var ds devStatus
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if ds.LastRebuild.Status != "build_failed" {
		t.Fatalf("expected status=build_failed, got %s", ds.LastRebuild.Status)
	}
	if len(ds.LastRebuild.BuildErrors) != 1 {
		t.Fatalf("expected 1 build error, got %d", len(ds.LastRebuild.BuildErrors))
	}
	if ds.LastRebuild.BuildErrors[0].File != "LoginView.swift" {
		t.Fatalf("expected LoginView.swift, got %s", ds.LastRebuild.BuildErrors[0].File)
	}
}

func TestWriteDevStatus_Skipped(t *testing.T) {
	dir := t.TempDir()
	statusPath := dir + "/status.json"

	result := devRebuildResult{
		elapsed:  3 * time.Second,
		skipped:  true,
		manifest: &build.AppManifest{Hash: "abc"},
	}

	writeDevStatus(statusPath, nil, "", "", "", "", "ios", 5, true, result)

	data, _ := os.ReadFile(statusPath)
	var ds devStatus
	_ = json.Unmarshal(data, &ds)

	if ds.LastRebuild.Status != "skipped" {
		t.Fatalf("expected status=skipped, got %s", ds.LastRebuild.Status)
	}
	if ds.LastRebuild.PushMode != "none" {
		t.Fatalf("expected push_mode=none, got %s", ds.LastRebuild.PushMode)
	}
}

func TestBuildDevStatusOutput_FallsBackToContextTunnelMetadata(t *testing.T) {
	ctxMeta := &DevContext{
		Name:         "default",
		Platform:     "ios",
		SessionID:    "sess-123",
		SessionOwned: true,
		ViewerURL:    "https://app.revyl.ai/sessions/sess-123",
		TunnelURL:    "https://hr-abc.revyl.ai",
		DeepLinkURL:  "myapp://expo-development-client/?url=https://hr-abc.revyl.ai",
		Transport:    "relay",
		State:        devContextStateRunning,
	}

	out := buildDevStatusOutput("default", 4242, ctxMeta, nil)

	if got := out["tunnel_url"]; got != "https://hr-abc.revyl.ai" {
		t.Fatalf("tunnel_url = %v, want relay URL", got)
	}
	if got := out["deep_link_url"]; got != "myapp://expo-development-client/?url=https://hr-abc.revyl.ai" {
		t.Fatalf("deep_link_url = %v, want relay deep link", got)
	}
	if got := out["transport"]; got != "relay" {
		t.Fatalf("transport = %v, want relay", got)
	}
	if got := out["session_id"]; got != "sess-123" {
		t.Fatalf("session_id = %v, want sess-123", got)
	}
}

func TestParseExternalTunnelInput_HTTPURL(t *testing.T) {
	input := "https://example.ngrok.app"

	parsed, err := parseExternalTunnelInput(input)
	if err != nil {
		t.Fatalf("parseExternalTunnelInput() error = %v", err)
	}

	if parsed.tunnelURL != input {
		t.Fatalf("tunnelURL = %q, want %q", parsed.tunnelURL, input)
	}
	if parsed.deepLinkURL != "" {
		t.Fatalf("deepLinkURL = %q, want empty", parsed.deepLinkURL)
	}
	if parsed.fromDeepLink {
		t.Fatal("fromDeepLink = true, want false")
	}
}

func TestParseExternalTunnelInput_ExpoDeepLink(t *testing.T) {
	input := "myapp://expo-development-client/?url=https%3A%2F%2Fexample.ngrok.app"

	parsed, err := parseExternalTunnelInput(input)
	if err != nil {
		t.Fatalf("parseExternalTunnelInput() error = %v", err)
	}

	if parsed.tunnelURL != "https://example.ngrok.app" {
		t.Fatalf("tunnelURL = %q, want nested tunnel URL", parsed.tunnelURL)
	}
	if parsed.deepLinkURL != input {
		t.Fatalf("deepLinkURL = %q, want original input", parsed.deepLinkURL)
	}
	if !parsed.fromDeepLink {
		t.Fatal("fromDeepLink = false, want true")
	}
}

func TestParseExternalTunnelInput_ExpoDeepLinkWithReadableTunnelURL(t *testing.T) {
	input := "myapp://expo-development-client/?url=https://example.ngrok.app"

	parsed, err := parseExternalTunnelInput(input)
	if err != nil {
		t.Fatalf("parseExternalTunnelInput() error = %v", err)
	}

	if parsed.tunnelURL != "https://example.ngrok.app" {
		t.Fatalf("tunnelURL = %q, want nested tunnel URL", parsed.tunnelURL)
	}
	if parsed.deepLinkURL != input {
		t.Fatalf("deepLinkURL = %q, want original input", parsed.deepLinkURL)
	}
	if !parsed.fromDeepLink {
		t.Fatal("fromDeepLink = false, want true")
	}
}

func TestParseExternalTunnelInput_ExpPrefixedDeepLink(t *testing.T) {
	input := "exp+myapp://expo-development-client/?url=https%3A%2F%2Fu.expo.dev%2Fabc%3Fchannel-name%3Dmain"

	parsed, err := parseExternalTunnelInput(input)
	if err != nil {
		t.Fatalf("parseExternalTunnelInput() error = %v", err)
	}

	if parsed.tunnelURL != "https://u.expo.dev/abc?channel-name=main" {
		t.Fatalf("tunnelURL = %q, want decoded nested tunnel URL", parsed.tunnelURL)
	}
	if parsed.deepLinkURL != input {
		t.Fatalf("deepLinkURL = %q, want original input", parsed.deepLinkURL)
	}
}

func TestParseExternalTunnelInput_RejectsDeepLinkWithoutTunnelURL(t *testing.T) {
	_, err := parseExternalTunnelInput("myapp://expo-development-client/")
	if err == nil {
		t.Fatal("parseExternalTunnelInput() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "url=") {
		t.Fatalf("error = %v, want url= hint", err)
	}
}
