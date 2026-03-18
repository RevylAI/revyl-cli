// Package config provides project configuration management.
//
// This package handles reading and writing .revyl/config.yaml files
// and local test definitions in .revyl/tests/.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the .revyl/config.yaml file.
type ProjectConfig struct {
	// Project contains project identification.
	Project Project `yaml:"project"`

	// Build contains build configuration.
	Build BuildConfig `yaml:"build"`

	// Defaults contains default settings.
	Defaults Defaults `yaml:"defaults,omitempty"`

	// HotReload contains hot reload configuration for rapid development iteration.
	HotReload HotReloadConfig `yaml:"hotreload,omitempty"`

	// Publish contains app store publishing configuration.
	Publish PublishConfig `yaml:"publish,omitempty"`

	// LastSyncedAt records when this config was last synced with the server (RFC3339).
	LastSyncedAt string `yaml:"last_synced_at,omitempty"`
}

// PublishConfig contains configuration for app store publishing.
//
// This section is used by `revyl publish` commands to auto-resolve app identifiers,
// target tracks, and TestFlight group assignments.
type PublishConfig struct {
	// IOS contains iOS App Store Connect publishing settings.
	IOS IOSPublishConfig `yaml:"ios,omitempty"`

	// Android contains Google Play publishing settings.
	Android AndroidPublishConfig `yaml:"android,omitempty"`
}

// IOSPublishConfig contains iOS-specific publishing configuration.
type IOSPublishConfig struct {
	// BundleID is the iOS bundle identifier (e.g., "com.nof1.experiments").
	// Auto-detected from app.json or Info.plist if not specified.
	BundleID string `yaml:"bundle_id,omitempty"`

	// ASCAppID is the App Store Connect numeric app ID (e.g., "6758900172").
	// Required for upload operations.
	ASCAppID string `yaml:"asc_app_id,omitempty"`

	// TestFlightGroups is the list of TestFlight group names to distribute to by default.
	TestFlightGroups []string `yaml:"testflight_groups,omitempty"`
}

// AndroidPublishConfig contains Android-specific publishing configuration.
type AndroidPublishConfig struct {
	// PackageName is the Android package name (e.g., "com.nof1.experiments").
	// Auto-detected from app.json or build.gradle if not specified.
	PackageName string `yaml:"package_name,omitempty"`

	// Track is the Google Play release track (internal, alpha, beta, production).
	// Defaults to "internal" if not specified.
	Track string `yaml:"track,omitempty"`
}

// MarkSynced sets the LastSyncedAt timestamp to now (UTC, RFC3339).
func (c *ProjectConfig) MarkSynced() {
	c.LastSyncedAt = time.Now().UTC().Format(time.RFC3339)
}

// HotReloadConfig contains configuration for hot reload mode.
//
// Hot reload enables rapid development iteration by:
//   - Starting a local dev server (Expo, Swift, or Android)
//   - Creating a Cloudflare tunnel to expose it
//   - Running tests against a pre-built dev client
//
// Supports multiple providers for cross-platform projects. Use the Default field
// to specify which provider to use when --provider is not specified, or let the
// CLI auto-select based on project detection confidence.
type HotReloadConfig struct {
	// Default is the default provider to use when --provider is not specified.
	// If empty, auto-selects based on detection confidence.
	Default string `yaml:"default,omitempty"`

	// Providers maps provider names to their configurations.
	// Supported providers: "expo", "react-native", "swift" (future), "android" (future).
	Providers map[string]*ProviderConfig `yaml:"providers,omitempty"`
}

// ProviderConfig contains configuration for a single hot reload provider.
type ProviderConfig struct {
	// Port is the port for the dev server (default varies by provider).
	Port int `yaml:"port,omitempty"`

	// Expo-specific fields
	// AppScheme is the app's URL scheme from app.json (e.g., "myapp").
	AppScheme string `yaml:"app_scheme,omitempty"`

	// PlatformKeys optionally maps OS platform ("ios"/"android") to build.platforms keys.
	// Example: {"ios":"ios-dev","android":"android-dev"}.
	PlatformKeys map[string]string `yaml:"platform_keys,omitempty"`

	// UseExpPrefix controls whether to use the "exp+" prefix in deep links.
	// When true: exp+{scheme}://expo-development-client/?url=...
	// When false: {scheme}://expo-development-client/?url=...
	// Default is false for maximum compatibility with existing builds.
	// Set to true if your dev client was built with addGeneratedScheme: true (Expo SDK 45+).
	UseExpPrefix bool `yaml:"use_exp_prefix,omitempty"`

	// Swift-specific fields
	// BundleID is the iOS bundle identifier.
	BundleID string `yaml:"bundle_id,omitempty"`

	// InjectionPath is the path to InjectionIII.app.
	InjectionPath string `yaml:"injection_path,omitempty"`

	// ProjectPath is the path to the Xcode project file.
	ProjectPath string `yaml:"project_path,omitempty"`

	// Android-specific fields
	// PackageName is the Android package name (e.g., "com.myapp").
	PackageName string `yaml:"package_name,omitempty"`
}

// GetPort returns the port for a provider, with appropriate defaults.
//
// Parameters:
//   - providerName: The provider name
//
// Returns:
//   - int: The configured port or default (8081 for expo/android)
func (c *ProviderConfig) GetPort(providerName string) int {
	if c.Port > 0 {
		return c.Port
	}
	// Default ports by provider (Metro-based providers all default to 8081)
	switch providerName {
	case "expo", "react-native", "android":
		return 8081
	default:
		return 8081
	}
}

// IsConfigured returns true if hot reload is configured.
//
// Returns:
//   - bool: True if hot reload configuration exists
func (c *HotReloadConfig) IsConfigured() bool {
	return len(c.Providers) > 0
}

// GetProviderConfig returns the configuration for a specific provider.
//
// Parameters:
//   - providerName: The provider name ("expo", "swift", "android")
//
// Returns:
//   - *ProviderConfig: The provider configuration, or nil if not found
func (c *HotReloadConfig) GetProviderConfig(providerName string) *ProviderConfig {
	if c.Providers != nil {
		if cfg, ok := c.Providers[providerName]; ok {
			return cfg
		}
	}
	return nil
}

// GetActiveProvider returns the provider name to use based on configuration.
// Priority: explicit provider > default > first configured provider.
//
// Parameters:
//   - explicitProvider: Provider specified via --provider flag (empty if not specified)
//
// Returns:
//   - string: The provider name to use
//   - error: Error if no provider is configured or explicit provider not found
func (c *HotReloadConfig) GetActiveProvider(explicitProvider string) (string, error) {
	// 1. Explicit --provider flag takes priority
	if explicitProvider != "" {
		if c.GetProviderConfig(explicitProvider) != nil {
			return explicitProvider, nil
		}
		return "", fmt.Errorf("provider '%s' is not configured", explicitProvider)
	}

	// 2. Use configured default if set
	if c.Default != "" {
		if c.GetProviderConfig(c.Default) != nil {
			return c.Default, nil
		}
		return "", fmt.Errorf("default provider '%s' is not configured", c.Default)
	}

	// 3. Return first configured provider (caller should use detection for better selection)
	if len(c.Providers) > 0 {
		for name := range c.Providers {
			return name, nil
		}
	}

	return "", fmt.Errorf("no hot reload provider configured")
}

// Validate checks that the hot reload configuration is valid.
//
// Returns:
//   - error: Validation error or nil if valid
func (c *HotReloadConfig) Validate() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("no hot reload providers configured")
	}

	for name, cfg := range c.Providers {
		if err := c.validateProviderConfig(name, cfg); err != nil {
			return fmt.Errorf("hotreload.providers.%s: %w", name, err)
		}
	}
	return nil
}

// ValidateProvider validates configuration for a specific provider.
//
// Parameters:
//   - providerName: The provider name to validate
//
// Returns:
//   - error: Validation error or nil if valid
func (c *HotReloadConfig) ValidateProvider(providerName string) error {
	cfg := c.GetProviderConfig(providerName)
	if cfg == nil {
		return fmt.Errorf("provider '%s' is not configured", providerName)
	}
	return c.validateProviderConfig(providerName, cfg)
}

// validateProviderConfig validates a single provider configuration.
func (c *HotReloadConfig) validateProviderConfig(name string, cfg *ProviderConfig) error {
	switch name {
	case "expo":
		if cfg.AppScheme == "" {
			return fmt.Errorf("app_scheme is required for Expo")
		}
		for targetPlatform, platformKey := range cfg.PlatformKeys {
			normalizedTarget := strings.ToLower(strings.TrimSpace(targetPlatform))
			if normalizedTarget != "ios" && normalizedTarget != "android" {
				return fmt.Errorf("platform_keys.%s must be ios or android", targetPlatform)
			}
			if strings.TrimSpace(platformKey) == "" {
				return fmt.Errorf("platform_keys.%s cannot be empty", targetPlatform)
			}
		}
	case "react-native":
		for targetPlatform, platformKey := range cfg.PlatformKeys {
			normalizedTarget := strings.ToLower(strings.TrimSpace(targetPlatform))
			if normalizedTarget != "ios" && normalizedTarget != "android" {
				return fmt.Errorf("platform_keys.%s must be ios or android", targetPlatform)
			}
			if strings.TrimSpace(platformKey) == "" {
				return fmt.Errorf("platform_keys.%s cannot be empty", targetPlatform)
			}
		}
	case "swift":
		return fmt.Errorf("swift hot reload is not yet supported")
	case "android":
		return fmt.Errorf("android hot reload is not yet supported")
	default:
		return fmt.Errorf("unknown provider: %s (supported: expo, react-native)", name)
	}

	return nil
}

// Project contains project identification.
type Project struct {
	// ID is the Revyl project ID (optional).
	ID string `yaml:"id,omitempty"`

	// Name is the project name.
	Name string `yaml:"name"`

	// OrgID is the organization ID this project is bound to (optional).
	OrgID string `yaml:"org_id,omitempty"`
}

// BuildConfig contains build configuration.
type BuildConfig struct {
	// System is the detected build system (gradle, xcode, expo, flutter, react-native).
	System string `yaml:"system,omitempty"`

	// Command is the build command to run.
	Command string `yaml:"command,omitempty"`

	// Output is the path to the build output artifact.
	Output string `yaml:"output,omitempty"`

	// Platforms contains platform-specific build configurations keyed by platform name
	// (e.g. "ios", "android", "ios-dev").
	Platforms map[string]BuildPlatform `yaml:"platforms,omitempty"`
}

// BuildPlatform represents a platform-specific build configuration.
//
// Each entry in build.platforms maps a key (e.g. "ios", "android", "ios-dev")
// to a build command, output path, and associated Revyl app ID.
type BuildPlatform struct {
	// Command is the build command for this platform.
	Command string `yaml:"command"`

	// Output is the output artifact path for this platform.
	Output string `yaml:"output"`

	// AppID is the Revyl app ID that stores builds for this platform.
	AppID string `yaml:"app_id,omitempty"`

	// Scheme is the Xcode scheme to use for iOS builds.
	// When set, replaces -scheme * in the build command with the specified scheme.
	Scheme string `yaml:"scheme,omitempty"`
}

// Defaults contains default settings.
type Defaults struct {
	// OpenBrowser controls whether to open browser after test completion.
	OpenBrowser *bool `yaml:"open_browser,omitempty"`

	// Timeout is the default timeout in seconds.
	Timeout int `yaml:"timeout,omitempty"`
}

const (
	// DefaultOpenBrowser is the default for defaults.open_browser when omitted.
	DefaultOpenBrowser = true

	// DefaultTimeoutSeconds is the default for defaults.timeout when omitted or invalid.
	DefaultTimeoutSeconds = 600
)

// ApplyDefaults normalizes omitted/invalid project defaults in-place.
//
// Parameters:
//   - cfg: Project config to normalize (no-op when nil)
func ApplyDefaults(cfg *ProjectConfig) {
	if cfg == nil {
		return
	}

	if cfg.Defaults.OpenBrowser == nil {
		open := DefaultOpenBrowser
		cfg.Defaults.OpenBrowser = &open
	}
	if cfg.Defaults.Timeout <= 0 {
		cfg.Defaults.Timeout = DefaultTimeoutSeconds
	}
}

// EffectiveOpenBrowser returns the effective open-browser setting.
//
// Parameters:
//   - cfg: Project config (nil uses default)
//
// Returns:
//   - bool: Effective setting
func EffectiveOpenBrowser(cfg *ProjectConfig) bool {
	if cfg == nil || cfg.Defaults.OpenBrowser == nil {
		return DefaultOpenBrowser
	}
	return *cfg.Defaults.OpenBrowser
}

// EffectiveTimeoutSeconds returns the effective timeout.
//
// Parameters:
//   - cfg: Project config (nil uses fallback)
//   - fallback: Timeout fallback in seconds when config is nil/invalid
//
// Returns:
//   - int: Effective timeout in seconds
func EffectiveTimeoutSeconds(cfg *ProjectConfig, fallback int) int {
	if cfg != nil && cfg.Defaults.Timeout > 0 {
		return cfg.Defaults.Timeout
	}
	if fallback > 0 {
		return fallback
	}
	return DefaultTimeoutSeconds
}

// LoadProjectConfig loads a project configuration from a file.
//
// Parameters:
//   - path: Path to the config.yaml file
//
// Returns:
//   - *ProjectConfig: The loaded configuration
//   - error: Any error that occurred during loading
func LoadProjectConfig(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg ProjectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Build.Platforms == nil {
		cfg.Build.Platforms = make(map[string]BuildPlatform)
	}
	ApplyDefaults(&cfg)

	return &cfg, nil
}

// WriteProjectConfig writes a project configuration to a file.
//
// Parameters:
//   - path: Path to write the config.yaml file
//   - cfg: The configuration to write
//
// Returns:
//   - error: Any error that occurred during writing
func WriteProjectConfig(path string, cfg *ProjectConfig) error {
	ApplyDefaults(cfg)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	header := "# Revyl CLI Configuration\n# Generated by: revyl init\n# Edit these settings or run: revyl config set <key> <value>\n# Docs: https://docs.revyl.ai/cli/hot-reload\n\n"
	content := header + annotateHotReloadConfig(string(data), cfg)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// annotateHotReloadConfig injects inline YAML comments into the serialized
// config to help users understand each hot reload field without reading docs.
func annotateHotReloadConfig(yamlStr string, cfg *ProjectConfig) string {
	if cfg.HotReload.Default == "" {
		return yamlStr
	}

	replacements := map[string]string{
		"hotreload:": "# Dev mode / hot reload configuration\nhotreload:",
	}

	provider := cfg.HotReload.Default
	switch provider {
	case "expo":
		replacements["    app_scheme:"] = "    # URL scheme from app.json or app.config.js (required for Expo deep linking)\n    app_scheme:"
		replacements["    port:"] = "    # Metro bundler port (default 8081). Change if port conflicts.\n    port:"
		replacements["    use_exp_prefix:"] = "    # Use \"exp+\" prefix in deep links. Try true if deep links fail.\n    use_exp_prefix:"
	case "react-native":
		replacements["    port:"] = "    # Metro bundler port (default 8081). Change if port conflicts.\n    port:"
	}

	replacements["    platform_keys:"] = "    # Maps platform to build.platforms key for dev build resolution\n    platform_keys:"

	for old, annotated := range replacements {
		yamlStr = strings.Replace(yamlStr, old, annotated, 1)
	}

	return yamlStr
}

// LocalTest represents a local test definition in .revyl/tests/.
type LocalTest struct {
	// Meta contains sync metadata.
	Meta TestMeta `yaml:"_meta"`

	// Test contains the test definition.
	Test TestDefinition `yaml:"test"`
}

// TestMeta contains sync metadata for a local test.
type TestMeta struct {
	// RemoteID is the test ID on the server.
	RemoteID string `yaml:"remote_id,omitempty"`

	// RemoteVersion is the version on the server at last sync.
	RemoteVersion int `yaml:"remote_version"`

	// LocalVersion is the local version (increments on local edit).
	LocalVersion int `yaml:"local_version"`

	// LastSyncedAt is when the test was last synced.
	LastSyncedAt string `yaml:"last_synced_at,omitempty"`

	// LastSyncedBy is who last synced the test.
	LastSyncedBy string `yaml:"last_synced_by,omitempty"`

	// Checksum is a hash of the test content for change detection.
	Checksum string `yaml:"checksum,omitempty"`
}

// TestDeviceConfig contains device target preferences for the test.
type TestDeviceConfig struct {
	// Model is the target device model name (e.g. "iPhone 15 Pro").
	Model string `yaml:"model,omitempty"`

	// OSVersion is the target OS version (e.g. "17.4").
	OSVersion string `yaml:"os_version,omitempty"`

	// Orientation is portrait or landscape.
	Orientation string `yaml:"orientation,omitempty"`
}

// TestLocation contains initial GPS coordinates for the test.
type TestLocation struct {
	// Latitude is the GPS latitude.
	Latitude float64 `yaml:"latitude"`

	// Longitude is the GPS longitude.
	Longitude float64 `yaml:"longitude"`
}

// TestDefinition contains the actual test definition.
type TestDefinition struct {
	// Metadata contains test metadata.
	Metadata TestMetadata `yaml:"metadata"`

	// Build contains build configuration for this test.
	Build TestBuildConfig `yaml:"build,omitempty"`

	// Device contains device target preferences.
	Device *TestDeviceConfig `yaml:"device,omitempty"`

	// Variables contains custom test variables (key-value pairs).
	Variables map[string]string `yaml:"variables,omitempty"`

	// EnvVars contains app launch environment variables.
	EnvVars map[string]string `yaml:"env_vars,omitempty"`

	// Location contains initial GPS coordinates.
	Location *TestLocation `yaml:"location,omitempty"`

	// Blocks contains the test steps.
	Blocks []TestBlock `yaml:"blocks"`
}

// TestMetadata contains test metadata.
type TestMetadata struct {
	// Name is the test name.
	Name string `yaml:"name"`

	// Platform is the target platform (ios, android).
	Platform string `yaml:"platform,omitempty"`

	// Description is an optional test description.
	Description string `yaml:"description,omitempty"`

	// Tags is an optional list of tag names associated with this test.
	Tags []string `yaml:"tags,omitempty"`
}

// TestBuildConfig contains build configuration for a test.
type TestBuildConfig struct {
	// Name is the app name.
	Name string `yaml:"name"`

	// PinnedVersion is an optional pinned version.
	PinnedVersion string `yaml:"pinned_version,omitempty"`

	// SystemPrompt is the app-level system prompt for the LLM agent.
	SystemPrompt string `yaml:"system_prompt,omitempty"`
}

// TestBlock represents a test step/block.
type TestBlock struct {
	// ID is the block ID (optional).
	ID string `yaml:"id,omitempty" json:"id,omitempty"`

	// Type is the block type (instructions, validation, if, while).
	Type string `yaml:"type" json:"type"`

	// StepType is the step type (instruction, validation, etc.).
	StepType string `yaml:"step_type,omitempty" json:"step_type,omitempty"`

	// StepDescription is the step description/instruction.
	StepDescription string `yaml:"step_description,omitempty" json:"step_description,omitempty"`

	// Condition is the condition for if/while blocks.
	Condition string `yaml:"condition,omitempty" json:"condition,omitempty"`

	// Then contains blocks for the then branch (if blocks).
	Then []TestBlock `yaml:"then,omitempty" json:"then,omitempty"`

	// Else contains blocks for the else branch (if blocks).
	Else []TestBlock `yaml:"else,omitempty" json:"else,omitempty"`

	// Body contains blocks for the loop body (while blocks).
	Body []TestBlock `yaml:"body,omitempty" json:"body,omitempty"`

	// VariableName is the variable name for extraction blocks.
	VariableName string `yaml:"variable_name,omitempty" json:"variable_name,omitempty"`

	// ModuleID is the module UUID for module_import blocks.
	ModuleID string `yaml:"module_id,omitempty" json:"module_id,omitempty"`

	// Script is the human-readable script name for code_execution blocks.
	// Resolved to a UUID (step_description) on push/sync.
	Script string `yaml:"script,omitempty" json:"script,omitempty"`

	// Module is the human-readable module name for module_import blocks.
	// Resolved to a UUID (module_id) on push/sync.
	Module string `yaml:"module,omitempty" json:"module,omitempty"`
}

// ComputeTestChecksum computes a SHA-256 checksum of the test definition.
//
// This function serializes the test definition to YAML and computes a hash,
// which is used to detect local modifications to test files.
//
// Parameters:
//   - test: The test definition to compute checksum for
//
// Returns:
//   - string: Hex-encoded SHA-256 checksum, or empty string on error
func ComputeTestChecksum(test *TestDefinition) string {
	if test == nil {
		return ""
	}

	data, err := yaml.Marshal(test)
	if err != nil {
		return ""
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// HasLocalChanges returns true if the test content differs from the stored checksum.
//
// This method compares the current content checksum against the stored checksum
// to detect if the user has modified the test file since the last sync.
//
// Returns:
//   - bool: True if content has changed, false if unchanged or no checksum stored
func (t *LocalTest) HasLocalChanges() bool {
	if t.Meta.Checksum == "" {
		// No checksum stored, assume no changes (legacy file or new test)
		return false
	}

	currentChecksum := ComputeTestChecksum(&t.Test)
	return currentChecksum != t.Meta.Checksum
}

// CountLinkedTests returns the number of local YAML test files that have a
// non-empty _meta.remote_id (i.e. are linked to a remote test).
// Returns 0 if the directory does not exist or is empty.
//
// Parameters:
//   - testsDir: Path to the .revyl/tests/ directory
//
// Returns:
//   - int: Number of linked tests
func CountLinkedTests(testsDir string) int {
	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return 0
	}

	count := 0
	for _, entry := range entries {
		if testAliasFromFilename(entry) == "" {
			continue
		}
		path := filepath.Join(testsDir, entry.Name())
		lt, err := LoadLocalTest(path)
		if err == nil && lt.Meta.RemoteID != "" {
			count++
		}
	}
	return count
}

// GetLocalTestRemoteID loads a single local test YAML by alias name and returns
// its _meta.remote_id. Returns ("", nil) if the file does not exist or has no
// remote_id. Returns a non-nil error only on parse failures.
//
// Parameters:
//   - testsDir: Path to the .revyl/tests/ directory
//   - alias: Test alias (filename without .yaml extension)
//
// Returns:
//   - string: The remote test UUID, or empty string
//   - error: Parse error (nil if file missing or has no remote_id)
func GetLocalTestRemoteID(testsDir, alias string) (string, error) {
	path := filepath.Join(testsDir, alias+".yaml")
	lt, err := LoadLocalTest(path)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return lt.Meta.RemoteID, nil
}

// testAliasFromFilename extracts a test alias from a directory entry,
// returning "" for entries that are not valid test YAML files (directories,
// non-.yaml extensions, or bare ".yaml" with no stem).
func testAliasFromFilename(entry os.DirEntry) string {
	if entry.IsDir() {
		return ""
	}
	name := entry.Name()
	if filepath.Ext(name) != ".yaml" || len(name) <= 5 {
		return ""
	}
	return name[:len(name)-5]
}

// ListLocalTestAliases returns all test aliases (filenames without .yaml) from the tests directory.
// Returns nil if the directory does not exist.
//
// Parameters:
//   - testsDir: Path to the .revyl/tests/ directory
//
// Returns:
//   - []string: Sorted list of test aliases
func ListLocalTestAliases(testsDir string) []string {
	entries, err := os.ReadDir(testsDir)
	if err != nil {
		return nil
	}

	var aliases []string
	for _, entry := range entries {
		if alias := testAliasFromFilename(entry); alias != "" {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	return aliases
}

// LoadLocalTests loads all local test definitions from a directory.
//
// Parameters:
//   - testsDir: Path to the .revyl/tests/ directory
//
// Returns:
//   - map[string]*LocalTest: Map of test name to test definition
//   - error: Any error that occurred during loading
func LoadLocalTests(testsDir string) (map[string]*LocalTest, error) {
	tests := make(map[string]*LocalTest)

	entries, err := os.ReadDir(testsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return tests, nil
		}
		return nil, fmt.Errorf("failed to read tests directory: %w", err)
	}

	for _, entry := range entries {
		alias := testAliasFromFilename(entry)
		if alias == "" {
			continue
		}

		path := filepath.Join(testsDir, entry.Name())
		test, err := LoadLocalTest(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping unparseable test file %s: %v\n", entry.Name(), err)
			continue
		}

		tests[alias] = test
	}

	return tests, nil
}

// LoadLocalTest loads a single local test definition.
//
// Parameters:
//   - path: Path to the test YAML file
//
// Returns:
//   - *LocalTest: The loaded test definition
//   - error: Any error that occurred during loading
func LoadLocalTest(path string) (*LocalTest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read test file: %w", err)
	}

	var test LocalTest
	if err := yaml.Unmarshal(data, &test); err != nil {
		return nil, fmt.Errorf("failed to parse test file: %w", err)
	}

	return &test, nil
}

// SaveLocalTest saves a local test definition.
//
// This function computes and stores a checksum of the test content before saving,
// which is used to detect local modifications on subsequent loads.
//
// Parameters:
//   - path: Path to save the test YAML file
//   - test: The test definition to save
//
// Returns:
//   - error: Any error that occurred during saving
func SaveLocalTest(path string, test *LocalTest) error {
	// Compute and store checksum of test content before saving
	test.Meta.Checksum = ComputeTestChecksum(&test.Test)

	data, err := yaml.Marshal(test)
	if err != nil {
		return fmt.Errorf("failed to marshal test: %w", err)
	}

	header := "# Revyl Test Definition\n\n"
	content := header + string(data)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write test file: %w", err)
	}

	return nil
}
