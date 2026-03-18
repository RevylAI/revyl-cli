// Package providers contains implementations of the Provider interface
// for different development frameworks and platforms.
package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	// Register the Expo provider with the default registry
	hotreload.RegisterProvider(&ExpoProvider{})
}

// ExpoProvider implements the Provider interface for Expo/React Native projects.
//
// Detection looks for any combination of:
//   - app.json with expo configuration
//   - app.config.js or app.config.ts (dynamic Expo config)
//   - eas.json (EAS Build config, definitive Expo indicator)
//   - .expo/ directory (Expo metadata)
//   - "expo" dependency in package.json
//
// At least one project indicator AND "expo" in package.json are required.
// Fully supported with ExpoDevServer for hot reload.
type ExpoProvider struct{}

// NewExpoProvider creates a new Expo provider instance.
//
// Returns:
//   - hotreload.Provider: A new Expo provider
func NewExpoProvider() hotreload.Provider {
	return &ExpoProvider{}
}

// Name returns the unique identifier for this provider.
//
// Returns:
//   - string: "expo"
func (p *ExpoProvider) Name() string {
	return "expo"
}

// DisplayName returns the human-readable name for this provider.
//
// Returns:
//   - string: "Expo"
func (p *ExpoProvider) DisplayName() string {
	return "Expo"
}

// Detect checks if this is an Expo project.
//
// Detection requires "expo" in package.json dependencies PLUS at least one
// project indicator: app.json, app.config.js/ts, eas.json, or .expo/ directory.
// This handles modern Expo projects that use dynamic config (app.config.js)
// instead of app.json, and monorepos where app.json may be absent.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.DetectionResult: Detection result with confidence 0.9, or nil if not detected
//   - error: Any error that occurred during detection
func (p *ExpoProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	packageJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, nil
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		if !strings.Contains(string(data), "\"expo\"") {
			return nil, nil
		}
	} else if !pkg.hasDependency("expo") {
		return nil, nil
	}

	var indicators []string
	indicators = append(indicators, "expo in package.json")

	if _, err := os.Stat(filepath.Join(dir, "app.json")); err == nil {
		indicators = append(indicators, "app.json")
	}
	if _, err := os.Stat(filepath.Join(dir, "app.config.js")); err == nil {
		indicators = append(indicators, "app.config.js")
	}
	if _, err := os.Stat(filepath.Join(dir, "app.config.ts")); err == nil {
		indicators = append(indicators, "app.config.ts")
	}
	if _, err := os.Stat(filepath.Join(dir, "eas.json")); err == nil {
		indicators = append(indicators, "eas.json")
	}
	if info, err := os.Stat(filepath.Join(dir, ".expo")); err == nil && info.IsDir() {
		indicators = append(indicators, ".expo/")
	}

	if len(indicators) < 2 {
		return nil, nil
	}

	return &hotreload.DetectionResult{
		Provider:   "expo",
		Confidence: 0.9,
		Platform:   "cross-platform",
		Indicators: indicators,
	}, nil
}

// GetProjectInfo extracts Expo project information from app.json or package.json.
//
// Tries app.json first for scheme/name/slug. Falls back to package.json name
// when app.json is missing (common in projects using app.config.js/ts).
// The scheme may be empty when using dynamic config; callers should prompt
// for it or accept the --app-scheme flag.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.ProjectInfo: Extracted project information
//   - error: Any error that occurred during extraction
func (p *ExpoProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	appJSON, err := parseAppJSON(dir)
	if err == nil {
		return &hotreload.ProjectInfo{
			Name:     appJSON.Expo.Name,
			Platform: "cross-platform",
			Expo: &hotreload.ExpoProjectInfo{
				Scheme: appJSON.Expo.Scheme,
				Name:   appJSON.Expo.Name,
				Slug:   appJSON.Expo.Slug,
			},
		}, nil
	}

	packageJSONPath := filepath.Join(dir, "package.json")
	data, readErr := os.ReadFile(packageJSONPath)
	if readErr != nil {
		return nil, fmt.Errorf("no app.json or package.json found: %w (app.json: %w)", readErr, err)
	}

	var pkg packageJSON
	if jsonErr := json.Unmarshal(data, &pkg); jsonErr != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", jsonErr)
	}

	return &hotreload.ProjectInfo{
		Name:     pkg.Name,
		Platform: "cross-platform",
		Expo: &hotreload.ExpoProjectInfo{
			Name: pkg.Name,
		},
	}, nil
}

// GetDefaultConfig returns a default configuration for Expo projects.
//
// Parameters:
//   - info: Project information from GetProjectInfo
//
// Returns:
//   - *config.ProviderConfig: Default configuration with app scheme and port 8081
func (p *ExpoProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	cfg := &config.ProviderConfig{
		Port: 8081,
	}
	if info.Expo != nil {
		cfg.AppScheme = info.Expo.Scheme
	}
	return cfg
}

// CreateDevServer creates an Expo development server instance.
//
// Parameters:
//   - cfg: Provider configuration from .revyl/config.yaml
//   - workDir: Working directory for the project
//
// Returns:
//   - hotreload.DevServer: The Expo dev server instance
//   - error: Any error that occurred
func (p *ExpoProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	port := cfg.GetPort("expo")
	return NewExpoDevServer(workDir, cfg.AppScheme, port, cfg.UseExpPrefix), nil
}

// IsSupported returns true as Expo is fully supported.
//
// Returns:
//   - bool: true
func (p *ExpoProvider) IsSupported() bool {
	return true
}

// AppJSON represents the structure of an Expo app.json file.
type AppJSON struct {
	Expo struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Scheme string `json:"scheme"`
	} `json:"expo"`
}

// parseAppJSON reads and parses the app.json file from a directory.
//
// Parameters:
//   - dir: The directory containing app.json
//
// Returns:
//   - *AppJSON: Parsed app.json contents
//   - error: Any error that occurred during parsing
func parseAppJSON(dir string) (*AppJSON, error) {
	appJSONPath := filepath.Join(dir, "app.json")
	data, err := os.ReadFile(appJSONPath)
	if err != nil {
		return nil, err
	}

	var appJSON AppJSON
	if err := json.Unmarshal(data, &appJSON); err != nil {
		return nil, err
	}

	return &appJSON, nil
}
