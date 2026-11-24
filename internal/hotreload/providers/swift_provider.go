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
	hotreload.RegisterProvider(&SwiftProvider{})
}

// SwiftProvider detects native iOS/Swift projects; live hot reload is not supported.
type SwiftProvider struct{}

func NewSwiftProvider() hotreload.Provider {
	return &SwiftProvider{}
}

func (p *SwiftProvider) Name() string {
	return "swift"
}

func (p *SwiftProvider) DisplayName() string {
	return "Swift/iOS"
}

func (p *SwiftProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var indicators []string

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".xcodeproj") {
			indicators = append(indicators, name)
		}
		if strings.HasSuffix(name, ".xcworkspace") {
			indicators = append(indicators, name)
		}
	}

	swiftFiles, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	if len(swiftFiles) > 0 {
		indicators = append(indicators, "Swift files found")
	}

	if _, err := os.Stat(filepath.Join(dir, "Package.swift")); err == nil {
		indicators = append(indicators, "Package.swift")
	}

	iosDir := filepath.Join(dir, "ios")
	if info, err := os.Stat(iosDir); err == nil && info.IsDir() {
		iosEntries, _ := os.ReadDir(iosDir)
		for _, entry := range iosEntries {
			name := entry.Name()
			if strings.HasSuffix(name, ".xcodeproj") {
				indicators = append(indicators, "ios/"+name)
			}
			if strings.HasSuffix(name, ".xcworkspace") {
				indicators = append(indicators, "ios/"+name)
			}
		}
	}

	if len(indicators) == 0 {
		return nil, nil
	}

	return &hotreload.DetectionResult{
		Provider:   "swift",
		Confidence: 0.7,
		Platform:   "ios",
		Indicators: indicators,
	}, nil
}

func (p *SwiftProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	entries, _ := os.ReadDir(dir)

	var projectName string
	var projectPath string

	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".xcodeproj") {
			projectName = strings.TrimSuffix(name, ".xcodeproj")
			projectPath = filepath.Join(dir, name)
			break
		}
		if strings.HasSuffix(name, ".xcworkspace") {
			projectName = strings.TrimSuffix(name, ".xcworkspace")
			projectPath = filepath.Join(dir, name)
			break
		}
	}

	if projectName == "" {
		iosDir := filepath.Join(dir, "ios")
		if iosEntries, err := os.ReadDir(iosDir); err == nil {
			for _, entry := range iosEntries {
				name := entry.Name()
				if strings.HasSuffix(name, ".xcodeproj") {
					projectName = strings.TrimSuffix(name, ".xcodeproj")
					projectPath = filepath.Join(iosDir, name)
					break
				}
				if strings.HasSuffix(name, ".xcworkspace") {
					projectName = strings.TrimSuffix(name, ".xcworkspace")
					projectPath = filepath.Join(iosDir, name)
					break
				}
			}
		}
	}

	if projectName == "" {
		projectName = filepath.Base(dir)
	}

	return &hotreload.ProjectInfo{
		Name:     projectName,
		Platform: "ios",
		Swift: &hotreload.SwiftProjectInfo{
			// TODO: Extract bundle ID from Xcode project
			BundleID:    "",
			Scheme:      projectName,
			ProjectPath: projectPath,
		},
	}, nil
}

func (p *SwiftProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	cfg := &config.ProviderConfig{
		InjectionPath: "/Applications/InjectionIII.app",
	}
	if info.Swift != nil {
		cfg.ProjectPath = info.Swift.ProjectPath
		cfg.BundleID = info.Swift.BundleID
	}
	return cfg
}

func (p *SwiftProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	return nil, fmt.Errorf("Swift hot reload is not yet supported. Coming soon!")
}

func (p *SwiftProvider) IsSupported() bool {
	return false
}
