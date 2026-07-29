package beforesession

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveLoadClearSessionValues(t *testing.T) {
	root := t.TempDir()

	if err := SaveSessionValues(root, "sess-1", map[string]string{
		"REVYL_AUTH_BYPASS_TOKEN": "tok-boot",
	}); err != nil {
		t.Fatalf("SaveSessionValues() error = %v", err)
	}

	got, err := LoadSessionValues(root, "sess-1")
	if err != nil {
		t.Fatalf("LoadSessionValues() error = %v", err)
	}
	if got["REVYL_AUTH_BYPASS_TOKEN"] != "tok-boot" {
		t.Fatalf("LoadSessionValues() = %#v, want tok-boot", got)
	}

	info, err := os.Stat(filepath.Join(root, ".revyl", valuesFileName))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %04o, want 0600", info.Mode().Perm())
	}

	if err := ClearSessionValues(root, "sess-1"); err != nil {
		t.Fatalf("ClearSessionValues() error = %v", err)
	}
	got, err = LoadSessionValues(root, "sess-1")
	if err != nil {
		t.Fatalf("LoadSessionValues() after clear error = %v", err)
	}
	if got != nil {
		t.Fatalf("LoadSessionValues() after clear = %#v, want nil", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".revyl", valuesFileName)); !os.IsNotExist(err) {
		t.Fatalf("values file still present after clearing last session: %v", err)
	}
}

func TestSaveSessionValuesIsolatesSessions(t *testing.T) {
	root := t.TempDir()
	if err := SaveSessionValues(root, "sess-a", map[string]string{"TOKEN": "a"}); err != nil {
		t.Fatalf("SaveSessionValues(a) error = %v", err)
	}
	if err := SaveSessionValues(root, "sess-b", map[string]string{"TOKEN": "b"}); err != nil {
		t.Fatalf("SaveSessionValues(b) error = %v", err)
	}

	a, err := LoadSessionValues(root, "sess-a")
	if err != nil {
		t.Fatalf("LoadSessionValues(a) error = %v", err)
	}
	b, err := LoadSessionValues(root, "sess-b")
	if err != nil {
		t.Fatalf("LoadSessionValues(b) error = %v", err)
	}
	if a["TOKEN"] != "a" || b["TOKEN"] != "b" {
		t.Fatalf("session isolation broken: a=%#v b=%#v", a, b)
	}

	if err := ClearSessionValues(root, "sess-a"); err != nil {
		t.Fatalf("ClearSessionValues(a) error = %v", err)
	}
	b, err = LoadSessionValues(root, "sess-b")
	if err != nil || b["TOKEN"] != "b" {
		t.Fatalf("clearing sess-a affected sess-b: %#v err=%v", b, err)
	}
}

func TestLoadSessionValuesMissingFile(t *testing.T) {
	got, err := LoadSessionValues(t.TempDir(), "missing")
	if err != nil {
		t.Fatalf("LoadSessionValues() error = %v", err)
	}
	if got != nil {
		t.Fatalf("LoadSessionValues() = %#v, want nil", got)
	}
}
