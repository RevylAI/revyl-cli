package auth

import (
	"net/url"
	"testing"
)

func TestBrowserAuthGetAuthURLCarriesSignupAttribution(t *testing.T) {
	browserAuth := NewBrowserAuth(BrowserAuthConfig{
		AppURL:      "https://app.revyl.ai",
		Attribution: SignupAttribution{Command: SignupCommandInit, Version: "1.42.7"},
	})

	parsed, err := url.Parse(browserAuth.GetAuthURL(4567, "state-token"))
	if err != nil {
		t.Fatal(err)
	}

	assertQuery(t, parsed.Query(), map[string]string{
		"port":         "4567",
		"state":        "state-token",
		"utm_source":   "cli",
		"utm_medium":   SignupFlowBrowserLogin,
		"utm_campaign": SignupCommandInit,
		"utm_content":  "1.42.7",
	})
}

func TestWithSignupAttributionKeepsTheApprovalCode(t *testing.T) {
	approvalURL := WithSignupAttribution(
		"https://app.revyl.ai/cli/device?code=HTKD-4W2N",
		SignupFlowDeviceApproval,
		SignupAttribution{Command: SignupCommandAuthLogin, Version: "v1.42.7"},
	)

	parsed, err := url.Parse(approvalURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/cli/device" {
		t.Fatalf("path = %q, want /cli/device", parsed.Path)
	}
	assertQuery(t, parsed.Query(), map[string]string{
		"code":         "HTKD-4W2N",
		"utm_source":   "cli",
		"utm_medium":   SignupFlowDeviceApproval,
		"utm_campaign": SignupCommandAuthLogin,
		"utm_content":  "1.42.7",
	})
}

func TestWithSignupAttributionSendsUnreleasedVersionsAsDev(t *testing.T) {
	for _, version := range []string{"", "dev", "1.42.7-3-gabc1234-dirty", "1.42"} {
		parsed, err := url.Parse(WithSignupAttribution(
			"https://app.revyl.ai/cli/device?code=HTKD-4W2N",
			SignupFlowDeviceApproval,
			SignupAttribution{Command: SignupCommandAuthLogin, Version: version},
		))
		if err != nil {
			t.Fatal(err)
		}
		if got := parsed.Query().Get("utm_content"); got != "dev" {
			t.Fatalf("version %q: utm_content = %q, want dev", version, got)
		}
	}
}

func TestWithSignupAttributionKeepsParamsTheURLAlreadyCarries(t *testing.T) {
	parsed, err := url.Parse(WithSignupAttribution(
		"https://app.revyl.ai/cli/device?code=HTKD-4W2N&utm_source=cursor_plugin",
		SignupFlowDeviceApproval,
		SignupAttribution{Command: SignupCommandAuthLogin, Version: "1.42.7"},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("utm_source"); got != "cursor_plugin" {
		t.Fatalf("utm_source = %q, want cursor_plugin", got)
	}
}

func TestWithSignupAttributionLeavesAnUnusableURLAlone(t *testing.T) {
	for _, rawURL := range []string{"", "not a url", "%zz"} {
		if got := WithSignupAttribution(rawURL, SignupFlowDeviceApproval, SignupAttribution{}); got != rawURL {
			t.Fatalf("WithSignupAttribution(%q) = %q", rawURL, got)
		}
	}
}

func assertQuery(t *testing.T, query url.Values, want map[string]string) {
	t.Helper()
	for name, value := range want {
		if got := query.Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
}
