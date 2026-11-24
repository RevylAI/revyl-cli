package providers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	hotreload.RegisterProvider(&AndroidProvider{})
}

// AndroidProvider detects native Android projects; live hot reload is not supported.
type AndroidProvider struct{}

func NewAndroidProvider() hotreload.Provider {
	return &AndroidProvider{}
}

func (p *AndroidProvider) Name() string {
	return "android"
}

func (p *AndroidProvider) DisplayName() string {
	return "Android"
}

func (p *AndroidProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	var indicators []string

	androidDir := filepath.Join(dir, "android")
	if info, err := os.Stat(androidDir); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(androidDir, "build.gradle")); err == nil {
			indicators = append(indicators, "android/build.gradle")
		}
		if _, err := os.Stat(filepath.Join(androidDir, "build.gradle.kts")); err == nil {
			indicators = append(indicators, "android/build.gradle.kts")
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "build.gradle")); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "app", "build.gradle")); err == nil {
			indicators = append(indicators, "build.gradle", "app/build.gradle")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "build.gradle.kts")); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "app", "build.gradle.kts")); err == nil {
			indicators = append(indicators, "build.gradle.kts", "app/build.gradle.kts")
		}
	}

	manifestPaths := []string{
		filepath.Join(dir, "app", "src", "main", "AndroidManifest.xml"),
		filepath.Join(dir, "android", "app", "src", "main", "AndroidManifest.xml"),
	}
	for _, path := range manifestPaths {
		if _, err := os.Stat(path); err == nil {
			indicators = append(indicators, "AndroidManifest.xml")
			break
		}
	}

	if len(indicators) == 0 {
		return nil, nil
	}

	return &hotreload.DetectionResult{
		Provider:   "android",
		Confidence: 0.6,
		Platform:   "android",
		Indicators: indicators,
	}, nil
}

func (p *AndroidProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	projectName := filepath.Base(dir)

	var projectPath string
	androidDir := filepath.Join(dir, "android")
	if info, err := os.Stat(androidDir); err == nil && info.IsDir() {
		projectPath = androidDir
	} else {
		projectPath = dir
	}

	// TODO: Parse AndroidManifest.xml for package name
	// For now, return basic info
	return &hotreload.ProjectInfo{
		Name:     projectName,
		Platform: "android",
		Android: &hotreload.AndroidProjectInfo{
			PackageName: "",
			ProjectPath: projectPath,
		},
	}, nil
}

func (p *AndroidProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	cfg := &config.ProviderConfig{
		Port: 8081,
	}
	if info.Android != nil {
		cfg.PackageName = info.Android.PackageName
	}
	return cfg
}

func (p *AndroidProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	return nil, fmt.Errorf("Android hot reload is not yet supported. Coming soon!")
}

func (p *AndroidProvider) IsSupported() bool {
	return false
}
