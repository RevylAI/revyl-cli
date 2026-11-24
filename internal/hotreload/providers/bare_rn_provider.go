package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	hotreload.RegisterProvider(&BareRNProvider{})
}

// BareRNProvider implements hotreload.Provider for bare React Native projects without Expo.
type BareRNProvider struct{}

func (p *BareRNProvider) Name() string {
	return "react-native"
}

func (p *BareRNProvider) DisplayName() string {
	return "React Native"
}

func (p *BareRNProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	packageJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, nil
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, nil
	}

	hasReactNative := pkg.hasDependency("react-native")
	if !hasReactNative {
		return nil, nil
	}

	hasExpo := pkg.hasDependency("expo")
	if hasExpo {
		return nil, nil
	}

	var indicators []string
	indicators = append(indicators, "react-native in package.json (without expo)")

	if _, err := os.Stat(filepath.Join(dir, "metro.config.js")); err == nil {
		indicators = append(indicators, "metro.config.js")
	}
	if _, err := os.Stat(filepath.Join(dir, "metro.config.ts")); err == nil {
		indicators = append(indicators, "metro.config.ts")
	}
	if _, err := os.Stat(filepath.Join(dir, "react-native.config.js")); err == nil {
		indicators = append(indicators, "react-native.config.js")
	}
	if _, err := os.Stat(filepath.Join(dir, "react-native.config.ts")); err == nil {
		indicators = append(indicators, "react-native.config.ts")
	}
	if _, err := os.Stat(filepath.Join(dir, "android")); err == nil {
		indicators = append(indicators, "android/")
	}
	if _, err := os.Stat(filepath.Join(dir, "ios")); err == nil {
		indicators = append(indicators, "ios/")
	}

	return &hotreload.DetectionResult{
		Provider:   "react-native",
		Confidence: 0.8,
		Platform:   "cross-platform",
		Indicators: indicators,
	}, nil
}

func (p *BareRNProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	packageJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, err
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	return &hotreload.ProjectInfo{
		Name:     pkg.Name,
		Platform: "cross-platform",
		ReactNative: &hotreload.ReactNativeProjectInfo{
			Name: pkg.Name,
		},
	}, nil
}

func (p *BareRNProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	return &config.ProviderConfig{
		Port: 8081,
	}
}

func (p *BareRNProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	port := cfg.GetPort("react-native")
	return NewBareRNDevServer(workDir, port), nil
}

func (p *BareRNProvider) IsSupported() bool {
	return true
}

type packageJSON struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func (p *packageJSON) hasDependency(name string) bool {
	if _, ok := p.Dependencies[name]; ok {
		return true
	}
	if _, ok := p.DevDependencies[name]; ok {
		return true
	}

	for key := range p.Dependencies {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	for key := range p.DevDependencies {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}
