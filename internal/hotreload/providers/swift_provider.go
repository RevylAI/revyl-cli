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
	// Register the Swift provider with the default registry
	hotreload.RegisterProvider(&SwiftProvider{})
}

// SwiftProvider implements the Provider interface for native iOS/Swift projects.
//
// Currently a stub - detection works but setup returns "not yet supported".
// Future implementation will use InjectionIII for hot reload.
//
// Detection looks for:
//   - *.xcodeproj or *.xcworkspace files
//   - Swift source files
//   - Package.swift (Swift Package Manager)
type SwiftProvider struct{}

// NewSwiftProvider creates a new Swift provider instance.
//
// Returns:
//   - hotreload.Provider: A new Swift provider
func NewSwiftProvider() hotreload.Provider {
	return &SwiftProvider{}
}

// Name returns the unique identifier for this provider.
//
// Returns:
//   - string: "swift"
func (p *SwiftProvider) Name() string {
	return "swift"
}

// DisplayName returns the human-readable name for this provider.
//
// Returns:
//   - string: "Swift/iOS"
func (p *SwiftProvider) DisplayName() string {
	return "Swift/iOS"
}

// Detect checks if this is a Swift/iOS project.
//
// Detection criteria:
//   - *.xcodeproj or *.xcworkspace exists
//   - Swift files present
//   - Package.swift exists (Swift Package Manager)
//
// Uses lower confidence (0.7) than Expo to avoid false positives on
// React Native projects that have ios/ directories.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.DetectionResult: Detection result with confidence 0.7, or nil if not detected
//   - error: Any error that occurred during detection
func (p *SwiftProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}

	var indicators []string

	// Look for Xcode project/workspace
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".xcodeproj") {
			indicators = append(indicators, name)
		}
		if strings.HasSuffix(name, ".xcworkspace") {
			indicators = append(indicators, name)
		}
	}

	// Look for Swift files in root
	swiftFiles, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	if len(swiftFiles) > 0 {
		indicators = append(indicators, "Swift files found")
	}

	// Check for Swift Package Manager
	if _, err := os.Stat(filepath.Join(dir, "Package.swift")); err == nil {
		indicators = append(indicators, "Package.swift")
	}

	// Also check ios/ subdirectory (common in cross-platform projects)
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

	// Lower confidence than Expo to avoid false positives on React Native projects
	return &hotreload.DetectionResult{
		Provider:   "swift",
		Confidence: 0.7,
		Platform:   "ios",
		Indicators: indicators,
	}, nil
}

// GetProjectInfo extracts Swift/iOS project information.
//
// Currently extracts basic information from Xcode project structure.
// Future: Parse Xcode project for bundle ID and scheme.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.ProjectInfo: Extracted project information
//   - error: Any error that occurred during extraction
func (p *SwiftProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	entries, _ := os.ReadDir(dir)

	var projectName string
	var projectPath string

	// Find Xcode project/workspace
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

	// Check ios/ subdirectory if not found in root
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

// GetDefaultConfig returns a default configuration for Swift projects.
//
// Parameters:
//   - info: Project information from GetProjectInfo
//
// Returns:
//   - *config.ProviderConfig: Default configuration with InjectionIII path
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

// CreateDevServer returns an error as Swift hot reload is not yet supported.
//
// Parameters:
//   - cfg: Provider configuration (unused)
//   - workDir: Working directory (unused)
//
// Returns:
//   - hotreload.DevServer: nil
//   - error: "Swift hot reload is not yet supported"
func (p *SwiftProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	return nil, fmt.Errorf("Swift hot reload is not yet supported. Coming soon!")
}

// IsSupported returns false as Swift hot reload is not yet implemented.
//
// Returns:
//   - bool: false
func (p *SwiftProvider) IsSupported() bool {
	return false
}
