// Package hotreload provides hot reload functionality for rapid development iteration.
//
// This file contains the auto-setup logic for configuring hot reload.
package hotreload

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// SetupResult contains the result of auto-setup.
type SetupResult struct {
	// Config is the generated hot reload configuration.
	Config *config.ProviderConfig

	// Provider is the detected provider.
	Provider Provider

	// ProviderName is the provider name for config storage.
	ProviderName string

	// ProjectInfo contains detected project information.
	ProjectInfo *ProjectInfo

	// Detection contains the detection result with confidence.
	Detection *DetectionResult
}

// SetupOptions contains options for auto-setup.
type SetupOptions struct {
	// WorkDir is the working directory for the project.
	WorkDir string

	// ExplicitProvider is the provider specified via --provider flag.
	// If empty, auto-detection is used.
	ExplicitProvider string

	// Platform is the platform filter for build search ("ios", "android", or empty).
	Platform string
}

// AutoSetup attempts to auto-configure hot reload for a project.
//
// This function:
//  1. Detects the project type using the provider registry
//  2. Extracts project information (app scheme, etc.)
//  3. Returns a SetupResult with the default configuration
//
// Note: Build selection is handled at runtime via --platform or --build-id flags.
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: API client (unused, kept for backwards compatibility)
//   - opts: Setup options
//
// Returns:
//   - *SetupResult: Setup result with configuration
//   - error: Any error that occurred
func AutoSetup(ctx context.Context, client *api.Client, opts SetupOptions) (*SetupResult, error) {
	registry := DefaultRegistry()

	// 1. Detect or get explicit provider
	var provider Provider
	var detection *DetectionResult
	var err error

	if opts.ExplicitProvider != "" {
		provider, err = registry.GetProvider(opts.ExplicitProvider)
		if err != nil {
			return nil, fmt.Errorf("unknown provider '%s': %w", opts.ExplicitProvider, err)
		}
		detection, err = provider.Detect(opts.WorkDir)
		if err != nil || detection == nil {
			// Provider was explicitly requested but project doesn't match
			// Still allow setup, just with lower confidence
			detection = &DetectionResult{
				Provider:   opts.ExplicitProvider,
				Confidence: 0.5,
				Platform:   "unknown",
				Indicators: []string{"explicitly requested"},
			}
		}
	} else {
		provider, detection, err = registry.DetectProvider(opts.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("failed to detect project type: %w", err)
		}
	}

	// 2. Get project info
	info, err := provider.GetProjectInfo(opts.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get project info: %w", err)
	}

	// 3. Get default config (without build ID - user specifies at runtime)
	cfg := provider.GetDefaultConfig(info)

	return &SetupResult{
		Config:       cfg,
		Provider:     provider,
		ProviderName: provider.Name(),
		ProjectInfo:  info,
		Detection:    detection,
	}, nil
}

// AutoSetupAll detects and sets up all compatible providers for a project.
//
// This is useful for cross-platform projects that may have multiple
// hot reload providers (e.g., Expo + Android).
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: API client for searching builds
//   - workDir: Working directory for the project
//
// Returns:
//   - []*SetupResult: Setup results for each detected provider
//   - error: Any error that occurred
func AutoSetupAll(ctx context.Context, client *api.Client, workDir string) ([]*SetupResult, error) {
	registry := DefaultRegistry()
	detections := registry.DetectAllProviders(workDir)

	if len(detections) == 0 {
		return nil, fmt.Errorf("no compatible hot reload providers found")
	}

	var results []*SetupResult
	for _, d := range detections {
		result, err := AutoSetup(ctx, client, SetupOptions{
			WorkDir:          workDir,
			ExplicitProvider: d.Provider.Name(),
			Platform:         d.Detection.Platform,
		})
		if err != nil {
			// Log but continue with other providers
			continue
		}
		results = append(results, result)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("failed to setup any providers")
	}

	return results, nil
}

// ApplySetupResult applies a setup result to a project configuration.
//
// Parameters:
//   - cfg: The project configuration to update
//   - result: The setup result to apply
//   - setAsDefault: Whether to set this provider as the default
func ApplySetupResult(cfg *config.ProjectConfig, result *SetupResult, setAsDefault bool) {
	// Initialize providers map if needed
	if cfg.HotReload.Providers == nil {
		cfg.HotReload.Providers = make(map[string]*config.ProviderConfig)
	}

	// Add the provider config
	cfg.HotReload.Providers[result.ProviderName] = result.Config

	// Set as default if requested or if it's the only provider
	if setAsDefault || len(cfg.HotReload.Providers) == 1 {
		cfg.HotReload.Default = result.ProviderName
	}
}

// ValidateSetupResult checks if a setup result is complete and ready to use.
//
// Note: Build selection is validated at runtime via --platform/--build-id
// and hotreload.providers.<provider>.platform_keys mappings.
//
// Parameters:
//   - result: The setup result to validate
//
// Returns:
//   - error: Validation error or nil if valid
func ValidateSetupResult(result *SetupResult) error {
	if result.Config == nil {
		return fmt.Errorf("no configuration generated")
	}

	if !result.Provider.IsSupported() {
		return fmt.Errorf("%s hot reload is not yet supported", result.Provider.DisplayName())
	}

	return nil
}
