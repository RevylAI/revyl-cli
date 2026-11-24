// Package providers registers hotreload.Provider and hotreload.DevServer implementations.
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
	hotreload.RegisterProvider(&ExpoProvider{})
}

// ExpoProvider implements hotreload.Provider for Expo projects.
type ExpoProvider struct{}

func NewExpoProvider() hotreload.Provider {
	return &ExpoProvider{}
}

func (p *ExpoProvider) Name() string {
	return "expo"
}

func (p *ExpoProvider) DisplayName() string {
	return "Expo"
}

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

func (p *ExpoProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	cfg := &config.ProviderConfig{
		Port: 8081,
	}
	if info.Expo != nil {
		cfg.AppScheme = info.Expo.Scheme
		if strings.TrimSpace(cfg.AppScheme) == "" && strings.TrimSpace(info.Expo.Slug) != "" {
			cfg.AppScheme = strings.TrimSpace(info.Expo.Slug)
			cfg.UseExpPrefix = true
		}
	}
	return cfg
}

func (p *ExpoProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	port := cfg.GetPort("expo")
	return NewExpoDevServer(workDir, cfg.AppScheme, port, cfg.UseExpPrefix), nil
}

func (p *ExpoProvider) IsSupported() bool {
	return true
}

type AppJSON struct {
	Expo struct {
		Name   string `json:"name"`
		Slug   string `json:"slug"`
		Scheme string `json:"scheme"`
	} `json:"expo"`
}

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
