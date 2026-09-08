package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/revyl/cli/internal/cursorpluginrelease"
)

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
	var plugin struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifestJSON, &plugin); err != nil {
		return err
	}
	if plugin.Name != "revyl" || !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(plugin.Version) {
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
	files := []generatedFile{{
		path: filepath.Join(pluginRoot, "runtime-manifest.json"), content: append(runtimeJSON, '\n'), mode: 0644,
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
	return nil
}
