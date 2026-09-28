package ui

import (
	"strings"
	"testing"
)

func TestGetCondensedHelpUsesCanonicalDocsURL(t *testing.T) {
	help := GetCondensedHelp()
	if !strings.Contains(help, DocsURL) {
		t.Fatalf("expected condensed help to contain docs URL %q", DocsURL)
	}
	if strings.Contains(help, "docs.revyl.com") {
		t.Fatal("expected condensed help to avoid legacy docs.revyl.com URL")
	}
}

func TestGetHelpTextUsesCanonicalDocsURL(t *testing.T) {
	help := GetHelpText()
	if !strings.Contains(help, DocsURL) {
		t.Fatalf("expected help text to contain docs URL %q", DocsURL)
	}
	if strings.Contains(help, "docs.revyl.com") {
		t.Fatal("expected help text to avoid legacy docs.revyl.com URL")
	}
}

func TestDisplayVersionPrefixesExactlyOneV(t *testing.T) {
	for version, want := range map[string]string{
		"v0.1.121":        "v0.1.121",
		"0.1.129":         "v0.1.129",
		" v1.2.3-beta.1 ": "v1.2.3-beta.1",
	} {
		if got := DisplayVersion(version); got != want {
			t.Errorf("DisplayVersion(%q) = %q, want %q", version, got, want)
		}
	}
}
