package main

import "testing"

func TestDetectInstallMethodFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		execPath string
		expected string
	}{
		{
			name:     "homebrew cellar path",
			execPath: "/opt/homebrew/Cellar/revyl/0.1.0/bin/revyl",
			expected: "homebrew",
		},
		{
			name:     "npm global path",
			execPath: "/usr/local/lib/node_modules/@revyl/cli/bin/revyl",
			expected: "npm",
		},
		{
			name:     "pip site-packages path",
			execPath: "/opt/venv/lib/python3.12/site-packages/revyl/bin/revyl",
			expected: "pip",
		},
		{
			name:     "pip dist-packages path",
			execPath: "/usr/lib/python3/dist-packages/revyl/bin/revyl",
			expected: "pip",
		},
		{
			name:     "downloaded binary in revyl home",
			execPath: "/Users/alice/.revyl/bin/revyl-darwin-arm64",
			expected: "direct",
		},
		{
			name:     "default direct path",
			execPath: "/usr/local/bin/revyl",
			expected: "direct",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := detectInstallMethodFromPath(tc.execPath)
			if actual != tc.expected {
				t.Fatalf("detectInstallMethodFromPath(%q) = %q, want %q", tc.execPath, actual, tc.expected)
			}
		})
	}
}
