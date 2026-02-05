// Package hotreload provides hot reload functionality for rapid development iteration.
//
// This file defines the Provider interface and Registry for extensible hot reload support.
// New frameworks can be added by implementing the Provider interface and registering
// with the DefaultRegistry.
package hotreload

import (
	"fmt"

	"github.com/revyl/cli/internal/config"
)

// Provider defines the interface for hot reload providers.
//
// Implement this interface to add support for new frameworks/platforms.
// Each provider handles:
//   - Detection: Can this provider handle this project?
//   - Configuration: Extract project-specific info
//   - Runtime: Create and manage the dev server
//
// Current implementations:
//   - ExpoProvider: Expo/React Native projects (fully supported)
//   - SwiftProvider: Native iOS projects (stub - detection only)
//   - AndroidProvider: Native Android projects (stub - detection only)
type Provider interface {
	// Name returns the unique identifier for this provider.
	// Used in configuration files and CLI flags.
	//
	// Returns:
	//   - string: Provider identifier (e.g., "expo", "swift", "android")
	Name() string

	// DisplayName returns the human-readable name for this provider.
	// Used in user-facing messages and prompts.
	//
	// Returns:
	//   - string: Display name (e.g., "Expo", "Swift/iOS", "Android")
	DisplayName() string

	// Detect checks if this provider can handle the project in the given directory.
	// Returns nil if the provider cannot handle this project.
	//
	// Parameters:
	//   - dir: The project directory to analyze
	//
	// Returns:
	//   - *DetectionResult: Detection result with confidence score, or nil if not detected
	//   - error: Any error that occurred during detection
	Detect(dir string) (*DetectionResult, error)

	// GetProjectInfo extracts project-specific information from the directory.
	// Should only be called after Detect returns a positive result.
	//
	// Parameters:
	//   - dir: The project directory to analyze
	//
	// Returns:
	//   - *ProjectInfo: Extracted project information
	//   - error: Any error that occurred during extraction
	GetProjectInfo(dir string) (*ProjectInfo, error)

	// GetDefaultConfig returns a default configuration for this provider.
	// Used during auto-setup to pre-populate configuration values.
	//
	// Parameters:
	//   - info: Project information from GetProjectInfo
	//
	// Returns:
	//   - *config.ProviderConfig: Default configuration for this provider
	GetDefaultConfig(info *ProjectInfo) *config.ProviderConfig

	// CreateDevServer creates a development server instance for this provider.
	// The returned DevServer manages the lifecycle of the local dev server.
	//
	// Parameters:
	//   - cfg: Provider configuration from .revyl/config.yaml
	//   - workDir: Working directory for the project
	//
	// Returns:
	//   - DevServer: The dev server instance
	//   - error: Any error that occurred (e.g., "not yet supported" for stubs)
	CreateDevServer(cfg *config.ProviderConfig, workDir string) (DevServer, error)

	// IsSupported returns whether this provider is fully implemented.
	// Stub providers return false, indicating detection works but setup will fail.
	//
	// Returns:
	//   - bool: True if provider is fully supported
	IsSupported() bool
}

// DetectionResult contains the result of provider detection.
type DetectionResult struct {
	// Provider is the provider name that detected this project.
	Provider string

	// Confidence is a score from 0.0 to 1.0 indicating detection confidence.
	// Higher values indicate stronger matches.
	// - 0.9+: Strong match (e.g., app.json + expo in package.json)
	// - 0.7-0.8: Good match (e.g., Xcode project found)
	// - 0.5-0.6: Weak match (e.g., android/ directory exists)
	Confidence float64

	// Platform indicates the target platform(s).
	// Values: "ios", "android", "cross-platform"
	Platform string

	// Indicators lists the files/patterns that triggered detection.
	// Used for user feedback and debugging.
	Indicators []string
}

// ProjectInfo contains detected project information.
// Provider-specific details are stored in nested structs.
type ProjectInfo struct {
	// Name is the project name.
	Name string

	// Platform is the target platform ("ios", "android", "cross-platform").
	Platform string

	// Expo contains Expo-specific information (nil if not an Expo project).
	Expo *ExpoProjectInfo

	// Swift contains Swift/iOS-specific information (nil if not a Swift project).
	Swift *SwiftProjectInfo

	// Android contains Android-specific information (nil if not an Android project).
	Android *AndroidProjectInfo
}

// ExpoProjectInfo contains Expo-specific project information.
type ExpoProjectInfo struct {
	// Scheme is the app's URL scheme from app.json.
	Scheme string

	// Name is the app name from app.json.
	Name string

	// Slug is the app slug from app.json.
	Slug string
}

// SwiftProjectInfo contains Swift/iOS-specific project information.
type SwiftProjectInfo struct {
	// BundleID is the iOS bundle identifier.
	BundleID string

	// Scheme is the Xcode build scheme.
	Scheme string

	// ProjectPath is the path to the .xcodeproj or .xcworkspace.
	ProjectPath string
}

// AndroidProjectInfo contains Android-specific project information.
type AndroidProjectInfo struct {
	// PackageName is the Android package name from AndroidManifest.xml.
	PackageName string

	// ProjectPath is the path to the Android project root.
	ProjectPath string
}

// Registry manages available hot reload providers.
// Use DefaultRegistry() to get the standard registry with all built-in providers.
type Registry struct {
	providers []Provider
}

// NewRegistry creates an empty provider registry.
//
// Returns:
//   - *Registry: A new empty registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make([]Provider, 0),
	}
}

// Register adds a provider to the registry.
//
// Parameters:
//   - provider: The provider to register
func (r *Registry) Register(provider Provider) {
	r.providers = append(r.providers, provider)
}

// defaultRegistry is the singleton registry instance.
// Initialized lazily by DefaultRegistry().
var defaultRegistry *Registry

// DefaultRegistry returns the registry with all built-in providers.
// Providers are registered in order of typical priority.
//
// Note: This function is called by the providers package during init,
// so we use lazy initialization to avoid circular dependencies.
//
// Returns:
//   - *Registry: Registry with all built-in providers
func DefaultRegistry() *Registry {
	if defaultRegistry == nil {
		defaultRegistry = NewRegistry()
	}
	return defaultRegistry
}

// RegisterProvider registers a provider with the default registry.
// This is called by provider implementations during package init.
//
// Parameters:
//   - provider: The provider to register
func RegisterProvider(provider Provider) {
	DefaultRegistry().Register(provider)
}

// DetectProvider finds the best matching provider for a directory.
// Returns the provider with the highest confidence score.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - Provider: The best matching provider
//   - *DetectionResult: Detection result with confidence score
//   - error: Error if no provider matches
func (r *Registry) DetectProvider(dir string) (Provider, *DetectionResult, error) {
	var bestProvider Provider
	var bestResult *DetectionResult

	for _, p := range r.providers {
		result, err := p.Detect(dir)
		if err != nil || result == nil {
			continue
		}
		if bestResult == nil || result.Confidence > bestResult.Confidence {
			bestProvider = p
			bestResult = result
		}
	}

	if bestProvider == nil {
		return nil, nil, fmt.Errorf("no compatible hot reload provider found")
	}

	return bestProvider, bestResult, nil
}

// DetectAllProviders finds all providers that match a directory.
// Returns providers sorted by confidence (highest first).
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - []ProviderDetection: All matching providers with their detection results
func (r *Registry) DetectAllProviders(dir string) []ProviderDetection {
	var results []ProviderDetection

	for _, p := range r.providers {
		result, err := p.Detect(dir)
		if err != nil || result == nil {
			continue
		}
		results = append(results, ProviderDetection{
			Provider:  p,
			Detection: result,
		})
	}

	// Sort by confidence (highest first)
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Detection.Confidence > results[i].Detection.Confidence {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results
}

// ProviderDetection pairs a provider with its detection result.
type ProviderDetection struct {
	Provider  Provider
	Detection *DetectionResult
}

// GetProvider returns a provider by name.
//
// Parameters:
//   - name: The provider name (e.g., "expo", "swift", "android")
//
// Returns:
//   - Provider: The provider instance
//   - error: Error if provider not found
func (r *Registry) GetProvider(name string) (Provider, error) {
	for _, p := range r.providers {
		if p.Name() == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown provider: %s", name)
}

// SupportedProviders returns only providers that are fully implemented.
// Stub providers (IsSupported() == false) are excluded.
//
// Returns:
//   - []Provider: List of fully supported providers
func (r *Registry) SupportedProviders() []Provider {
	var supported []Provider
	for _, p := range r.providers {
		if p.IsSupported() {
			supported = append(supported, p)
		}
	}
	return supported
}

// AllProviders returns all registered providers.
//
// Returns:
//   - []Provider: List of all providers
func (r *Registry) AllProviders() []Provider {
	return r.providers
}

// SelectProvider selects the appropriate provider based on configuration and detection.
// Priority: explicit provider > configured default > auto-detection by confidence.
//
// Parameters:
//   - cfg: Hot reload configuration from .revyl/config.yaml
//   - explicitProvider: Provider specified via --provider flag (empty if not specified)
//   - workDir: Working directory for detection
//
// Returns:
//   - Provider: The selected provider
//   - *config.ProviderConfig: Configuration for the selected provider
//   - error: Error if no suitable provider found
func (r *Registry) SelectProvider(cfg *config.HotReloadConfig, explicitProvider string, workDir string) (Provider, *config.ProviderConfig, error) {
	// 1. Explicit --provider flag takes priority
	if explicitProvider != "" {
		provider, err := r.GetProvider(explicitProvider)
		if err != nil {
			return nil, nil, err
		}
		providerCfg := cfg.GetProviderConfig(explicitProvider)
		if providerCfg == nil {
			return nil, nil, fmt.Errorf("provider '%s' is not configured in .revyl/config.yaml", explicitProvider)
		}
		return provider, providerCfg, nil
	}

	// 2. Use configured default if set
	if cfg.Default != "" {
		provider, err := r.GetProvider(cfg.Default)
		if err != nil {
			return nil, nil, fmt.Errorf("default provider '%s' not found: %w", cfg.Default, err)
		}
		providerCfg := cfg.GetProviderConfig(cfg.Default)
		if providerCfg == nil {
			return nil, nil, fmt.Errorf("default provider '%s' is not configured", cfg.Default)
		}
		return provider, providerCfg, nil
	}

	// 3. Auto-select based on detection confidence
	provider, _, err := r.DetectProvider(workDir)
	if err != nil {
		// Fall back to first configured provider
		providerName, err := cfg.GetActiveProvider("")
		if err != nil {
			return nil, nil, fmt.Errorf("no provider configured and auto-detection failed")
		}
		provider, _ = r.GetProvider(providerName)
		if provider == nil {
			return nil, nil, fmt.Errorf("provider '%s' not found in registry", providerName)
		}
	}

	providerCfg := cfg.GetProviderConfig(provider.Name())
	if providerCfg == nil {
		return nil, nil, fmt.Errorf("provider '%s' was auto-detected but is not configured in .revyl/config.yaml. Run 'revyl hotreload setup' to configure it", provider.Name())
	}
	return provider, providerCfg, nil
}

// NewExpoProvider creates a new Expo provider.
// This is a forward declaration - the actual implementation is in providers/expo_provider.go.
var NewExpoProvider func() Provider

// NewSwiftProvider creates a new Swift provider.
// This is a forward declaration - the actual implementation is in providers/swift_provider.go.
var NewSwiftProvider func() Provider

// NewAndroidProvider creates a new Android provider.
// This is a forward declaration - the actual implementation is in providers/android_provider.go.
var NewAndroidProvider func() Provider
