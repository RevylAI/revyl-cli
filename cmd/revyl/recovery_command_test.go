package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestCLIRecoveryCommandPreservesSelectedRoot(t *testing.T) {
	originalRoot, _ := rootCmd.PersistentFlags().GetString("chdir")
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("chdir", originalRoot)
	})
	_ = rootCmd.PersistentFlags().Set("chdir", "apps/My App")

	quotedRoot := "'apps/My App'"
	if runtime.GOOS == "windows" {
		quotedRoot = `"apps/My App"`
	}
	want := "revyl -C " + quotedRoot + " config migrate --check"
	if got := cliRecoveryCommand("config", "migrate", "--check"); got != want {
		t.Fatalf("recovery command = %q, want %q", got, want)
	}
}

func requireCLIRecoveryCommand(t *testing.T, got string, directory string, arguments ...string) {
	t.Helper()
	want := cliRecoveryCommandInDirectory(directory, arguments...)
	if got == want {
		return
	}

	prefix := "revyl -C "
	suffix := " " + strings.Join(arguments, " ")
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("recovery command = %q, want %q", got, want)
	}
	gotDirectory := strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix)
	if len(gotDirectory) >= 2 &&
		((gotDirectory[0] == '"' && gotDirectory[len(gotDirectory)-1] == '"') ||
			(gotDirectory[0] == '\'' && gotDirectory[len(gotDirectory)-1] == '\'')) {
		gotDirectory = gotDirectory[1 : len(gotDirectory)-1]
	}
	if got != cliRecoveryCommandInDirectory(gotDirectory, arguments...) {
		t.Fatalf("recovery command = %q, want a shell-safe command for %q", got, gotDirectory)
	}
	gotInfo, err := os.Stat(gotDirectory)
	if err != nil {
		t.Fatalf("stat recovery directory %q: %v", gotDirectory, err)
	}
	wantInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("stat expected recovery directory %q: %v", directory, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("recovery command directory = %q, want filesystem-equivalent %q", gotDirectory, directory)
	}
}
