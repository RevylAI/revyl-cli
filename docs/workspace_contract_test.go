package docs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExperimentalWorkspaceDocsDiscovery(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "](experimental-workspace.md)") {
		t.Fatal("CLI docs index must link the experimental workspace guide")
	}
	guide, err := os.ReadFile("experimental-workspace.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(guide), "--experimental-workspace") {
		t.Fatal("experimental workspace guide must document the opt-in flag")
	}
}

func TestPublishedWorkspaceCommandReference(t *testing.T) {
	path := filepath.Join("..", "..", "cognisim-docs", "cli", "command-reference.mdx")
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("published command reference is not present in the exported CLI package")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"revyl mcp serve --profile core --experimental-workspace",
		"opt-in",
		"compatible MCP Apps hosts",
	} {
		if !strings.Contains(string(content), required) {
			t.Errorf("published command reference must include %q", required)
		}
	}
}
