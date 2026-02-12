package util

import "testing"

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
