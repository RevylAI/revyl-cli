//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDevStatusFileAllowsAtomicReplacementWhileOpen(t *testing.T) {
	statusPath := filepath.Join(t.TempDir(), ".dev-status.json")
	if err := os.WriteFile(statusPath, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	openSnapshot, err := openDevStatusFile(statusPath)
	if err != nil {
		t.Fatalf("openDevStatusFile() error = %v", err)
	}
	defer openSnapshot.Close()

	if err := writeDevStatusFile(statusPath, []byte("new"), 0644); err != nil {
		t.Fatalf("writeDevStatusFile() with active reader error = %v", err)
	}

	oldData, err := io.ReadAll(openSnapshot)
	if err != nil {
		t.Fatalf("reading open snapshot: %v", err)
	}
	if string(oldData) != "old" {
		t.Fatalf("open snapshot contents = %q, want old", oldData)
	}

	newData, err := readDevStatusFile(statusPath)
	if err != nil {
		t.Fatalf("readDevStatusFile() error = %v", err)
	}
	if string(newData) != "new" {
		t.Fatalf("replacement contents = %q, want new", newData)
	}
}
