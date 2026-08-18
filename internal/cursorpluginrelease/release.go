// Package cursorpluginrelease prepares immutable Cursor plugin runtime pins.
package cursorpluginrelease

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	runtimeManifestFilename = "runtime-manifest.json"
	maxChecksumBytes        = 1024 * 1024
	minimumRuntimeVersion   = "0.1.63"
)

var semanticVersionPattern = regexp.MustCompile(
	`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`,
)

// Input contains every explicit release preparation dependency.
type Input struct {
	PluginRoot      string
	MarketplacePath string
	PluginVersion   string
	RuntimeVersion  string
	CLIVersionPath  string
	ReleaseBaseURL  string
	Bump            string
	CheckOnly       bool
	HTTPClient      *http.Client
}

// Result summarizes one deterministic preparation.
type Result struct {
	PreviousPluginVersion  string
	PreparedPluginVersion  string
	PreparedRuntimeVersion string
	ChangedFiles           []string
}

// RuntimeManifest is the flat cross-shell bootstrap contract.
type RuntimeManifest struct {
	SchemaVersion      int    `json:"schema_version"`
	GeneratedBy        string `json:"generated_by"`
	Prepared           bool   `json:"prepared"`
	PluginVersion      string `json:"plugin_version"`
	RuntimeVersion     string `json:"runtime_version"`
	ReleaseTag         string `json:"release_tag"`
	ReleaseBaseURL     string `json:"release_base_url"`
	DarwinAMD64Asset   string `json:"darwin_amd64_asset"`
	DarwinAMD64SHA256  string `json:"darwin_amd64_sha256"`
	DarwinARM64Asset   string `json:"darwin_arm64_asset"`
	DarwinARM64SHA256  string `json:"darwin_arm64_sha256"`
	LinuxAMD64Asset    string `json:"linux_amd64_asset"`
	LinuxAMD64SHA256   string `json:"linux_amd64_sha256"`
	LinuxARM64Asset    string `json:"linux_arm64_asset"`
	LinuxARM64SHA256   string `json:"linux_arm64_sha256"`
	WindowsAMD64Asset  string `json:"windows_amd64_asset"`
	WindowsAMD64SHA256 string `json:"windows_amd64_sha256"`
	WindowsARM64Asset  string `json:"windows_arm64_asset"`
	WindowsARM64SHA256 string `json:"windows_arm64_sha256"`
}

type releaseAsset struct {
	Name   string
	SHA256 string
}

type pluginAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type pluginDocument struct {
	Name        string       `json:"name"`
	DisplayName string       `json:"displayName"`
	Description string       `json:"description"`
	Version     string       `json:"version"`
	Author      pluginAuthor `json:"author"`
	Homepage    string       `json:"homepage"`
	Repository  string       `json:"repository"`
	License     string       `json:"license"`
	Keywords    []string     `json:"keywords"`
	Logo        string       `json:"logo"`
	Skills      string       `json:"skills"`
	Rules       string       `json:"rules"`
	Hooks       string       `json:"hooks"`
	MCPServers  string       `json:"mcpServers,omitempty"`
}

type marketplaceOwner struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type marketplaceMetadata struct {
	Description string `json:"description"`
	Version     string `json:"version"`
}

type marketplacePlugin struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
}

type marketplaceDocument struct {
	Name     string              `json:"name"`
	Owner    marketplaceOwner    `json:"owner"`
	Metadata marketplaceMetadata `json:"metadata"`
	Plugins  []marketplacePlugin `json:"plugins"`
}

type preparedFile struct {
	Path    string
	Content []byte
	Mode    os.FileMode
}

// Prepare validates a published CLI release and updates the plugin pin atomically.
//
// Parameters:
//   - ctx: Cancellation context for bounded release asset requests.
//   - input: Explicit paths, versions, network client, and check-only mode.
//
// Returns:
//   - Result: Previous/new version mapping and changed files.
//   - error: Validation, network, drift, decoding, or atomic write failure.
func Prepare(ctx context.Context, input Input) (Result, error) {
	normalizedInput, err := resolveOmittedVersions(input)
	if err != nil {
		return Result{}, err
	}
	normalizedInput, err = normalizeInput(normalizedInput)
	if err != nil {
		return Result{}, err
	}

	pluginPath := filepath.Join(
		normalizedInput.PluginRoot,
		".cursor-plugin",
		"plugin.json",
	)
	plugin, pluginMode, err := readJSONFile[pluginDocument](pluginPath)
	if err != nil {
		return Result{}, fmt.Errorf("read plugin manifest: %w", err)
	}
	marketplace, marketplaceMode, err := readJSONFile[marketplaceDocument](
		normalizedInput.MarketplacePath,
	)
	if err != nil {
		return Result{}, fmt.Errorf("read marketplace manifest: %w", err)
	}
	if plugin.Name != "revyl" || marketplace.Name != "revyl" {
		return Result{}, errors.New("plugin and marketplace manifests must describe Revyl")
	}
	if len(marketplace.Plugins) != 1 ||
		marketplace.Plugins[0].Name != plugin.Name {
		return Result{}, errors.New("marketplace must contain exactly the Revyl plugin")
	}

	releaseTag := "v" + normalizedInput.RuntimeVersion
	releaseURL := strings.TrimRight(normalizedInput.ReleaseBaseURL, "/") + "/" + releaseTag
	assets, err := fetchReleaseAssets(
		ctx,
		normalizedInput.HTTPClient,
		releaseURL,
	)
	if err != nil {
		return Result{}, unpublishedRuntimePinError(
			normalizedInput.RuntimeVersion,
			err,
		)
	}
	runtimeManifest, err := buildRuntimeManifest(
		normalizedInput.PluginVersion,
		normalizedInput.RuntimeVersion,
		releaseURL,
		assets,
	)
	if err != nil {
		return Result{}, err
	}
	if err := verifyReleaseAssets(
		ctx,
		normalizedInput.HTTPClient,
		releaseURL,
		runtimeManifestAssets(runtimeManifest),
	); err != nil {
		return Result{}, err
	}

	previousPluginVersion := plugin.Version
	plugin.Version = normalizedInput.PluginVersion
	marketplace.Metadata.Version = normalizedInput.PluginVersion

	pluginContent, err := marshalDocument(plugin)
	if err != nil {
		return Result{}, fmt.Errorf("encode plugin manifest: %w", err)
	}
	marketplaceContent, err := marshalDocument(marketplace)
	if err != nil {
		return Result{}, fmt.Errorf("encode marketplace manifest: %w", err)
	}
	runtimeContent, err := marshalDocument(runtimeManifest)
	if err != nil {
		return Result{}, fmt.Errorf("encode runtime manifest: %w", err)
	}

	files := []preparedFile{
		{Path: pluginPath, Content: pluginContent, Mode: pluginMode},
		{
			Path:    normalizedInput.MarketplacePath,
			Content: marketplaceContent,
			Mode:    marketplaceMode,
		},
		{
			Path: filepath.Join(
				normalizedInput.PluginRoot,
				runtimeManifestFilename,
			),
			Content: runtimeContent,
			Mode:    0o644,
		},
	}
	result := Result{
		PreviousPluginVersion:  previousPluginVersion,
		PreparedPluginVersion:  normalizedInput.PluginVersion,
		PreparedRuntimeVersion: normalizedInput.RuntimeVersion,
	}
	for _, file := range files {
		matches, compareErr := fileMatches(file.Path, file.Content)
		if compareErr != nil {
			return Result{}, compareErr
		}
		if !matches {
			result.ChangedFiles = append(result.ChangedFiles, file.Path)
		}
	}

	if normalizedInput.CheckOnly {
		if len(result.ChangedFiles) > 0 {
			return result, fmt.Errorf(
				"Cursor plugin release metadata has drift: %s",
				strings.Join(result.ChangedFiles, ", "),
			)
		}
		return result, nil
	}

	for _, file := range files {
		if err := writeFileAtomically(file); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

// resolveOmittedVersions fills empty plugin/runtime versions from maintained files.
//
// Parameters:
//   - input: Caller-supplied release input. Empty PluginVersion or RuntimeVersion
//     are replaced from plugin.json and the CLI VERSION file.
//
// Returns:
//   - Input: The same input with resolved semantic versions.
//   - error: Missing files, invalid current versions, or read failures.
func resolveOmittedVersions(input Input) (Input, error) {
	if strings.TrimSpace(input.PluginVersion) == "" {
		pluginPath := filepath.Join(
			filepath.Clean(strings.TrimSpace(input.PluginRoot)),
			".cursor-plugin",
			"plugin.json",
		)
		plugin, _, err := readJSONFile[pluginDocument](pluginPath)
		if err != nil {
			return Input{}, fmt.Errorf("read plugin version for default: %w", err)
		}
		if input.CheckOnly {
			input.PluginVersion = plugin.Version
		} else {
			nextVersion, err := NextPluginVersion(plugin.Version, input.Bump)
			if err != nil {
				return Input{}, err
			}
			input.PluginVersion = nextVersion
		}
	}
	if strings.TrimSpace(input.RuntimeVersion) == "" {
		versionPath := strings.TrimSpace(input.CLIVersionPath)
		if versionPath == "" {
			versionPath = "VERSION"
		}
		runtimeVersion, err := ReadCLIVersion(versionPath)
		if err != nil {
			return Input{}, err
		}
		input.RuntimeVersion = runtimeVersion
	}
	return input, nil
}

// ReadCLIVersion returns the published CLI version from a VERSION file.
//
// Parameters:
//   - path: Path to the CLI VERSION file.
//
// Returns:
//   - string: Trimmed semantic version without a leading v.
//   - error: Read failure or an invalid version string.
func ReadCLIVersion(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read CLI VERSION: %w", err)
	}
	version := strings.TrimPrefix(strings.TrimSpace(string(content)), "v")
	if !semanticVersionPattern.MatchString(version) {
		return "", fmt.Errorf("invalid CLI VERSION %q", version)
	}
	return version, nil
}

// NextPluginPatchVersion increments the patch component of a semantic version.
//
// Parameters:
//   - current: The current plugin.json version, optionally with a prerelease suffix.
//
// Returns:
//   - string: major.minor.(patch+1), with any prerelease suffix dropped.
//   - error: An unparseable current version.
func NextPluginPatchVersion(current string) (string, error) {
	major, minor, patch, err := pluginVersionParts(current)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch+1), nil
}

// NextPluginMinorVersion increments the minor component of a semantic version.
//
// Parameters:
//   - current: The current plugin.json version, optionally with a prerelease suffix.
//
// Returns:
//   - string: major.(minor+1).0, with any prerelease suffix dropped.
//   - error: An unparseable current version.
func NextPluginMinorVersion(current string) (string, error) {
	major, minor, _, err := pluginVersionParts(current)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.0", major, minor+1), nil
}

// NextPluginMajorVersion increments the major component of a semantic version.
//
// Parameters:
//   - current: The current plugin.json version, optionally with a prerelease suffix.
//
// Returns:
//   - string: (major+1).0.0, with any prerelease suffix dropped.
//   - error: An unparseable current version.
func NextPluginMajorVersion(current string) (string, error) {
	major, _, _, err := pluginVersionParts(current)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.0.0", major+1), nil
}

// pluginVersionParts parses major, minor, and patch from a semantic version.
//
// Parameters:
//   - current: The plugin version string, optionally prefixed with v or a prerelease suffix.
//
// Returns:
//   - int: Major version.
//   - int: Minor version.
//   - int: Patch version.
//   - error: An unparseable current version.
func pluginVersionParts(current string) (int, int, int, error) {
	current = strings.TrimPrefix(strings.TrimSpace(current), "v")
	if !semanticVersionPattern.MatchString(current) {
		return 0, 0, 0, fmt.Errorf("invalid plugin version %q", current)
	}
	core, _, _ := strings.Cut(current, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid plugin version %q", current)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid plugin major in %q: %w", current, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid plugin minor in %q: %w", current, err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid plugin patch in %q: %w", current, err)
	}
	return major, minor, patch, nil
}

// normalizeInput validates required versions, paths, and dependencies.
func normalizeInput(input Input) (Input, error) {
	input.PluginRoot = filepath.Clean(strings.TrimSpace(input.PluginRoot))
	input.MarketplacePath = filepath.Clean(strings.TrimSpace(input.MarketplacePath))
	input.PluginVersion = strings.TrimPrefix(
		strings.TrimSpace(input.PluginVersion),
		"v",
	)
	input.RuntimeVersion = strings.TrimPrefix(
		strings.TrimSpace(input.RuntimeVersion),
		"v",
	)
	input.ReleaseBaseURL = strings.TrimRight(
		strings.TrimSpace(input.ReleaseBaseURL),
		"/",
	)
	if input.PluginRoot == "." || input.PluginRoot == "" {
		return Input{}, errors.New("plugin root is required")
	}
	if input.MarketplacePath == "." || input.MarketplacePath == "" {
		return Input{}, errors.New("marketplace path is required")
	}
	if !semanticVersionPattern.MatchString(input.PluginVersion) {
		return Input{}, fmt.Errorf(
			"invalid plugin version %q",
			input.PluginVersion,
		)
	}
	if !semanticVersionPattern.MatchString(input.RuntimeVersion) {
		return Input{}, fmt.Errorf(
			"invalid runtime version %q",
			input.RuntimeVersion,
		)
	}
	if !versionAtLeast(input.RuntimeVersion, minimumRuntimeVersion) {
		return Input{}, fmt.Errorf(
			"runtime version %s predates focused dev profile support in %s",
			input.RuntimeVersion,
			minimumRuntimeVersion,
		)
	}
	if input.ReleaseBaseURL == "" ||
		strings.Contains(input.ReleaseBaseURL, "/latest") {
		return Input{}, errors.New("immutable release base URL is required")
	}
	if input.HTTPClient == nil {
		return Input{}, errors.New("HTTP client is required")
	}
	return input, nil
}

// checksumHTTPError is a non-OK response when fetching checksums.txt.
type checksumHTTPError struct {
	StatusCode int
}

// Error names the checksum fetch status without echoing the URL.
func (e *checksumHTTPError) Error() string {
	return fmt.Sprintf("fetch runtime checksums: HTTP %d", e.StatusCode)
}

// unpublishedRuntimePinError tells the operator to publish the CLI before pinning.
//
// Parameters:
//   - runtimeVersion: CLI semver the plugin tried to pin.
//   - err: Error from fetchReleaseAssets.
//
// Returns:
//   - error: Actionable copy for missing checksums, otherwise err unchanged.
func unpublishedRuntimePinError(runtimeVersion string, err error) error {
	var httpErr *checksumHTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	return fmt.Errorf(
		"CLI runtime v%s is not published yet (checksums.txt HTTP %d); run ./scripts/bump patch, wait for https://github.com/RevylAI/revyl-cli/releases/download/v%s/checksums.txt, then retry ./scripts/bump patch --plugin",
		runtimeVersion,
		httpErr.StatusCode,
		runtimeVersion,
	)
}

// fetchReleaseAssets downloads and parses the canonical checksum manifest.
func fetchReleaseAssets(
	ctx context.Context,
	client *http.Client,
	releaseURL string,
) ([]releaseAsset, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		releaseURL+"/checksums.txt",
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create checksum request: %w", err)
	}
	request.Header.Set("User-Agent", "revyl-cursor-plugin-release")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch runtime checksums: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &checksumHTTPError{StatusCode: response.StatusCode}
	}

	limitedBody := io.LimitReader(response.Body, maxChecksumBytes+1)
	content, err := io.ReadAll(limitedBody)
	if err != nil {
		return nil, fmt.Errorf("read runtime checksums: %w", err)
	}
	if len(content) == 0 || len(content) > maxChecksumBytes {
		return nil, errors.New("runtime checksum manifest is empty or too large")
	}
	return parseChecksums(content)
}

// parseChecksums decodes strict SHA256 and filename pairs without ambiguity.
func parseChecksums(content []byte) ([]releaseAsset, error) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	var assets []releaseAsset
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, fmt.Errorf(
				"invalid checksum line %d",
				lineNumber,
			)
		}
		checksum := strings.ToLower(strings.TrimPrefix(fields[0], "*"))
		name := strings.TrimPrefix(fields[1], "*")
		if !validSHA256(checksum) || filepath.Base(name) != name {
			return nil, fmt.Errorf(
				"invalid checksum entry on line %d",
				lineNumber,
			)
		}
		for _, existing := range assets {
			if existing.Name == name {
				return nil, fmt.Errorf(
					"duplicate checksum entry for %s",
					name,
				)
			}
		}
		assets = append(assets, releaseAsset{Name: name, SHA256: checksum})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan runtime checksums: %w", err)
	}
	return assets, nil
}

// buildRuntimeManifest maps the required release matrix into the bootstrap contract.
func buildRuntimeManifest(
	pluginVersion string,
	runtimeVersion string,
	releaseURL string,
	assets []releaseAsset,
) (RuntimeManifest, error) {
	darwinAMD64, err := requireReleaseAsset(assets, "revyl-darwin-amd64")
	if err != nil {
		return RuntimeManifest{}, err
	}
	darwinARM64, err := requireReleaseAsset(assets, "revyl-darwin-arm64")
	if err != nil {
		return RuntimeManifest{}, err
	}
	linuxAMD64, err := requireReleaseAsset(assets, "revyl-linux-amd64")
	if err != nil {
		return RuntimeManifest{}, err
	}
	linuxARM64, err := requireReleaseAsset(assets, "revyl-linux-arm64")
	if err != nil {
		return RuntimeManifest{}, err
	}
	windowsAMD64, err := requireReleaseAsset(
		assets,
		"revyl-windows-amd64.exe",
	)
	if err != nil {
		return RuntimeManifest{}, err
	}
	windowsARM64, err := requireReleaseAsset(
		assets,
		"revyl-windows-arm64.exe",
	)
	if err != nil {
		return RuntimeManifest{}, err
	}

	return RuntimeManifest{
		SchemaVersion:      1,
		GeneratedBy:        "make -C revyl-cli prepare-cursor-plugin-release",
		Prepared:           true,
		PluginVersion:      pluginVersion,
		RuntimeVersion:     runtimeVersion,
		ReleaseTag:         "v" + runtimeVersion,
		ReleaseBaseURL:     releaseURL,
		DarwinAMD64Asset:   darwinAMD64.Name,
		DarwinAMD64SHA256:  darwinAMD64.SHA256,
		DarwinARM64Asset:   darwinARM64.Name,
		DarwinARM64SHA256:  darwinARM64.SHA256,
		LinuxAMD64Asset:    linuxAMD64.Name,
		LinuxAMD64SHA256:   linuxAMD64.SHA256,
		LinuxARM64Asset:    linuxARM64.Name,
		LinuxARM64SHA256:   linuxARM64.SHA256,
		WindowsAMD64Asset:  windowsAMD64.Name,
		WindowsAMD64SHA256: windowsAMD64.SHA256,
		WindowsARM64Asset:  windowsARM64.Name,
		WindowsARM64SHA256: windowsARM64.SHA256,
	}, nil
}

// requireReleaseAsset resolves one exact required asset.
func requireReleaseAsset(
	assets []releaseAsset,
	name string,
) (releaseAsset, error) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("runtime release is missing %s", name)
}

// runtimeManifestAssets returns the fixed supported release matrix.
func runtimeManifestAssets(manifest RuntimeManifest) []releaseAsset {
	return []releaseAsset{
		{Name: manifest.DarwinAMD64Asset, SHA256: manifest.DarwinAMD64SHA256},
		{Name: manifest.DarwinARM64Asset, SHA256: manifest.DarwinARM64SHA256},
		{Name: manifest.LinuxAMD64Asset, SHA256: manifest.LinuxAMD64SHA256},
		{Name: manifest.LinuxARM64Asset, SHA256: manifest.LinuxARM64SHA256},
		{Name: manifest.WindowsAMD64Asset, SHA256: manifest.WindowsAMD64SHA256},
		{Name: manifest.WindowsARM64Asset, SHA256: manifest.WindowsARM64SHA256},
	}
}

// verifyReleaseAssets confirms every checksum entry has a downloadable artifact.
func verifyReleaseAssets(
	ctx context.Context,
	client *http.Client,
	releaseURL string,
	assets []releaseAsset,
) error {
	for _, asset := range assets {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodHead,
			releaseURL+"/"+asset.Name,
			nil,
		)
		if err != nil {
			return fmt.Errorf("create asset request for %s: %w", asset.Name, err)
		}
		request.Header.Set("User-Agent", "revyl-cursor-plugin-release")
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("verify runtime asset %s: %w", asset.Name, err)
		}
		_ = response.Body.Close()
		if response.StatusCode < http.StatusOK ||
			response.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf(
				"verify runtime asset %s: HTTP %d",
				asset.Name,
				response.StatusCode,
			)
		}
	}
	return nil
}

// readJSONFile decodes one maintained JSON document and preserves its mode.
func readJSONFile[T any](path string) (T, os.FileMode, error) {
	var result T
	info, err := os.Stat(path)
	if err != nil {
		return result, 0, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return result, 0, err
	}
	if err := json.Unmarshal(content, &result); err != nil {
		return result, 0, err
	}
	return result, info.Mode().Perm(), nil
}

// marshalDocument renders stable, newline-terminated JSON.
func marshalDocument(value any) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

// fileMatches compares one generated document without mutating it.
func fileMatches(path string, expected []byte) (bool, error) {
	actual, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read generated file %s: %w", path, err)
	}
	return bytes.Equal(actual, expected), nil
}

// writeFileAtomically writes one generated document through a same-directory rename.
func writeFileAtomically(file preparedFile) error {
	directory := filepath.Dir(file.Path)
	temporary, err := os.CreateTemp(directory, ".cursor-plugin-release-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", file.Path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if _, err := temporary.Write(file.Content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file for %s: %w", file.Path, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary file for %s: %w", file.Path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", file.Path, err)
	}
	mode := file.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		return fmt.Errorf("set mode for %s: %w", file.Path, err)
	}
	if err := os.Rename(temporaryPath, file.Path); err != nil {
		return fmt.Errorf("replace generated file %s: %w", file.Path, err)
	}
	return nil
}

// validSHA256 reports whether text is one lowercase 64-character digest.
func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// versionAtLeast compares the numeric core of two validated semantic versions.
func versionAtLeast(actual string, minimum string) bool {
	actualParts := semanticVersionCore(actual)
	minimumParts := semanticVersionCore(minimum)
	for index := range actualParts {
		if actualParts[index] > minimumParts[index] {
			return true
		}
		if actualParts[index] < minimumParts[index] {
			return false
		}
	}
	return true
}

// semanticVersionCore returns the major, minor, and patch integers.
func semanticVersionCore(version string) [3]int {
	core := strings.SplitN(version, "-", 2)[0]
	segments := strings.Split(core, ".")
	var result [3]int
	for index := range result {
		if index >= len(segments) {
			break
		}
		result[index], _ = strconv.Atoi(segments[index])
	}
	return result
}
