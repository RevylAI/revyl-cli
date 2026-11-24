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

	"github.com/revyl/cli/internal/util"
	"gopkg.in/yaml.v3"
)

type ProjectConfig struct {
	Project Project `yaml:"project"`

	Build BuildConfig `yaml:"build"`

	Defaults Defaults `yaml:"defaults,omitempty"`

	HotReload HotReloadConfig `yaml:"hotreload,omitempty"`

	// PRReview contains GitHub PR automation config-as-code (the pr_review section).
	// When committed to the default branch, Revyl reconciles it into the repo's
	// PR automation. Nil/omitted means PR automation is managed in the Revyl UI.
	PRReview *PRReviewConfig `yaml:"pr_review,omitempty"`

	// BeforeSession contains the repository-local setup step run before every
	// device session starts. It produces the values auth_bypass consumes.
	BeforeSession *BeforeSessionConfig `yaml:"before_session,omitempty"`

	// AuthBypass holds the legacy test-only auth bypass config; dev/device starts
	// apply launch vars and attempt the deep link best-effort without aborting.
	AuthBypass *AuthBypassConfig `yaml:"auth_bypass,omitempty"`

	LastSyncedAt string `yaml:"last_synced_at,omitempty"`

	// Deprecated: legacy alias-to-UUID maps migrated explicitly via config migrate.
	Tests     map[string]string `yaml:"tests,omitempty"`
	Workflows map[string]string `yaml:"workflows,omitempty"`
}

func (c *ProjectConfig) MarkSynced() {
	c.LastSyncedAt = time.Now().UTC().Format(time.RFC3339)
}

const DefaultBeforeSessionTimeoutSeconds = 120

// BeforeSessionConfig declares a repository-local setup script run before
// device sessions start. Stdout KEY=VALUE lines become session-scoped launch
// values (non-KEY=VALUE prose is ignored, but empty values or duplicate keys
// fail). Unlike auth_bypass, a failing before_session script is fatal.
type BeforeSessionConfig struct {
	// Script is resolved relative to the project root and must stay contained
	// within the discovered Git worktree root (or project root fallback).
	Script string `yaml:"script,omitempty"`

	TimeoutSeconds int `yaml:"timeout_seconds,omitempty"`
}

func (b *BeforeSessionConfig) IsConfigured() bool {
	if b == nil {
		return false
	}
	return strings.TrimSpace(b.Script) != ""
}

func (b *BeforeSessionConfig) EffectiveTimeout() time.Duration {
	if b == nil || b.TimeoutSeconds <= 0 {
		return DefaultBeforeSessionTimeoutSeconds * time.Second
	}
	return time.Duration(b.TimeoutSeconds) * time.Second
}

// AuthBypassConfig configures the test-only auth bypass contract for the app
// under test (see the revyl-cli-auth-bypass skill for the app-side handler).
//
// The contract: org launch variables gate and feed the bypass
// (e.g. REVYL_AUTH_BYPASS_ENABLED, REVYL_AUTH_BYPASS_TOKEN), and a deep link
// fired after app launch signs the session in
// (e.g. myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}&redirect=/home).
type AuthBypassConfig struct {
	// LaunchVars are org launch-variable keys (or IDs) merged (deduplicated) with
	// explicit --launch-var flags on device session start.
	LaunchVars []string `yaml:"launch_vars,omitempty"`

	// DeepLink is fired after app launch; ${VAR} placeholders resolve from
	// before_session output or org launch variables.
	DeepLink string `yaml:"deep_link,omitempty"`
}

func (a *AuthBypassConfig) IsConfigured() bool {
	if a == nil {
		return false
	}
	return len(a.LaunchVars) > 0 || strings.TrimSpace(a.DeepLink) != ""
}

// HotReloadConfig contains configuration for hot reload mode.
//
// Hot reload enables rapid development iteration by:
//   - Starting a local dev server (Expo, Swift, or Android)
//   - Creating a backend-owned relay to expose it
//   - Running tests against a pre-built dev client
//
// Supports multiple providers for cross-platform projects. Use the Default field
// to specify which provider to use when --provider is not specified, or let the
// CLI auto-select based on project detection confidence.
type HotReloadConfig struct {
	Default string `yaml:"default,omitempty"`

	Transport string `yaml:"transport,omitempty"`

	// Providers maps provider names ("expo", "react-native", "swift", "android", "flutter") to configs.
	Providers map[string]*ProviderConfig `yaml:"providers,omitempty"`
}

type ProviderConfig struct {
	// Port is the dev server port (GetPort defaults to 8081 when non-positive).
	Port int `yaml:"port,omitempty"`

	AppScheme string `yaml:"app_scheme,omitempty"`

	// PlatformKeys optionally maps OS platform ("ios"/"android") to build.platforms keys.
	// Example: {"ios":"ios-dev","android":"android-dev"}.
	PlatformKeys map[string]string `yaml:"platform_keys,omitempty"`

	UseExpPrefix bool `yaml:"use_exp_prefix,omitempty"`

	BundleID string `yaml:"bundle_id,omitempty"`

	InjectionPath string `yaml:"injection_path,omitempty"`

	ProjectPath string `yaml:"project_path,omitempty"`

	PackageName string `yaml:"package_name,omitempty"`
}

func (c *ProviderConfig) GetPort(providerName string) int {
	if c.Port > 0 {
		return c.Port
	}
	switch providerName {
	case "expo", "react-native", "android":
		return 8081
	default:
		return 8081
	}
}

func (c *HotReloadConfig) IsConfigured() bool {
	return len(c.Providers) > 0
}

func (c *HotReloadConfig) GetTransport() string {
	transport := strings.ToLower(strings.TrimSpace(c.Transport))
	if transport == "" {
		return "relay"
	}
	return transport
}

func (c *HotReloadConfig) GetProviderConfig(providerName string) *ProviderConfig {
	if c.Providers != nil {
		if cfg, ok := c.Providers[providerName]; ok {
			return cfg
		}
	}
	return nil
}

func (c *HotReloadConfig) GetActiveProvider(explicitProvider string) (string, error) {
	if explicitProvider != "" {
		if c.GetProviderConfig(explicitProvider) != nil {
			return explicitProvider, nil
		}
		return "", fmt.Errorf("provider '%s' is not configured", explicitProvider)
	}

	if c.Default != "" {
		if c.GetProviderConfig(c.Default) != nil {
			return c.Default, nil
		}
		return "", fmt.Errorf("default provider '%s' is not configured", c.Default)
	}

	if len(c.Providers) > 0 {
		for name := range c.Providers {
			return name, nil
		}
	}

	return "", fmt.Errorf("no hot reload provider configured")
}

func (c *HotReloadConfig) Validate() error {
	transport := c.GetTransport()
	if transport != "relay" {
		return fmt.Errorf("transport must be relay")
	}
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

func (c *HotReloadConfig) ValidateProvider(providerName string) error {
	cfg := c.GetProviderConfig(providerName)
	if cfg == nil {
		return fmt.Errorf("provider '%s' is not configured", providerName)
	}
	return c.validateProviderConfig(providerName, cfg)
}

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
	case "android":
	case "flutter":
		// No hot reload dev server; uses auto-rebuild dev loop with file watching.
	default:
		return fmt.Errorf("unknown provider: %s (supported: expo, react-native, swift, android, flutter)", name)
	}

	return nil
}

type Project struct {
	ID string `yaml:"id,omitempty"`

	Name string `yaml:"name"`

	OrgID string `yaml:"org_id,omitempty"`
}

type BuildConfig struct {
	System string `yaml:"system,omitempty"`

	Command string `yaml:"command,omitempty"`

	Output string `yaml:"output,omitempty"`

	// NoBuild disables build commands globally. When true, revyl dev and similar
	// commands will never execute build commands and instead require a pre-existing
	// build (resolved via app_id or --build-version-id). The --build flag
	// explicitly overrides this setting.
	NoBuild bool `yaml:"no_build,omitempty"`

	Source BuildSource `yaml:"source,omitempty"`

	Caches []BuildCache `yaml:"caches,omitempty"`

	Platforms map[string]BuildPlatform `yaml:"platforms,omitempty"`
}

type BuildCache struct {
	// Key is the org-local cache key. The backend prefixes it with the org ID.
	Key string `yaml:"key" json:"key"`

	// Paths are project-relative or ~/ paths stored in this cache archive.
	Paths []string `yaml:"paths" json:"paths"`

	// Hash are optional project-relative or ~/ files or globs whose contents
	// decide whether the restored cache changed and needs saving again.
	Hash []string `yaml:"hash,omitempty" json:"hash,omitempty"`
}

type BuildSource struct {
	Type string `yaml:"type,omitempty"`

	RepoURL string `yaml:"repo_url,omitempty"`

	Ref string `yaml:"ref,omitempty"`

	Subdir string `yaml:"subdir,omitempty"`

	LFS bool `yaml:"lfs,omitempty"`
}

type BuildPlatform struct {
	Command string `yaml:"command"`

	Commands []string `yaml:"commands,omitempty"`

	Output string `yaml:"output"`

	Image string `yaml:"image,omitempty"`

	AppID string `yaml:"app_id,omitempty"`

	// Scheme is the Xcode scheme to use for iOS builds.
	// When set, replaces -scheme * in the build command with the specified scheme.
	Scheme string `yaml:"scheme,omitempty"`

	Setup string `yaml:"setup,omitempty"`

	// Timeout is the remote build timeout in seconds for this platform.
	// Optional: when omitted the server default applies. Local builds
	// ignore it (they have no deadline).
	Timeout int `yaml:"timeout,omitempty"`

	// Env holds non-secret environment variables passed to the remote build
	// runner for this platform. Use Secrets for credentials and tokens.
	Env map[string]string `yaml:"env,omitempty"`

	// Secrets contains names of encrypted org build secrets. Values are never
	// serialized into project configuration.
	Secrets []string `yaml:"secrets,omitempty"`

	Caches []BuildCache `yaml:"caches,omitempty"`
}

func (p BuildPlatform) BuildCommands() []string {
	var commands []string
	for _, command := range p.Commands {
		if trimmed := strings.TrimSpace(command); trimmed != "" {
			commands = append(commands, trimmed)
		}
	}
	if len(commands) > 0 {
		return commands
	}
	if trimmed := strings.TrimSpace(p.Command); trimmed != "" {
		return []string{trimmed}
	}
	return nil
}

// JoinedBuildCommand returns the effective build commands as one shell string.
// It is used as a compatibility fallback for older API surfaces that still
// accept only build_command.
func (p BuildPlatform) JoinedBuildCommand() string {
	return strings.Join(p.BuildCommands(), " && ")
}

func EffectiveBuildCaches(buildCfg BuildConfig, platformCfg BuildPlatform) []BuildCache {
	var result []BuildCache
	indexByKey := make(map[string]int)

	appendCaches := func(caches []BuildCache) {
		for _, cache := range caches {
			key := strings.TrimSpace(cache.Key)
			resultIndex, ok := indexByKey[key]
			if !ok {
				result = append(result, BuildCache{Key: key})
				resultIndex = len(result) - 1
				indexByKey[key] = resultIndex
			}

			result[resultIndex].Paths = append(result[resultIndex].Paths, cache.Paths...)
			result[resultIndex].Hash = append(result[resultIndex].Hash, cache.Hash...)
		}
	}

	appendCaches(buildCfg.Caches)
	appendCaches(platformCfg.Caches)
	return result
}

type Defaults struct {
	OpenBrowser *bool `yaml:"open_browser,omitempty"`

	Timeout int `yaml:"timeout,omitempty"`
}

const (
	DefaultOpenBrowser = false

	DefaultTimeoutSeconds = 30 * 60
)

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

func EffectiveOpenBrowser(cfg *ProjectConfig) bool {
	if cfg == nil || cfg.Defaults.OpenBrowser == nil {
		return DefaultOpenBrowser
	}
	return *cfg.Defaults.OpenBrowser
}

func EffectiveTimeoutSeconds(cfg *ProjectConfig, fallback int) int {
	if cfg != nil && cfg.Defaults.Timeout > 0 {
		return cfg.Defaults.Timeout
	}
	if fallback > 0 {
		return fallback
	}
	return DefaultTimeoutSeconds
}

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

func WriteProjectConfig(path string, cfg *ProjectConfig) error {
	if err := rejectCanonicalConfigLegacyWrite(path); err != nil {
		return err
	}
	ApplyDefaults(cfg)

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	header := "# Revyl CLI Configuration\n# Generated by: revyl init\n# Edit these settings directly, then run: revyl config validate\n# Docs: https://docs.revyl.com/reference/cli/configuration\n\n"
	content := header + annotateHotReloadConfig(string(data), cfg)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// rejectCanonicalConfigLegacyWrite keeps transitional legacy callers from
// truncating a canonical config they cannot faithfully represent.
func rejectCanonicalConfigLegacyWrite(path string) error {
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read existing config before legacy write: %w", err)
	}
	_, document, err := loadYAMLDocument(existing)
	if err != nil {
		return err
	}
	kind, err := ClassifyConfigDocument(document)
	if err != nil {
		return err
	}
	if kind == "canonical" {
		return newConfigError("write", "legacy_writer_canonical_config_forbidden", nil, "")
	}
	return nil
}

func annotateHotReloadConfig(yamlStr string, cfg *ProjectConfig) string {
	if cfg.HotReload.Default == "" {
		return yamlStr
	}

	annotations := buildHotReloadAnnotations(cfg.HotReload.Default)
	if len(annotations) == 0 {
		return yamlStr
	}

	lines := strings.Split(yamlStr, "\n")
	annotatedLines := make([]string, 0, len(lines)+len(annotations))
	pathStack := make([]yamlPathEntry, 0, 4)

	for _, line := range lines {
		indent, key, ok := parseYAMLMappingLine(line)
		if ok {
			for len(pathStack) > 0 && pathStack[len(pathStack)-1].indent >= indent {
				pathStack = pathStack[:len(pathStack)-1]
			}

			currentPath := make([]string, 0, len(pathStack)+1)
			for _, entry := range pathStack {
				currentPath = append(currentPath, entry.key)
			}
			currentPath = append(currentPath, key)

			if comment, found := lookupHotReloadAnnotation(annotations, currentPath); found {
				annotatedLines = append(
					annotatedLines,
					strings.Repeat(" ", indent)+comment,
				)
			}

			pathStack = append(pathStack, yamlPathEntry{indent: indent, key: key})
		}

		annotatedLines = append(annotatedLines, line)
	}

	return strings.Join(annotatedLines, "\n")
}

type yamlPathEntry struct {
	indent int
	key    string
}

type yamlCommentAnnotation struct {
	path    string
	comment string
}

func buildHotReloadAnnotations(provider string) []yamlCommentAnnotation {
	annotations := []yamlCommentAnnotation{
		{
			path:    joinYAMLPath("hotreload"),
			comment: "# Dev mode / hot reload configuration",
		},
		{
			path:    joinYAMLPath("hotreload", "transport"),
			comment: "# Public transport for hot reload: relay",
		},
	}

	switch provider {
	case "expo":
		annotations = append(
			annotations,
			yamlCommentAnnotation{
				path:    joinYAMLPath("hotreload", "providers", "expo", "port"),
				comment: "# Metro bundler port (default 8081). Change if port conflicts.",
			},
			yamlCommentAnnotation{
				path:    joinYAMLPath("hotreload", "providers", "expo", "app_scheme"),
				comment: "# URL scheme from app.json or app.config.js (required for Expo deep linking)",
			},
			yamlCommentAnnotation{
				path:    joinYAMLPath("hotreload", "providers", "expo", "use_exp_prefix"),
				comment: "# Use \"exp+\" prefix in deep links. Try true if deep links fail.",
			},
			yamlCommentAnnotation{
				path:    joinYAMLPath("hotreload", "providers", "expo", "platform_keys"),
				comment: "# Maps platform to build.platforms key for dev build resolution",
			},
		)
	case "react-native":
		annotations = append(
			annotations,
			yamlCommentAnnotation{
				path:    joinYAMLPath("hotreload", "providers", "react-native", "port"),
				comment: "# Metro bundler port (default 8081). Change if port conflicts.",
			},
			yamlCommentAnnotation{
				path:    joinYAMLPath("hotreload", "providers", "react-native", "platform_keys"),
				comment: "# Maps platform to build.platforms key for dev build resolution",
			},
		)
	}

	return annotations
}

func lookupHotReloadAnnotation(
	annotations []yamlCommentAnnotation,
	path []string,
) (string, bool) {
	joinedPath := joinYAMLPath(path...)
	for _, annotation := range annotations {
		if annotation.path == joinedPath {
			return annotation.comment, true
		}
	}
	return "", false
}

func parseYAMLMappingLine(line string) (int, string, bool) {
	trimmedLine := strings.TrimSpace(line)
	if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") || strings.HasPrefix(trimmedLine, "-") {
		return 0, "", false
	}

	key, _, found := strings.Cut(trimmedLine, ":")
	if !found {
		return 0, "", false
	}

	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return 0, "", false
	}

	indent := len(line) - len(strings.TrimLeft(line, " "))
	return indent, normalizedKey, true
}

func joinYAMLPath(path ...string) string {
	return strings.Join(path, "/")
}

type LocalTest struct {
	Meta TestMeta `yaml:"_meta"`

	Test TestDefinition `yaml:"test"`
}

type TestMeta struct {
	RemoteID string `yaml:"remote_id,omitempty"`

	RemoteVersion int `yaml:"remote_version"`

	// LocalVersion tracks the last synced version written by sync operations.
	LocalVersion int `yaml:"local_version"`

	LastSyncedAt string `yaml:"last_synced_at,omitempty"`

	LastSyncedBy string `yaml:"last_synced_by,omitempty"`

	Checksum string `yaml:"checksum,omitempty"`
}

type TestDeviceConfig struct {
	Model string `yaml:"model,omitempty"`

	OSVersion string `yaml:"os_version,omitempty"`

	// Orientation is portrait or landscape.
	Orientation string `yaml:"orientation,omitempty"`
}

type TestLocation struct {
	Latitude float64 `yaml:"latitude"`

	Longitude float64 `yaml:"longitude"`
}

type TestDefinition struct {
	Metadata TestMetadata `yaml:"metadata"`

	Build TestBuildConfig `yaml:"build,omitempty"`

	Device *TestDeviceConfig `yaml:"device,omitempty"`

	Variables map[string]string `yaml:"variables,omitempty"`

	// EnvVars contains attached org launch variable keys.
	EnvVars []string `yaml:"env_vars,omitempty"`

	Location *TestLocation `yaml:"location,omitempty"`

	Blocks []TestBlock `yaml:"blocks"`
}

type TestMetadata struct {
	Name string `yaml:"name"`

	// Platform is the target platform (ios, android).
	Platform string `yaml:"platform,omitempty"`

	Description string `yaml:"description,omitempty"`

	Tags []string `yaml:"tags,omitempty"`
}

type TestBuildConfig struct {
	Name string `yaml:"name"`

	PinnedVersion  string `yaml:"pinned_version,omitempty"`
	BranchID       string `yaml:"branch_id,omitempty"`
	BuildSelection string `yaml:"build_selection,omitempty"`
	PinnedBuildID  string `yaml:"pinned_build_id,omitempty"`

	// SystemPrompt is the app-level system prompt for the LLM agent.
	SystemPrompt string `yaml:"system_prompt,omitempty"`
}

type TestBlock struct {
	ID string `yaml:"id,omitempty" json:"id,omitempty"`

	// Type is the block type (instructions, validation, if, while).
	Type string `yaml:"type" json:"type"`

	// StepType is the step type (instruction, validation, etc.).
	StepType string `yaml:"step_type,omitempty" json:"step_type,omitempty"`

	StepDescription string `yaml:"step_description,omitempty" json:"step_description,omitempty"`

	Condition string `yaml:"condition,omitempty" json:"condition,omitempty"`

	Then []TestBlock `yaml:"then,omitempty" json:"thenChildren,omitempty"`

	Else []TestBlock `yaml:"else,omitempty" json:"elseChildren,omitempty"`

	Body []TestBlock `yaml:"body,omitempty" json:"children,omitempty"`

	// VariableName is the variable name for extraction blocks.
	VariableName string `yaml:"variable_name,omitempty" json:"variable_name,omitempty"`

	ModuleID string `yaml:"module_id,omitempty" json:"module_id,omitempty"`

	ScriptID string `yaml:"script_id,omitempty" json:"script_id,omitempty"`

	TimeoutSeconds *int `yaml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`

	Script string `yaml:"script,omitempty" json:"script,omitempty"`

	Module string `yaml:"module,omitempty" json:"module,omitempty"`

	// File is the org file name for manual/download_file blocks.
	File string `yaml:"file,omitempty" json:"file,omitempty"`

	// FileURI is the resolved revyl-file:// URI for manual/download_file blocks.
	FileURI string `yaml:"file_uri,omitempty" json:"file_uri,omitempty"`
}

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

func (t *LocalTest) HasLocalChanges() bool {
	if t.Meta.Checksum == "" {
		return false
	}

	currentChecksum := ComputeTestChecksum(&t.Test)
	return currentChecksum != t.Meta.Checksum
}

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

// GetLocalTestRemoteID loads a local test YAML by alias and returns _meta.remote_id.
// Returns ("", nil) when the file is missing or unlinked, and an error on
// invalid alias paths, filesystem read errors, or YAML parse failures.
func GetLocalTestRemoteID(testsDir, alias string) (string, error) {
	path, pathErr := util.SafeTestPath(testsDir, alias)
	if pathErr != nil {
		return "", pathErr
	}
	lt, err := LoadLocalTest(path)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return lt.Meta.RemoteID, nil
}

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

func SaveLocalTest(path string, test *LocalTest) error {
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

// HasDefinition reports whether the local test carries definition content
// that would be lost on overwrite. Name and platform are excluded: they carry
// no information beyond the filename and create flags. New TestDefinition
// fields that hold user-authored content must be added here.
func (t *LocalTest) HasDefinition() bool {
	d := t.Test
	return len(d.Blocks) > 0 ||
		len(d.EnvVars) > 0 ||
		len(d.Variables) > 0 ||
		d.Metadata.Description != "" ||
		len(d.Metadata.Tags) > 0 ||
		d.Build != (TestBuildConfig{}) ||
		d.Device != nil ||
		d.Location != nil
}

// LinkLocalTestToRemote sets _meta.remote_id on path (creating a fresh stub if
// missing) and saves via SaveLocalTest, which recomputes Checksum and reserializes
// typed YAML. Non-missing load errors leave the file untouched. Returns whether
// an existing local definition was preserved.
func LinkLocalTestToRemote(path, remoteID string) (preserved bool, err error) {
	test, loadErr := LoadLocalTest(path)
	if loadErr != nil {
		if !errors.Is(loadErr, os.ErrNotExist) {
			return false, loadErr
		}
		test = &LocalTest{}
	}
	test.Meta.RemoteID = remoteID
	return test.HasDefinition(), SaveLocalTest(path, test)
}
