// Package config provides project configuration management.
//
// This package handles reading and writing .revyl/config.yaml files
// and local test definitions in .revyl/tests/.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// ProjectConfig represents the .revyl/config.yaml file.
type ProjectConfig struct {
	// Project contains project identification.
	Project Project `yaml:"project"`

	// Build contains build configuration.
	Build BuildConfig `yaml:"build"`

	// Tests maps test aliases to test IDs.
	Tests map[string]string `yaml:"tests,omitempty"`

	// Workflows maps workflow aliases to workflow IDs.
	Workflows map[string]string `yaml:"workflows,omitempty"`

	// Defaults contains default settings.
	Defaults Defaults `yaml:"defaults,omitempty"`

	// HotReload contains hot reload configuration for rapid development iteration.
	HotReload HotReloadConfig `yaml:"hotreload,omitempty"`

	// LastSyncedAt records when this config was last synced with the server (RFC3339).
	LastSyncedAt string `yaml:"last_synced_at,omitempty"`
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
	// Supported providers: "expo", "swift" (future), "android" (future).
	Providers map[string]*ProviderConfig `yaml:"providers,omitempty"`
}

// ProviderConfig contains configuration for a single hot reload provider.
type ProviderConfig struct {
	// DevClientBuildID is the build version ID of the pre-built development client.
	// Optional: can be specified at runtime via --platform or --build-id flags.
	DevClientBuildID string `yaml:"dev_client_build_id,omitempty"`

	// Port is the port for the dev server (default varies by provider).
	Port int `yaml:"port,omitempty"`

	// Expo-specific fields
	// AppScheme is the app's URL scheme from app.json (e.g., "myapp").
	AppScheme string `yaml:"app_scheme,omitempty"`

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
	// Default ports by provider
	switch providerName {
	case "expo", "android":
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
// Note: DevClientBuildID is optional - it can be specified at runtime via --platform or --build-id.
func (c *HotReloadConfig) validateProviderConfig(name string, cfg *ProviderConfig) error {
	switch name {
	case "expo":
		if cfg.AppScheme == "" {
			return fmt.Errorf("app_scheme is required for Expo")
		}
	case "swift":
		return fmt.Errorf("swift hot reload is not yet supported")
	case "android":
		return fmt.Errorf("android hot reload is not yet supported")
	default:
		return fmt.Errorf("unknown provider: %s (supported: expo)", name)
	}

	return nil
}

// Project contains project identification.
type Project struct {
	// ID is the Revyl project ID (optional).
	ID string `yaml:"id,omitempty"`

	// Name is the project name.
	Name string `yaml:"name"`
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
}

// Defaults contains default settings.
type Defaults struct {
	// OpenBrowser controls whether to open browser after test completion.
	OpenBrowser bool `yaml:"open_browser"`

	// Timeout is the default timeout in seconds.
	Timeout int `yaml:"timeout"`
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

	// Guarantee maps are never nil so callers don't need defensive checks
	if cfg.Tests == nil {
		cfg.Tests = make(map[string]string)
	}
	if cfg.Workflows == nil {
		cfg.Workflows = make(map[string]string)
	}
	if cfg.Build.Platforms == nil {
		cfg.Build.Platforms = make(map[string]BuildPlatform)
	}

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
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Add header comment
	header := "# Revyl CLI Configuration\n# Generated by: revyl init\n\n"
	content := header + string(data)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
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

// TestDefinition contains the actual test definition.
type TestDefinition struct {
	// Metadata contains test metadata.
	Metadata TestMetadata `yaml:"metadata"`

	// Build contains build configuration for this test.
	Build TestBuildConfig `yaml:"build,omitempty"`

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
}

// TestBuildConfig contains build configuration for a test.
type TestBuildConfig struct {
	// Name is the app name.
	Name string `yaml:"name"`

	// PinnedVersion is an optional pinned version.
	PinnedVersion string `yaml:"pinned_version,omitempty"`
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
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}

		path := filepath.Join(testsDir, entry.Name())
		test, err := LoadLocalTest(path)
		if err != nil {
			continue // Skip invalid files
		}

		// Use filename without extension as key
		name := entry.Name()[:len(entry.Name())-5]
		tests[name] = test
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

	// Add header comment
	header := fmt.Sprintf("# Revyl Test Definition\n# Last synced: %s\n\n",
		time.Now().Format(time.RFC3339))
	content := header + string(data)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write test file: %w", err)
	}

	return nil
}
