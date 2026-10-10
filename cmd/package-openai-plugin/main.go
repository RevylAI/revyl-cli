package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const pluginSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
const mcpSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

var bundledPaths = []string{
	"references/revyl-cli-dev-loop.md",
	"runtime-manifest.json",
	"runtime-version",
	"scripts/launch-revyl",
	"scripts/launch-revyl.cmd",
	"scripts/launch-revyl.ps1",
	"scripts/launch-runtime",
	"scripts/launch-runtime.ps1",
	"skills/revyl-codex-dev-loop/SKILL.md",
	"skills/revyl-codex-proof-ci/SKILL.md",
}

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type archiveFile struct {
	name    string
	content []byte
}

func main() {
	if err := run(".", os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "package OpenAI plugin: %v\n", err)
		os.Exit(1)
	}
}

func run(packageDirectory string, args []string) error {
	flags := flag.NewFlagSet("package-openai-plugin", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	out := flags.String("out", "", "new output ZIP path (never overwritten)")
	appID := flags.String("registered-app-id", "", "registered app ID for a private development package")
	mcpURL := flags.String("mcp-url", "", "production HTTPS MCP URL for a public submission package")
	packageName := flags.String("package-name", "", "existing private plugin name when uploading a new version")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *out == "" || (*appID == "") == (*mcpURL == "") {
		return errors.New("use --out PATH and exactly one of --registered-app-id ID or --mcp-url URL")
	}
	connectionFlags := 0
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "registered-app-id" || f.Name == "mcp-url" {
			connectionFlags++
		}
	})
	if connectionFlags != 1 {
		return errors.New("use exactly one of --registered-app-id or --mcp-url, not both")
	}
	if *packageName != "" && (*appID == "" || len(*packageName) > 64 || !regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`).MatchString(*packageName)) {
		return errors.New("--package-name requires a registered app and a lowercase kebab-case name of at most 64 characters")
	}
	registeredID := strings.TrimPrefix(*appID, "plugin_")
	if *appID != "" && !regexp.MustCompile(`^asdk_app_[A-Za-z0-9][A-Za-z0-9_-]*$`).MatchString(registeredID) {
		return errors.New("--registered-app-id must start with asdk_app_ or plugin_asdk_app_, followed by a letter or digit and only letters, digits, underscores, or hyphens")
	}
	if *mcpURL != "" {
		if err := validateMCPURL(*mcpURL); err != nil {
			return err
		}
	}
	root, err := os.OpenRoot(packageDirectory)
	if err != nil {
		return errors.New("open CLI package directory; run from revyl-cli")
	}
	defer root.Close()
	var files []archiveFile
	for _, name := range bundledPaths {
		file, err := readSource(root, "plugins/revyl/"+name, name)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	if err := validateRuntime(files); err != nil {
		return err
	}
	for _, name := range []string{"assets/icon.png", "skills/revyl-workspace/SKILL.md", "skills/revyl-cloud-app/SKILL.md"} {
		file, err := readSource(root, "plugins/revyl-openai/"+name, name)
		if err != nil {
			return err
		}
		files = append(files, file)
	}
	manifestFile, err := readSource(root, "plugins/revyl-openai/plugin.json", "plugin.json")
	if err != nil {
		return err
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(manifestFile.content, &manifest); err != nil || manifest == nil {
		return errors.New("overlay plugin.json must contain a JSON object")
	}
	for key := range manifest {
		if !slices.Contains([]string{"$schema", "name", "version", "description", "author", "homepage", "repository", "license", "keywords", "extensions"}, key) {
			return errors.New("overlay plugin.json contains an unsupported portable manifest field")
		}
	}
	if jsonString(manifest["$schema"]) != pluginSchema || jsonString(manifest["name"]) != "revyl-workspace" || !releaseVersion.MatchString(jsonString(manifest["version"])) {
		return errors.New("overlay plugin.json requires the portable schema, name revyl-workspace, and a release semantic version")
	}
	var extensions map[string]map[string]json.RawMessage
	if err := json.Unmarshal(manifest["extensions"], &extensions); err != nil || len(extensions) != 1 || extensions["com.openai"] == nil {
		return errors.New("overlay plugin.json requires only the com.openai extension")
	}
	openAI := extensions["com.openai"]
	for key := range openAI {
		if !slices.Contains([]string{"interface", "review", "publication"}, key) {
			return errors.New("overlay com.openai supports only interface, review, and publication; MCP mapping is supplied by the packaging flags and hooks are not supported")
		}
	}
	var presentation map[string]json.RawMessage
	if err := json.Unmarshal(openAI["interface"], &presentation); err != nil || presentation == nil {
		return errors.New("overlay com.openai.interface must be an object")
	}
	for _, key := range []string{"composerIcon", "logo"} {
		if value, exists := presentation[key]; exists && jsonString(value) != "./assets/icon.png" {
			return errors.New("overlay icons must reference the bundled ./assets/icon.png")
		}
	}
	if _, exists := presentation["screenshots"]; exists {
		return errors.New("screenshots are not in this package's asset allowlist")
	}
	var connection archiveFile
	if registeredID != "" {
		if *packageName != "" {
			manifest["name"], err = json.Marshal(*packageName)
			if err != nil {
				return err
			}
		}
		delete(openAI, "review")
		openAI["apps"] = json.RawMessage(`"./.app.json"`)
		connection, err = jsonFile(".app.json", map[string]any{
			"apps": map[string]any{"revyl": map[string]string{"id": registeredID}},
		})
	} else {
		connection, err = jsonFile("mcp.json", map[string]any{
			"$schema": mcpSchema,
			"mcpServers": map[string]any{"revyl": map[string]string{
				"type": "streamable-http", "url": *mcpURL,
			}},
		})
	}
	if err != nil {
		return err
	}
	manifest["extensions"], err = json.Marshal(extensions)
	if err != nil {
		return err
	}
	manifestFile, err = jsonFile("plugin.json", manifest)
	if err != nil {
		return err
	}
	files = append(files, manifestFile, connection)
	return writeArchive(*out, files)
}

func readSource(root *os.Root, source, name string) (archiveFile, error) {
	parts := strings.Split(source, "/")
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return archiveFile{}, fmt.Errorf("source %s must exist without symlinks; prepare assets with make sync-codex-plugin", source)
		}
	}
	content, err := root.ReadFile(filepath.FromSlash(source))
	if err != nil || len(content) == 0 {
		return archiveFile{}, fmt.Errorf("source %s must be readable and nonempty", source)
	}
	return archiveFile{name: name, content: content}, nil
}

func jsonString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func validateRuntime(files []archiveFile) error {
	var manifest map[string]json.RawMessage
	var version string
	for _, file := range files {
		switch file.name {
		case "runtime-version":
			version = strings.TrimSpace(string(file.content))
		case "runtime-manifest.json":
			if json.Unmarshal(file.content, &manifest) != nil {
				return errors.New("invalid runtime metadata; run make sync-codex-plugin")
			}
		}
	}
	invalid := errors.New("runtime metadata is missing, unprepared, or inconsistent; run make sync-codex-plugin")
	if !releaseVersion.MatchString(version) || string(manifest["schema_version"]) != "1" || string(manifest["prepared"]) != "true" || jsonString(manifest["generated_by"]) != "make -C revyl-cli sync-codex-plugin" || !releaseVersion.MatchString(jsonString(manifest["plugin_version"])) || jsonString(manifest["runtime_version"]) != version || jsonString(manifest["release_tag"]) != "v"+version || jsonString(manifest["release_base_url"]) != "https://github.com/RevylAI/revyl-cli/releases/download/v"+version {
		return invalid
	}
	if len(manifest) != 19 {
		return invalid
	}
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"} {
		asset := "revyl-" + strings.ReplaceAll(platform, "_", "-")
		if strings.HasPrefix(platform, "windows_") {
			asset += ".exe"
		}
		if jsonString(manifest[platform+"_asset"]) != asset || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(jsonString(manifest[platform+"_sha256"])) {
			return invalid
		}
	}
	return nil
}

func validateMCPURL(value string) error {
	invalid := errors.New("--mcp-url must be an HTTPS public hostname without credentials, query, fragment, IP literals, or known development/tunnel hosts; this check does not verify a live endpoint")
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || strings.ContainsAny(value, "\\\r\n\t ") {
		return invalid
	}
	for _, character := range u.Path {
		if character < 32 || character == 127 {
			return invalid
		}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "api.openai.com" && strings.HasPrefix(u.Path, "/v1/tunnel") {
		return invalid
	}
	if net.ParseIP(host) != nil || len(host) > 253 || !strings.Contains(host, ".") {
		return invalid
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(label) {
			return invalid
		}
	}
	if !regexp.MustCompile(`^[a-z]{2,63}$`).MatchString(labels[len(labels)-1]) {
		return invalid
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return invalid
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return invalid
	}
	for _, suffix := range []string{"localhost", "local", "internal", "test", "invalid", "home.arpa", "ngrok.io", "ngrok.com", "ngrok.app", "ngrok.dev", "ngrok-free.app", "ngrok-free.dev", "trycloudflare.com", "capysandbox.net", "loca.lt", "localtunnel.me", "localhost.run", "lhr.life", "serveo.net", "devtunnels.ms", "tunnels.ms", "ts.net", "tailscale.net", "nip.io", "sslip.io", "lvh.me", "localtest.me", "github.dev", "gitpod.io", "replit.dev", "pinggy.link", "pinggy.io", "bore.pub", "tunnelmole.net"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return invalid
		}
	}
	for _, label := range labels {
		if slices.Contains([]string{"tunnel", "tunnels", "localhost", "local", "internal", "dev", "staging", "preview"}, label) {
			return invalid
		}
	}
	if strings.HasPrefix(host, "hr-") && strings.HasSuffix(host, ".revyl.ai") {
		return invalid
	}
	return nil
}

func jsonFile(name string, value any) (archiveFile, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	return archiveFile{name: name, content: append(content, '\n')}, err
}

func writeArchive(out string, files []archiveFile) error {
	slices.SortFunc(files, func(a, b archiveFile) int { return strings.Compare(a.name, b.name) })
	var content bytes.Buffer
	writer := zip.NewWriter(&content)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
		mode := os.FileMode(0644)
		if file.name == "scripts/launch-revyl" || file.name == "scripts/launch-runtime" {
			mode = 0755
		}
		header.SetMode(mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := entry.Write(file.content); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	directory, err := os.OpenRoot(filepath.Dir(out))
	if err != nil {
		return errors.New("output directory must already exist and be accessible")
	}
	defer directory.Close()
	temporary := ".revyl-plugin-" + rand.Text() + ".tmp"
	file, err := directory.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("create temporary ZIP in output directory")
	}
	defer directory.Remove(temporary)
	_, writeErr := file.Write(content.Bytes())
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return errors.New("write complete ZIP in output directory")
	}
	if err := directory.Link(temporary, filepath.Base(out)); err != nil {
		return errors.New("publish ZIP without overwriting: choose a new output path on a filesystem supporting hard links")
	}
	return nil
}
