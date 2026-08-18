package cursorplugin_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// sameFilesystemPath reports whether two paths refer to the same existing file/dir.
// Windows CI often mixes 8.3 short paths from Go temp dirs with long paths from PowerShell.
func sameFilesystemPath(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

const (
	hookCommand               = "./hooks/ensure-revyl"
	runtimeUnavailableMessage = "The Revyl plugin runtime is not ready. Update or reinstall the plugin, or set REVYL_BINARY to an executable Revyl CLI path."
	loginGuidance             = "Run revyl auth login and post the printed approval URL as a clickable markdown link. Wait for approval. REVYL_API_KEY is optional for unattended agents."
	installOnFirstCommand     = "The Revyl CLI installs on the first revyl command."
)

type pluginManifest struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Logo    string `json:"logo"`
	Skills  string `json:"skills"`
	Rules   string `json:"rules"`
	Hooks   string `json:"hooks"`
}

type marketplaceManifest struct {
	Name     string                   `json:"name"`
	Owner    marketplaceOwner         `json:"owner"`
	Metadata marketplaceMetadata      `json:"metadata"`
	Plugins  []marketplacePluginEntry `json:"plugins"`
}

type marketplaceOwner struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type marketplaceMetadata struct {
	Description string `json:"description"`
	Version     string `json:"version"`
}

type marketplacePluginEntry struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

type hookDefinition struct {
	Command    string `json:"command"`
	Matcher    string `json:"matcher"`
	Timeout    int    `json:"timeout"`
	FailClosed bool   `json:"failClosed"`
}

type hooksConfig struct {
	Version int                         `json:"version"`
	Hooks   map[string][]hookDefinition `json:"hooks"`
}

// TestMarketplaceArtifactContract verifies the public repository manifest resolves to this plugin.
func TestMarketplaceArtifactContract(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	publicRepositoryRoot := filepath.Dir(pluginRoot)
	marketplace := readJSON[marketplaceManifest](t, filepath.Join(publicRepositoryRoot, ".cursor-plugin", "marketplace.json"))

	if marketplace.Name != "revyl" {
		t.Fatalf("marketplace name = %q, want revyl", marketplace.Name)
	}
	if marketplace.Owner.Name != "Revyl" || marketplace.Owner.Email != "support@revyl.ai" {
		t.Fatalf("marketplace owner = %#v, want Revyl support owner", marketplace.Owner)
	}
	if marketplace.Metadata.Description == "" || marketplace.Metadata.Version == "" {
		t.Fatalf("marketplace metadata = %#v, want description and semantic version", marketplace.Metadata)
	}
	if len(marketplace.Plugins) != 1 {
		t.Fatalf("marketplace plugin count = %d, want 1", len(marketplace.Plugins))
	}

	entry := marketplace.Plugins[0]
	if entry.Name != "revyl" || entry.Source != "./cursor-plugin" || entry.Description == "" {
		t.Fatalf("marketplace plugin entry = %#v, want revyl sourced from ./cursor-plugin", entry)
	}
	resolvedSource := filepath.Clean(filepath.Join(publicRepositoryRoot, filepath.FromSlash(entry.Source)))
	if resolvedSource != filepath.Clean(pluginRoot) {
		t.Fatalf("marketplace source resolves to %q, want %q", resolvedSource, pluginRoot)
	}
	sourceManifest := readJSON[pluginManifest](t, filepath.Join(resolvedSource, ".cursor-plugin", "plugin.json"))
	if sourceManifest.Name != entry.Name {
		t.Fatalf("marketplace plugin name = %q, source manifest name = %q", entry.Name, sourceManifest.Name)
	}
	if sourceManifest.Version != marketplace.Metadata.Version {
		t.Fatalf(
			"marketplace version = %q, source manifest version = %q",
			marketplace.Metadata.Version,
			sourceManifest.Version,
		)
	}
}

// TestPluginArtifactContract verifies manifests, hooks, skills, and rules without plugin MCP.
func TestPluginArtifactContract(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	manifest := readJSON[pluginManifest](t, filepath.Join(pluginRoot, ".cursor-plugin", "plugin.json"))

	if manifest.Name != "revyl" {
		t.Fatalf("plugin name = %q, want revyl", manifest.Name)
	}
	requirePluginRelativeFile(t, pluginRoot, manifest.Logo)
	requireDirectory(t, pluginRoot, manifest.Skills)
	requireDirectory(t, pluginRoot, manifest.Rules)
	requireFile(t, pluginRoot, manifest.Hooks)
	requireFile(t, pluginRoot, "runtime-manifest.json")
	requireFile(t, pluginRoot, "hooks/launch-revyl")
	requireFile(t, pluginRoot, "hooks/launch-revyl.cmd")
	requireFile(t, pluginRoot, "hooks/launch-revyl.ps1")
	requireExactSkills(t, filepath.Join(pluginRoot, filepath.Clean(manifest.Skills)))
	if _, err := os.Stat(filepath.Join(pluginRoot, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("plugin still ships mcp.json: %v", err)
	}
	pluginJSON := readText(t, filepath.Join(pluginRoot, ".cursor-plugin", "plugin.json"))
	if strings.Contains(pluginJSON, `"mcpServers"`) {
		t.Fatal("plugin.json still declares mcpServers")
	}

	hooks := readJSON[hooksConfig](t, filepath.Join(pluginRoot, filepath.Clean(manifest.Hooks)))
	if hooks.Version != 1 {
		t.Fatalf("hooks version = %d, want 1", hooks.Version)
	}
	requireHookDefinition(t, hooks, "sessionStart", "", 30)
	requireHookDefinition(t, hooks, "beforeShellExecution", "revyl", 60)
	if len(hooks.Hooks) != 2 {
		t.Fatalf("hook event count = %d, want 2", len(hooks.Hooks))
	}

	rule := readText(t, filepath.Join(pluginRoot, "rules", "revyl-dev-loop.mdc"))
	cloudSkill := readText(t, filepath.Join(pluginRoot, "skills", "revyl-cloud-agent", "SKILL.md"))
	for path, content := range map[string]string{
		"rules/revyl-dev-loop.mdc":          rule,
		"skills/revyl-cloud-agent/SKILL.md": cloudSkill,
	} {
		if !strings.Contains(content, "`revyl-cli-dev-loop`") &&
			!strings.Contains(content, "The plugin does not start MCP") {
			t.Fatalf("%s does not route to the CLI loop or say the plugin skips MCP", path)
		}
		if strings.Contains(content, "`revyl-dev-loop`") {
			t.Fatalf("%s still loads removed revyl-dev-loop", path)
		}
	}
	if strings.Contains(cloudSkill, "plugin install locations") {
		t.Fatal("cloud-agent skill still calls curl PATH dirs plugin install locations")
	}
	if !strings.Contains(cloudSkill, `export PATH="$HOME/.revyl/bin:$HOME/.local/bin:$PATH"`) {
		t.Fatal("cloud-agent skill does not prepend the PATH dirs the plugin publishes onto")
	}
}

// TestCopiedPluginSkillsMatchSource keeps Makefile-copied skills identical to
// the embedded skills that make sync-cursor-plugin-skills copies.
func TestCopiedPluginSkillsMatchSource(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	copied := []string{
		"revyl-cli-dev-loop",
		"revyl-cli-auth-bypass",
	}
	for _, skill := range copied {
		bundled := readText(t, filepath.Join(pluginRoot, "skills", skill, "SKILL.md"))
		canonical := readText(t, filepath.Join(pluginRoot, "..", "skills", skill, "SKILL.md"))
		if bundled != canonical {
			t.Fatalf("cursor-plugin/skills/%s/SKILL.md must match skills/%s/SKILL.md", skill, skill)
		}
	}
}

// TestProofAutomationPromptMatchesExample keeps the bundled Automation prompt identical
// to the public examples copy linked from product docs.
func TestProofAutomationPromptMatchesExample(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	bundled := readText(t, filepath.Join(pluginRoot, "skills", "revyl-proof-automation", "PROMPT.md"))
	canonical := readText(t, filepath.Join(pluginRoot, "..", "examples", "cursor-automation", "prove-mobile-pr", "PROMPT.md"))
	if bundled != canonical {
		t.Fatal("cursor-plugin/skills/revyl-proof-automation/PROMPT.md must match examples/cursor-automation/prove-mobile-pr/PROMPT.md")
	}
	for _, forbidden := range []string{
		"Or MCP `list_builds`",
		"equivalent MCP stop",
		"Enable **Revyl MCP**",
	} {
		if strings.Contains(bundled, forbidden) {
			t.Fatalf("Automation PROMPT still treats MCP as a runtime path: %q", forbidden)
		}
	}
	if !strings.Contains(bundled, "install.sh") {
		t.Fatal("Automation PROMPT does not install the CLI on the runner")
	}
	skill := readText(t, filepath.Join(pluginRoot, "skills", "revyl-proof-automation", "SKILL.md"))
	if strings.Contains(skill, "Enable **Revyl MCP**") {
		t.Fatal("revyl-proof-automation skill still treats MCP as a setup gate")
	}
}

// TestMaintainerReleaseLeadsWithPluginBumpPatch verifies the operator bump command is documented.
func TestMaintainerReleaseLeadsWithPluginBumpPatch(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	readme := readText(t, filepath.Join(pluginRoot, "README.md"))
	if !strings.Contains(readme, "./scripts/bump patch --plugin") {
		t.Fatal("maintainer README must document ./scripts/bump patch --plugin")
	}
	makefile := readText(t, filepath.Join(pluginRoot, "..", "Makefile"))
	for _, target := range []string{
		"cursor-plugin-bump-patch:",
		"cursor-plugin-bump-minor:",
		"cursor-plugin-bump-major:",
	} {
		if !strings.Contains(makefile, target) {
			t.Fatalf("revyl-cli Makefile missing %s", target)
		}
	}
}

// TestHookScriptContract verifies both platform entrypoints are safe, local-only prerequisite checks.
func TestHookScriptContract(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	posixPath := filepath.Join(pluginRoot, "hooks", "ensure-revyl")
	windowsPath := filepath.Join(pluginRoot, "hooks", "ensure-revyl.cmd")
	windowsScriptPath := filepath.Join(pluginRoot, "hooks", "ensure-revyl.ps1")

	requireFile(t, pluginRoot, "hooks/ensure-revyl")
	requireFile(t, pluginRoot, "hooks/ensure-revyl.cmd")
	requireFile(t, pluginRoot, "hooks/ensure-revyl.ps1")
	if _, err := os.Stat(filepath.Join(pluginRoot, "hooks", "ensure-revyl.sh")); !os.IsNotExist(err) {
		t.Fatalf("legacy ensure-revyl.sh still exists: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(posixPath)
		if err != nil {
			t.Fatalf("stat POSIX hook: %v", err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatal("POSIX hook is not executable")
		}
	}

	for path, content := range map[string]string{
		posixPath:         readText(t, posixPath),
		windowsScriptPath: readText(t, windowsScriptPath),
	} {
		if !strings.Contains(content, runtimeUnavailableMessage) {
			t.Fatalf("%s does not contain runtime guidance", filepath.Base(path))
		}
		if !strings.Contains(content, loginGuidance) {
			t.Fatalf("%s does not tell the agent to run revyl auth login", filepath.Base(path))
		}
		if !strings.Contains(content, "persist-cloud-env") {
			t.Fatalf("%s does not bridge an injected REVYL_API_KEY", filepath.Base(path))
		}
		for _, forbidden := range []string{
			"auth status",
			"install.sh",
			"https://",
		} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s contains forbidden bootstrap behavior %q", filepath.Base(path), forbidden)
			}
		}
	}
	if !strings.Contains(readText(t, posixPath), "runtime-manifest.json") ||
		!strings.Contains(readText(t, windowsScriptPath), "runtime-manifest.json") {
		t.Fatal("runtime readiness hooks do not inspect the plugin manifest")
	}

	if !strings.Contains(readText(t, posixPath), "cat 2>/dev/null") {
		t.Fatal("POSIX hook does not read hook JSON from stdin")
	}
	if !strings.Contains(readText(t, windowsPath), "ensure-revyl.ps1") {
		t.Fatal("Windows hook entrypoint does not invoke its PowerShell implementation")
	}
	if !strings.Contains(readText(t, windowsScriptPath), "[Console]::In.ReadToEnd()") {
		t.Fatal("Windows hook does not read hook JSON from stdin")
	}
}

// TestLocalInstaller verifies isolated copies and live worktree links without plugin MCP.
func TestLocalInstaller(t *testing.T) {
	pluginRoot := pluginRootPath(t)

	t.Run("production command", func(t *testing.T) {
		localRoot := t.TempDir()
		runLocalInstaller(t, pluginRoot, environmentWithOverrides(
			"CURSOR_PLUGIN_LOCAL_DIR="+localRoot,
			"REVYL_BINARY=",
		))
		destination := filepath.Join(localRoot, "revyl")
		requireRealInstalledPlugin(t, destination)

		stalePath := filepath.Join(destination, "stale-file")
		if err := os.WriteFile(stalePath, []byte("stale"), 0o600); err != nil {
			t.Fatalf("write stale marker: %v", err)
		}
		runLocalInstaller(t, pluginRoot, environmentWithOverrides(
			"CURSOR_PLUGIN_LOCAL_DIR="+localRoot,
			"REVYL_BINARY=",
		))
		if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
			t.Fatalf("stale marker still exists after reinstall: %v", err)
		}
	})

	t.Run("worktree command", func(t *testing.T) {
		localRoot := t.TempDir()
		binaryRoot := filepath.Join(t.TempDir(), "worktree bin")
		if err := os.Mkdir(binaryRoot, 0o700); err != nil {
			t.Fatalf("create worktree binary directory: %v", err)
		}
		selectedBinary := writeFakeCLI(t, binaryRoot)
		runLocalInstaller(t, pluginRoot, environmentWithOverrides(
			"CURSOR_PLUGIN_LOCAL_DIR="+localRoot,
			"REVYL_BINARY="+selectedBinary,
		))

		destination := filepath.Join(localRoot, "revyl")
		requireRealInstalledPlugin(t, destination)
		installedHook := filepath.Join(destination, "hooks", "ensure-revyl")
		if runtime.GOOS == "windows" {
			installedHook += ".cmd"
		}
		hookOutput := runHook(
			t,
			installedHook,
			`{"hook_event_name":"sessionStart"}`,
			environmentWithOverrides("REVYL_BINARY="),
		)
		requireHookOutput(t, hookOutput, map[string]string{"additional_context": loginGuidance})
	})

	t.Run("linked worktree command", func(t *testing.T) {
		localRoot := t.TempDir()
		binaryRoot := filepath.Join(t.TempDir(), "linked worktree bin")
		if err := os.Mkdir(binaryRoot, 0o700); err != nil {
			t.Fatalf("create linked worktree binary directory: %v", err)
		}
		selectedBinary := writeFakeCLI(t, binaryRoot)
		runLocalInstaller(
			t,
			pluginRoot,
			environmentWithOverrides(
				"CURSOR_PLUGIN_LOCAL_DIR="+localRoot,
				"REVYL_BINARY="+selectedBinary,
			),
			"--link",
		)

		destination := filepath.Join(localRoot, "revyl")
		requireRealInstalledPlugin(t, destination)
		requireLinkedPluginDirectories(t, pluginRoot, destination)
		if _, err := os.Stat(filepath.Join(pluginRoot, "mcp.json")); !os.IsNotExist(err) {
			t.Fatalf("linked installation resurrected mcp.json: %v", err)
		}

		installedHook := filepath.Join(destination, "hooks", "ensure-revyl")
		sourceHook := filepath.Join(pluginRoot, "hooks", "ensure-revyl")
		if runtime.GOOS == "windows" {
			installedHook += ".cmd"
			sourceHook += ".cmd"
		}
		logicalInstallOutput := runHook(
			t,
			installedHook,
			`{"hook_event_name":"sessionStart"}`,
			environmentWithOverrides("REVYL_BINARY=", "CURSOR_PLUGIN_ROOT="),
		)
		requireHookOutput(t, logicalInstallOutput, map[string]string{"additional_context": loginGuidance})
		explicitInstallOutput := runHook(
			t,
			sourceHook,
			`{"hook_event_name":"sessionStart"}`,
			environmentWithOverrides(
				"REVYL_BINARY=",
				"CURSOR_PLUGIN_ROOT="+destination,
			),
		)
		requireHookOutput(t, explicitInstallOutput, map[string]string{"additional_context": loginGuidance})

		stalePath := filepath.Join(destination, "stale-file")
		if err := os.WriteFile(stalePath, []byte("stale"), 0o600); err != nil {
			t.Fatalf("write linked stale marker: %v", err)
		}
		nextPluginRoot := copyPluginFixture(t, pluginRoot)
		runLocalInstaller(
			t,
			nextPluginRoot,
			environmentWithOverrides(
				"CURSOR_PLUGIN_LOCAL_DIR="+localRoot,
				"REVYL_BINARY="+selectedBinary,
			),
			"--link",
		)
		if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
			t.Fatalf("linked stale marker still exists after reinstall: %v", err)
		}
		requireLinkedPluginDirectories(t, nextPluginRoot, destination)

		statusOutput := runLocalInstaller(
			t,
			nextPluginRoot,
			environmentWithOverrides("CURSOR_PLUGIN_LOCAL_DIR="+localRoot),
			"--status",
		)
		for _, expected := range []string{"Installed: yes", "Mode: link", "plugin-pinned runtime"} {
			if !strings.Contains(statusOutput, expected) {
				t.Fatalf("linked status output %q does not contain %q", statusOutput, expected)
			}
		}
		requireStatusPath(t, statusOutput, "Source", nextPluginRoot)
	})

	t.Run("linked dry run", func(t *testing.T) {
		localRoot := filepath.Join(t.TempDir(), "not-created")
		binaryRoot := filepath.Join(t.TempDir(), "dry run bin")
		if err := os.Mkdir(binaryRoot, 0o700); err != nil {
			t.Fatalf("create dry-run binary directory: %v", err)
		}
		selectedBinary := writeFakeCLI(t, binaryRoot)
		output := runLocalInstaller(
			t,
			pluginRoot,
			environmentWithOverrides(
				"CURSOR_PLUGIN_LOCAL_DIR="+localRoot,
				"REVYL_BINARY="+selectedBinary,
			),
			"--link",
			"--dry-run",
		)
		if !strings.Contains(output, "Changes: none") {
			t.Fatalf("dry-run output = %q, want no-change report", output)
		}
		if _, err := os.Stat(localRoot); !os.IsNotExist(err) {
			t.Fatalf("dry run created local plugin root: %v", err)
		}
	})
}

// TestInstallerStructuralContract verifies both native installers validate and stage the artifact.
func TestInstallerStructuralContract(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	posixInstaller := readText(t, filepath.Join(pluginRoot, "install-local.sh"))
	windowsInstaller := readText(t, filepath.Join(pluginRoot, "install-local.ps1"))

	for path, content := range map[string]string{
		"install-local.sh":  posixInstaller,
		"install-local.ps1": windowsInstaller,
	} {
		for _, required := range []string{
			".cursor-plugin",
			"plugin.json",
			"REVYL_BINARY",
			"hooks",
			"skills",
			"rules",
		} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s does not contain required installer contract %q", path, required)
			}
		}
	}
	for _, required := range []string{"--dry-run", "--status", "--link"} {
		if !strings.Contains(posixInstaller, required) {
			t.Fatalf("POSIX installer does not contain local mode %q", required)
		}
	}
	for _, required := range []string{"$DryRun", "$Status", `"Link"`} {
		if !strings.Contains(windowsInstaller, required) {
			t.Fatalf("Windows installer does not contain local mode %q", required)
		}
	}
	if !strings.Contains(posixInstaller, `cp -R "$SOURCE_DIR/." "$STAGING/"`) {
		t.Fatal("POSIX installer does not copy the complete plugin into staging")
	}
	if !strings.Contains(windowsInstaller, "Get-ChildItem -LiteralPath $SourceDirectory -Force") {
		t.Fatal("Windows installer does not copy hidden plugin files into staging")
	}
}

// TestDistributableContainsNoDeveloperData rejects local paths and credential-like values.
func TestDistributableContainsNoDeveloperData(t *testing.T) {
	pluginRoot := pluginRootPath(t)
	paths := []string{
		pluginRoot,
		filepath.Join(filepath.Dir(pluginRoot), ".cursor-plugin", "marketplace.json"),
	}
	for _, path := range paths {
		err := filepath.WalkDir(path, func(currentPath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			content, err := os.ReadFile(currentPath)
			if err != nil {
				return err
			}
			text := string(content)
			for _, forbidden := range []string{"/Users/", `:\Users\`, "file:///", "rk_live_", "sk_live_"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%s contains developer path or credential marker %q", currentPath, forbidden)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan distributable path %s: %v", path, err)
		}
	}
}

// requireHookDefinition verifies an event has one fail-open definition with exact settings.
func requireHookDefinition(t *testing.T, hooks hooksConfig, eventName, matcher string, timeout int) {
	t.Helper()
	definitions := hooks.Hooks[eventName]
	if len(definitions) != 1 {
		t.Fatalf("hook event %q definition count = %d, want 1", eventName, len(definitions))
	}
	definition := definitions[0]
	if definition.Command != hookCommand || definition.Matcher != matcher || definition.Timeout != timeout || definition.FailClosed {
		t.Fatalf("hook event %q definition = %#v, want command %q matcher %q timeout %d and fail-open", eventName, definition, hookCommand, matcher, timeout)
	}
}

// requireExactSkills verifies the plugin bundles only the supported canonical skill set.
func requireExactSkills(t *testing.T, skillsRoot string) {
	t.Helper()
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		t.Fatalf("read skills directory: %v", err)
	}
	var skillNames []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		requireFile(t, skillsRoot, filepath.Join(entry.Name(), "SKILL.md"))
		skillNames = append(skillNames, entry.Name())
	}
	slices.Sort(skillNames)
	expected := []string{
		"revyl-ci-sync",
		"revyl-cli-auth-bypass",
		"revyl-cli-dev-loop",
		"revyl-cloud-agent",
		"revyl-proof-automation",
		"revyl-proof-ci",
	}
	if !slices.Equal(skillNames, expected) {
		t.Fatalf("bundled skills = %#v, want %#v", skillNames, expected)
	}
}

// requireRealInstalledPlugin verifies a copied artifact without plugin MCP.
func requireRealInstalledPlugin(t *testing.T, destination string) {
	t.Helper()
	info, err := os.Lstat(destination)
	if err != nil {
		t.Fatalf("stat installed plugin: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("installed plugin mode = %v, want a real directory", info.Mode())
	}
	manifest := readJSON[pluginManifest](t, filepath.Join(destination, ".cursor-plugin", "plugin.json"))
	if manifest.Name != "revyl" {
		t.Fatalf("installed manifest name = %q, want revyl", manifest.Name)
	}
	requirePluginRelativeFile(t, destination, manifest.Logo)
	requireDirectory(t, destination, "hooks")
	requireDirectory(t, destination, "skills")
	requireDirectory(t, destination, "rules")
	if _, err := os.Stat(filepath.Join(destination, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("installed plugin still ships mcp.json: %v", err)
	}
	if strings.Contains(readText(t, filepath.Join(destination, ".cursor-plugin", "plugin.json")), `"mcpServers"`) {
		t.Fatal("installed plugin.json still declares mcpServers")
	}
}

// requireLinkedPluginDirectories verifies development surfaces resolve to the worktree.
func requireLinkedPluginDirectories(t *testing.T, pluginRoot, destination string) {
	t.Helper()
	for _, relativePath := range []string{".cursor-plugin", "assets", "hooks", "rules", "skills"} {
		installedPath := filepath.Join(destination, relativePath)
		if runtime.GOOS != "windows" {
			info, err := os.Lstat(installedPath)
			if err != nil {
				t.Fatalf("stat linked plugin directory %s: %v", relativePath, err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("plugin directory %s mode = %v, want symlink", relativePath, info.Mode())
			}
		}
		resolvedPath, err := filepath.EvalSymlinks(installedPath)
		if err != nil {
			t.Fatalf("resolve linked plugin directory %s: %v", relativePath, err)
		}
		expectedPath := filepath.Join(pluginRoot, relativePath)
		if !sameFilesystemPath(resolvedPath, expectedPath) {
			t.Fatalf(
				"linked plugin directory %s resolves to %q, want %q",
				relativePath,
				resolvedPath,
				expectedPath,
			)
		}
	}
}

// requireStatusPath verifies one installer status field names the expected filesystem entry.
func requireStatusPath(t *testing.T, statusOutput, fieldName, expectedPath string) {
	t.Helper()
	fieldPrefix := fieldName + ": "
	for line := range strings.SplitSeq(statusOutput, "\n") {
		value, found := strings.CutPrefix(strings.TrimSpace(line), fieldPrefix)
		if !found {
			continue
		}
		if !sameFilesystemPath(value, expectedPath) {
			t.Fatalf("installer status %s path = %q, want %q", fieldName, value, expectedPath)
		}
		return
	}
	t.Fatalf("installer status output %q has no %s field", statusOutput, fieldName)
}

// runLocalInstaller executes the native local installer with an isolated environment.
func runLocalInstaller(
	t *testing.T,
	pluginRoot string,
	environment []string,
	arguments ...string,
) string {
	t.Helper()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		powerShell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		commandArguments := []string{
			"-NoProfile",
			"-NonInteractive",
			"-ExecutionPolicy",
			"Bypass",
			"-File",
			filepath.Join(pluginRoot, "install-local.ps1"),
		}
		for _, argument := range arguments {
			switch argument {
			case "--link":
				commandArguments = append(commandArguments, "-Mode", "Link")
			case "--copy":
				commandArguments = append(commandArguments, "-Mode", "Copy")
			case "--dry-run":
				commandArguments = append(commandArguments, "-DryRun")
			case "--status":
				commandArguments = append(commandArguments, "-Status")
			default:
				t.Fatalf("unsupported Windows installer test argument %q", argument)
			}
		}
		command = exec.Command(powerShell, commandArguments...)
	} else {
		commandArguments := append([]string{filepath.Join(pluginRoot, "install-local.sh")}, arguments...)
		command = exec.Command("sh", commandArguments...)
	}
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("local installer failed: %v\n%s", err, output)
	}
	return string(output)
}

// copyPluginFixture creates a second worktree-like plugin source for switch tests.
func copyPluginFixture(t *testing.T, sourceRoot string) string {
	t.Helper()
	destinationRoot := filepath.Join(t.TempDir(), "cursor-plugin")
	if err := os.CopyFS(destinationRoot, os.DirFS(sourceRoot)); err != nil {
		t.Fatalf("copy plugin fixture: %v", err)
	}
	return destinationRoot
}

// runHook executes one native hook event and returns its JSON response.
func runHook(t *testing.T, hookPath, input string, environment []string) string {
	t.Helper()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		commandProcessor := os.Getenv("ComSpec")
		if commandProcessor == "" {
			commandProcessor = "cmd.exe"
		}
		command = exec.Command(commandProcessor, "/d", "/c", hookPath)
	} else {
		command = exec.Command(hookPath)
	}
	command.Env = environment
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed open contract: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

// requireHookOutput verifies a hook emits exactly the event-supported string fields.
func requireHookOutput(t *testing.T, output string, expected map[string]string) {
	t.Helper()
	var actual map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &actual); err != nil {
		t.Fatalf("decode hook output %q: %v", output, err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("hook output fields = %#v, want exactly %#v", actual, expected)
	}
	for name, expectedValue := range expected {
		rawValue, ok := actual[name]
		if !ok {
			t.Fatalf("hook output has no %q field: %s", name, output)
		}
		var actualValue string
		if err := json.Unmarshal(rawValue, &actualValue); err != nil {
			t.Fatalf("decode hook field %q: %v", name, err)
		}
		if actualValue != expectedValue {
			t.Fatalf("hook field %q = %q, want %q", name, actualValue, expectedValue)
		}
	}
}

// requireNoSecret verifies hook output never includes the test credential value.
func requireNoSecret(t *testing.T, output string) {
	t.Helper()
	if strings.Contains(output, testSecret()) {
		t.Fatal("hook output revealed REVYL_API_KEY")
	}
}

// writeFakeCLI creates a native revyl fixture and returns its absolute path.
func writeFakeCLI(t *testing.T, directory string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		content := "@echo off\r\n" +
			"exit /b 0\r\n"
		path := filepath.Join(directory, "revyl.cmd")
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatalf("write Windows fake CLI: %v", err)
		}
		return path
	}
	content := "#!/bin/sh\nexit 0\n"
	return writeExecutable(t, directory, "revyl", content)
}

// environmentWithOverrides returns the current environment with selected keys replaced.
func environmentWithOverrides(overrides ...string) []string {
	environment := os.Environ()
	for _, override := range overrides {
		name, _, _ := strings.Cut(override, "=")
		filtered := environment[:0]
		for _, entry := range environment {
			entryName, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(entryName, name) {
				filtered = append(filtered, entry)
			}
		}
		environment = append(filtered, override)
	}
	return environment
}

// testSecret returns a credential-shaped sentinel assembled outside distributable scans.
func testSecret() string {
	return "hook-" + "secret-" + "sentinel"
}

// writeExecutable creates an executable test fixture and returns its path.
func writeExecutable(t *testing.T, directory, name, content string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write executable %s: %v", name, err)
	}
	return path
}

// pluginRootPath returns the absolute plugin source directory for a test invocation.
func pluginRootPath(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("get plugin working directory: %v", err)
	}
	return root
}

// readJSON decodes a JSON file into the requested structured contract.
func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var result T
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return result
}

// readText returns a UTF-8 text artifact or fails the current test.
func readText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// requireDirectory verifies a manifest path resolves to an existing directory.
func requireDirectory(t *testing.T, root, relativePath string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.Clean(relativePath)))
	if err != nil {
		t.Fatalf("manifest directory %q: %v", relativePath, err)
	}
	if !info.IsDir() {
		t.Fatalf("manifest path %q is not a directory", relativePath)
	}
}

// requireFile verifies a relative path resolves to an existing regular file.
func requireFile(t *testing.T, root, relativePath string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, filepath.Clean(relativePath)))
	if err != nil {
		t.Fatalf("artifact file %q: %v", relativePath, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("artifact path %q is not a regular file", relativePath)
	}
}

// requirePluginRelativeFile verifies an asset stays within the artifact and is regular.
func requirePluginRelativeFile(t *testing.T, pluginRoot, relativePath string) {
	t.Helper()
	cleanPath := filepath.Clean(relativePath)
	if relativePath == "" || filepath.IsAbs(cleanPath) || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		t.Fatalf("plugin file path must be a non-empty relative path inside the artifact: %q", relativePath)
	}
	requireFile(t, pluginRoot, cleanPath)
}
