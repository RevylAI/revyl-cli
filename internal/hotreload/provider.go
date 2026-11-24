// Package hotreload coordinates local dev servers, relay tunnels, file watching,
// and provider detection for Revyl's hot-reload dev loop.
package hotreload

import (
	"fmt"

	"github.com/revyl/cli/internal/config"
)

// Provider handles project detection, default config, and dev-server creation for a framework.
type Provider interface {
	Name() string
	DisplayName() string
	Detect(dir string) (*DetectionResult, error)
	GetProjectInfo(dir string) (*ProjectInfo, error)
	GetDefaultConfig(info *ProjectInfo) *config.ProviderConfig
	CreateDevServer(cfg *config.ProviderConfig, workDir string) (DevServer, error)
	// IsSupported reports whether this provider supports a live hot-reload dev server.
	IsSupported() bool
}

type DetectionResult struct {
	Provider   string
	Confidence float64
	Platform   string
	Indicators []string
}

type ProjectInfo struct {
	Name        string
	Platform    string
	Expo        *ExpoProjectInfo
	ReactNative *ReactNativeProjectInfo
	Swift       *SwiftProjectInfo
	Android     *AndroidProjectInfo
	Flutter     *FlutterProjectInfo
}

type ExpoProjectInfo struct {
	Scheme string
	Name   string
	Slug   string
}

type SwiftProjectInfo struct {
	BundleID    string
	Scheme      string
	ProjectPath string
}

type ReactNativeProjectInfo struct {
	Name string
}

type AndroidProjectInfo struct {
	PackageName string
	ProjectPath string
}

type FlutterProjectInfo struct {
	Name string
}

type Registry struct {
	providers []Provider
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make([]Provider, 0),
	}
}

func (r *Registry) Register(provider Provider) {
	r.providers = append(r.providers, provider)
}

var defaultRegistry *Registry

// DefaultRegistry returns the lazily initialized singleton provider registry.
func DefaultRegistry() *Registry {
	if defaultRegistry == nil {
		defaultRegistry = NewRegistry()
	}
	return defaultRegistry
}

func RegisterProvider(provider Provider) {
	DefaultRegistry().Register(provider)
}

// DetectProvider returns the registered provider with the highest detection confidence.
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

// DetectAllProviders returns all matching providers sorted by descending confidence.
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

	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Detection.Confidence > results[i].Detection.Confidence {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	return results
}

type ProviderDetection struct {
	Provider  Provider
	Detection *DetectionResult
}

func (r *Registry) GetProvider(name string) (Provider, error) {
	for _, p := range r.providers {
		if p.Name() == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown provider: %s", name)
}

func (r *Registry) SupportedProviders() []Provider {
	var supported []Provider
	for _, p := range r.providers {
		if p.IsSupported() {
			supported = append(supported, p)
		}
	}
	return supported
}

func (r *Registry) AllProviders() []Provider {
	return r.providers
}

// SelectProvider resolves the provider in priority order: explicitProvider, configured default, then auto-detection.
func (r *Registry) SelectProvider(cfg *config.HotReloadConfig, explicitProvider string, workDir string) (Provider, *config.ProviderConfig, error) {
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

	provider, _, err := r.DetectProvider(workDir)
	if err != nil {
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
		return nil, nil, fmt.Errorf("provider '%s' was auto-detected but is not configured in .revyl/config.yaml. Run 'revyl init --hotreload' to configure it", provider.Name())
	}
	return provider, providerCfg, nil
}

var NewExpoProvider func() Provider
var NewSwiftProvider func() Provider
var NewAndroidProvider func() Provider
var NewFlutterProvider func() Provider
