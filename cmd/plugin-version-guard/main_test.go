package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPluginVersionGuardLabelHatch covers the no-plugin-release opt-out through the command.
func TestPluginVersionGuardLabelHatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the guard command test execs the compiled binary")
	}
	binary := filepath.Join(t.TempDir(), "plugin-version-guard")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = commandDirectory(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build guard: %v\n%s", err, output)
	}

	command := exec.Command(binary)
	command.Env = append(
		os.Environ(),
		"CHANGED_FILES=revyl-cli/cursor-plugin/hooks/ensure-revyl\n",
		"BASE_PLUGIN_VERSION=0.1.3",
		"HEAD_PLUGIN_VERSION=0.1.3",
		`PR_LABELS=["no-plugin-release"]`,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("guard with hatch: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "no-plugin-release") {
		t.Fatalf("output = %s, want no-plugin-release", output)
	}
}

// commandDirectory returns this command's source directory.
func commandDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}
