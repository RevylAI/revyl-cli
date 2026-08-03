package cursorplugin_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type runtimeManifest struct {
	SchemaVersion      int    `json:"schema_version"`
	GeneratedBy        string `json:"generated_by"`
	Prepared           bool   `json:"prepared"`
	PluginVersion      string `json:"plugin_version"`
	RuntimeVersion     string `json:"runtime_version"`
	ReleaseTag         string `json:"release_tag"`
	ReleaseBaseURL     string `json:"release_base_url"`
	DarwinAMD64Asset   string `json:"darwin_amd64_asset"`
	DarwinAMD64SHA256  string `json:"darwin_amd64_sha256"`
	DarwinARM64Asset   string `json:"darwin_arm64_asset"`
	DarwinARM64SHA256  string `json:"darwin_arm64_sha256"`
	LinuxAMD64Asset    string `json:"linux_amd64_asset"`
	LinuxAMD64SHA256   string `json:"linux_amd64_sha256"`
	LinuxARM64Asset    string `json:"linux_arm64_asset"`
	LinuxARM64SHA256   string `json:"linux_arm64_sha256"`
	WindowsAMD64Asset  string `json:"windows_amd64_asset"`
	WindowsAMD64SHA256 string `json:"windows_amd64_sha256"`
	WindowsARM64Asset  string `json:"windows_arm64_asset"`
	WindowsARM64SHA256 string `json:"windows_arm64_sha256"`
}

type runtimeAssetContract struct {
	AssetName string
	SHA256    string
}

// TestRuntimeManifestContract verifies prepared and pending runtime metadata.
func TestRuntimeManifestContract(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	manifest := readJSON[runtimeManifest](t, filepath.Join(pluginRoot, "runtime-manifest.json"))
	plugin := readJSON[pluginManifest](t, filepath.Join(pluginRoot, ".cursor-plugin", "plugin.json"))

	if manifest.SchemaVersion != 1 {
		t.Fatalf("runtime schema = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.GeneratedBy != "make -C revyl-cli prepare-cursor-plugin-release" {
		t.Fatalf("runtime generator = %q", manifest.GeneratedBy)
	}
	if manifest.PluginVersion != plugin.Version {
		t.Fatalf("runtime plugin version = %q, want %q", manifest.PluginVersion, plugin.Version)
	}
	if manifest.ReleaseTag != "v"+manifest.RuntimeVersion {
		t.Fatalf("runtime tag = %q, want v%s", manifest.ReleaseTag, manifest.RuntimeVersion)
	}
	expectedBaseURL := "https://github.com/RevylAI/revyl-cli/releases/download/" + manifest.ReleaseTag
	if manifest.ReleaseBaseURL != expectedBaseURL {
		t.Fatalf("runtime base URL = %q, want %q", manifest.ReleaseBaseURL, expectedBaseURL)
	}

	assets := runtimeManifestAssets(manifest)
	expectedNames := []string{
		"revyl-darwin-amd64",
		"revyl-darwin-arm64",
		"revyl-linux-amd64",
		"revyl-linux-arm64",
		"revyl-windows-amd64.exe",
		"revyl-windows-arm64.exe",
	}
	if len(assets) != len(expectedNames) {
		t.Fatalf("runtime asset count = %d, want %d", len(assets), len(expectedNames))
	}
	for index, asset := range assets {
		if asset.AssetName != expectedNames[index] {
			t.Errorf("runtime asset %d = %q, want %q", index, asset.AssetName, expectedNames[index])
		}
		if manifest.Prepared {
			requireSHA256(t, asset.SHA256)
		} else if asset.SHA256 != "" {
			t.Errorf("pending runtime asset %s has a checksum", asset.AssetName)
		}
	}
}

// TestRuntimeLauncherUsesExplicitOverride verifies dogfood and offline installs skip downloads.
func TestRuntimeLauncherUsesExplicitOverride(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	outputPath := filepath.Join(t.TempDir(), "runtime-output.txt")
	fakeRuntime := writeRecordingRuntime(t, t.TempDir(), outputPath)
	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Env = environmentWithOverrides(
		"REVYL_BINARY="+fakeRuntime,
		"REVYL_RUNTIME_OUTPUT="+outputPath,
		"REVYL_API_KEY=${env:REVYL_API_KEY}",
	)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime launcher override failed: %v\n%s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("runtime launcher wrote to MCP output: %q", output)
	}
	recording := readText(t, outputPath)
	if !recordingContainsExecutable(recording, fakeRuntime) {
		t.Fatalf("runtime recording %q does not contain executable=%q", recording, fakeRuntime)
	}
	if !strings.Contains(recording, "args=mcp serve --profile dev") {
		t.Fatalf("runtime recording %q does not contain args=mcp serve --profile dev", recording)
	}
	if !strings.Contains(recording, "api_key_set=no") {
		t.Fatalf("runtime recording %q retained the unresolved API-key placeholder", recording)
	}
}

// recordingContainsExecutable reports whether a launcher recording used the expected binary.
func recordingContainsExecutable(recording, expectedExecutable string) bool {
	const prefix = "executable="
	for _, line := range strings.Split(recording, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		actual := strings.TrimPrefix(line, prefix)
		if actual == expectedExecutable || sameFilesystemPath(actual, expectedExecutable) {
			return true
		}
	}
	return false
}

// TestRuntimeLauncherFailureKeepsStdoutClean protects the MCP transport boundary.
func TestRuntimeLauncherFailureKeepsStdoutClean(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")
	command.Env = environmentWithOverrides(
		"REVYL_BINARY=",
		"REVYL_RUNTIME_MANIFEST="+writeUnpreparedRuntimeManifest(t, t.TempDir()),
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if err == nil {
		t.Fatal("unprepared runtime launcher unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("runtime failure corrupted MCP stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no prepared runtime") {
		t.Fatalf("runtime failure stderr = %q", stderr.String())
	}
}

// TestPOSIXRuntimeLauncherDownloadsCachesAndRepairs verifies first-use lifecycle behavior.
func TestPOSIXRuntimeLauncherDownloadsCachesAndRepairs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixtureRoot := t.TempDir()
	fakeCommands := filepath.Join(fixtureRoot, "bin")
	if err := os.Mkdir(fakeCommands, 0o700); err != nil {
		t.Fatalf("create fake command directory: %v", err)
	}
	outputPath := filepath.Join(fixtureRoot, "runtime-output.txt")
	fakeRuntime := writeRecordingRuntime(t, fixtureRoot, outputPath)
	runtimeChecksum := fileSHA256(t, fakeRuntime)
	manifestPath, platform, assetName := writePreparedRuntimeManifest(
		t,
		fixtureRoot,
		runtimeChecksum,
	)
	writeFakeCurl(t, fakeCommands, false)

	cacheRoot := filepath.Join(fixtureRoot, "cache")
	environment := environmentWithOverrides(
		"PATH="+fakeCommands+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+filepath.Join(fixtureRoot, "home"),
		"REVYL_BINARY=",
		"REVYL_RUNTIME_MANIFEST="+manifestPath,
		"REVYL_PLUGIN_CACHE_DIR="+cacheRoot,
		"REVYL_RUNTIME_SOURCE="+fakeRuntime,
		"REVYL_RUNTIME_OUTPUT="+outputPath,
	)

	runRuntimeLauncher(t, pluginRoot, environment)
	cachedRuntime := filepath.Join(cacheRoot, "9.8.7", platform, "revyl")
	if got := fileSHA256(t, cachedRuntime); got != runtimeChecksum {
		t.Fatalf("cached runtime checksum = %s, want %s", got, runtimeChecksum)
	}

	if err := os.Remove(cachedRuntime); err != nil {
		t.Fatalf("remove runtime before concurrent start: %v", err)
	}
	type concurrentLaunchResult struct {
		Output []byte
		Err    error
	}
	results := make(chan concurrentLaunchResult, 2)
	for range 2 {
		go func() {
			command := runtimeLauncherCommandForTest(pluginRoot)
			command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")
			command.Env = environment
			output, runErr := command.CombinedOutput()
			results <- concurrentLaunchResult{Output: output, Err: runErr}
		}()
	}
	for range 2 {
		result := <-results
		if result.Err != nil || len(result.Output) != 0 {
			t.Fatalf(
				"concurrent runtime launch failed: %v\n%s",
				result.Err,
				result.Output,
			)
		}
	}
	if got := fileSHA256(t, cachedRuntime); got != runtimeChecksum {
		t.Fatalf("concurrent cached runtime checksum = %s, want %s", got, runtimeChecksum)
	}

	writeFakeCurl(t, fakeCommands, true)
	if err := os.Remove(outputPath); err != nil {
		t.Fatalf("remove first runtime output: %v", err)
	}
	runRuntimeLauncher(t, pluginRoot, environment)

	if err := os.WriteFile(cachedRuntime, []byte("corrupted"), 0o700); err != nil {
		t.Fatalf("corrupt cached runtime: %v", err)
	}
	writeFakeCurl(t, fakeCommands, false)
	runRuntimeLauncher(t, pluginRoot, environment)
	if got := fileSHA256(t, cachedRuntime); got != runtimeChecksum {
		t.Fatalf("repaired runtime checksum = %s, want %s", got, runtimeChecksum)
	}
	recording := readText(t, outputPath)
	if !strings.Contains(recording, "args=mcp serve --profile dev") {
		t.Fatalf("runtime recording for %s is incomplete: %q", assetName, recording)
	}
}

// TestPOSIXRuntimeLauncherRejectsMismatchedDownload verifies bad assets never execute.
func TestPOSIXRuntimeLauncherRejectsMismatchedDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixtureRoot := t.TempDir()
	fakeCommands := filepath.Join(fixtureRoot, "bin")
	if err := os.Mkdir(fakeCommands, 0o700); err != nil {
		t.Fatalf("create fake command directory: %v", err)
	}
	expectedRuntime := writeRecordingRuntime(
		t,
		fixtureRoot,
		filepath.Join(fixtureRoot, "runtime-output.txt"),
	)
	manifestPath, platform, _ := writePreparedRuntimeManifest(
		t,
		fixtureRoot,
		fileSHA256(t, expectedRuntime),
	)
	corruptRuntime := filepath.Join(fixtureRoot, "corrupt-runtime")
	if err := os.WriteFile(corruptRuntime, []byte("not the pinned runtime"), 0o700); err != nil {
		t.Fatalf("write corrupt runtime: %v", err)
	}
	writeFakeCurl(t, fakeCommands, false)
	cacheRoot := filepath.Join(fixtureRoot, "cache")
	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")
	command.Env = environmentWithOverrides(
		"PATH="+fakeCommands+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+filepath.Join(fixtureRoot, "home"),
		"REVYL_BINARY=",
		"REVYL_RUNTIME_MANIFEST="+manifestPath,
		"REVYL_PLUGIN_CACHE_DIR="+cacheRoot,
		"REVYL_RUNTIME_SOURCE="+corruptRuntime,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if err == nil {
		t.Fatal("mismatched runtime download unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("checksum failure corrupted MCP stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "checksum verification failed") {
		t.Fatalf("checksum failure stderr = %q", stderr.String())
	}
	cachedRuntime := filepath.Join(cacheRoot, "9.8.7", platform, "revyl")
	if _, statErr := os.Stat(cachedRuntime); !os.IsNotExist(statErr) {
		t.Fatalf("mismatched runtime was cached: %v", statErr)
	}
}

// adoptionFixture prepares an isolated launcher environment for local-install coverage.
//
// Every path is scoped to one t.TempDir(), and the manifest pins a fixture-specific
// digest, so neither a developer's real Revyl CLI nor their cache can change a result.
type adoptionFixture struct {
	Root         string
	FakeCommands string
	Home         string
	CacheRoot    string
	OutputPath   string
	manifestPath string
	platform     string
}

// newAdoptionFixture creates the isolated directories a launcher run needs.
//
// The runtime pin is applied separately because a recording fixture embeds this
// fixture's output path, so its digest is only known after it is written.
func newAdoptionFixture(t *testing.T) *adoptionFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &adoptionFixture{
		Root:         root,
		FakeCommands: filepath.Join(root, "bin"),
		Home:         filepath.Join(root, "home"),
		CacheRoot:    filepath.Join(root, "cache"),
		OutputPath:   filepath.Join(root, "runtime-output.txt"),
	}
	for _, directory := range []string{fixture.FakeCommands, fixture.Home} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create fixture directory %s: %v", directory, err)
		}
	}
	return fixture
}

// PinRuntime writes the manifest fixture that pins one expected runtime digest.
func (fixture *adoptionFixture) PinRuntime(t *testing.T, checksum string) {
	t.Helper()
	manifestPath, platform, _ := writePreparedRuntimeManifest(t, fixture.Root, checksum)
	fixture.manifestPath = manifestPath
	fixture.platform = platform
}

// Environment returns the launcher environment plus any test-specific overrides.
//
// PATH keeps the standard command directories so the bootstrap can still resolve
// utilities such as dirname and sed, while the fixture directory shadows curl.
func (fixture *adoptionFixture) Environment(t *testing.T, overrides ...string) []string {
	t.Helper()
	if fixture.manifestPath == "" {
		t.Fatal("adoption fixture used before PinRuntime")
	}
	base := []string{
		"PATH=" + fixture.FakeCommands + string(os.PathListSeparator) + "/usr/bin:/bin",
		"HOME=" + fixture.Home,
		"REVYL_BINARY=",
		"REVYL_RUNTIME_MANIFEST=" + fixture.manifestPath,
		"REVYL_PLUGIN_CACHE_DIR=" + fixture.CacheRoot,
		"REVYL_RUNTIME_OUTPUT=" + fixture.OutputPath,
	}
	return environmentWithOverrides(append(base, overrides...)...)
}

// CachedRuntime returns the versioned cache path the launcher must populate.
func (fixture *adoptionFixture) CachedRuntime() string {
	return filepath.Join(fixture.CacheRoot, "9.8.7", fixture.platform, "revyl")
}

// TestPOSIXRuntimeLauncherAdoptsMatchingPathInstall verifies an offline start from PATH.
//
// This is the reported Cloud Agent failure: the release download is unavailable while a
// byte-identical CLI is already installed.
func TestPOSIXRuntimeLauncherAdoptsMatchingPathInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	installedRuntime := writeRecordingRuntimeNamed(
		t,
		fixture.FakeCommands,
		"revyl",
		fixture.OutputPath,
	)
	runtimeChecksum := fileSHA256(t, installedRuntime)
	fixture.PinRuntime(t, runtimeChecksum)
	writeFakeCurl(t, fixture.FakeCommands, true)

	runRuntimeLauncher(t, pluginRoot, fixture.Environment(t))

	cachedRuntime := fixture.CachedRuntime()
	if got := fileSHA256(t, cachedRuntime); got != runtimeChecksum {
		t.Fatalf("adopted cache checksum = %s, want %s", got, runtimeChecksum)
	}
	recording := readText(t, fixture.OutputPath)
	if !recordingContainsExecutable(recording, cachedRuntime) {
		t.Fatalf("adoption ran %q instead of the cached runtime %q", recording, cachedRuntime)
	}
}

// TestPOSIXRuntimeLauncherAdoptsHomeInstall verifies the documented home install location.
func TestPOSIXRuntimeLauncherAdoptsHomeInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	installDirectory := filepath.Join(fixture.Home, ".revyl", "bin")
	if err := os.MkdirAll(installDirectory, 0o700); err != nil {
		t.Fatalf("create home install directory: %v", err)
	}
	installedRuntime := writeRecordingRuntimeNamed(
		t,
		installDirectory,
		"revyl",
		fixture.OutputPath,
	)
	runtimeChecksum := fileSHA256(t, installedRuntime)
	fixture.PinRuntime(t, runtimeChecksum)
	writeFakeCurl(t, fixture.FakeCommands, true)

	runRuntimeLauncher(t, pluginRoot, fixture.Environment(t))

	if got := fileSHA256(t, fixture.CachedRuntime()); got != runtimeChecksum {
		t.Fatalf("home-adopted cache checksum = %s, want %s", got, runtimeChecksum)
	}
}

// TestPOSIXRuntimeLauncherIgnoresMismatchedLocalInstall protects the checksum trust boundary.
func TestPOSIXRuntimeLauncherIgnoresMismatchedLocalInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	// The pinned runtime is only reachable over the download path.
	pinnedRuntime := writeRecordingRuntimeNamed(
		t,
		t.TempDir(),
		"revyl",
		fixture.OutputPath,
	)
	runtimeChecksum := fileSHA256(t, pinnedRuntime)
	fixture.PinRuntime(t, runtimeChecksum)
	writeExecutable(
		t,
		fixture.FakeCommands,
		"revyl",
		"#!/bin/sh\nprintf 'not the pinned runtime\\n'\n",
	)
	writeFakeCurl(t, fixture.FakeCommands, false)

	runRuntimeLauncher(
		t,
		pluginRoot,
		fixture.Environment(t, "REVYL_RUNTIME_SOURCE="+pinnedRuntime),
	)

	cachedRuntime := fixture.CachedRuntime()
	if got := fileSHA256(t, cachedRuntime); got != runtimeChecksum {
		t.Fatalf("mismatched install was adopted: cache checksum = %s", got)
	}
	recording := readText(t, fixture.OutputPath)
	if !recordingContainsExecutable(recording, cachedRuntime) {
		t.Fatalf("launcher ran %q instead of the downloaded runtime %q", recording, cachedRuntime)
	}
}

// TestPOSIXRuntimeLauncherRetriesTransientDownloadFailures verifies the bounded retry budget.
func TestPOSIXRuntimeLauncherRetriesTransientDownloadFailures(t *testing.T) {
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
	counterPath := filepath.Join(t.TempDir(), "attempts.txt")
	writeFlakyCurl(t, fixture.FakeCommands, counterPath, 2)

	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")
	command.Env = fixture.Environment(t, "REVYL_RUNTIME_SOURCE="+sourceRuntime)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		t.Fatalf("retried runtime launch failed: %v\n%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("retry progress corrupted MCP stdout: %q", stdout.String())
	}
	if attempts := countLines(t, counterPath); attempts != 3 {
		t.Fatalf("download attempts = %d, want 3", attempts)
	}
	if !strings.Contains(stderr.String(), "retrying in") {
		t.Fatalf("retry progress missing from stderr: %q", stderr.String())
	}
	if got := fileSHA256(t, fixture.CachedRuntime()); got != runtimeChecksum {
		t.Fatalf("retried download cache checksum = %s, want %s", got, runtimeChecksum)
	}
}

// TestPOSIXRuntimeLauncherReportsExhaustedDownloadRetries verifies an observable terminal failure.
func TestPOSIXRuntimeLauncherReportsExhaustedDownloadRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher coverage")
	}

	pluginRoot := pluginRootPath(t)
	fixture := newAdoptionFixture(t)
	fixture.PinRuntime(t, strings.Repeat("a", 64))
	counterPath := filepath.Join(t.TempDir(), "attempts.txt")
	writeFlakyCurl(t, fixture.FakeCommands, counterPath, 99)

	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")
	command.Env = fixture.Environment(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err == nil {
		t.Fatal("exhausted download retries unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("exhausted retries corrupted MCP stdout: %q", stdout.String())
	}
	if attempts := countLines(t, counterPath); attempts != 3 {
		t.Fatalf("download attempts = %d, want 3", attempts)
	}
	for _, expected := range []string{"after 3 attempts", "REVYL_BINARY", "revyl-"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("terminal failure stderr %q does not mention %q", stderr.String(), expected)
		}
	}
	if _, err := os.Stat(fixture.CachedRuntime()); !os.IsNotExist(err) {
		t.Fatalf("failed download left a cached runtime: %v", err)
	}
}

// TestRuntimeLauncherScriptsShareResolutionOrder keeps the Windows launcher from drifting.
//
// The PowerShell bootstrap has no runner on POSIX CI, so its resolution order is pinned
// structurally against the POSIX launcher instead.
func TestRuntimeLauncherScriptsShareResolutionOrder(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	posixLauncher := readText(t, filepath.Join(pluginRoot, "hooks", "launch-revyl"))
	windowsLauncher := readText(t, filepath.Join(pluginRoot, "hooks", "launch-revyl.ps1"))

	for expected, content := range map[string]string{
		"resolve_installed_runtime": posixLauncher,
		"adopt_installed_runtime":   posixLauncher,
		"download_attempt":          posixLauncher,
		"DOWNLOAD_ATTEMPTS=3":       posixLauncher,
		"Resolve-InstalledRuntime":  windowsLauncher,
		"DownloadAttempts = 3":      windowsLauncher,
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("runtime launcher does not define %q", expected)
		}
	}

	for _, launcher := range []string{posixLauncher, windowsLauncher} {
		if !strings.Contains(launcher, "REVYL_BINARY") {
			t.Error("runtime launcher failure guidance does not name REVYL_BINARY")
		}
	}
	if strings.Contains(posixLauncher, "--tries=2") {
		t.Error("POSIX launcher lets wget duplicate the shell retry budget")
	}
}

// runtimeManifestAssets returns the fixed release matrix in manifest order.
func runtimeManifestAssets(manifest runtimeManifest) []runtimeAssetContract {
	return []runtimeAssetContract{
		{AssetName: manifest.DarwinAMD64Asset, SHA256: manifest.DarwinAMD64SHA256},
		{AssetName: manifest.DarwinARM64Asset, SHA256: manifest.DarwinARM64SHA256},
		{AssetName: manifest.LinuxAMD64Asset, SHA256: manifest.LinuxAMD64SHA256},
		{AssetName: manifest.LinuxARM64Asset, SHA256: manifest.LinuxARM64SHA256},
		{AssetName: manifest.WindowsAMD64Asset, SHA256: manifest.WindowsAMD64SHA256},
		{AssetName: manifest.WindowsARM64Asset, SHA256: manifest.WindowsARM64SHA256},
	}
}

// requireSHA256 verifies one lowercase SHA256 digest.
func requireSHA256(t *testing.T, digest string) {
	t.Helper()
	if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		t.Fatalf("invalid SHA256 digest %q", digest)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		t.Fatalf("invalid SHA256 digest %q: %v", digest, err)
	}
}

// runtimeLauncherCommandForTest returns the native launcher command.
func runtimeLauncherCommandForTest(pluginRoot string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		commandProcessor := os.Getenv("ComSpec")
		if commandProcessor == "" {
			commandProcessor = "cmd.exe"
		}
		return exec.Command(
			commandProcessor,
			"/d",
			"/c",
			filepath.Join(pluginRoot, "hooks", "launch-revyl.cmd"),
		)
	}
	return exec.Command(filepath.Join(pluginRoot, "hooks", "launch-revyl"))
}

// writeRecordingRuntime creates a fixture that records its executable and arguments.
func writeRecordingRuntime(t *testing.T, directory, outputPath string) string {
	t.Helper()
	return writeRecordingRuntimeNamed(t, directory, "revyl-fixture", outputPath)
}

// writeRecordingRuntimeNamed creates a recording fixture under an explicit file name.
//
// Adoption coverage needs a fixture discoverable as `revyl` on PATH, so the name is a
// parameter rather than the default fixture name.
func writeRecordingRuntimeNamed(t *testing.T, directory, name, outputPath string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		content := "@echo off\r\n" +
			"> \"%REVYL_RUNTIME_OUTPUT%\" echo executable=%REVYL_MCP_EXECUTABLE%\r\n" +
			">> \"%REVYL_RUNTIME_OUTPUT%\" echo args=%*\r\n" +
			"if defined REVYL_API_KEY (>> \"%REVYL_RUNTIME_OUTPUT%\" echo api_key_set=yes) else (>> \"%REVYL_RUNTIME_OUTPUT%\" echo api_key_set=no)\r\n"
		path := filepath.Join(directory, name+".cmd")
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatalf("write Windows recording runtime: %v", err)
		}
		return path
	}
	content := fmt.Sprintf(
		"#!/bin/sh\nprintf 'executable=%%s\\n' \"$REVYL_MCP_EXECUTABLE\" > %q\nprintf 'args=%%s\\n' \"$*\" >> %q\nif [ \"${REVYL_API_KEY+x}\" = x ]; then printf 'api_key_set=yes\\n' >> %q; else printf 'api_key_set=no\\n' >> %q; fi\n",
		outputPath,
		outputPath,
		outputPath,
		outputPath,
	)
	return writeExecutable(t, directory, name, content)
}

// writePreparedRuntimeManifest creates a current-platform manifest fixture.
func writePreparedRuntimeManifest(
	t *testing.T,
	directory string,
	checksum string,
) (string, string, string) {
	t.Helper()
	platform := runtime.GOOS + "_" + runtime.GOARCH
	assetName := "revyl-" + runtime.GOOS + "-" + runtime.GOARCH
	manifest := runtimeManifest{
		SchemaVersion:     1,
		GeneratedBy:       "make -C revyl-cli prepare-cursor-plugin-release",
		Prepared:          true,
		PluginVersion:     "1.2.3",
		RuntimeVersion:    "9.8.7",
		ReleaseTag:        "v9.8.7",
		ReleaseBaseURL:    "https://github.com/RevylAI/revyl-cli/releases/download/v9.8.7",
		DarwinAMD64Asset:  "revyl-darwin-amd64",
		DarwinARM64Asset:  "revyl-darwin-arm64",
		LinuxAMD64Asset:   "revyl-linux-amd64",
		LinuxARM64Asset:   "revyl-linux-arm64",
		WindowsAMD64Asset: "revyl-windows-amd64.exe",
		WindowsARM64Asset: "revyl-windows-arm64.exe",
	}
	switch platform {
	case "darwin_amd64":
		manifest.DarwinAMD64SHA256 = checksum
	case "darwin_arm64":
		manifest.DarwinARM64SHA256 = checksum
	case "linux_amd64":
		manifest.LinuxAMD64SHA256 = checksum
	case "linux_arm64":
		manifest.LinuxARM64SHA256 = checksum
	default:
		t.Fatalf("unsupported POSIX fixture platform: %s", platform)
	}
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode runtime manifest fixture: %v", err)
	}
	manifestPath := filepath.Join(directory, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, append(content, '\n'), 0o600); err != nil {
		t.Fatalf("write runtime manifest fixture: %v", err)
	}
	return manifestPath, platform, assetName
}

// writeUnpreparedRuntimeManifest creates a manifest fixture that pins no runtime.
//
// Release preparation flips the maintained manifest to prepared, so unavailable-runtime
// coverage owns an explicit fixture instead of depending on the shipped file's state.
//
// Returns:
//   - string: Path to the unprepared runtime manifest fixture.
func writeUnpreparedRuntimeManifest(t *testing.T, directory string) string {
	t.Helper()
	manifest := runtimeManifest{
		SchemaVersion:     1,
		GeneratedBy:       "make -C revyl-cli prepare-cursor-plugin-release",
		Prepared:          false,
		PluginVersion:     "0.0.0",
		RuntimeVersion:    "0.0.0",
		ReleaseTag:        "v0.0.0",
		ReleaseBaseURL:    "https://github.com/RevylAI/revyl-cli/releases/download/v0.0.0",
		DarwinAMD64Asset:  "revyl-darwin-amd64",
		DarwinARM64Asset:  "revyl-darwin-arm64",
		LinuxAMD64Asset:   "revyl-linux-amd64",
		LinuxARM64Asset:   "revyl-linux-arm64",
		WindowsAMD64Asset: "revyl-windows-amd64.exe",
		WindowsARM64Asset: "revyl-windows-arm64.exe",
	}
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode unprepared runtime manifest fixture: %v", err)
	}
	manifestPath := filepath.Join(directory, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, append(content, '\n'), 0o600); err != nil {
		t.Fatalf("write unprepared runtime manifest fixture: %v", err)
	}
	return manifestPath
}

// writeFakeCurl creates a deterministic downloader or an intentional failure.
func writeFakeCurl(t *testing.T, directory string, fail bool) {
	t.Helper()
	if fail {
		writeExecutable(t, directory, "curl", "#!/bin/sh\nexit 73\n")
		return
	}
	content := `#!/bin/sh
destination=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      destination=$2
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
[ -n "$destination" ] || exit 74
cp "$REVYL_RUNTIME_SOURCE" "$destination"
`
	writeExecutable(t, directory, "curl", content)
}

// writeFlakyCurl creates a downloader that fails a fixed number of times before succeeding.
//
// Each invocation appends one line to counterPath, so a test can assert the exact
// attempt count the launcher's retry budget produced.
func writeFlakyCurl(t *testing.T, directory, counterPath string, failures int) {
	t.Helper()
	content := fmt.Sprintf(`#!/bin/sh
printf 'attempt\n' >> %q
destination=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      destination=$2
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done
if [ "$(wc -l < %q)" -le %d ]; then
  printf 'curl: (22) The requested URL returned error: 500\n' >&2
  exit 22
fi
[ -n "$destination" ] || exit 74
cp "$REVYL_RUNTIME_SOURCE" "$destination"
`, counterPath, counterPath, failures)
	writeExecutable(t, directory, "curl", content)
}

// countLines returns how many lines one fixture counter file recorded.
func countLines(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read counter %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return 0
	}
	return len(strings.Split(trimmed, "\n"))
}

// runRuntimeLauncher executes the POSIX bootstrap and requires clean MCP stdout.
func runRuntimeLauncher(t *testing.T, pluginRoot string, environment []string) {
	t.Helper()
	command := runtimeLauncherCommandForTest(pluginRoot)
	command.Args = append(command.Args, "mcp", "serve", "--profile", "dev")
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime launcher failed: %v\n%s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("runtime launcher wrote to MCP output: %q", output)
	}
}

// fileSHA256 returns one fixture file's lowercase digest.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s for SHA256: %v", path, err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
