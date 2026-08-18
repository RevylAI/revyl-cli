package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBumpScriptRejectsUnknownAndMissingArgs protects the --plugin flag parser.
func TestBumpScriptRejectsUnknownAndMissingArgs(t *testing.T) {
	requirePOSIXBump(t)
	script := bumpScriptPath(t)

	unknown, err := exec.Command(script, "patch", "--wat").CombinedOutput()
	if err == nil || !strings.Contains(string(unknown), "unknown argument") {
		t.Fatalf("unknown flag: err=%v output=%s", err, unknown)
	}

	missing, err := exec.Command(script).CombinedOutput()
	if err == nil || !strings.Contains(string(missing), "missing patch") {
		t.Fatalf("missing level: err=%v output=%s", err, missing)
	}

	combined, err := exec.Command(script, "patch", "minor").CombinedOutput()
	if err == nil || !strings.Contains(string(combined), "refuse combining") {
		t.Fatalf("combined levels: err=%v output=%s", err, combined)
	}
}

// TestBumpScriptDispatchesMakeTargets records CLI vs --plugin make invocations.
func TestBumpScriptDispatchesMakeTargets(t *testing.T) {
	requirePOSIXBump(t)
	script := bumpScriptPath(t)
	recordPath := filepath.Join(t.TempDir(), "make-args")
	fakeMake := writeFakeMake(t, recordPath)

	testCases := []struct {
		name string
		args []string
		want string
	}{
		{name: "cli patch", args: []string{"patch"}, want: "bump-patch"},
		{name: "plugin patch flag first", args: []string{"--plugin", "patch"}, want: "cursor-plugin-bump-patch"},
		{name: "plugin minor", args: []string{"minor", "--plugin"}, want: "cursor-plugin-bump-minor"},
		{name: "plugin major", args: []string{"major", "--plugin"}, want: "cursor-plugin-bump-major"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.Remove(recordPath); err != nil && !os.IsNotExist(err) {
				t.Fatalf("reset make record: %v", err)
			}
			command := exec.Command(script, testCase.args...)
			command.Env = append(os.Environ(), "MAKE="+fakeMake)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("bump %v: %v\n%s", testCase.args, err, output)
			}
			recorded, readErr := os.ReadFile(recordPath)
			if readErr != nil {
				t.Fatalf("read make record: %v", readErr)
			}
			if got := strings.TrimSpace(string(recorded)); got != testCase.want {
				t.Fatalf("make args = %q, want %q", got, testCase.want)
			}
		})
	}
}

// requirePOSIXBump skips Windows, where the bump wrapper is a POSIX shell script.
func requirePOSIXBump(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bump is a POSIX shell script")
	}
}

// bumpScriptPath returns the absolute path to scripts/bump.
func bumpScriptPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(installerPath(t)), "bump")
}

// writeFakeMake creates a make stand-in that records its arguments.
//
// Parameters:
//   - recordPath: File the fixture writes argv into.
//
// Returns:
//   - string: Absolute path to the executable fixture.
func writeFakeMake(t *testing.T, recordPath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "make")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >'" + recordPath + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake make: %v", err)
	}
	return path
}
