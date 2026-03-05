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
