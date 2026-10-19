package testutil

import (
	"os"
	"strings"
)

// ClearCIEnvironment removes the GitHub Actions and Buildkite variables a test
// binary inherits from its runner, so CI detection sees only what a test sets.
func ClearCIEnvironment() {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "GITHUB_") && !strings.HasPrefix(key, "BUILDKITE") {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			panic("clear " + key + ": " + err.Error())
		}
	}
}
