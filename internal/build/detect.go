// Package build provides build system detection and execution.
//
// This package handles auto-detecting build systems (Gradle, Xcode, Expo,
// Flutter, React Native) and executing build commands.
package build

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// System represents a build system type.
type System int

const (
	// SystemUnknown represents an unknown build system.
	SystemUnknown System = iota

	// SystemGradle represents a Gradle (Android) project.
	SystemGradle

	// SystemXcode represents an Xcode (iOS) project.
	SystemXcode

	// SystemExpo represents an Expo project.
	SystemExpo

	// SystemFlutter represents a Flutter project.
	SystemFlutter

	// SystemReactNative represents a React Native project.
	SystemReactNative
)

// String returns the string representation of a build system.
func (s System) String() string {
	switch s {
	case SystemGradle:
		return "gradle"
	case SystemXcode:
		return "xcode"
	case SystemExpo:
		return "expo"
	case SystemFlutter:
		return "flutter"
	case SystemReactNative:
		return "react-native"
	default:
		return "unknown"
	}
}

// DetectedBuild contains information about a detected build system.
type DetectedBuild struct {
	// System is the detected build system.
	System System

	// Command is the suggested build command.
	Command string

	// Output is the expected output path.
	Output string

	// Platform is the detected platform (ios, android).
	Platform string

	// Variants contains detected build variants.
	Variants map[string]BuildVariant
}

// BuildVariant represents a build variant.
type BuildVariant struct {
	Command string
	Output  string
}

// EASBuildError represents a detected EAS build error with guidance.
// This type is used to provide helpful error messages when EAS builds fail
// due to common configuration issues like missing credentials.
type EASBuildError struct {
	// OriginalError is the original error message from the build.
	OriginalError string

	// Guidance contains actionable instructions to fix the error.
	Guidance string
}

// Error implements the error interface.
//
// Returns:
//   - string: The original error message
func (e *EASBuildError) Error() string {
	return e.OriginalError
}

// detectEASError checks build output for known EAS error patterns and returns
// helpful guidance if a known error is detected.
//
// Parameters:
//   - output: The captured build output lines
//   - platform: The target platform (ios, android)
//
// Returns:
//   - *EASBuildError: Error with guidance if detected, nil otherwise
func detectEASError(output []string, platform string) *EASBuildError {
	fullOutput := strings.Join(output, "\n")

	// Android keystore not configured
	if strings.Contains(fullOutput, "Generating a new Keystore is not supported in --non-interactive mode") {
		return &EASBuildError{
			OriginalError: "Android keystore not configured",
			Guidance: `To fix this, you need to set up Android signing credentials:

Option 1: Generate keystore on Expo servers (recommended for first-time setup)
  Run this command once interactively:
  $ npx eas build --platform android --profile development

  This will prompt you to generate a keystore and store it on Expo's servers.
  After this, revyl build upload will work automatically.

Option 2: Use local credentials
  Create a credentials.json file in your project root:
  {
    "android": {
      "keystore": {
        "keystorePath": "path/to/your.keystore",
        "keystorePassword": "your-password",
        "keyAlias": "your-alias",
        "keyPassword": "your-key-password"
      }
    }
  }

Learn more: https://docs.expo.dev/app-signing/local-credentials/`,
		}
	}

	// iOS provisioning profile issues
	if platform == "ios" && strings.Contains(fullOutput, "provisioning profile") {
		return &EASBuildError{
			OriginalError: "iOS provisioning profile not configured",
			Guidance: `To fix this, set up iOS signing credentials:

Run this command once interactively:
  $ npx eas build --platform ios --profile development

This will guide you through Apple Developer account setup.

Learn more: https://docs.expo.dev/app-signing/app-credentials/`,
		}
	}

	// iOS distribution certificate issues
	if platform == "ios" && strings.Contains(fullOutput, "distribution certificate") {
		return &EASBuildError{
			OriginalError: "iOS distribution certificate not configured",
			Guidance: `To fix this, set up iOS signing credentials:

Run this command once interactively:
  $ npx eas build --platform ios --profile development

This will guide you through Apple Developer account setup.

Learn more: https://docs.expo.dev/app-signing/app-credentials/`,
		}
	}

	return nil
}

// detectPlatformFromCommand extracts the platform from a build command.
// This is used to provide platform-specific error guidance.
//
// Parameters:
//   - command: The build command to analyze
//
// Returns:
//   - string: The detected platform ("ios", "android", or empty string)
func detectPlatformFromCommand(command string) string {
	cmdLower := strings.ToLower(command)

	// Check for explicit platform flags
	if strings.Contains(cmdLower, "--platform ios") || strings.Contains(cmdLower, "--platform=ios") {
		return "ios"
	}
	if strings.Contains(cmdLower, "--platform android") || strings.Contains(cmdLower, "--platform=android") {
		return "android"
	}

	// Check for platform-specific keywords
	if strings.Contains(cmdLower, "ios") || strings.Contains(cmdLower, "iphonesimulator") ||
		strings.Contains(cmdLower, "xcodebuild") || strings.Contains(cmdLower, ".xcworkspace") {
		return "ios"
	}
	if strings.Contains(cmdLower, "android") || strings.Contains(cmdLower, "apk") ||
		strings.Contains(cmdLower, "aab") || strings.Contains(cmdLower, "gradlew") {
		return "android"
	}

	return ""
}

// Detect auto-detects the build system in a directory.
//
// Parameters:
//   - dir: The directory to scan
//
// Returns:
//   - *DetectedBuild: Information about the detected build system
//   - error: Any error that occurred during detection
func Detect(dir string) (*DetectedBuild, error) {
	// Check for various build systems in order of specificity

	// 1. Check for Expo (app.json with expo key)
	if detected := detectExpo(dir); detected != nil {
		return detected, nil
	}

	// 2. Check for Flutter (pubspec.yaml with flutter key)
	if detected := detectFlutter(dir); detected != nil {
		return detected, nil
	}

	// 3. Check for React Native (package.json with react-native)
	if detected := detectReactNative(dir); detected != nil {
		return detected, nil
	}

	// 4. Check for Gradle (build.gradle or build.gradle.kts)
	if detected := detectGradle(dir); detected != nil {
		return detected, nil
	}

	// 5. Check for Xcode (*.xcodeproj or *.xcworkspace)
	if detected := detectXcode(dir); detected != nil {
		return detected, nil
	}

	return &DetectedBuild{System: SystemUnknown}, nil
}

// detectExpo checks for an Expo project.
func detectExpo(dir string) *DetectedBuild {
	appJSONPath := filepath.Join(dir, "app.json")
	if _, err := os.Stat(appJSONPath); err != nil {
		return nil
	}

	// Check if it's an Expo project by looking for expo in package.json
	packageJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil
	}

	if !strings.Contains(string(data), "\"expo\"") {
		return nil
	}

	// Use EAS Build local commands (expo build:* is deprecated)
	// Include --non-interactive flag to prevent stdin prompts in CI/automated environments
	return &DetectedBuild{
		System:   SystemExpo,
		Command:  "npx eas build --local --platform ios --profile development --non-interactive",
		Output:   "build-*.tar.gz",
		Platform: "ios",
		Variants: map[string]BuildVariant{
			"ios": {
				Command: "npx eas build --local --platform ios --profile development --non-interactive",
				Output:  "build-*.tar.gz",
			},
			"android": {
				Command: "npx eas build --local --platform android --profile development --non-interactive",
				Output:  "build-*.apk",
			},
		},
	}
}

// detectFlutter checks for a Flutter project.
func detectFlutter(dir string) *DetectedBuild {
	pubspecPath := filepath.Join(dir, "pubspec.yaml")
	data, err := os.ReadFile(pubspecPath)
	if err != nil {
		return nil
	}

	if !strings.Contains(string(data), "flutter:") {
		return nil
	}

	return &DetectedBuild{
		System:   SystemFlutter,
		Command:  "flutter build apk --debug",
		Output:   "build/app/outputs/flutter-apk/app-debug.apk",
		Platform: "android",
		Variants: map[string]BuildVariant{
			"android-debug": {
				Command: "flutter build apk --debug",
				Output:  "build/app/outputs/flutter-apk/app-debug.apk",
			},
			"android-release": {
				Command: "flutter build apk --release",
				Output:  "build/app/outputs/flutter-apk/app-release.apk",
			},
			"ios-debug": {
				Command: "flutter build ios --debug --simulator",
				Output:  "build/ios/iphonesimulator/*.app",
			},
		},
	}
}

// detectReactNative checks for a React Native project.
func detectReactNative(dir string) *DetectedBuild {
	packageJSONPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil
	}

	if !strings.Contains(string(data), "\"react-native\"") {
		return nil
	}

	// Check for android directory
	androidDir := filepath.Join(dir, "android")
	if _, err := os.Stat(androidDir); err == nil {
		return &DetectedBuild{
			System:   SystemReactNative,
			Command:  "cd android && ./gradlew assembleDebug",
			Output:   "android/app/build/outputs/apk/debug/app-debug.apk",
			Platform: "android",
			Variants: map[string]BuildVariant{
				"android-debug": {
					Command: "cd android && ./gradlew assembleDebug",
					Output:  "android/app/build/outputs/apk/debug/app-debug.apk",
				},
				"android-release": {
					Command: "cd android && ./gradlew assembleRelease",
					Output:  "android/app/build/outputs/apk/release/app-release.apk",
				},
			},
		}
	}

	// Check for ios directory
	iosDir := filepath.Join(dir, "ios")
	if _, err := os.Stat(iosDir); err == nil {
		return &DetectedBuild{
			System:   SystemReactNative,
			Command:  "cd ios && xcodebuild -workspace *.xcworkspace -scheme * -configuration Debug -sdk iphonesimulator",
			Output:   "ios/build/Build/Products/Debug-iphonesimulator/*.app",
			Platform: "ios",
		}
	}

	return nil
}

// detectGradle checks for a Gradle project.
func detectGradle(dir string) *DetectedBuild {
	// Check for build.gradle or build.gradle.kts
	gradleFile := filepath.Join(dir, "build.gradle")
	gradleKtsFile := filepath.Join(dir, "build.gradle.kts")

	if _, err := os.Stat(gradleFile); err != nil {
		if _, err := os.Stat(gradleKtsFile); err != nil {
			return nil
		}
	}

	// Determine the gradle wrapper path
	gradlewPath := "./gradlew"
	if _, err := os.Stat(filepath.Join(dir, "gradlew")); err != nil {
		gradlewPath = "gradle"
	}

	// Try to find the app module output path
	appBuildGradle := filepath.Join(dir, "app", "build.gradle")
	if _, err := os.Stat(appBuildGradle); err != nil {
		appBuildGradle = filepath.Join(dir, "app", "build.gradle.kts")
	}

	outputPath := "app/build/outputs/apk/debug/app-debug.apk"
	if _, err := os.Stat(appBuildGradle); err == nil {
		// Standard Android project structure
		outputPath = "app/build/outputs/apk/debug/app-debug.apk"
	}

	return &DetectedBuild{
		System:   SystemGradle,
		Command:  gradlewPath + " assembleDebug",
		Output:   outputPath,
		Platform: "android",
		Variants: map[string]BuildVariant{
			"debug": {
				Command: gradlewPath + " assembleDebug",
				Output:  "app/build/outputs/apk/debug/app-debug.apk",
			},
			"release": {
				Command: gradlewPath + " assembleRelease",
				Output:  "app/build/outputs/apk/release/app-release.apk",
			},
		},
	}
}

// detectXcode checks for an Xcode project.
func detectXcode(dir string) *DetectedBuild {
	// Look for .xcworkspace first (preferred)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var workspaceName string
	var projectName string

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".xcworkspace") {
			workspaceName = entry.Name()
		}
		if strings.HasSuffix(entry.Name(), ".xcodeproj") {
			projectName = entry.Name()
		}
	}

	if workspaceName == "" && projectName == "" {
		return nil
	}

	// Prefer workspace over project
	var buildCmd string
	if workspaceName != "" {
		schemeName := strings.TrimSuffix(workspaceName, ".xcworkspace")
		buildCmd = fmt.Sprintf("xcodebuild -workspace %s -scheme %s -configuration Debug -sdk iphonesimulator -derivedDataPath build",
			workspaceName, schemeName)
	} else {
		schemeName := strings.TrimSuffix(projectName, ".xcodeproj")
		buildCmd = fmt.Sprintf("xcodebuild -project %s -scheme %s -configuration Debug -sdk iphonesimulator -derivedDataPath build",
			projectName, schemeName)
	}

	return &DetectedBuild{
		System:   SystemXcode,
		Command:  buildCmd,
		Output:   "build/Build/Products/Debug-iphonesimulator/*.app",
		Platform: "ios",
		Variants: map[string]BuildVariant{
			"debug": {
				Command: buildCmd,
				Output:  "build/Build/Products/Debug-iphonesimulator/*.app",
			},
		},
	}
}

// Runner executes build commands.
type Runner struct {
	workDir string
}

// NewRunner creates a new build runner.
//
// Parameters:
//   - workDir: The working directory for builds
//
// Returns:
//   - *Runner: A new runner instance
func NewRunner(workDir string) *Runner {
	return &Runner{workDir: workDir}
}

// Run executes a build command.
//
// Parameters:
//   - command: The command to execute
//   - onOutput: Callback for each line of output
//
// Returns:
//   - error: Any error that occurred during execution
func (r *Runner) Run(command string, onOutput func(string)) error {
	// Split command into parts
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	// Handle cd commands
	if parts[0] == "cd" && len(parts) >= 2 {
		// Find the && separator
		for i, part := range parts {
			if part == "&&" {
				// Change to the directory
				newDir := filepath.Join(r.workDir, parts[1])
				r.workDir = newDir
				// Execute the rest of the command
				return r.Run(strings.Join(parts[i+1:], " "), onOutput)
			}
		}
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = r.workDir

	// Set CI environment variables to ensure non-interactive mode for build tools
	// CI=1 tells most build tools (including EAS CLI) to run in non-interactive mode
	// EAS_NO_VCS=1 prevents EAS from requiring git repository checks
	cmd.Env = append(os.Environ(), "CI=1", "EAS_NO_VCS=1")

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Collect output for error detection
	var outputLines []string
	var outputMu sync.Mutex

	// Read output in goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			outputMu.Lock()
			outputLines = append(outputLines, line)
			outputMu.Unlock()
			if onOutput != nil {
				onOutput(line)
			}
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			outputMu.Lock()
			outputLines = append(outputLines, line)
			outputMu.Unlock()
			if onOutput != nil {
				onOutput(line)
			}
		}
	}()

	// Wait for output readers to finish
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		// Check for known EAS errors and provide guidance
		platform := detectPlatformFromCommand(command)
		if easErr := detectEASError(outputLines, platform); easErr != nil {
			return easErr
		}
		return fmt.Errorf("command failed: %w", err)
	}

	return nil
}

// GenerateVersionString generates a version string for a build.
//
// Returns:
//   - string: A version string like "local-20240115-103045"
func GenerateVersionString() string {
	return fmt.Sprintf("local-%s", time.Now().Format("20060102-150405"))
}

// CollectMetadata collects build metadata.
//
// Parameters:
//   - workDir: The working directory
//   - command: The build command used
//   - variant: The build variant (optional)
//   - duration: The build duration
//
// Returns:
//   - map[string]interface{}: The collected metadata
func CollectMetadata(workDir, command, variant string, duration time.Duration) map[string]interface{} {
	metadata := map[string]interface{}{
		"source": map[string]interface{}{
			"type":        "local",
			"machine":     getHostname(),
			"user":        getUsername(),
			"working_dir": workDir,
		},
		"build": map[string]interface{}{
			"command":     command,
			"variant":     variant,
			"duration_ms": duration.Milliseconds(),
		},
		"cli": map[string]interface{}{
			"version":   "1.0.0",
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}

	// Add git info if available
	if gitInfo := collectGitInfo(workDir); gitInfo != nil {
		metadata["git"] = gitInfo
	}

	return metadata
}

// collectGitInfo collects git repository information.
func collectGitInfo(workDir string) map[string]interface{} {
	// Check if it's a git repo
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		return nil
	}

	info := make(map[string]interface{})

	// Get commit SHA
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = workDir
	if output, err := cmd.Output(); err == nil {
		info["commit"] = strings.TrimSpace(string(output))
	}

	// Get branch name
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
	if output, err := cmd.Output(); err == nil {
		info["branch"] = strings.TrimSpace(string(output))
	}

	// Check for uncommitted changes
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = workDir
	if output, err := cmd.Output(); err == nil {
		info["dirty"] = len(output) > 0
	}

	return info
}

// getHostname returns the machine hostname.
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

// getUsername returns the current username.
func getUsername() string {
	return os.Getenv("USER")
}

// ResolveArtifactPath resolves a glob pattern to the most recent matching file.
// This is useful for build outputs that include timestamps (e.g., build-1770083113150.tar.gz).
//
// Parameters:
//   - baseDir: The base directory to search in
//   - pattern: The glob pattern to match (e.g., "build-*.tar.gz")
//
// Returns:
//   - string: The path to the most recently modified matching file
//   - error: Any error that occurred, or if no files match
func ResolveArtifactPath(baseDir, pattern string) (string, error) {
	fullPattern := filepath.Join(baseDir, pattern)

	// Check if it's a direct path (no glob characters)
	if !strings.ContainsAny(pattern, "*?[]") {
		if _, err := os.Stat(fullPattern); err == nil {
			return fullPattern, nil
		}
		return "", fmt.Errorf("file not found: %s", pattern)
	}

	// Use glob to find matching files
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return "", fmt.Errorf("invalid glob pattern: %w", err)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no files matching pattern: %s", pattern)
	}

	// If only one match, return it
	if len(matches) == 1 {
		return matches[0], nil
	}

	// Find the most recently modified file
	return findMostRecentFile(matches)
}

// findMostRecentFile returns the path to the most recently modified file from a list.
//
// Parameters:
//   - paths: List of file paths to compare
//
// Returns:
//   - string: The path to the most recently modified file
//   - error: Any error that occurred
func findMostRecentFile(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no files provided")
	}

	var mostRecent string
	var mostRecentTime time.Time

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		if mostRecent == "" || info.ModTime().After(mostRecentTime) {
			mostRecent = path
			mostRecentTime = info.ModTime()
		}
	}

	if mostRecent == "" {
		return "", fmt.Errorf("could not determine most recent file")
	}

	return mostRecent, nil
}
