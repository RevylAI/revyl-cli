package launcharguments

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		tokens  []string
		wantErr string
	}{
		{name: "unset"},
		{name: "ordered exact tokens", tokens: []string{"--mode", "value with spaces", "--mode", "  padded  ", "🧪"}},
		{name: "whitespace-only token", tokens: []string{"   "}},
		{name: "empty token", tokens: []string{"--mode", ""}, wantErr: "token 2 cannot be empty"},
		{name: "NUL token", tokens: []string{"before\x00after"}, wantErr: "token 1 cannot contain NUL bytes"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.tokens)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
