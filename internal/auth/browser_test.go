package auth

import (
	"net/url"
	"testing"
)

func TestBrowserAuthGetAuthURLIncludesClientIdentity(t *testing.T) {
	auth := NewBrowserAuth(BrowserAuthConfig{
		AppURL:           "https://app.revyl.ai",
		ClientInstanceID: "client-123",
		DeviceLabel:      "Work Mac",
	})

	rawURL := auth.GetAuthURL(4567, "state-token")
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("GetAuthURL() returned invalid URL: %v", err)
	}

	query := parsed.Query()
	if parsed.Path != "/cli/auth" {
		t.Fatalf("path = %q, want %q", parsed.Path, "/cli/auth")
	}
	if got := query.Get("port"); got != "4567" {
		t.Fatalf("port query = %q, want %q", got, "4567")
	}
	if got := query.Get("state"); got != "state-token" {
		t.Fatalf("state query = %q, want %q", got, "state-token")
	}
	if got := query.Get("client_instance_id"); got != "client-123" {
		t.Fatalf("client_instance_id query = %q, want %q", got, "client-123")
	}
	if got := query.Get("device_label"); got != "Work Mac" {
		t.Fatalf("device_label query = %q, want %q", got, "Work Mac")
	}
}
