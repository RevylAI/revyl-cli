package revylplugin_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/revyl/cli/internal/cursorpluginrelease"
)

func pluginRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate plugin")
	}
	return filepath.Dir(filename)
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestCodexManifestAndMarketplace(t *testing.T) {
	root := pluginRoot(t)
	var manifest struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		Skills     string `json:"skills"`
		MCPServers any    `json:"mcpServers"`
		Hooks      any    `json:"hooks"`
		Apps       any    `json:"apps"`
		Interface  struct {
			DisplayName      string   `json:"displayName"`
			ShortDescription string   `json:"shortDescription"`
			ComposerIcon     string   `json:"composerIcon"`
			Logo             string   `json:"logo"`
			Prompts          []string `json:"defaultPrompt"`
		} `json:"interface"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(root, ".codex-plugin/plugin.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "revyl" || manifest.Version == "" || manifest.Skills != "./skills/" || manifest.Interface.DisplayName != "Revyl" || len(manifest.Interface.Prompts) != 2 {
		t.Fatalf("invalid plugin manifest: %+v", manifest)
	}
	if manifest.MCPServers != nil || manifest.Hooks != nil || manifest.Apps != nil {
		t.Fatal("Codex plugin must not require MCP, apps, or hooks")
	}
	shortDescription := manifest.Interface.ShortDescription
	if strings.TrimSpace(shortDescription) == "" || utf8.RuneCountInString(shortDescription) > 30 || strings.ContainsAny(shortDescription, "\r\n") {
		t.Fatalf("short description must be nonempty, single-line, and at most 30 characters: %q", shortDescription)
	}
	for field, path := range map[string]string{
		"composerIcon": manifest.Interface.ComposerIcon,
		"logo":         manifest.Interface.Logo,
	} {
		if path != "./assets/icon.svg" {
			t.Errorf("interface.%s = %q, want the bundled ./assets/icon.svg", field, path)
		}
	}
	for _, path := range []string{"hooks", ".mcp.json", "mcp.json", ".app.json"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("unexpected component %s: %v", path, err)
		}
	}
	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Source string `json:"source"`
				Path   string `json:"path"`
			} `json:"source"`
			Policy struct {
				Installation   string `json:"installation"`
				Authentication string `json:"authentication"`
			} `json:"policy"`
		} `json:"plugins"`
	}
	publicRoot := filepath.Dir(filepath.Dir(root))
	if err := json.Unmarshal(readFile(t, filepath.Join(publicRoot, ".agents/plugins/marketplace.json")), &marketplace); err != nil {
		t.Fatal(err)
	}
	if marketplace.Name != "revyl" || len(marketplace.Plugins) != 1 {
		t.Fatalf("invalid marketplace: %+v", marketplace)
	}
	entry := marketplace.Plugins[0]
	if entry.Name != manifest.Name || entry.Source.Source != "local" || filepath.Join(publicRoot, entry.Source.Path) != root || entry.Policy.Installation != "AVAILABLE" || entry.Policy.Authentication != "ON_USE" {
		t.Fatalf("invalid marketplace entry: %+v", entry)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type()&os.ModeSymlink != 0 {
			t.Errorf("package contains a symlink: %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCodexBrandingIconMeetsDirectoryRequirements(t *testing.T) {
	content := readFile(t, filepath.Join(pluginRoot(t), "assets/icon.svg"))
	if len(content) > 5*1024*1024 {
		t.Fatal("branding icon exceeds the directory's 5 MiB limit")
	}
	var icon struct {
		XMLName xml.Name `xml:"svg"`
		Width   int      `xml:"width,attr"`
		Height  int      `xml:"height,attr"`
		ViewBox string   `xml:"viewBox,attr"`
	}
	if err := xml.Unmarshal(content, &icon); err != nil {
		t.Fatalf("invalid branding SVG: %v", err)
	}
	if icon.Width != 400 || icon.Height != 400 || icon.ViewBox != "0 0 400 400" {
		t.Fatalf("expected the square 400x400 branding SVG, got %+v", icon)
	}
}

func TestCodexLauncherPreservesArgumentsAndExitStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	fixture := newLauncherFixture(t)
	for _, exitCode := range []string{"0", "1"} {
		command := fixture.command(t, "device", "validation", "The screen is visible", "--json")
		command.Env = append(command.Env, "REVYL_BINARY="+fixture.binary, "REVYL_CLIENT_SOURCE=cursor_plugin", "CODEX_THREAD_ID=test-codex-thread", "FAKE_EXIT_CODE="+exitCode)
		output, err := command.CombinedOutput()
		if (exitCode == "0") != (err == nil) {
			t.Fatalf("exit %s: %v, %s", exitCode, err, output)
		}
		want := fixture.appRoot + "\n\ntest-codex-thread\ndevice\nvalidation\nThe screen is visible\n--json\n"
		if string(output) != want {
			t.Fatalf("launcher output = %q, want %q", output, want)
		}
	}
}

func TestCodexLauncherUsesVerifiedCacheWithoutPublishingCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	fixture := newLauncherFixture(t)
	cacheBinary := filepath.Join(fixture.home, ".cache/revyl/codex-plugin", fixture.manifest.RuntimeVersion, runtime.GOOS+"_"+runtime.GOARCH, "revyl")
	if err := os.MkdirAll(filepath.Dir(cacheBinary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheBinary, readFile(t, fixture.binary), 0700); err != nil {
		t.Fatal(err)
	}
	command := fixture.command(t, "version")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("verified cached runtime: %v, %s", err, output)
	}
	for _, relative := range []string{".revyl/bin/revyl", ".local/bin/revyl", ".revyl/credentials.json"} {
		if _, err := os.Stat(filepath.Join(fixture.home, relative)); !os.IsNotExist(err) {
			t.Fatalf("launcher created %s: %v", relative, err)
		}
	}
	if err := os.WriteFile(cacheBinary, []byte("#!/bin/sh\necho CORRUPT_EXECUTED\n"), 0700); err != nil {
		t.Fatal(err)
	}
	output, err = fixture.command(t, "version").CombinedOutput()
	if err == nil || bytes.Contains(output, []byte("CORRUPT_EXECUTED")) || !bytes.Contains(output, []byte("may not download")) {
		t.Fatalf("corrupt cache must fail closed offline: %v, %s", err, output)
	}
}

func TestCodexLauncherRejectsUnknownHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	fixture := newLauncherFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(pluginRoot(t), "scripts/launch-runtime"), "version")
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + fixture.home, "REVYL_PLUGIN_HOST=unknown", "REVYL_BINARY=" + fixture.binary}
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("unsupported plugin host")) {
		t.Fatalf("unknown host accepted: %v, %s", err, output)
	}
}

type launcherFixture struct {
	pluginRoot   string
	appRoot      string
	home         string
	binary       string
	manifestPath string
	manifest     cursorpluginrelease.RuntimeManifest
}

func newLauncherFixture(t *testing.T) launcherFixture {
	t.Helper()
	root := t.TempDir()
	f := launcherFixture{
		pluginRoot: pluginRoot(t), appRoot: filepath.Join(root, "nested app"), home: filepath.Join(root, "home"),
		binary: filepath.Join(root, "fake revyl"), manifestPath: filepath.Join(root, "runtime-manifest.json"),
	}
	for _, dir := range []string{f.appRoot, f.home} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary := []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"${REVYL_CLIENT_SOURCE:-}\" \"${CODEX_THREAD_ID:-}\" \"$@\"\nexit \"${FAKE_EXIT_CODE:-0}\"\n")
	if err := os.WriteFile(f.binary, binary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(f.pluginRoot, "runtime-manifest.json")), &f.manifest); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	checksum := hex.EncodeToString(digest[:])
	f.manifest.LinuxAMD64SHA256 = checksum
	f.manifest.LinuxARM64SHA256 = checksum
	f.manifest.DarwinAMD64SHA256 = checksum
	f.manifest.DarwinARM64SHA256 = checksum
	manifestJSON, err := json.MarshalIndent(f.manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.manifestPath, manifestJSON, 0600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f launcherFixture) command(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, filepath.Join(f.pluginRoot, "scripts/launch-revyl"), args...)
	command.Dir = f.appRoot
	command.Env = []string{
		"PATH=/usr/bin:/bin", "HOME=" + f.home, "REVYL_RUNTIME_NO_DOWNLOAD=1", "REVYL_RUNTIME_MANIFEST=" + f.manifestPath,
	}
	return command
}

func TestCodexWindowsAdapterUsesTheSharedRuntime(t *testing.T) {
	root := pluginRoot(t)
	adapter := string(readFile(t, filepath.Join(root, "scripts/launch-revyl.ps1")))
	for _, value := range []string{`$env:REVYL_PLUGIN_HOST = "codex"`, `"launch-runtime.ps1"`, "@RevylArguments", "exit $LASTEXITCODE"} {
		if !strings.Contains(adapter, value) {
			t.Errorf("Windows adapter missing %s", value)
		}
	}
}

func TestCodexRuntimePinHasEveryPublishedPlatform(t *testing.T) {
	root := pluginRoot(t)
	var manifest cursorpluginrelease.RuntimeManifest
	if err := json.Unmarshal(readFile(t, filepath.Join(root, "runtime-manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	version := strings.TrimSpace(string(readFile(t, filepath.Join(root, "runtime-version"))))
	if !manifest.Prepared || manifest.RuntimeVersion != version || manifest.ReleaseTag != "v"+version || manifest.ReleaseBaseURL != "https://github.com/RevylAI/revyl-cli/releases/download/v"+version {
		t.Fatalf("invalid runtime pin: %+v", manifest)
	}
	for _, checksum := range []string{manifest.DarwinAMD64SHA256, manifest.DarwinARM64SHA256, manifest.LinuxAMD64SHA256, manifest.LinuxARM64SHA256, manifest.WindowsAMD64SHA256, manifest.WindowsARM64SHA256} {
		digest, err := hex.DecodeString(checksum)
		if err != nil || len(digest) != sha256.Size {
			t.Fatalf("invalid runtime SHA256: %q", checksum)
		}
	}
}

func TestCodexSkillsResolveOnlyBundledLaunchers(t *testing.T) {
	root := pluginRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("skill count = %d, want 2", len(entries))
	}
	for _, name := range []string{"revyl-codex-dev-loop", "revyl-codex-proof-ci"} {
		path := filepath.Join(root, "skills", name, "SKILL.md")
		content := string(readFile(t, path))
		for _, required := range []string{"name: " + name, "../../scripts/launch-revyl", "launch-revyl.cmd"} {
			if !strings.Contains(content, required) {
				t.Errorf("%s is missing %q", name, required)
			}
		}
		for _, relative := range []string{"../../scripts/launch-revyl", "../../scripts/launch-revyl.cmd"} {
			readFile(t, filepath.Join(filepath.Dir(path), relative))
		}
	}
}
