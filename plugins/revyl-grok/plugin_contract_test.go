package grokplugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	hostedWorkspaceServerName = "revyl-workspace"
	hostedWorkspaceServerURL  = "https://backend.revyl.ai/api/v1/mcp"
)

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return value
}

func TestManifestDeclaresOnlySkillsAndLogo(t *testing.T) {
	manifest := readJSON[map[string]any](t, filepath.Join(".grok-plugin", "plugin.json"))
	if manifest["name"] != "revyl" {
		t.Fatalf("plugin.json name = %v, want revyl", manifest["name"])
	}
	for _, key := range []string{"hooks", "commands", "agents", "mcpServers", "lspServers"} {
		if _, ok := manifest[key]; ok {
			t.Fatalf("plugin.json declares %q; the plugin ships only skills and .mcp.json", key)
		}
	}
	if _, err := os.Stat(manifest["logo"].(string)); err != nil {
		t.Fatalf("plugin.json logo: %v", err)
	}
	if _, err := os.Stat("hooks"); !os.IsNotExist(err) {
		t.Fatal("hooks/ exists; the Grok plugin must not run local commands")
	}
}

func TestMCPConfigNamesOnlyTheHostedWorkspaceServer(t *testing.T) {
	config := readJSON[map[string]map[string]map[string]string](t, ".mcp.json")
	servers := config["mcpServers"]
	want := map[string]string{"type": "http", "url": hostedWorkspaceServerURL}
	if len(config) != 1 || len(servers) != 1 || !mapsEqual(servers[hostedWorkspaceServerName], want) {
		t.Fatalf(".mcp.json = %#v, want only %s at %s", config, hostedWorkspaceServerName, hostedWorkspaceServerURL)
	}

	cursor := readJSON[map[string]map[string]map[string]string](t, filepath.Join("..", "..", "cursor-plugin", "mcp.json"))
	if cursor["mcpServers"][hostedWorkspaceServerName]["url"] != hostedWorkspaceServerURL {
		t.Fatalf("cursor-plugin/mcp.json hosted server differs from the Grok plugin's: %#v", cursor)
	}
}

func TestSkillUsesOnlyHostedTools(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("skills", "revyl-workspace", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	skill := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(skill, "---\nname: revyl-workspace\n") {
		t.Fatal("SKILL.md frontmatter must start with name: revyl-workspace")
	}
	hostedTools := []string{"list_apps", "open_revyl_workspace", "list_device_sessions", "start_device_session", "screenshot", "stop_device_session"}
	for _, tool := range hostedTools {
		if !strings.Contains(skill, "`"+tool) {
			t.Fatalf("SKILL.md does not cover %s", tool)
		}
	}
	for _, cliOnly := range []string{"revyl auth", "revyl dev", "manage_tests", "run_test"} {
		if strings.Contains(skill, cliOnly) {
			t.Fatalf("SKILL.md references %q, which the hosted server does not provide", cliOnly)
		}
	}
}

func mapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range right {
		if left[key] != value {
			return false
		}
	}
	return true
}
