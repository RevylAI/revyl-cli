package testutil

import (
	"io/fs"
	"runtime"
	"testing"
)

func AssertPOSIXPermissions(t *testing.T, info fs.FileInfo, want fs.FileMode) {
	t.Helper()
	if runtime.GOOS != "windows" && info.Mode().Perm() != want {
		t.Fatalf("%s permissions = %o, want %o", info.Name(), info.Mode().Perm(), want)
	}
}
