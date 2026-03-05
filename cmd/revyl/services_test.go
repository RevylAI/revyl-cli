package main

import (
	"path/filepath"
	"testing"
)

func TestWritePIDFileReturnsErrorOnInvalidPath(t *testing.T) {
	invalidPath := filepath.Join(t.TempDir(), "missing", ".services.pid")

	err := writePIDFile(invalidPath, []string{"frontend"}, []int{1234})
	if err == nil {
		t.Fatal("expected writePIDFile to return an error for an invalid path")
	}
}

func TestAppendPIDFileReturnsErrorOnInvalidPath(t *testing.T) {
	invalidPath := filepath.Join(t.TempDir(), "missing", ".services.pid")

	err := appendPIDFile(invalidPath, []string{"backend"}, []int{4321})
	if err == nil {
		t.Fatal("expected appendPIDFile to return an error for an invalid path")
	}
}
