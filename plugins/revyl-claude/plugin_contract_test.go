package claudeplugin_test

import (
	"encoding/json"
	"maps"
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

func TestManifestDeclaresNoLocalComponents(t *testing.T) {
	manifest := readJSON[map[string]any](t, filepath.Join(".claude-plugin", "plugin.json"))
	if manifest["name"] != "revyl" {
		t.Fatalf("plugin.json name = %v, want revyl", manifest["name"])
	}
	for _, key := range []string{"hooks", "commands", "agents", "mcpServers", "lspServers", "userConfig"} {
		if _, ok := manifest[key]; ok {
			t.Fatalf("plugin.json declares %q; the plugin ships only skills and .mcp.json", key)
		}
	}
	for _, dir := range []string{"hooks", "commands", "agents"} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s/ exists; the Claude plugin must not run local commands", dir)
		}
	}
}

func TestMCPConfigMatchesTheGrokPlugin(t *testing.T) {
	config := readJSON[map[string]map[string]map[string]string](t, ".mcp.json")
	servers := config["mcpServers"]
	want := map[string]string{"type": "http", "url": hostedWorkspaceServerURL}
	if len(config) != 1 || len(servers) != 1 || !maps.Equal(servers[hostedWorkspaceServerName], want) {
		t.Fatalf(".mcp.json = %#v, want only %s at %s", config, hostedWorkspaceServerName, hostedWorkspaceServerURL)
	}
	grok := readJSON[map[string]map[string]map[string]string](t, filepath.Join("..", "revyl-grok", ".mcp.json"))
	if !maps.Equal(grok["mcpServers"][hostedWorkspaceServerName], want) {
		t.Fatalf("revyl-grok/.mcp.json hosted server differs from the Claude plugin's: %#v", grok)
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
	for _, tool := range []string{"list_apps", "open_revyl_workspace", "list_device_sessions", "start_device_session", "screenshot", "stop_device_session"} {
		if !strings.Contains(skill, "`"+tool) {
			t.Fatalf("SKILL.md does not cover %s", tool)
		}
	}
	// Claude does not show the embedded views, so the browser links are the
	// only way to see Atlas or a live device.
	for _, link := range []string{"`workspace_url`", "`viewer_url`"} {
		if !strings.Contains(skill, link) {
			t.Fatalf("SKILL.md does not give the user %s", link)
		}
	}
	for _, cliOnly := range []string{"revyl auth", "revyl dev", "manage_tests", "run_test"} {
		if strings.Contains(skill, cliOnly) {
			t.Fatalf("SKILL.md references %q, which the hosted server does not provide", cliOnly)
		}
	}
}
