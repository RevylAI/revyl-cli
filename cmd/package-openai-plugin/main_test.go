package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func writeFixtureFile(t *testing.T, root, name string, content []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func writeFixtureJSON(t *testing.T, root, name string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, name, content, 0644)
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range bundledPaths {
		mode := os.FileMode(0644)
		if name == "scripts/launch-revyl" || name == "scripts/launch-runtime" {
			mode = 0755
		}
		writeFixtureFile(t, root, "plugins/revyl/"+name, []byte("fixture "+name+"\n"), mode)
	}
	writeFixtureFile(t, root, "plugins/revyl/runtime-version", []byte("0.1.108\n"), 0644)
	manifest := map[string]any{
		"schema_version": 1, "prepared": true,
		"generated_by":   "make -C revyl-cli sync-codex-plugin",
		"plugin_version": "0.1.0", "runtime_version": "0.1.108", "release_tag": "v0.1.108",
		"release_base_url": "https://github.com/RevylAI/revyl-cli/releases/download/v0.1.108",
	}
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"} {
		asset := "revyl-" + strings.ReplaceAll(platform, "_", "-")
		if strings.HasPrefix(platform, "windows_") {
			asset += ".exe"
		}
		manifest[platform+"_asset"] = asset
		manifest[platform+"_sha256"] = strings.Repeat("a", 64)
	}
	writeFixtureJSON(t, root, "plugins/revyl/runtime-manifest.json", manifest)
	writeFixtureJSON(t, root, "plugins/revyl-openai/plugin.json", map[string]any{
		"$schema": pluginSchema, "name": "revyl-workspace", "version": "0.1.0", "description": "Combined Revyl plugin",
		"extensions": map[string]any{"com.openai": map[string]any{
			"interface": map[string]any{
				"displayName": "Revyl", "composerIcon": "./assets/icon.png", "logo": "./assets/icon.png",
			},
			"review": map[string]any{"commerce": false},
		}},
	})
	writeFixtureFile(t, root, "plugins/revyl-openai/assets/icon.png", []byte("fixture PNG"), 0644)
	writeFixtureFile(t, root, "plugins/revyl-openai/skills/revyl-workspace/SKILL.md", []byte("---\nname: revyl-workspace\n---\nUse Revyl.\n"), 0644)
	writeFixtureFile(t, root, "plugins/revyl-openai/skills/revyl-cloud-app/SKILL.md", []byte("---\nname: revyl-cloud-app\n---\nBuild and explore with Revyl.\n"), 0644)
	return root
}

func readFixtureJSON(t *testing.T, root, name string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func readArchive(t *testing.T, path string) (map[string][]byte, []*zip.File) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	contents := make(map[string][]byte)
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name], err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return contents, archive.File
}

func TestMaintainedPackageIncludesCloudWorkflow(t *testing.T) {
	root := filepath.Join("..", "..")
	out := filepath.Join(t.TempDir(), "plugin.zip")
	if err := run(root, []string{"--out", out, "--registered-app-id", "asdk_app_fixture"}); err != nil {
		t.Fatal(err)
	}
	contents, _ := readArchive(t, out)
	for _, name := range []string{"revyl-cloud-app", "revyl-workspace", "revyl-codex-dev-loop", "revyl-codex-proof-ci"} {
		path := "skills/" + name + "/SKILL.md"
		source := "plugins/revyl/"
		if name == "revyl-cloud-app" || name == "revyl-workspace" {
			source = "plugins/revyl-openai/"
		}
		original, err := os.ReadFile(filepath.Join(root, source, path))
		frontmatter := bytes.ReplaceAll(original, []byte("\r\n"), []byte("\n"))
		if err != nil || !bytes.Equal(original, contents[path]) || !bytes.Contains(frontmatter, []byte("\nname: "+name+"\n")) {
			t.Fatalf("missing or changed maintained skill %s: %v", name, err)
		}
	}
}

func TestPackageRoundTrip(t *testing.T) {
	for _, mode := range []struct {
		name, flag, value, connection string
	}{
		{"registered", "--registered-app-id", "asdk_app_fixture-123", ".app.json"},
		{"browser_id", "--registered-app-id", "plugin_asdk_app_fixture-123", ".app.json"},
		{"public", "--mcp-url", "https://mcp.example.com/mcp", "mcp.json"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			root := fixture(t)
			for _, name := range []string{".env", ".revyl/credentials.json", "plugins/revyl/.env", "plugins/revyl/.codex-plugin/plugin.json", "plugins/revyl/plugin_test.go", "plugins/revyl/scripts/secret", "plugins/revyl/skills/extra/SKILL.md", "plugins/revyl/.app.json", "plugins/revyl/hooks/hooks.json", "plugins/revyl-openai/.env", "plugins/revyl-openai/.app.json"} {
				writeFixtureFile(t, root, name, []byte("NEVER_PACKAGE_THIS_FIXTURE"), 0600)
			}
			out := filepath.Join(t.TempDir(), "plugin.zip")
			if err := run(root, []string{"--out", out, mode.flag, mode.value}); err != nil {
				t.Fatal(err)
			}
			contents, entries := readArchive(t, out)
			expected := []string{"assets/icon.png", "references/revyl-cli-dev-loop.md", "runtime-manifest.json", "runtime-version", "scripts/launch-revyl", "scripts/launch-revyl.cmd", "scripts/launch-revyl.ps1", "scripts/launch-runtime", "scripts/launch-runtime.ps1", "skills/revyl-codex-dev-loop/SKILL.md", "skills/revyl-codex-proof-ci/SKILL.md", "skills/revyl-workspace/SKILL.md", "skills/revyl-cloud-app/SKILL.md", "plugin.json", mode.connection}
			slices.Sort(expected)
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name)
				if !entry.Mode().IsRegular() || !entry.Modified.Equal(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
					t.Fatalf("unexpected ZIP metadata: %s, %v, %v", entry.Name, entry.Mode(), entry.Modified)
				}
				if bytes.Contains(contents[entry.Name], []byte("NEVER_PACKAGE_THIS_FIXTURE")) {
					t.Fatalf("unexpected file content in %s", entry.Name)
				}
				want := os.FileMode(0644)
				if entry.Name == "scripts/launch-revyl" || entry.Name == "scripts/launch-runtime" {
					want = 0755
				}
				if entry.Mode().Perm() != want {
					t.Errorf("%s mode = %v, want %v", entry.Name, entry.Mode().Perm(), want)
				}
			}
			if !slices.Equal(names, expected) {
				t.Fatalf("ZIP entries = %v, want %v", names, expected)
			}
			for _, name := range bundledPaths {
				original, err := os.ReadFile(filepath.Join(root, "plugins/revyl", filepath.FromSlash(name)))
				if err != nil || !bytes.Equal(original, contents[name]) {
					t.Fatalf("bundled source changed: %s (%v)", name, err)
				}
			}
			var manifest map[string]any
			if err := json.Unmarshal(contents["plugin.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			openAI := manifest["extensions"].(map[string]any)["com.openai"].(map[string]any)
			if manifest["$schema"] != pluginSchema || manifest["name"] != "revyl-workspace" || openAI["interface"].(map[string]any)["displayName"] != "Revyl" {
				t.Fatalf("invalid manifest: %v", manifest)
			}
			_, hasReview := openAI["review"]
			if hasReview != (mode.name == "public") {
				t.Fatal("public review metadata must be present only with the declared public MCP server")
			}
			var connection map[string]any
			if err := json.Unmarshal(contents[mode.connection], &connection); err != nil {
				t.Fatal(err)
			}
			if mode.connection == ".app.json" {
				if openAI["apps"] != "./.app.json" || len(connection) != 1 {
					t.Fatalf("invalid private connection: %v, %v", openAI, connection)
				}
				apps := connection["apps"].(map[string]any)
				app := apps["revyl"].(map[string]any)
				if len(apps) != 1 || len(app) != 1 || app["id"] != "asdk_app_fixture-123" {
					t.Fatalf("incorrect registered server mapping: %v", apps)
				}
			} else {
				if _, exists := openAI["apps"]; exists || len(connection) != 2 || connection["$schema"] != mcpSchema {
					t.Fatalf("invalid public connection: %v, %v", openAI, connection)
				}
				servers := connection["mcpServers"].(map[string]any)
				server := servers["revyl"].(map[string]any)
				if len(servers) != 1 || len(server) != 2 || server["type"] != "streamable-http" || server["url"] != mode.value {
					t.Fatalf("invalid MCP server: %v", servers)
				}
			}
		})
	}
}

func TestDeterministicOutputAndNoClobber(t *testing.T) {
	root := fixture(t)
	directory := t.TempDir()
	first := filepath.Join(directory, "first.zip")
	second := filepath.Join(directory, "second.zip")
	args := []string{"--registered-app-id", "asdk_app_fixture", "--out", first}
	if err := run(root, args); err != nil {
		t.Fatal(err)
	}
	for _, name := range bundledPaths {
		if err := os.Chtimes(filepath.Join(root, "plugins/revyl", filepath.FromSlash(name)), time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	args[len(args)-1] = second
	if err := run(root, args); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("output is not deterministic: %v", err)
	}
	if err := run(root, args); err == nil {
		t.Fatal("existing output overwritten")
	}
	b, err = os.ReadFile(second)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("existing output changed: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary artifacts left behind: %v, %v", entries, err)
	}
}

func TestArchiveIgnoresHostPermissions(t *testing.T) {
	root := fixture(t)
	var previous []byte
	for _, mode := range []os.FileMode{0600, 0777} {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			return os.Chmod(path, mode)
		}); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "plugin.zip")
		if err := run(root, []string{"--out", out, "--registered-app-id", "asdk_app_fixture"}); err != nil {
			t.Fatal(err)
		}
		_, entries := readArchive(t, out)
		for _, entry := range entries {
			want := os.FileMode(0644)
			if entry.Name == "scripts/launch-revyl" || entry.Name == "scripts/launch-runtime" {
				want = 0755
			}
			if entry.Mode().Perm() != want {
				t.Errorf("host mode %04o: %s mode = %04o, want %04o", mode, entry.Name, entry.Mode().Perm(), want)
			}
		}
		content, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && !bytes.Equal(previous, content) {
			t.Fatal("archive bytes depend on host permissions")
		}
		previous = content
	}
}

func TestOpenAIAttributesSurvivePackageExport(t *testing.T) {
	attributes, err := os.ReadFile(filepath.Join("..", "..", ".gitattributes"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), attributes, 0600); err != nil {
		t.Fatal(err)
	}
	runGit := func(input []byte, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = root
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return output
	}
	runGit(nil, "init", "--quiet")
	for _, name := range []string{"plugin.json", "skills/revyl-workspace/SKILL.md", "skills/revyl-cloud-app/SKILL.md", "assets/icon.png"} {
		t.Run(name, func(t *testing.T) {
			content := []byte("first line\nsecond line\n")
			if strings.HasSuffix(name, ".png") {
				content = []byte("\x89PNG\r\n\x1a\nfixture\r\npixels\n")
			}
			objectID := strings.TrimSpace(string(runGit(content, "-c", "core.autocrlf=true", "hash-object", "-w", "--stdin", "--path=plugins/revyl-openai/"+name)))
			checkedOut := runGit(nil, "-c", "core.autocrlf=true", "cat-file", "--filters", "--path=plugins/revyl-openai/"+name, objectID)
			if !bytes.Equal(checkedOut, content) {
				t.Fatalf("autocrlf changed %s: %q", name, checkedOut)
			}
		})
	}
}

func TestWorkspaceBridgeMakeAndCIContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Makefile validation requires POSIX tooling")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "make", "--no-print-directory", "-n", "test")
	command.Dir = filepath.Join("..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n test: %v\n%s", err, output)
	}
	for _, required := range []string{"node --test internal/mcp/workspace_app.test.cjs", "./scripts/go-test-summary.sh ./..."} {
		if !strings.Contains(string(output), required) {
			t.Errorf("make test does not run %q: %s", required, output)
		}
	}
	workflowPath := filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml")
	workflow, err := os.ReadFile(workflowPath)
	if os.IsNotExist(err) {
		t.Skip("monorepo CI workflow is not present in the exported CLI package")
	}
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses             string
				Run              string
				WorkingDirectory string `yaml:"working-directory"`
				With             map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(workflow, &config); err != nil {
		t.Fatal(err)
	}
	nodeReady, runsTests, checksDocs := false, false, false
	for _, step := range config.Jobs["cli-tests"].Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-node@") && step.With["node-version"] != "" {
			nodeReady = true
		}
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			paths := strings.Fields(step.With["sparse-checkout"])
			checksDocs = slices.Contains(paths, "cognisim-docs/cli") && slices.Contains(paths, ".github/workflows")
		}
		if step.Run == "make test" && step.WorkingDirectory == "revyl-cli" {
			runsTests = nodeReady
		}
	}
	if !runsTests || !checksDocs {
		t.Fatalf("CLI CI must set up Node, run make test, and check out documentation and CI contracts: tests=%v docs=%v", runsTests, checksDocs)
	}
}

func TestConcurrentPublicationHasOneWinner(t *testing.T) {
	root := fixture(t)
	directory := t.TempDir()
	out := filepath.Join(directory, "plugin.zip")
	var group sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		group.Go(func() { results <- run(root, []string{"--out", out, "--registered-app-id", "asdk_app_fixture"}) })
	}
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("successful publications = %d, want 1", winners)
	}
	readArchive(t, out)
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary artifacts left behind: %v, %v", entries, err)
	}
}

func TestExistingPrivatePackageIdentity(t *testing.T) {
	root := fixture(t)
	out := filepath.Join(t.TempDir(), "private-update.zip")
	if err := run(root, []string{"--out", out, "--registered-app-id", "asdk_app_fixture", "--package-name", "dev-existing-plugin"}); err != nil {
		t.Fatal(err)
	}
	files, _ := readArchive(t, out)
	var manifest map[string]any
	if err := json.Unmarshal(files["plugin.json"], &manifest); err != nil || manifest["name"] != "dev-existing-plugin" {
		t.Fatal("private update must preserve the explicitly selected package identity")
	}
	for _, args := range [][]string{
		{"--mcp-url", "https://mcp.example.com/mcp", "--package-name", "dev-existing-plugin"},
		{"--registered-app-id", "asdk_app_fixture", "--package-name", "../secret"},
		{"--registered-app-id", "asdk_app_fixture", "--package-name", strings.Repeat("a", 65)},
	} {
		if err := run(root, append([]string{"--out", filepath.Join(t.TempDir(), "invalid.zip")}, args...)); err == nil {
			t.Fatal("unsafe or public package identity override was accepted")
		}
	}
}

func TestRejectInvalidFlagsWithoutEchoingValues(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--out", "plugin.zip"},
		{"--registered-app-id", "asdk_app_fixture"},
		{"--out", "plugin.zip", "--registered-app-id", "asdk_app_fixture", "--mcp-url", "https://example.com/mcp"},
		{"--out", "plugin.zip", "--registered-app-id", "asdk_app_fixture", "--mcp-url", ""},
		{"--out", "plugin.zip", "--registered-app-id", "asdk_app_fixture", "extra"},
		{"--out", "plugin.zip", "--unknown=TEST_SECRET"},
	} {
		if err := run(t.TempDir(), args); err == nil || strings.Contains(err.Error(), "TEST_SECRET") {
			t.Fatalf("flags should fail safely: %v", err)
		}
	}
	for _, id := range []string{"TEST_SECRET", "asdk_app_", "plugin_asdk_app_", "asdk_app_-TEST_SECRET", "asdk_app_.TEST_SECRET", "asdk_app_TEST_SECRET/x", "asdk_app_TEST_SECRET?token=secret", "asdk_app_TEST_SECRET\n", " asdk_app_TEST_SECRET", "asdk_app_é", "connector_TEST_SECRET", "plugin_plugin_asdk_app_TEST_SECRET"} {
		if err := run(t.TempDir(), []string{"--out", "plugin.zip", "--registered-app-id", id}); err == nil || strings.Contains(err.Error(), "TEST_SECRET") {
			t.Fatalf("ID should fail safely: %v", err)
		}
	}
}

func TestMCPURLSafety(t *testing.T) {
	for _, value := range []string{
		"http://mcp.example.com/mcp", "https:///mcp", "https:mcp.example.com", "https://user:TEST_SECRET@mcp.example.com/mcp",
		"https://mcp.example.com/mcp?key=TEST_SECRET", "https://mcp.example.com/mcp?", "https://mcp.example.com/mcp#TEST_SECRET", "https://mcp.example.com/mcp#",
		"https://localhost/mcp", "https://sub.localhost/mcp", "https://localhost./mcp", "https://127.0.0.1/mcp", "https://10.0.0.1/mcp", "https://8.8.8.8/mcp", "https://[::1]/mcp", "https://[::ffff:127.0.0.1]/mcp", "https://127.1/mcp", "https://2130706433/mcp", "https://0x7f000001/mcp",
		"https://test.ngrok.io/mcp", "https://test.ngrok-free.app/mcp", "https://test.ngrok.dev/mcp", "https://test.trycloudflare.com/mcp", "https://test.loca.lt/mcp", "https://test.devtunnels.ms/mcp", "https://test.ts.net/mcp", "https://127.0.0.1.nip.io/mcp", "https://TUNNELS.example.com./mcp", "https://dev.example.com/mcp", "https://hr-fixture.revyl.ai/mcp",
		"https://preview.capysandbox.net/mcp", "https://api.openai.com/v1/tunnel/tunnel_fixture", "https://api.openai.com/v1/tunnels/tunnel_fixture",
		"https://mcp.internal/mcp", "https://mcp.local/mcp", "https://mcp.home.arpa/mcp", "https://mcp.example.com:0/mcp", "https://mcp.example.com:65536/mcp", "https://mcp.example.com:abc/mcp", "https://mcp.example.com:/mcp", "https://mcp.example.com/TEST_SECRET\n", "https://mcp.example.com/%0aTEST_SECRET", "https://mcp.example.com/%00TEST_SECRET", "https://mcp.example.com\\@evil.com/mcp", "https://-bad.example.com/mcp", "https://bad..example.com/mcp",
	} {
		t.Run(value, func(t *testing.T) {
			err := validateMCPURL(value)
			if err == nil || strings.Contains(err.Error(), "TEST_SECRET") {
				t.Fatalf("URL should fail safely: %v", err)
			}
		})
	}
	for _, value := range []string{"https://mcp.example.com/mcp", "https://mcp.example.com:8443/mcp", "https://mcp.example.com/v1/mcp", "https://mcp.example.com./mcp"} {
		if err := validateMCPURL(value); err != nil {
			t.Fatalf("public URL rejected: %v", err)
		}
	}
}

func TestRejectMissingAndUnpreparedRuntime(t *testing.T) {
	for name, value := range map[string]any{
		"schema_version": 2, "prepared": false, "generated_by": "unprepared", "plugin_version": "", "runtime_version": "0.1.109", "release_tag": "latest", "release_base_url": "https://example.com/TEST_SECRET", "windows_arm64_sha256": "", "darwin_amd64_sha256": "not-a-digest", "linux_arm64_asset": "../../TEST_SECRET", "extra": "TEST_SECRET",
	} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			manifest := readFixtureJSON(t, root, "plugins/revyl/runtime-manifest.json")
			manifest[name] = value
			writeFixtureJSON(t, root, "plugins/revyl/runtime-manifest.json", manifest)
			assertFailedWithoutOutput(t, root)
		})
	}
	for _, name := range []string{"runtime-version", "runtime-manifest.json", "scripts/launch-runtime", "skills/revyl-codex-dev-loop/SKILL.md"} {
		t.Run("missing_"+name, func(t *testing.T) {
			root := fixture(t)
			if err := os.Remove(filepath.Join(root, "plugins/revyl", filepath.FromSlash(name))); err != nil {
				t.Fatal(err)
			}
			assertFailedWithoutOutput(t, root)
		})
	}
}

func assertFailedWithoutOutput(t *testing.T, root string) {
	t.Helper()
	directory := t.TempDir()
	err := run(root, []string{"--out", filepath.Join(directory, "plugin.zip"), "--registered-app-id", "asdk_app_fixture"})
	if err == nil || strings.Contains(err.Error(), "TEST_SECRET") {
		t.Fatalf("expected safe packaging failure: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed packaging left artifacts: %v, %v", entries, err)
	}
}

func TestRejectOverlayHooksAndUnbundledPaths(t *testing.T) {
	for _, field := range []string{"hooks", "apps", "mcpServers", "skills", "composerIcon", "logo", "screenshots", "$schema", "extensions"} {
		t.Run(field, func(t *testing.T) {
			root := fixture(t)
			manifest := readFixtureJSON(t, root, "plugins/revyl-openai/plugin.json")
			openAI := manifest["extensions"].(map[string]any)["com.openai"].(map[string]any)
			switch field {
			case "hooks", "apps", "mcpServers":
				openAI[field] = "../../TEST_SECRET"
			case "composerIcon", "logo", "screenshots":
				openAI["interface"].(map[string]any)[field] = "../../TEST_SECRET"
			default:
				manifest[field] = "../../TEST_SECRET"
			}
			writeFixtureJSON(t, root, "plugins/revyl-openai/plugin.json", manifest)
			assertFailedWithoutOutput(t, root)
		})
	}
}

func TestRejectSourceSymlinksAndSpecialFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require Windows developer mode")
	}
	for _, name := range []string{"plugins/revyl/scripts/launch-revyl", "plugins/revyl/scripts", "plugins/revyl-openai/plugin.json", "plugins/revyl-openai/skills/revyl-workspace", "plugins/revyl-openai/skills/revyl-cloud-app"} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			path := filepath.Join(root, filepath.FromSlash(name))
			target := filepath.Join(t.TempDir(), "target")
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			assertFailedWithoutOutput(t, root)
		})
	}
	root := fixture(t)
	path := filepath.Join(root, "plugins/revyl/runtime-manifest.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	assertFailedWithoutOutput(t, root)
}

func TestRejectOutputSymlinkAndMissingDirectory(t *testing.T) {
	root := fixture(t)
	directory := t.TempDir()
	if err := run(root, []string{"--out", filepath.Join(directory, "missing/plugin.zip"), "--registered-app-id", "asdk_app_fixture"}); err == nil {
		t.Fatal("missing output directory accepted")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require Windows developer mode")
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(directory, "plugin.zip")
	if err := os.Symlink(target, out); err != nil {
		t.Fatal(err)
	}
	if err := run(root, []string{"--out", out, "--registered-app-id", "asdk_app_fixture"}); err == nil {
		t.Fatal("output symlink overwritten")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "untouched" {
		t.Fatalf("symlink target modified: %v", err)
	}
}
