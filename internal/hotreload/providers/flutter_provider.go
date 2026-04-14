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

// FlutterProvider implements the Provider interface for Flutter projects.
//
// Flutter uses a rebuild-based dev loop with automatic file watching:
// Dart file changes trigger `flutter build` + delta push to the cloud device.
// No live hot-reload dev server is needed; the CLI watches files locally.
//
// Detection looks for:
//   - pubspec.yaml with "sdk: flutter" dependency
//   - lib/ directory with .dart files
type FlutterProvider struct{}

// Name returns the unique identifier for this provider.
//
// Returns:
//   - string: "flutter"
func (p *FlutterProvider) Name() string {
	return "flutter"
}

// DisplayName returns the human-readable name for this provider.
//
// Returns:
//   - string: "Flutter"
func (p *FlutterProvider) DisplayName() string {
	return "Flutter"
}

// Detect checks if this is a Flutter project.
//
// Detection criteria:
//   - pubspec.yaml exists and contains "sdk: flutter"
//   - Optionally: lib/ directory with .dart files
//
// Confidence is 0.75, between Swift (0.7) and bare React Native (0.8),
// to avoid conflicts with cross-platform projects that may also contain
// ios/ or android/ directories.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.DetectionResult: Detection result with confidence 0.75, or nil if not detected
//   - error: Any error that occurred during detection
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

// GetProjectInfo extracts Flutter project information from pubspec.yaml.
//
// Parses the project name from the "name:" field in pubspec.yaml.
//
// Parameters:
//   - dir: The project directory to analyze
//
// Returns:
//   - *hotreload.ProjectInfo: Extracted project information
//   - error: Any error that occurred during extraction
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

// GetDefaultConfig returns a default configuration for Flutter projects.
//
// Flutter's rebuild-based dev loop requires no special provider config
// (no port, scheme, or bundle ID). The build command and output are
// managed by the build.platforms section of config.yaml.
//
// Parameters:
//   - info: Project information from GetProjectInfo
//
// Returns:
//   - *config.ProviderConfig: Empty default configuration
func (p *FlutterProvider) GetDefaultConfig(info *hotreload.ProjectInfo) *config.ProviderConfig {
	return &config.ProviderConfig{}
}

// CreateDevServer returns an error because Flutter uses a rebuild-based dev loop
// rather than a live dev server. The rebuild loop is handled by runDevRebuildOnly
// in dev.go with automatic file watching.
//
// Parameters:
//   - cfg: Provider configuration (unused)
//   - workDir: Working directory (unused)
//
// Returns:
//   - hotreload.DevServer: nil
//   - error: Explanation that Flutter uses rebuild-based dev loop
func (p *FlutterProvider) CreateDevServer(cfg *config.ProviderConfig, workDir string) (hotreload.DevServer, error) {
	return nil, fmt.Errorf("Flutter uses an auto-rebuild dev loop — no dev server needed")
}

// IsSupported returns false because Flutter does not use a hot-reload dev server.
// It routes through the rebuild-only path in revyl dev with file watching.
//
// Returns:
//   - bool: false
func (p *FlutterProvider) IsSupported() bool {
	return false
}

// parsePubspecName extracts the "name:" field from a pubspec.yaml file.
// Returns empty string if the file cannot be read or parsed.
//
// Parameters:
//   - path: Path to the pubspec.yaml file
//
// Returns:
//   - string: The project name, or empty string on failure
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

// dirExists checks if a directory exists at the given path.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
