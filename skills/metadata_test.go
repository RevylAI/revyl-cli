package skills

import (
	"bytes"
	"strings"
	"testing"
)

func TestAuthBypassLoadsOnlyMatchingPlatformReference(t *testing.T) {
	for _, required := range []string{
		"## Shared Contract", "## Detect the App Stack", "Expo Router",
		"## Implement for the Detected Stack", "Read only the reference matching the detected app stack",
		"do not load unrelated platform recipes", "## Implementation Rules", "## Verification",
		"Do not commit real tokens", "Validate the token before changing app state",
		"Allowlist roles and redirects", "existing auth/session primitives", "accepted and rejected states",
		"separate from normal production login", "Make failure observable", "Production builds cannot activate",
		"REVYL_AUTH_BYPASS_ENABLED=true", "Unknown role is rejected", "Unknown redirect is rejected",
		"Disabled or missing launch-var gate is rejected", "Wrong token is rejected",
	} {
		if !strings.Contains(RevylCLIAuthBypassContent, required) {
			t.Errorf("auth-bypass root is missing shared instruction %q", required)
		}
	}
	platforms := []string{"expo", "react-native", "ios", "android", "flutter"}
	for _, platform := range platforms {
		reference := "references/" + platform + ".md"
		if !strings.Contains(RevylCLIAuthBypassContent, "("+reference+")") {
			t.Errorf("auth-bypass root must link to %s", reference)
		}
		content, err := packages.ReadFile(RevylCLIAuthBypassName + "/" + reference)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(content, []byte("shared contract, implementation rules, and verification")) {
			t.Errorf("%s must retain the shared safety contract", reference)
		}
		for _, other := range platforms {
			if other != platform && bytes.Contains(content, []byte(other+".md")) {
				t.Errorf("%s must not direct readers to unrelated recipe %s", reference, other)
			}
		}
	}
	for _, implementation := range []string{"### Expo", "### React Native", "### Native iOS", "### Native Android", "### Flutter", "func launchValue"} {
		if strings.Contains(RevylCLIAuthBypassContent, implementation) {
			t.Errorf("platform implementation %q belongs in its reference, not the root", implementation)
		}
	}
}
