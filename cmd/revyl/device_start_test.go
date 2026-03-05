package main

import (
	"strings"
	"testing"
)

func TestNormalizeDeviceStartPlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "defaults to ios when empty", input: "", want: "ios"},
		{name: "defaults to ios when whitespace", input: "   ", want: "ios"},
		{name: "accepts ios", input: "ios", want: "ios"},
		{name: "accepts ios uppercase", input: "IOS", want: "ios"},
		{name: "accepts android", input: "android", want: "android"},
		{name: "accepts android mixed case", input: "AnDrOiD", want: "android"},
		{name: "rejects invalid platform", input: "web", wantErr: "platform must be 'ios' or 'android'"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeDeviceStartPlatform(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizeDeviceStartPlatform(%q) error = nil, want %q", tt.input, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeDeviceStartPlatform(%q) error = %q, want contains %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("normalizeDeviceStartPlatform(%q) error = %v, want nil", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeDeviceStartPlatform(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDeviceStartPlatformFlagDefault(t *testing.T) {
	flag := deviceStartCmd.Flags().Lookup("platform")
	if flag == nil {
		t.Fatal("expected --platform flag on device start command")
	}
	if flag.DefValue != "ios" {
		t.Fatalf("device start --platform default = %q, want %q", flag.DefValue, "ios")
	}
	if strings.Contains(strings.ToLower(flag.Usage), "required") {
		t.Fatalf("device start --platform usage should not say required, got %q", flag.Usage)
	}
}
