package hotreload

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

type SetupResult struct {
	Config       *config.ProviderConfig
	Provider     Provider
	ProviderName string
	ProjectInfo  *ProjectInfo
	Detection    *DetectionResult
}

type SetupOptions struct {
	WorkDir          string
	ExplicitProvider string
}

// AutoSetup detects a provider, extracts project info, and builds a default config.
// client is unused and retained for caller compatibility.
func AutoSetup(ctx context.Context, client *api.Client, opts SetupOptions) (*SetupResult, error) {
	registry := DefaultRegistry()

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

	info, err := provider.GetProjectInfo(opts.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get project info: %w", err)
	}

	cfg := provider.GetDefaultConfig(info)

	return &SetupResult{
		Config:       cfg,
		Provider:     provider,
		ProviderName: provider.Name(),
		ProjectInfo:  info,
		Detection:    detection,
	}, nil
}

func ApplySetupResult(cfg *config.ProjectConfig, result *SetupResult, setAsDefault bool) {
	if cfg.HotReload.Providers == nil {
		cfg.HotReload.Providers = make(map[string]*config.ProviderConfig)
	}

	cfg.HotReload.Providers[result.ProviderName] = result.Config

	if setAsDefault || len(cfg.HotReload.Providers) == 1 {
		cfg.HotReload.Default = result.ProviderName
	}
}

func ValidateSetupResult(result *SetupResult) error {
	if result.Config == nil {
		return fmt.Errorf("no configuration generated")
	}

	if !result.Provider.IsSupported() {
		return fmt.Errorf("%s hot reload is not yet supported", result.Provider.DisplayName())
	}

	return nil
}
