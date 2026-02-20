package build

import (
	"strings"
	"testing"
)

func TestParseEASError_InvalidNpxInvocation(t *testing.T) {
	stderr := `npm error could not determine executable to run
npm error A complete log of this run can be found in: /tmp/npm.log
npm ERR! npm exec eas build --platform ios`

	err := parseEASError(stderr)
	if err == nil {
		t.Fatalf("parseEASError() returned nil")
	}
	if err.Message != "invalid npx eas invocation" {
		t.Fatalf("Message = %q, want %q", err.Message, "invalid npx eas invocation")
	}
	if !strings.Contains(err.Guidance, "npx --yes eas-cli build ...") {
		t.Fatalf("Guidance = %q, expected npx --yes eas-cli guidance", err.Guidance)
	}
}

func TestParseEASError_LoginRequiredFromNonInteractivePrompt(t *testing.T) {
	stderr := `An Expo user account is required to proceed.

Log in to EAS with email or username (exit and run eas login --help to see other login options)
Input is required, but stdin is not readable. Failed to display prompt: Email or username`

	err := parseEASError(stderr)
	if err == nil {
		t.Fatalf("parseEASError() returned nil")
	}
	if err.Message != "Not logged in to EAS" {
		t.Fatalf("Message = %q, want %q", err.Message, "Not logged in to EAS")
	}
	if !strings.Contains(err.Guidance, "npx --yes eas-cli login") {
		t.Fatalf("Guidance = %q, expected npx --yes eas-cli login guidance", err.Guidance)
	}
}
