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

// BareRNProvider implements the Provider interface for bare React Native
// projects (those using react-native without Expo).
//
// Detection looks for:
//   - package.json with "react-native" in dependencies
//   - Absence of "expo" in package.json dependencies (Expo projects use ExpoProvider)
//
// Confidence is 0.8 -- lower than Expo (0.9) to avoid conflicts, but higher
// than Android (0.6) and Swift (0.7) since RN projects always contain
// android/ and ios/ directories.
type BareRNProvider struct{}

// Name returns the unique identifier for this provider.
//
// Returns:
//   - string: "react-native"
func (p *BareRNProvider) Name() string {
	return "react-native"
}

// DisplayName returns the human-readable name for this provider.
//
// Returns:
//   - string: "React Native"
func (p *BareRNProvider) DisplayName() string {
	return "React Native"
}

// Detect checks if this is a bare React Native project (without Expo).
//
// Detection criteria:
//   - package.json exists
//   - "react-native" appears in dependencies or devDependencies
//   - "expo" does NOT appear in dependencies (Expo projects use ExpoProvider)
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.DetectionResult: Detection result with confidence 0.8, or nil if not detected
//   - error: Any error that occurred during detection
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

	return &hotreload.DetectionResult{
		Provider:   "react-native",
		Confidence: 0.8,
		Platform:   "cross-platform",
		Indicators: indicators,
	}, nil
}

// GetProjectInfo extracts React Native project information from package.json.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.ProjectInfo: Extracted project information
//   - error: Any error that occurred during extraction
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

// GetDefaultConfig returns a default configuration for bare React Native projects.
//
// Parameters:
//   - info: Project information from GetProjectInfo
//
// Returns:
//   - *config.ProviderConfig: Default configuration with port 8081
func (p *BareRNProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	return &config.ProviderConfig{
		Port: 8081,
	}
}

// CreateDevServer creates a bare React Native development server instance.
//
// Parameters:
//   - cfg: Provider configuration from .revyl/config.yaml
//   - workDir: Working directory for the project
//
// Returns:
//   - hotreload.DevServer: The bare RN dev server instance
//   - error: Any error that occurred
func (p *BareRNProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	port := cfg.GetPort("react-native")
	return NewBareRNDevServer(workDir, port), nil
}

// IsSupported returns true as bare React Native hot reload is fully supported.
//
// Returns:
//   - bool: true
func (p *BareRNProvider) IsSupported() bool {
	return true
}

// packageJSON is a minimal representation of a package.json file for detection.
type packageJSON struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// hasDependency returns true if the named package appears in either
// dependencies or devDependencies.
func (p *packageJSON) hasDependency(name string) bool {
	if _, ok := p.Dependencies[name]; ok {
		return true
	}
	if _, ok := p.DevDependencies[name]; ok {
		return true
	}

	// Fallback: some package.json files use workspace or wildcard versions
	// that may not parse cleanly. Check key presence via string matching.
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
