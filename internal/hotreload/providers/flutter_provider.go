package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	hotreload.RegisterProvider(&FlutterProvider{})
}

// FlutterProvider detects Flutter projects; revyl dev uses a rebuild-based loop rather than a live dev server.
type FlutterProvider struct{}

func (p *FlutterProvider) Name() string {
	return "flutter"
}

func (p *FlutterProvider) DisplayName() string {
	return "Flutter"
}

func (p *FlutterProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	pubspecPath := filepath.Join(dir, "pubspec.yaml")
	content, err := os.ReadFile(pubspecPath)
	if err != nil {
		return nil, nil
	}

	if !strings.Contains(string(content), "sdk: flutter") {
		return nil, nil
	}

	indicators := []string{"pubspec.yaml (sdk: flutter)"}

	libDir := filepath.Join(dir, "lib")
	if info, err := os.Stat(libDir); err == nil && info.IsDir() {
		dartFiles, _ := filepath.Glob(filepath.Join(libDir, "*.dart"))
		if len(dartFiles) > 0 {
			indicators = append(indicators, fmt.Sprintf("lib/ (%d .dart files)", len(dartFiles)))
		}
	}

	hasAndroid := dirExists(filepath.Join(dir, "android"))
	hasIOS := dirExists(filepath.Join(dir, "ios"))
	if hasAndroid {
		indicators = append(indicators, "android/")
	}
	if hasIOS {
		indicators = append(indicators, "ios/")
	}

	platform := "cross-platform"
	if hasAndroid && !hasIOS {
		platform = "android"
	} else if hasIOS && !hasAndroid {
		platform = "ios"
	}

	return &hotreload.DetectionResult{
		Provider:   "flutter",
		Confidence: 0.75,
		Platform:   platform,
		Indicators: indicators,
	}, nil
}

func (p *FlutterProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	name := parsePubspecName(filepath.Join(dir, "pubspec.yaml"))
	if name == "" {
		name = filepath.Base(dir)
	}

	platform := "cross-platform"
	if dirExists(filepath.Join(dir, "android")) && !dirExists(filepath.Join(dir, "ios")) {
		platform = "android"
	} else if dirExists(filepath.Join(dir, "ios")) && !dirExists(filepath.Join(dir, "android")) {
		platform = "ios"
	}

	return &hotreload.ProjectInfo{
		Name:     name,
		Platform: platform,
		Flutter: &hotreload.FlutterProjectInfo{
			Name: name,
		},
	}, nil
}

func (p *FlutterProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	return &config.ProviderConfig{}
}

func (p *FlutterProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	return nil, fmt.Errorf("Flutter uses an auto-rebuild dev loop — no dev server needed")
}

func (p *FlutterProvider) IsSupported() bool {
	return false
}

func parsePubspecName(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name:") {
			value := strings.TrimPrefix(trimmed, "name:")
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
