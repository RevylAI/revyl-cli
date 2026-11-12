package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCommittedAssetsAreCurrent(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	if err := syncPlugin(root, true, releaseClient(t, filepath.Join(root, "plugins/revyl/runtime-manifest.json"))); err != nil {
		t.Fatal(err)
	}
}

func TestSyncPluginChecksDriftWithoutWriting(t *testing.T) {
	root := syncFixture(t)
	client := releaseClient(t, "")
	if err := syncPlugin(root, false, client); err != nil {
		t.Fatal(err)
	}
	if err := syncPlugin(root, true, client); err != nil {
		t.Fatal(err)
	}
	generatedPath := filepath.Join(root, "plugins/revyl/scripts/launch-runtime")
	if err := os.WriteFile(generatedPath, []byte("stale"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := syncPlugin(root, true, client); err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("check error = %v, want drift", err)
	}
	content, err := os.ReadFile(generatedPath)
	if err != nil || string(content) != "stale" {
		t.Fatalf("check modified stale asset: %q, %v", content, err)
	}
	if err := syncPlugin(root, false, client); err != nil {
		t.Fatal(err)
	}
	if err := syncPlugin(root, true, client); err != nil {
		t.Fatal(err)
	}
}

func TestSyncPluginDerivesClaudeManifestFromCodexManifest(t *testing.T) {
	root := syncFixture(t)
	codexPath := filepath.Join(root, "plugins/revyl/.codex-plugin/plugin.json")
	codexManifest := `{"name":"revyl","version":"0.3.1","description":"Run apps.","license":"MIT","skills":"./skills/","interface":{"displayName":"Revyl","privacyPolicyURL":"https://example.com/privacy"}}`
	if err := os.WriteFile(codexPath, []byte(codexManifest), 0644); err != nil {
		t.Fatal(err)
	}
	client := releaseClient(t, "")
	if err := syncPlugin(root, false, client); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(root, "plugins/revyl/.claude-plugin/plugin.json")
	var claude map[string]any
	content, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &claude); err != nil {
		t.Fatal(err)
	}
	var userConfig any
	if err := json.Unmarshal([]byte(claudeUserConfig), &userConfig); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "revyl-cli", "displayName": "Revyl CLI", "version": "0.3.1", "description": "Run apps.", "license": "MIT", "skills": "./skills/", "privacyPolicyUrl": "https://example.com/privacy", "userConfig": userConfig}
	if fmt.Sprint(claude) != fmt.Sprint(want) {
		t.Fatalf("Claude manifest = %v, want %v", claude, want)
	}
	if err := os.WriteFile(claudePath, []byte(strings.Replace(string(content), "0.3.1", "0.3.0", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncPlugin(root, true, client); err == nil || !strings.Contains(err.Error(), ".claude-plugin") {
		t.Fatalf("check error = %v, want Claude manifest drift", err)
	}
}

func TestSyncPluginRejectsIncompatibleRuntime(t *testing.T) {
	root := syncFixture(t)
	if err := os.WriteFile(filepath.Join(root, "plugins/revyl/runtime-version"), []byte("0.1.89"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncPlugin(root, false, releaseClient(t, "")); err == nil || !strings.Contains(err.Error(), ">= 0.1.108") {
		t.Fatalf("error = %v, want incompatible runtime", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins/revyl/runtime-manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("generated a runtime on failure: %v", err)
	}
}

func TestSyncPluginPortsSkillsThroughTheLauncher(t *testing.T) {
	root := syncFixture(t)
	if err := syncPlugin(root, false, releaseClient(t, "")); err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(root, "plugins/revyl/skills", path))
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	skill := read("revyl-cli-auth-bypass/SKILL.md")
	if !strings.Contains(skill, "# Auth bypass\n\n## Run Revyl through this plugin\n") {
		t.Errorf("SKILL.md lacks the launcher section after its title:\n%s", skill)
	}
	if !strings.Contains(skill, "```bash\n\"$LAUNCHER\" dev --no-build \\\n  --launch-var revyl-auth\n") || !strings.Contains(skill, "Run `revyl dev`.") {
		t.Errorf("SKILL.md fences must call the launcher and prose must stay as written:\n%s", skill)
	}
	if web := read("revyl-cli-auth-bypass/references/web.md"); !strings.Contains(web, "cd app && \"$LAUNCHER\" device start") || strings.Contains(web, "## Run Revyl") {
		t.Errorf("references/web.md = %q", web)
	}
	if policy := read("revyl-cli-auth-bypass/agents/openai.yaml"); policy != "policy:\n  allow_implicit_invocation: false\n" {
		t.Errorf("agents/openai.yaml = %q, want a verbatim copy", policy)
	}
	if !strings.Contains(read("revyl-ci-sync/SKILL.md"), "## Run Revyl through this plugin") {
		t.Error("revyl-ci-sync is not ported")
	}
}

func TestSyncPluginRemovesPortedFilesWhoseSourceIsGone(t *testing.T) {
	root := syncFixture(t)
	client := releaseClient(t, "")
	if err := syncPlugin(root, false, client); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "skills/revyl-cli-auth-bypass/references/web.md")); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "plugins/revyl/skills/revyl-cli-auth-bypass/references/web.md")
	if err := syncPlugin(root, true, client); err == nil || !strings.Contains(err.Error(), "is obsolete") {
		t.Fatalf("check error = %v, want the removed reference reported", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("check removed the stale copy: %v", err)
	}
	if err := syncPlugin(root, false, client); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("sync kept the stale copy: %v", err)
	}
	if err := syncPlugin(root, true, client); err != nil {
		t.Fatal(err)
	}
}

func syncFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, contents := range map[string]string{
		"plugins/revyl/.codex-plugin/plugin.json":         `{"name":"revyl","version":"0.1.0"}`,
		"plugins/revyl/runtime-version":                   "0.1.108\n",
		"cursor-plugin/hooks/launch-revyl":                "#!/bin/sh\nexit 0\n",
		"cursor-plugin/hooks/launch-revyl.ps1":            "exit 0\n",
		"cursor-plugin/hooks/launch-revyl.cmd":            "@echo off\n",
		"cursor-plugin/assets/icon.svg":                   "<svg/>\n",
		"skills/revyl-cli-dev-loop/SKILL.md":              "# Dev loop\n",
		"skills/revyl-cli-auth-bypass/SKILL.md":           "---\nname: revyl-cli-auth-bypass\n---\n\n# Auth bypass\n\nRun `revyl dev`.\n\n```bash\nrevyl dev --no-build \\\n  --launch-var revyl-auth\n```\n",
		"skills/revyl-cli-auth-bypass/references/web.md":  "# Web\n\n```bash\ncd app && revyl device start --platform web\n```\n",
		"skills/revyl-cli-auth-bypass/agents/openai.yaml": "policy:\n  allow_implicit_invocation: false\n",
		"cursor-plugin/skills/revyl-ci-sync/SKILL.md":     "# CI sync\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(contents), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type releaseTransport struct {
	checksums string
}

func (r releaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodHead {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}
	if !strings.HasSuffix(request.URL.Path, "/checksums.txt") {
		return nil, fmt.Errorf("unexpected request: %s", request.URL.Path)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(r.checksums)), Header: make(http.Header)}, nil
}

func releaseClient(t *testing.T, manifestPath string) *http.Client {
	t.Helper()
	manifest := map[string]string{}
	if manifestPath != "" {
		content, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(content, &values); err != nil {
			t.Fatal(err)
		}
		for key, value := range values {
			if strings.HasSuffix(key, "_sha256") {
				var checksum string
				if err := json.Unmarshal(value, &checksum); err != nil {
					t.Fatal(err)
				}
				manifest[key] = checksum
			}
		}
	}
	var checksums strings.Builder
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"} {
		checksum := strings.Repeat("a", 64)
		if manifestPath != "" {
			checksum = manifest[platform+"_sha256"]
		}
		asset := "revyl-" + strings.ReplaceAll(platform, "_", "-")
		if strings.HasPrefix(platform, "windows") {
			asset += ".exe"
		}
		fmt.Fprintf(&checksums, "%s  %s\n", checksum, asset)
	}
	return &http.Client{Timeout: time.Second, Transport: releaseTransport{checksums: checksums.String()}}
}
