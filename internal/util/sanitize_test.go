package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeForFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "parentheses and spaces", input: "Login Test (iOS)", want: "login-test-ios"},
		{name: "parens no space", input: "my-test(v2)", want: "my-testv2"},
		{name: "brackets", input: "App [staging]", want: "app-staging"},
		{name: "leading trailing spaces", input: "  spaces  ", want: "spaces"},
		{name: "uppercase", input: "UPPERCASE", want: "uppercase"},
		{name: "already valid", input: "already-valid", want: "already-valid"},
		{name: "collapse hyphens", input: "a--b", want: "a-b"},
		{name: "empty string", input: "", want: ""},
		{name: "underscores preserved", input: "my_test_name", want: "my_test_name"},
		{name: "mixed special chars", input: "test!@#$%^&*name", want: "testname"},
		{name: "trailing hyphen after strip", input: "test-", want: "test"},
		{name: "leading hyphen after strip", input: "-test", want: "test"},
		{name: "only special chars", input: "()", want: ""},
		{name: "numbers", input: "test-123", want: "test-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeForFilename(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeForFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafeTestPath(t *testing.T) {
	testsDir := filepath.Join(os.TempDir(), "revyl-test-safepath")

	tests := []struct {
		name    string
		alias   string
		wantErr bool
	}{
		{name: "valid alias", alias: "login-flow", wantErr: false},
		{name: "valid with spaces", alias: "Login Test", wantErr: false},
		{name: "traversal attack", alias: "../../../etc/passwd", wantErr: false},
		{name: "dot-dot only", alias: "..", wantErr: true},
		{name: "empty after sanitize", alias: "!!!", wantErr: true},
		{name: "slash in name", alias: "foo/bar", wantErr: false},
		{name: "backslash in name", alias: `foo\bar`, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := SafeTestPath(testsDir, tt.alias)
			if tt.wantErr {
				if err == nil {
					t.Errorf("SafeTestPath(%q, %q) expected error, got path=%q", testsDir, tt.alias, path)
				}
				return
			}
			if err != nil {
				t.Errorf("SafeTestPath(%q, %q) unexpected error: %v", testsDir, tt.alias, err)
				return
			}
			absTests, _ := filepath.Abs(testsDir)
			absPath, _ := filepath.Abs(path)
			if len(absPath) <= len(absTests) || absPath[:len(absTests)] != absTests {
				t.Errorf("SafeTestPath(%q, %q) = %q escapes tests dir %q", testsDir, tt.alias, absPath, absTests)
			}
		})
	}
}
