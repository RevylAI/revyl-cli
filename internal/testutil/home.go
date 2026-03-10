// Package testutil provides helpers shared by Revyl CLI tests.
package testutil

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// SetHomeDir configures HOME-style environment variables so os.UserHomeDir resolves to dir in tests.
//
// Parameters:
//   - t: Current test instance used for cleanup-aware environment changes.
//   - dir: Directory to expose as the test home directory.
func SetHomeDir(t *testing.T, dir string) {
	t.Helper()

	t.Setenv("HOME", dir)
	if runtime.GOOS != "windows" {
		return
	}

	t.Setenv("USERPROFILE", dir)

	volume := filepath.VolumeName(dir)
	if volume == "" {
		return
	}

	t.Setenv("HOMEDRIVE", volume)

	homePath := strings.TrimPrefix(dir, volume)
	if homePath == "" {
		homePath = string(filepath.Separator)
	}
	t.Setenv("HOMEPATH", homePath)
}
