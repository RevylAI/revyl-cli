package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/revyl/cli/internal/cursorpluginrelease"
)

const claudePluginName = "revyl-cli"

var pluginVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type pluginManifest struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"displayName,omitempty"`
	Version     string          `json:"version"`
	Description string          `json:"description,omitempty"`
	Author      json.RawMessage `json:"author,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	Repository  string          `json:"repository,omitempty"`
	License     string          `json:"license,omitempty"`
	Keywords    []string        `json:"keywords,omitempty"`
	Skills      string          `json:"skills,omitempty"`
	Privacy     string          `json:"privacyPolicyUrl,omitempty"`
	UserConfig  json.RawMessage `json:"userConfig,omitempty"`
}

// claudeUserConfig declares the Claude Code options the plugin's hooks read as
// CLAUDE_PLUGIN_OPTION_<KEY>. Codex has no equivalent; its users set the
// environment variable the hook also accepts.
const claudeUserConfig = `{
  "offer_proof_after_commit": {
    "type": "boolean",
    "title": "Offer a device check after commits",
    "description": "After a commit, push, or new pull request that changes a Revyl app, the agent offers once to check it on a Revyl device. It never starts anything without your yes.",
    "default": false
  }
}`

// portedSkills are maintained for the CLI or the Cursor plugin and copied into
// this plugin with their shell commands routed through the bundled launcher.
var portedSkills = []struct{ source, name string }{
	{"skills/revyl-cli-auth-bypass", "revyl-cli-auth-bypass"},
	{"cursor-plugin/skills/revyl-ci-sync", "revyl-ci-sync"},
}

var fencedRevylCommand = regexp.MustCompile(`(^|[\s;&|(])revyl(\s)`)

//go:embed ported-skill-launcher.md
var portedSkillLauncher string

// routeThroughLauncher rewrites the Revyl commands in a ported skill's code
// fences to the bundled launcher and, for SKILL.md, adds the launcher section
// after the title.
func routeThroughLauncher(markdown []byte, skill bool) []byte {
	var lines []string
	inFence := false
	addSection := skill
	for _, line := range strings.Split(string(markdown), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		} else if inFence {
			line = fencedRevylCommand.ReplaceAllString(line, `${1}"$$LAUNCHER"${2}`)
		}
		lines = append(lines, line)
		if addSection && strings.HasPrefix(line, "# ") {
			lines = append(lines, "", strings.TrimSuffix(portedSkillLauncher, "\n"))
			addSection = false
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

type codexInterface struct {
	Interface struct {
		PrivacyPolicyURL string `json:"privacyPolicyURL"`
	} `json:"interface"`
}

type generatedFile struct {
	path    string
	content []byte
	mode    os.FileMode
}

func main() {
	check := flag.Bool("check", false, "verify generated Codex plugin assets without writing")
	flag.Parse()
	if err := syncPlugin(".", *check, &http.Client{Timeout: 60 * time.Second}); err != nil {
		fmt.Fprintf(os.Stderr, "sync Codex plugin: %v\n", err)
		os.Exit(1)
	}
}

func syncPlugin(root string, check bool, client *http.Client) error {
	packageRoot, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer packageRoot.Close()
	pluginRoot := filepath.Join("plugins", "revyl")
	manifestJSON, err := packageRoot.ReadFile(filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"))
	if err != nil {
		return err
	}
	var plugin pluginManifest
	if err := json.Unmarshal(manifestJSON, &plugin); err != nil {
		return err
	}
	if plugin.Name != "revyl" || !pluginVersionPattern.MatchString(plugin.Version) {
		return fmt.Errorf("expected Revyl plugin with a release semantic version")
	}
	versionBytes, err := packageRoot.ReadFile(filepath.Join(pluginRoot, "runtime-version"))
	if err != nil {
		return err
	}
	runtimeVersion := strings.TrimSpace(string(versionBytes))
	comparison, err := cursorpluginrelease.ComparePluginVersions(runtimeVersion, "0.1.108")
	if err != nil || comparison < 0 {
		return fmt.Errorf("Codex workflow commands require a published CLI runtime >= 0.1.108")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	runtimeManifest, err := cursorpluginrelease.ResolveRuntimeManifest(ctx, plugin.Version, runtimeVersion, "https://github.com/RevylAI/revyl-cli/releases/download", client)
	if err != nil {
		return fmt.Errorf("resolve CLI runtime %s: %w; select a published plugins/revyl/runtime-version and retry make sync-codex-plugin", runtimeVersion, err)
	}
	runtimeManifest.GeneratedBy = "make -C revyl-cli sync-codex-plugin"
	runtimeJSON, err := json.MarshalIndent(runtimeManifest, "", "  ")
	if err != nil {
		return err
	}
	var codexMetadata codexInterface
	if err := json.Unmarshal(manifestJSON, &codexMetadata); err != nil {
		return err
	}
	claudeManifest := plugin
	claudeManifest.Name = claudePluginName
	claudeManifest.DisplayName = "Revyl CLI"
	claudeManifest.Privacy = codexMetadata.Interface.PrivacyPolicyURL
	claudeManifest.UserConfig = json.RawMessage(claudeUserConfig)
	claudeJSON, err := json.MarshalIndent(claudeManifest, "", "  ")
	if err != nil {
		return err
	}
	files := []generatedFile{{
		path: filepath.Join(pluginRoot, "runtime-manifest.json"), content: append(runtimeJSON, '\n'), mode: 0644,
	}, {
		path: filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), content: append(claudeJSON, '\n'), mode: 0644,
	}}
	for _, copy := range []struct{ source, destination string }{
		{"cursor-plugin/hooks/launch-revyl", "scripts/launch-runtime"},
		{"cursor-plugin/hooks/launch-revyl.ps1", "scripts/launch-runtime.ps1"},
		{"cursor-plugin/hooks/launch-revyl.cmd", "scripts/launch-revyl.cmd"},
		{"cursor-plugin/assets/icon.svg", "assets/icon.svg"},
		{"skills/revyl-cli-dev-loop/SKILL.md", "references/revyl-cli-dev-loop.md"},
	} {
		source := filepath.FromSlash(copy.source)
		content, err := packageRoot.ReadFile(source)
		if err != nil {
			return err
		}
		info, err := packageRoot.Stat(source)
		if err != nil {
			return err
		}
		files = append(files, generatedFile{
			path: filepath.Join(pluginRoot, filepath.FromSlash(copy.destination)), content: content, mode: info.Mode().Perm(),
		})
	}
	for _, port := range portedSkills {
		err := fs.WalkDir(packageRoot.FS(), port.source, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			content, err := fs.ReadFile(packageRoot.FS(), path)
			if err != nil {
				return err
			}
			relative := strings.TrimPrefix(path, port.source+"/")
			if strings.HasSuffix(relative, ".md") {
				content = routeThroughLauncher(content, relative == "SKILL.md")
			}
			files = append(files, generatedFile{
				path: filepath.Join(pluginRoot, "skills", port.name, filepath.FromSlash(relative)), content: content, mode: 0644,
			})
			return nil
		})
		if err != nil {
			return fmt.Errorf("port skill %s: %w", port.source, err)
		}
	}
	for _, file := range files {
		if check {
			content, err := packageRoot.ReadFile(file.path)
			if err != nil {
				return err
			}
			if !bytes.Equal(content, file.content) {
				return fmt.Errorf("%s is stale; run make sync-codex-plugin", file.path)
			}
			if runtime.GOOS != "windows" {
				info, err := packageRoot.Stat(file.path)
				if err != nil {
					return err
				}
				if info.Mode().Perm()&0111 != file.mode&0111 {
					return fmt.Errorf("%s executable bits are stale; run make sync-codex-plugin", file.path)
				}
			}
			continue
		}
		if err := packageRoot.MkdirAll(filepath.Dir(file.path), 0700); err != nil {
			return err
		}
		output, err := packageRoot.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.mode)
		if err != nil {
			return err
		}
		if _, err := output.Write(file.content); err != nil {
			output.Close()
			return err
		}
		if runtime.GOOS != "windows" {
			if err := output.Chmod(file.mode); err != nil {
				output.Close()
				return err
			}
		}
		if err := output.Close(); err != nil {
			return err
		}
	}
	return removeObsoletePortedFiles(packageRoot, pluginRoot, files, check)
}

// removeObsoletePortedFiles deletes, or in check mode rejects, files left in a
// ported skill's directory after their source was removed or renamed.
func removeObsoletePortedFiles(packageRoot *os.Root, pluginRoot string, files []generatedFile, check bool) error {
	generated := make(map[string]bool, len(files))
	for _, file := range files {
		generated[filepath.Clean(file.path)] = true
	}
	var obsolete []string
	for _, port := range portedSkills {
		directory := filepath.ToSlash(filepath.Join(pluginRoot, "skills", port.name))
		err := fs.WalkDir(packageRoot.FS(), directory, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if !generated[filepath.FromSlash(path)] {
				obsolete = append(obsolete, filepath.FromSlash(path))
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("scan ported skill %s: %w", port.name, err)
		}
	}
	for _, path := range obsolete {
		if check {
			return fmt.Errorf("%s is obsolete; run make sync-codex-plugin", path)
		}
		if err := packageRoot.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
