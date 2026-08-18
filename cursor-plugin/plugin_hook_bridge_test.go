package cursorplugin_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHookBridgesInjectedAPIKey verifies sessionStart persists a real key and never prints it.
func TestHookBridgesInjectedAPIKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recording fixture is a POSIX shell script")
	}
	hookPath := filepath.Join(pluginRootPath(t), "hooks", "ensure-revyl")

	testCases := []struct {
		name          string
		apiKey        string
		wantBridgeRun bool
		wantContext   string
	}{
		{name: "injected key", apiKey: testSecret(), wantBridgeRun: true},
		{name: "absent key", apiKey: "", wantContext: loginGuidance},
		{name: "unresolved placeholder", apiKey: "${env:REVYL_API_KEY}", wantContext: loginGuidance},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fakeBin := t.TempDir()
			recordingPath := filepath.Join(t.TempDir(), "bridge-invocation")
			selectedBinary := writeRecordingCLI(t, fakeBin, recordingPath)
			environment := environmentWithOverrides(
				"PATH="+fakeBin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
				"REVYL_BINARY="+selectedBinary,
				"REVYL_API_KEY="+testCase.apiKey,
			)

			output := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, environment)
			if testCase.wantContext == "" {
				requireHookOutput(t, output, map[string]string{})
			} else {
				requireHookOutput(t, output, map[string]string{"additional_context": testCase.wantContext})
			}
			requireNoSecret(t, output)

			recording, err := os.ReadFile(recordingPath)
			if !testCase.wantBridgeRun {
				if err == nil {
					t.Fatalf("hook bridged without a usable key: %s", recording)
				}
				return
			}
			if err != nil {
				t.Fatalf("hook did not run the credential bridge: %v", err)
			}
			if got := string(recording); !strings.Contains(got, "arguments=auth persist-cloud-env") {
				t.Fatalf("bridge invocation = %q, want the persist-cloud-env subcommand", got)
			}
			if strings.Contains(string(recording), "arguments="+testSecret()) ||
				strings.Contains(string(recording), " "+testSecret()) {
				t.Fatal("hook passed the API key as a command argument")
			}
			if !strings.Contains(string(recording), "api_key_reached_bridge=yes") {
				t.Fatal("hook did not pass the API key to the bridge through the environment")
			}
		})
	}
}

// TestHookBeforeShellInstallsWithoutRequiredKey verifies the matcher may download, then asks for login.
func TestHookBeforeShellInstallsWithoutRequiredKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recording fixture is a POSIX shell script")
	}
	hookPath := filepath.Join(pluginRootPath(t), "hooks", "ensure-revyl")
	fakeBin := t.TempDir()
	recordingPath := filepath.Join(t.TempDir(), "bridge-invocation")
	selectedBinary := writeRecordingCLI(t, fakeBin, recordingPath)
	environment := environmentWithOverrides(
		"PATH="+fakeBin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
		"REVYL_BINARY="+selectedBinary,
		"REVYL_API_KEY=",
	)

	output := runHook(t, hookPath, `{"hook_event_name":"beforeShellExecution","command":"revyl dev"}`, environment)
	requireHookOutput(t, output, map[string]string{
		"permission":    "allow",
		"agent_message": loginGuidance,
	})
	requireNoSecret(t, output)

	recording, err := os.ReadFile(recordingPath)
	if err != nil {
		t.Fatalf("beforeShellExecution did not invoke the pinned runtime: %v", err)
	}
	if !strings.Contains(string(recording), "arguments=version") {
		t.Fatalf("beforeShellExecution invocation = %q, want version to populate the cache", recording)
	}
}

// TestHookRuntimeBehavior executes the native hook and verifies fail-open prerequisite reporting.
func TestHookRuntimeBehavior(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	hookPath := filepath.Join(pluginRoot, "hooks", "ensure-revyl")
	if runtime.GOOS == "windows" {
		hookPath += ".cmd"
	}

	isolatedPath := t.TempDir()
	if runtime.GOOS != "windows" {
		isolatedPath += string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	}
	unpreparedPluginRoot := t.TempDir()
	writeUnpreparedRuntimeManifest(t, unpreparedPluginRoot)
	missingEnvironment := environmentWithOverrides(
		"PATH="+isolatedPath,
		"REVYL_BINARY=",
		"CURSOR_PLUGIN_ROOT="+unpreparedPluginRoot,
		"REVYL_API_KEY="+testSecret(),
	)
	missingSession := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, missingEnvironment)
	requireHookOutput(t, missingSession, map[string]string{"additional_context": runtimeUnavailableMessage})
	requireNoSecret(t, missingSession)

	missingShell := runHook(t, hookPath, `{"hook_event_name":"beforeShellExecution","command":"revyl dev"}`, missingEnvironment)
	requireHookOutput(t, missingShell, map[string]string{
		"permission":    "allow",
		"agent_message": runtimeUnavailableMessage,
	})
	requireNoSecret(t, missingShell)

	if runtime.GOOS != "windows" {
		preparedPluginRoot := t.TempDir()
		writePreparedRuntimeManifest(t, preparedPluginRoot, strings.Repeat("a", 64))
		preparedEnvironment := environmentWithOverrides(
			"PATH="+isolatedPath,
			"REVYL_BINARY=",
			"CURSOR_PLUGIN_ROOT="+preparedPluginRoot,
			"REVYL_API_KEY="+testSecret(),
		)
		preparedSession := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, preparedEnvironment)
		requireHookOutput(t, preparedSession, map[string]string{"additional_context": installOnFirstCommand})
		requireNoSecret(t, preparedSession)

		preparedShell := runHook(t, hookPath, `{"hook_event_name":"beforeShellExecution","command":"revyl dev"}`, preparedEnvironment)
		requireHookOutput(t, preparedShell, map[string]string{
			"permission":    "allow",
			"agent_message": runtimeUnavailableMessage,
		})
		requireNoSecret(t, preparedShell)

		preparedNoKey := environmentWithOverrides(
			"PATH="+isolatedPath,
			"REVYL_BINARY=",
			"CURSOR_PLUGIN_ROOT="+preparedPluginRoot,
			"REVYL_API_KEY=",
		)
		noKeySession := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, preparedNoKey)
		requireHookOutput(t, noKeySession, map[string]string{"additional_context": loginGuidance})
	}

	fakeBin := t.TempDir()
	fakeCLIPath := fakeBin
	if runtime.GOOS != "windows" {
		fakeCLIPath += string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
	}
	selectedBinary := writeFakeCLI(t, fakeBin)
	installedEnvironment := environmentWithOverrides(
		"PATH="+fakeCLIPath,
		"REVYL_BINARY="+selectedBinary,
		"REVYL_API_KEY="+testSecret(),
	)
	installedSession := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, installedEnvironment)
	requireHookOutput(t, installedSession, map[string]string{})
	installedShell := runHook(t, hookPath, `{"hook_event_name":"beforeShellExecution"}`, installedEnvironment)
	requireHookOutput(t, installedShell, map[string]string{"permission": "allow"})

	missingKeyEnvironment := environmentWithOverrides(
		"PATH="+fakeCLIPath,
		"REVYL_BINARY="+selectedBinary,
		"REVYL_API_KEY=",
	)
	missingKeySession := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, missingKeyEnvironment)
	requireHookOutput(t, missingKeySession, map[string]string{"additional_context": loginGuidance})
	missingKeyShell := runHook(t, hookPath, `{"hook_event_name":"beforeShellExecution"}`, missingKeyEnvironment)
	requireHookOutput(t, missingKeyShell, map[string]string{
		"permission":    "allow",
		"agent_message": loginGuidance,
	})

	malformed := runHook(t, hookPath, `not-json`, installedEnvironment)
	requireHookOutput(t, malformed, map[string]string{})
}

// TestHookIgnoresLiteralBinaryInterpolation treats Cloud's unexpanded REVYL_BINARY as unset.
func TestHookIgnoresLiteralBinaryInterpolation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the unprepared fixture uses POSIX hook semantics")
	}
	hookPath := filepath.Join(pluginRootPath(t), "hooks", "ensure-revyl")
	unpreparedPluginRoot := t.TempDir()
	writeUnpreparedRuntimeManifest(t, unpreparedPluginRoot)
	environment := environmentWithOverrides(
		"PATH="+t.TempDir()+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
		"REVYL_BINARY=${env:REVYL_BINARY}",
		"CURSOR_PLUGIN_ROOT="+unpreparedPluginRoot,
		"REVYL_API_KEY=",
	)
	output := runHook(t, hookPath, `{"hook_event_name":"sessionStart"}`, environment)
	requireHookOutput(t, output, map[string]string{"additional_context": runtimeUnavailableMessage})
}

// writeRecordingCLI creates a revyl fixture that records how the hook invoked it.
//
// Parameters:
//   - directory: Directory that becomes the fixture's PATH entry.
//   - recordingPath: File the fixture writes its invocation record to.
//
// Returns:
//   - string: Absolute path to the executable fixture.
func writeRecordingCLI(t *testing.T, directory, recordingPath string) string {
	t.Helper()
	content := "#!/bin/sh\n" +
		"{\n" +
		"  printf 'arguments=%s\\n' \"$*\"\n" +
		"  if [ \"${REVYL_API_KEY:-}\" = '" + testSecret() + "' ]; then\n" +
		"    printf 'api_key_reached_bridge=yes\\n'\n" +
		"  else\n" +
		"    printf 'api_key_reached_bridge=no\\n'\n" +
		"  fi\n" +
		"} >'" + recordingPath + "'\n" +
		"exit 0\n"
	return writeExecutable(t, directory, "revyl", content)
}
