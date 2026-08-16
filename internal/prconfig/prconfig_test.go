package prconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPushContentReturnsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	want := "project:\n  name: demo\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := ReadPushContent(path)
	if err != nil {
		t.Fatalf("ReadPushContent() error = %v", err)
	}
	if got != want {
		t.Fatalf("ReadPushContent() = %q, want %q", got, want)
	}
}

func TestReadPushContentMissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")

	got, err := ReadPushContent(path)
	if err != nil {
		t.Fatalf("ReadPushContent() error = %v", err)
	}
	if got != "" {
		t.Fatalf("ReadPushContent() = %q, want empty string", got)
	}
}

func TestPushOutcomeMessage(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{status: "none", want: "No file-managed pr_review config on acme/mobile; settings stay UI-managed. Run 'revyl github init' to scaffold."},
		{status: "managed", want: "Applied pr_review config to acme/mobile"},
		{status: "", want: "Applied pr_review config to acme/mobile"},
	}
	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			got := PushOutcomeMessage("acme", "mobile", tc.status)
			if got != tc.want {
				t.Fatalf("PushOutcomeMessage(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}
