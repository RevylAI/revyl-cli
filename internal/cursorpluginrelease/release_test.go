package cursorpluginrelease

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var releaseAssetNames = []string{
	"revyl-darwin-amd64",
	"revyl-darwin-arm64",
	"revyl-linux-amd64",
	"revyl-linux-arm64",
	"revyl-windows-amd64.exe",
	"revyl-windows-arm64.exe",
}

// TestPrepareGeneratesPinnedReleaseAndCheckMode verifies the complete release contract.
func TestPrepareGeneratesPinnedReleaseAndCheckMode(t *testing.T) {
	root := t.TempDir()
	pluginRoot, marketplacePath := writeReleaseFixtures(t, root)
	server := newReleaseServer(t, releaseAssetNames)
	defer server.Close()

	input := Input{
		PluginRoot:      pluginRoot,
		MarketplacePath: marketplacePath,
		PluginVersion:   "0.2.0",
		RuntimeVersion:  "9.8.7",
		ReleaseBaseURL:  server.URL,
		HTTPClient:      server.Client(),
	}
	result, err := Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.PreviousPluginVersion != "0.1.0" ||
		result.PreparedPluginVersion != "0.2.0" ||
		result.PreparedRuntimeVersion != "9.8.7" ||
		len(result.ChangedFiles) != 3 {
		t.Fatalf("Prepare() result = %+v", result)
	}

	manifest, _, err := readJSONFile[RuntimeManifest](
		filepath.Join(pluginRoot, runtimeManifestFilename),
	)
	if err != nil {
		t.Fatalf("read prepared runtime manifest: %v", err)
	}
	if !manifest.Prepared ||
		manifest.GeneratedBy != "make -C revyl-cli prepare-cursor-plugin-release" ||
		manifest.PluginVersion != "0.2.0" ||
		manifest.RuntimeVersion != "9.8.7" ||
		manifest.ReleaseBaseURL != server.URL+"/v9.8.7" {
		t.Fatalf("prepared runtime manifest = %+v", manifest)
	}
	for _, asset := range runtimeManifestAssets(manifest) {
		if !validSHA256(asset.SHA256) {
			t.Errorf("asset %s has invalid checksum %q", asset.Name, asset.SHA256)
		}
	}

	preparedPlugin, err := os.ReadFile(filepath.Join(pluginRoot, ".cursor-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("read prepared plugin.json: %v", err)
	}
	if strings.Contains(string(preparedPlugin), `"mcpServers"`) {
		t.Fatal("prepared plugin.json still declares mcpServers")
	}

	input.CheckOnly = true
	checkResult, err := Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare(check) error = %v", err)
	}
	if len(checkResult.ChangedFiles) != 0 {
		t.Fatalf("Prepare(check) changed files = %v", checkResult.ChangedFiles)
	}

	manifestPath := filepath.Join(pluginRoot, runtimeManifestFilename)
	originalManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write drifted manifest: %v", err)
	}
	_, err = Prepare(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "has drift") {
		t.Fatalf("Prepare(check drift) error = %v", err)
	}
	driftedManifest, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatalf("read drifted manifest: %v", readErr)
	}
	if string(driftedManifest) != "{}\n" {
		t.Fatal("check-only preparation mutated the drifted manifest")
	}
	if len(originalManifest) == 0 {
		t.Fatal("generated runtime manifest was empty")
	}
}

func TestNextPluginPatchVersion(t *testing.T) {
	got, err := NextPluginPatchVersion("0.1.1")
	if err != nil {
		t.Fatalf("NextPluginPatchVersion() error = %v", err)
	}
	if got != "0.1.2" {
		t.Fatalf("NextPluginPatchVersion() = %q, want 0.1.2", got)
	}
}

func TestNextPluginMinorAndMajorVersion(t *testing.T) {
	minor, err := NextPluginMinorVersion("0.1.2")
	if err != nil {
		t.Fatalf("NextPluginMinorVersion() error = %v", err)
	}
	if minor != "0.2.0" {
		t.Fatalf("NextPluginMinorVersion() = %q, want 0.2.0", minor)
	}
	major, err := NextPluginMajorVersion("0.1.2")
	if err != nil {
		t.Fatalf("NextPluginMajorVersion() error = %v", err)
	}
	if major != "1.0.0" {
		t.Fatalf("NextPluginMajorVersion() = %q, want 1.0.0", major)
	}
}

func TestPrepareResolvesOmittedVersionsAndCheckDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	pluginRoot, marketplacePath := writeReleaseFixtures(t, root)
	versionPath := filepath.Join(root, "VERSION")
	if err := os.WriteFile(versionPath, []byte("9.8.7\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	server := newReleaseServer(t, releaseAssetNames)
	defer server.Close()

	input := Input{
		PluginRoot:      pluginRoot,
		MarketplacePath: marketplacePath,
		CLIVersionPath:  versionPath,
		ReleaseBaseURL:  server.URL,
		HTTPClient:      server.Client(),
	}
	result, err := Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare(defaults) error = %v", err)
	}
	if result.PreparedPluginVersion != "0.1.1" || result.PreparedRuntimeVersion != "9.8.7" {
		t.Fatalf("Prepare(defaults) result = %+v", result)
	}

	pluginPath := filepath.Join(pluginRoot, ".cursor-plugin", "plugin.json")
	manifestPath := filepath.Join(pluginRoot, runtimeManifestFilename)
	pluginBefore, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read prepared plugin.json: %v", err)
	}
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read prepared runtime manifest: %v", err)
	}

	input.CheckOnly = true
	checkResult, err := Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare(check defaults) error = %v", err)
	}
	if checkResult.PreparedPluginVersion != "0.1.1" ||
		checkResult.PreparedRuntimeVersion != "9.8.7" ||
		len(checkResult.ChangedFiles) != 0 {
		t.Fatalf("Prepare(check defaults) result = %+v", checkResult)
	}
	pluginAfter, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("reread plugin.json: %v", err)
	}
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("reread runtime manifest: %v", err)
	}
	if string(pluginAfter) != string(pluginBefore) || string(manifestAfter) != string(manifestBefore) {
		t.Fatal("CHECK=1 mutated generator-owned files")
	}
}

// TestPrepareRejectsIncompleteRuntimeRelease verifies all platform artifacts are mandatory.
func TestPrepareRejectsIncompleteRuntimeRelease(t *testing.T) {
	root := t.TempDir()
	pluginRoot, marketplacePath := writeReleaseFixtures(t, root)
	server := newReleaseServer(t, releaseAssetNames[:len(releaseAssetNames)-1])
	defer server.Close()

	_, err := Prepare(context.Background(), Input{
		PluginRoot:      pluginRoot,
		MarketplacePath: marketplacePath,
		PluginVersion:   "0.2.0",
		RuntimeVersion:  "9.8.7",
		ReleaseBaseURL:  server.URL,
		HTTPClient:      server.Client(),
	})
	if err == nil ||
		!strings.Contains(err.Error(), "revyl-windows-arm64.exe") {
		t.Fatalf("Prepare(incomplete release) error = %v", err)
	}
}

// TestPrepareRejectsUnpublishedRuntimeRelease tells the operator to bump CLI first.
func TestPrepareRejectsUnpublishedRuntimeRelease(t *testing.T) {
	root := t.TempDir()
	pluginRoot, marketplacePath := writeReleaseFixtures(t, root)
	server := newReleaseServer(t, releaseAssetNames)
	defer server.Close()

	_, err := Prepare(context.Background(), Input{
		PluginRoot:      pluginRoot,
		MarketplacePath: marketplacePath,
		PluginVersion:   "0.2.0",
		RuntimeVersion:  "9.9.9",
		ReleaseBaseURL:  server.URL,
		HTTPClient:      server.Client(),
	})
	if err == nil {
		t.Fatal("Prepare(unpublished runtime) error = nil")
	}
	message := err.Error()
	for _, fragment := range []string{
		"CLI runtime v9.9.9 is not published yet",
		"checksums.txt HTTP 404",
		"./scripts/bump patch --plugin",
	} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("Prepare(unpublished runtime) error = %q, want %q", message, fragment)
		}
	}
}
func TestParseChecksumsRejectsDuplicateAssets(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	_, err := parseChecksums([]byte(
		checksum + "  revyl-linux-amd64\n" +
			checksum + "  revyl-linux-amd64\n",
	))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("parseChecksums(duplicate) error = %v", err)
	}
}

// TestPrepareRejectsMutableReleaseURL verifies latest-style inputs cannot be pinned.
func TestPrepareRejectsMutableReleaseURL(t *testing.T) {
	_, err := normalizeInput(Input{
		PluginRoot:      "cursor-plugin",
		MarketplacePath: ".cursor-plugin/marketplace.json",
		PluginVersion:   "0.2.0",
		RuntimeVersion:  "9.8.7",
		ReleaseBaseURL:  "https://example.test/latest",
		HTTPClient:      http.DefaultClient,
	})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("normalizeInput(mutable URL) error = %v", err)
	}
}

// TestPrepareRejectsRuntimeWithoutFocusedDevProfile prevents incompatible pins.
func TestPrepareRejectsRuntimeWithoutFocusedDevProfile(t *testing.T) {
	_, err := normalizeInput(Input{
		PluginRoot:      "cursor-plugin",
		MarketplacePath: ".cursor-plugin/marketplace.json",
		PluginVersion:   "0.2.0",
		RuntimeVersion:  "0.1.61",
		ReleaseBaseURL:  "https://example.test/releases/download",
		HTTPClient:      http.DefaultClient,
	})
	if err == nil || !strings.Contains(err.Error(), minimumRuntimeVersion) {
		t.Fatalf("normalizeInput(old runtime) error = %v", err)
	}
}

// writeReleaseFixtures creates minimal maintained plugin and Marketplace manifests.
func writeReleaseFixtures(t *testing.T, root string) (string, string) {
	t.Helper()
	pluginRoot := filepath.Join(root, "cursor-plugin")
	pluginManifestDirectory := filepath.Join(pluginRoot, ".cursor-plugin")
	marketplaceDirectory := filepath.Join(root, ".cursor-plugin")
	for _, directory := range []string{pluginManifestDirectory, marketplaceDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create release fixture directory: %v", err)
		}
	}

	plugin := pluginDocument{
		Name:        "revyl",
		DisplayName: "Revyl",
		Description: "Fixture",
		Version:     "0.1.0",
		Author:      pluginAuthor{Name: "Revyl", Email: "support@revyl.ai"},
		Homepage:    "https://revyl.com",
		Repository:  "https://github.com/RevylAI/revyl-cli",
		License:     "MIT",
		Keywords:    []string{"mobile"},
		Logo:        "assets/icon.svg",
		Skills:      "./skills/",
		Rules:       "./rules/",
		Hooks:       "./hooks/hooks.json",
	}
	marketplace := marketplaceDocument{
		Name:  "revyl",
		Owner: marketplaceOwner{Name: "Revyl", Email: "support@revyl.ai"},
		Metadata: marketplaceMetadata{
			Description: "Fixture",
			Version:     "0.1.0",
		},
		Plugins: []marketplacePlugin{{
			Name:        "revyl",
			Source:      "./cursor-plugin",
			Description: "Fixture",
		}},
	}
	writeJSONFixture(
		t,
		filepath.Join(pluginManifestDirectory, "plugin.json"),
		plugin,
	)
	marketplacePath := filepath.Join(marketplaceDirectory, "marketplace.json")
	writeJSONFixture(t, marketplacePath, marketplace)
	return pluginRoot, marketplacePath
}

// writeJSONFixture writes one stable JSON test document.
func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode fixture %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

// newReleaseServer exposes a deterministic checksum file and asset HEAD responses.
func newReleaseServer(t *testing.T, assetNames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/v9.8.7/checksums.txt" &&
			request.Method == http.MethodGet {
			for index, assetName := range assetNames {
				checksumCharacter := string(rune('a' + index))
				_, _ = writer.Write([]byte(
					strings.Repeat(checksumCharacter, 64) +
						"  " + assetName + "\n",
				))
			}
			return
		}
		if request.Method == http.MethodHead {
			requestedAsset := strings.TrimPrefix(
				request.URL.Path,
				"/v9.8.7/",
			)
			for _, assetName := range assetNames {
				if requestedAsset == assetName {
					writer.WriteHeader(http.StatusOK)
					return
				}
			}
		}
		http.NotFound(writer, request)
	}))
}
