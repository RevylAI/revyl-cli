package cursorplugin_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestPOSIXRuntimeLauncherPublishesVerifiedRuntimeToUserPath verifies plugin-only install reaches PATH.
func TestPOSIXRuntimeLauncherPublishesVerifiedRuntimeToUserPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	sourceRuntime := writeRecordingRuntimeNamed(
		t,
		t.TempDir(),
		"revyl-source",
		fixture.OutputPath,
	)
	runtimeChecksum := fileSHA256(t, sourceRuntime)
	fixture.PinRuntime(t, runtimeChecksum)
	writeFakeCurl(t, fixture.FakeCommands, false)

	runRuntimeLauncher(t, pluginRoot, fixture.Environment(t, "REVYL_RUNTIME_SOURCE="+sourceRuntime))
	requirePublishedUserRuntime(t, fixture.Home, runtimeChecksum)
}

// TestPOSIXRuntimeLauncherPublishesFromCacheHit verifies an existing cache still reaches PATH.
func TestPOSIXRuntimeLauncherPublishesFromCacheHit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	sourceRuntime := writeRecordingRuntimeNamed(
		t,
		t.TempDir(),
		"revyl-source",
		fixture.OutputPath,
	)
	runtimeChecksum := fileSHA256(t, sourceRuntime)
	fixture.PinRuntime(t, runtimeChecksum)
	writeFakeCurl(t, fixture.FakeCommands, false)

	runRuntimeLauncher(t, pluginRoot, fixture.Environment(t, "REVYL_RUNTIME_SOURCE="+sourceRuntime))
	requirePublishedUserRuntime(t, fixture.Home, runtimeChecksum)
	removePublishedUserRuntime(t, fixture.Home)

	writeFakeCurl(t, fixture.FakeCommands, true)
	runRuntimeLauncher(t, pluginRoot, fixture.Environment(t))
	requirePublishedUserRuntime(t, fixture.Home, runtimeChecksum)
}

// TestPOSIXRuntimeLauncherDoesNotClobberMismatchedUserInstall protects a different PATH CLI.
func TestPOSIXRuntimeLauncherDoesNotClobberMismatchedUserInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	sourceRuntime := writeRecordingRuntimeNamed(
		t,
		t.TempDir(),
		"revyl-source",
		fixture.OutputPath,
	)
	runtimeChecksum := fileSHA256(t, sourceRuntime)
	fixture.PinRuntime(t, runtimeChecksum)
	writeFakeCurl(t, fixture.FakeCommands, false)

	homeInstall := filepath.Join(fixture.Home, ".revyl", "bin")
	if err := os.MkdirAll(homeInstall, 0o700); err != nil {
		t.Fatalf("create home install directory: %v", err)
	}
	mismatchedPath := filepath.Join(homeInstall, "revyl")
	const mismatchedContents = "not the pinned runtime\n"
	if err := os.WriteFile(mismatchedPath, []byte(mismatchedContents), 0o700); err != nil {
		t.Fatalf("write mismatched home install: %v", err)
	}

	runRuntimeLauncher(t, pluginRoot, fixture.Environment(t, "REVYL_RUNTIME_SOURCE="+sourceRuntime))

	got, err := os.ReadFile(mismatchedPath)
	if err != nil {
		t.Fatalf("read mismatched home install: %v", err)
	}
	if string(got) != mismatchedContents {
		t.Fatalf("mismatched home install was replaced: %q", got)
	}
	localInstall := filepath.Join(fixture.Home, ".local", "bin", "revyl")
	if fileSHA256(t, localInstall) != runtimeChecksum {
		t.Fatalf("empty user install was not published: %s", localInstall)
	}
}

// TestRuntimeLauncherOverrideDoesNotPublishUserInstall keeps dogfood binaries off PATH.
func TestRuntimeLauncherOverrideDoesNotPublishUserInstall(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	home := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "runtime-output.txt")
	fakeRuntime := writeRecordingRuntime(t, t.TempDir(), outputPath)
	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Env = environmentWithOverrides(
		"REVYL_BINARY="+fakeRuntime,
		"REVYL_RUNTIME_OUTPUT="+outputPath,
		"HOME="+home,
		"USERPROFILE="+home,
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
	)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime launcher override failed: %v\n%s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("runtime launcher wrote to MCP output: %q", output)
	}
	for _, path := range userRuntimePaths(home) {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("override published a user runtime at %s: %v", path, statErr)
		}
	}
}

// userRuntimePaths returns the PATH destinations the launcher may publish onto.
func userRuntimePaths(home string) []string {
	if runtime.GOOS == "windows" {
		return []string{
			filepath.Join(home, ".revyl", "bin", "revyl.exe"),
			filepath.Join(home, "AppData", "Local", "Revyl", "bin", "revyl.exe"),
		}
	}
	return []string{
		filepath.Join(home, ".revyl", "bin", "revyl"),
		filepath.Join(home, ".local", "bin", "revyl"),
	}
}

// requirePublishedUserRuntime asserts a verified pin is on a user PATH destination.
func requirePublishedUserRuntime(t *testing.T, home, checksum string) {
	t.Helper()
	for _, path := range userRuntimePaths(home) {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if fileSHA256(t, path) == checksum {
			return
		}
	}
	t.Fatalf("plugin-only install did not reach PATH under %s", home)
}

// removePublishedUserRuntime deletes published PATH copies so a later cache hit can republish.
func removePublishedUserRuntime(t *testing.T, home string) {
	t.Helper()
	for _, path := range userRuntimePaths(home) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove published runtime %s: %v", path, err)
		}
	}
}
