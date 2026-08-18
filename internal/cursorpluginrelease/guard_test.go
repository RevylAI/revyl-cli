package cursorpluginrelease

import (
	"strings"
	"testing"
)

func TestComparePluginVersions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		left    string
		right   string
		want    int
		wantErr bool
	}{
		{name: "patch increase", left: "0.1.4", right: "0.1.3", want: 1},
		{name: "equal", left: "0.1.3", right: "0.1.3", want: 0},
		{name: "major decrease", left: "0.9.0", right: "1.0.0", want: -1},
		{name: "release after prerelease", left: "1.0.0", right: "1.0.0-rc.1", want: 1},
		{name: "prerelease before release", left: "1.0.0-rc.1", right: "1.0.0", want: -1},
		{name: "prerelease order", left: "1.0.0-rc.2", right: "1.0.0-rc.1", want: 1},
		{name: "invalid", left: "nope", right: "0.1.0", wantErr: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := ComparePluginVersions(testCase.left, testCase.right)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("ComparePluginVersions() error = nil, want invalid version")
				}
				return
			}
			if err != nil {
				t.Fatalf("ComparePluginVersions() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("ComparePluginVersions(%q, %q) = %d, want %d", testCase.left, testCase.right, got, testCase.want)
			}
		})
	}
}

func TestIsPluginOwnedPath(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		path string
		want bool
	}{
		{path: "revyl-cli/cursor-plugin/hooks/ensure-revyl", want: true},
		{path: "revyl-cli/cursor-plugin/skills/revyl-cli-dev-loop/SKILL.md", want: true},
		{path: "revyl-cli/cursor-plugin/.cursor-plugin/plugin.json", want: true},
		{path: "revyl-cli/cursor-plugin/runtime-manifest.json", want: true},
		{path: "revyl-cli/.cursor-plugin/marketplace.json", want: true},
		{path: "revyl-cli/cursor-plugin/README.md", want: false},
		{path: "revyl-cli/cursor-plugin/install-local.sh", want: false},
		{path: "revyl-cli/cursor-plugin/plugin_contract_test.go", want: false},
		{path: "revyl-cli/cmd/revyl/main.go", want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()
			if got := IsPluginOwnedPath(testCase.path); got != testCase.want {
				t.Fatalf("IsPluginOwnedPath(%q) = %v, want %v", testCase.path, got, testCase.want)
			}
		})
	}
}

func TestEvaluateGuard(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		input        GuardInput
		wantExitZero bool
		wantOutput   string
	}{
		{
			name: "hook change without bump",
			input: GuardInput{
				ChangedFiles: "revyl-cli/cursor-plugin/hooks/ensure-revyl\n",
				BaseVersion:  "0.1.3",
				HeadVersion:  "0.1.3",
			},
			wantOutput: "./scripts/bump patch --plugin",
		},
		{
			name: "hook change with bump",
			input: GuardInput{
				ChangedFiles: "revyl-cli/cursor-plugin/hooks/ensure-revyl\nrevyl-cli/cursor-plugin/.cursor-plugin/plugin.json\n",
				BaseVersion:  "0.1.3",
				HeadVersion:  "0.1.4",
			},
			wantExitZero: true,
			wantOutput:   "Plugin version increased",
		},
		{
			name: "readme only",
			input: GuardInput{
				ChangedFiles: "revyl-cli/cursor-plugin/README.md\n",
				BaseVersion:  "0.1.3",
				HeadVersion:  "0.1.3",
			},
			wantExitZero: true,
			wantOutput:   "No plugin-owned files changed.",
		},
		{
			name: "test file only",
			input: GuardInput{
				ChangedFiles: "revyl-cli/cursor-plugin/plugin_contract_test.go\n",
				BaseVersion:  "0.1.3",
				HeadVersion:  "0.1.3",
			},
			wantExitZero: true,
			wantOutput:   "No plugin-owned files changed.",
		},
		{
			name: "no-plugin-release label",
			input: GuardInput{
				ChangedFiles: "revyl-cli/cursor-plugin/hooks/ensure-revyl\n",
				BaseVersion:  "0.1.3",
				HeadVersion:  "0.1.3",
				LabelsJSON:   `["no-plugin-release"]`,
			},
			wantExitZero: true,
			wantOutput:   "no-plugin-release",
		},
		{
			name: "invalid labels",
			input: GuardInput{
				ChangedFiles: "revyl-cli/cursor-plugin/hooks/ensure-revyl\n",
				BaseVersion:  "0.1.3",
				HeadVersion:  "0.1.3",
				LabelsJSON:   "{",
			},
			wantOutput: "PR_LABELS must be a JSON array",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := EvaluateGuard(testCase.input)
			if testCase.wantExitZero {
				if result.ExitCode != 0 {
					t.Fatalf("EvaluateGuard() exit = %d stdout=%s stderr=%s", result.ExitCode, result.Stdout, result.Stderr)
				}
			} else if result.ExitCode == 0 {
				t.Fatalf("EvaluateGuard() should fail, stdout=%s", result.Stdout)
			}
			combined := result.Stdout + result.Stderr
			if !strings.Contains(combined, testCase.wantOutput) {
				t.Fatalf("output = %s, want %q", combined, testCase.wantOutput)
			}
		})
	}
}

func TestNextPluginVersion(t *testing.T) {
	t.Parallel()

	got, err := NextPluginVersion("0.1.2", PluginBumpMinor)
	if err != nil {
		t.Fatalf("NextPluginVersion() error = %v", err)
	}
	if got != "0.2.0" {
		t.Fatalf("NextPluginVersion(minor) = %q, want 0.2.0", got)
	}
	got, err = NextPluginVersion("0.1.2", "")
	if err != nil {
		t.Fatalf("NextPluginVersion(default) error = %v", err)
	}
	if got != "0.1.3" {
		t.Fatalf("NextPluginVersion(default) = %q, want 0.1.3", got)
	}
}
