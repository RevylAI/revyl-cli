package providers

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
)

func init() {
	// Register the Android provider with the default registry
	hotreload.RegisterProvider(&AndroidProvider{})
}

// AndroidProvider implements the Provider interface for native Android projects.
//
// Currently a stub - detection works but setup returns "not yet supported".
// Future implementation will use Metro/Gradle for hot reload.
//
// Detection looks for:
//   - android/ directory with build.gradle
//   - Root-level build.gradle with app/build.gradle
//   - AndroidManifest.xml
type AndroidProvider struct{}

// NewAndroidProvider creates a new Android provider instance.
//
// Returns:
//   - hotreload.Provider: A new Android provider
func NewAndroidProvider() hotreload.Provider {
	return &AndroidProvider{}
}

// Name returns the unique identifier for this provider.
//
// Returns:
//   - string: "android"
func (p *AndroidProvider) Name() string {
	return "android"
}

// DisplayName returns the human-readable name for this provider.
//
// Returns:
//   - string: "Android"
func (p *AndroidProvider) DisplayName() string {
	return "Android"
}

// Detect checks if this is an Android project.
//
// Detection criteria:
//   - android/ directory with build.gradle or build.gradle.kts
//   - Root-level build.gradle with app/build.gradle
//   - AndroidManifest.xml present
//
// Uses lower confidence (0.6) than Expo to avoid conflicts with
// cross-platform projects where Expo should take priority.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.DetectionResult: Detection result with confidence 0.6, or nil if not detected
//   - error: Any error that occurred during detection
func (p *AndroidProvider) Detect(dir string) (*hotreload.DetectionResult, error) {
	var indicators []string

	// Check for android/ directory (common in cross-platform projects)
	androidDir := filepath.Join(dir, "android")
	if info, err := os.Stat(androidDir); err == nil && info.IsDir() {
		// Check for build.gradle in android/
		if _, err := os.Stat(filepath.Join(androidDir, "build.gradle")); err == nil {
			indicators = append(indicators, "android/build.gradle")
		}
		if _, err := os.Stat(filepath.Join(androidDir, "build.gradle.kts")); err == nil {
			indicators = append(indicators, "android/build.gradle.kts")
		}
	}

	// Check for root-level Android project
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

	// Check for AndroidManifest.xml
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

	// Lower confidence to avoid conflicts with Expo/React Native
	return &hotreload.DetectionResult{
		Provider:   "android",
		Confidence: 0.6,
		Platform:   "android",
		Indicators: indicators,
	}, nil
}

// GetProjectInfo extracts Android project information.
//
// Currently extracts basic information from project structure.
// Future: Parse AndroidManifest.xml for package name.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.ProjectInfo: Extracted project information
//   - error: Any error that occurred during extraction
func (p *AndroidProvider) GetProjectInfo(dir string) (*hotreload.ProjectInfo, error) {
	projectName := filepath.Base(dir)

	// Determine project path
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

// GetDefaultConfig returns a default configuration for Android projects.
//
// Parameters:
//   - info: Project information from GetProjectInfo
//
// Returns:
//   - *config.ProviderConfig: Default configuration with port 8081
func (p *AndroidProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	cfg := &config.ProviderConfig{
		Port: 8081,
	}
	if info.Android != nil {
		cfg.PackageName = info.Android.PackageName
	}
	return cfg
}

// CreateDevServer returns an error as Android hot reload is not yet supported.
//
// Parameters:
//   - cfg: Provider configuration (unused)
//   - workDir: Working directory (unused)
//
// Returns:
//   - hotreload.DevServer: nil
//   - error: "Android hot reload is not yet supported"
func (p *AndroidProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	return nil, fmt.Errorf("Android hot reload is not yet supported. Coming soon!")
}

// IsSupported returns false as Android hot reload is not yet implemented.
//
// Returns:
//   - bool: false
func (p *AndroidProvider) IsSupported() bool {
	return false
}
