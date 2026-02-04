// Package main provides the upgrade command for CLI self-update.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/ui"
)

const (
	// GitHubOwner is the GitHub organization/user that owns the CLI repo.
	GitHubOwner = "RevylAI"

	// GitHubRepo is the GitHub repository name.
	GitHubRepo = "revyl-cli"

	// GitHubAPIURL is the base URL for GitHub API.
	GitHubAPIURL = "https://api.github.com"

	// GitHubReleasesURL is the URL for downloading releases.
	GitHubReleasesURL = "https://github.com/RevylAI/revyl-cli/releases/download"
)

// GitHubRelease represents a GitHub release from the API.
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
}

// UpgradeResult contains the result of an upgrade check or operation.
type UpgradeResult struct {
	// CurrentVersion is the currently installed version.
	CurrentVersion string `json:"current_version"`

	// LatestVersion is the latest available version.
	LatestVersion string `json:"latest_version"`

	// UpdateAvailable is true if a newer version exists.
	UpdateAvailable bool `json:"update_available"`

	// InstallMethod describes how the CLI was installed.
	InstallMethod string `json:"install_method"`

	// UpgradeCommand is the command to run to upgrade (for package managers).
	UpgradeCommand string `json:"upgrade_command,omitempty"`

	// Message is a human-readable status message.
	Message string `json:"message"`
}

var (
	upgradeCheckOnly  bool
	upgradeForce      bool
	upgradeOutputJSON bool
	upgradePrerelease bool
)

// upgradeCmd checks for and installs CLI updates.
var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade Revyl CLI to the latest version",
	Long: `Check for and install updates to the Revyl CLI.

BEHAVIOR:
  - Detects how the CLI was installed (Homebrew, npm, pip, direct download)
  - For package managers: shows the upgrade command to run
  - For direct downloads: downloads and replaces the binary

FLAGS:
  --check       Only check for updates, don't install
  --force       Force upgrade even if already on latest version
  --prerelease  Include pre-release versions

EXAMPLES:
  revyl upgrade              # Check and upgrade
  revyl upgrade --check      # Only check for updates
  revyl upgrade --force      # Force reinstall
  revyl upgrade --prerelease # Include beta versions`,
	Aliases: []string{"update"},
	RunE:    runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheckOnly, "check", false, "Only check for updates, don't install")
	upgradeCmd.Flags().BoolVar(&upgradeForce, "force", false, "Force upgrade even if on latest version")
	upgradeCmd.Flags().BoolVar(&upgradeOutputJSON, "json", false, "Output results as JSON")
	upgradeCmd.Flags().BoolVar(&upgradePrerelease, "prerelease", false, "Include pre-release versions")
}

// runUpgrade checks for and installs updates.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runUpgrade(cmd *cobra.Command, args []string) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := upgradeOutputJSON
	if globalJSON, _ := cmd.Flags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	result := UpgradeResult{
		CurrentVersion: version,
	}

	// Detect installation method
	result.InstallMethod = detectInstallMethod()

	if !jsonOutput {
		ui.PrintBanner(version)
		ui.PrintInfo("Checking for updates...")
		ui.Println()
	}

	// Fetch latest release from GitHub
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	release, err := fetchLatestRelease(ctx, upgradePrerelease)
	if err != nil {
		if jsonOutput {
			result.Message = fmt.Sprintf("Failed to check for updates: %v", err)
			outputJSON(result)
		} else {
			ui.PrintError("Failed to check for updates: %v", err)
		}
		return err
	}

	result.LatestVersion = release.TagName

	// Compare versions
	currentClean := strings.TrimPrefix(version, "v")
	latestClean := strings.TrimPrefix(release.TagName, "v")

	if currentClean == latestClean && !upgradeForce {
		result.UpdateAvailable = false
		result.Message = "Already on the latest version"

		if jsonOutput {
			outputJSON(result)
		} else {
			ui.PrintSuccess("Already on the latest version (%s)", release.TagName)
		}
		return nil
	}

	result.UpdateAvailable = true

	// Handle based on installation method
	switch result.InstallMethod {
	case "homebrew":
		result.UpgradeCommand = "brew upgrade revyl"
		result.Message = "Update available via Homebrew"

		if jsonOutput {
			outputJSON(result)
		} else {
			ui.PrintInfo("Current version: %s", version)
			ui.PrintInfo("Latest version:  %s", release.TagName)
			ui.Println()
			ui.PrintWarning("Installed via Homebrew. Run:")
			ui.PrintDim("  brew upgrade revyl")
		}
		return nil

	case "npm":
		result.UpgradeCommand = "npm update -g @revyl/cli"
		result.Message = "Update available via npm"

		if jsonOutput {
			outputJSON(result)
		} else {
			ui.PrintInfo("Current version: %s", version)
			ui.PrintInfo("Latest version:  %s", release.TagName)
			ui.Println()
			ui.PrintWarning("Installed via npm. Run:")
			ui.PrintDim("  npm update -g @revyl/cli")
		}
		return nil

	case "pip":
		result.UpgradeCommand = "pip install --upgrade revyl"
		result.Message = "Update available via pip"

		if jsonOutput {
			outputJSON(result)
		} else {
			ui.PrintInfo("Current version: %s", version)
			ui.PrintInfo("Latest version:  %s", release.TagName)
			ui.Println()
			ui.PrintWarning("Installed via pip. Run:")
			ui.PrintDim("  pip install --upgrade revyl")
		}
		return nil

	default:
		// Direct download - can self-update
		if jsonOutput {
			result.Message = "Update available"
			outputJSON(result)
			return nil
		}

		ui.PrintInfo("Current version: %s", version)
		ui.PrintInfo("Latest version:  %s", release.TagName)
		ui.Println()

		if upgradeCheckOnly {
			ui.PrintSuccess("Update available: %s -> %s", version, release.TagName)
			ui.PrintInfo("Run 'revyl upgrade' to install")
			return nil
		}

		// Perform self-update
		return performSelfUpdate(ctx, release.TagName)
	}
}

// detectInstallMethod determines how the CLI was installed.
//
// Returns:
//   - string: The installation method (homebrew, npm, pip, direct)
func detectInstallMethod() string {
	execPath, err := os.Executable()
	if err != nil {
		return "direct"
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return "direct"
	}

	// Check for Homebrew
	if strings.Contains(execPath, "homebrew") || strings.Contains(execPath, "Cellar") {
		return "homebrew"
	}

	// Check for npm
	if strings.Contains(execPath, "node_modules") || strings.Contains(execPath, "npm") {
		return "npm"
	}

	// Check for pip (Python)
	if strings.Contains(execPath, ".revyl/bin") || strings.Contains(execPath, "site-packages") {
		return "pip"
	}

	return "direct"
}

// fetchLatestRelease fetches the latest release from GitHub.
//
// Parameters:
//   - ctx: Context for cancellation
//   - includePrerelease: Whether to include pre-release versions
//
// Returns:
//   - *GitHubRelease: The latest release
//   - error: Any error that occurred
func fetchLatestRelease(ctx context.Context, includePrerelease bool) (*GitHubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases", GitHubAPIURL, GitHubOwner, GitHubRepo)

	if !includePrerelease {
		url = fmt.Sprintf("%s/repos/%s/%s/releases/latest", GitHubAPIURL, GitHubOwner, GitHubRepo)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "revyl-cli/"+version)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	if includePrerelease {
		// Parse list of releases and find the latest (including prereleases)
		var releases []GitHubRelease
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return nil, fmt.Errorf("failed to parse releases: %w", err)
		}

		if len(releases) == 0 {
			return nil, fmt.Errorf("no releases found")
		}

		// Return the first non-draft release
		for _, r := range releases {
			if !r.Draft {
				return &r, nil
			}
		}
		return nil, fmt.Errorf("no releases found")
	}

	// Parse single release
	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}

	return &release, nil
}

// performSelfUpdate downloads and installs the new version.
//
// Parameters:
//   - ctx: Context for cancellation
//   - tagName: The release tag to download
//
// Returns:
//   - error: Any error that occurred
func performSelfUpdate(ctx context.Context, tagName string) error {
	// Determine binary name for this platform
	binaryName := fmt.Sprintf("revyl-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	// Download URLs
	binaryURL := fmt.Sprintf("%s/%s/%s", GitHubReleasesURL, tagName, binaryName)
	checksumURL := fmt.Sprintf("%s/%s/checksums.txt", GitHubReleasesURL, tagName)

	ui.PrintInfo("Downloading %s...", binaryName)

	// Download checksum file first
	checksums, err := downloadChecksums(ctx, checksumURL)
	if err != nil {
		ui.PrintWarning("Could not download checksums: %v", err)
		ui.PrintWarning("Proceeding without checksum verification")
	}

	// Get expected checksum for our binary
	expectedChecksum := ""
	if checksums != nil {
		expectedChecksum = checksums[binaryName]
	}

	// Download the binary
	tempFile, err := downloadBinary(ctx, binaryURL)
	if err != nil {
		ui.PrintError("Failed to download binary: %v", err)
		return err
	}
	defer os.Remove(tempFile)

	// Verify checksum if available
	if expectedChecksum != "" {
		ui.PrintInfo("Verifying checksum...")
		actualChecksum, err := calculateChecksum(tempFile)
		if err != nil {
			ui.PrintError("Failed to calculate checksum: %v", err)
			return err
		}

		if actualChecksum != expectedChecksum {
			ui.PrintError("Checksum mismatch!")
			ui.PrintError("Expected: %s", expectedChecksum)
			ui.PrintError("Got:      %s", actualChecksum)
			return fmt.Errorf("checksum verification failed")
		}
		ui.PrintSuccess("Checksum verified")
	}

	// Get current executable path
	execPath, err := os.Executable()
	if err != nil {
		ui.PrintError("Failed to get executable path: %v", err)
		return err
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		ui.PrintError("Failed to resolve executable path: %v", err)
		return err
	}

	// Create backup
	backupPath := execPath + ".old"
	ui.PrintInfo("Creating backup at %s", backupPath)

	if err := os.Rename(execPath, backupPath); err != nil {
		ui.PrintError("Failed to create backup: %v", err)
		ui.PrintError("You may need to run with elevated permissions (sudo)")
		return err
	}

	// Copy new binary
	ui.PrintInfo("Installing new version...")

	if err := copyFile(tempFile, execPath); err != nil {
		// Restore backup on failure
		os.Rename(backupPath, execPath)
		ui.PrintError("Failed to install new version: %v", err)
		return err
	}

	// Make executable
	if err := os.Chmod(execPath, 0755); err != nil {
		// Restore backup on failure
		os.Remove(execPath)
		os.Rename(backupPath, execPath)
		ui.PrintError("Failed to set permissions: %v", err)
		return err
	}

	// Remove backup
	os.Remove(backupPath)

	ui.Println()
	ui.PrintSuccess("Successfully upgraded to %s", tagName)
	ui.PrintInfo("Run 'revyl version' to verify")

	return nil
}

// downloadChecksums downloads and parses the checksums file.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: URL to the checksums file
//
// Returns:
//   - map[string]string: Map of filename to checksum
//   - error: Any error that occurred
func downloadChecksums(ctx context.Context, url string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	checksums := make(map[string]string)
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			checksums[parts[1]] = parts[0]
		}
	}

	return checksums, nil
}

// downloadBinary downloads the binary to a temporary file.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: URL to download
//
// Returns:
//   - string: Path to the temporary file
//   - error: Any error that occurred
func downloadBinary(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Create temp file
	tempFile, err := os.CreateTemp("", "revyl-upgrade-*")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	// Copy with progress (simplified - no progress bar for now)
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}

// calculateChecksum calculates the SHA256 checksum of a file.
//
// Parameters:
//   - path: Path to the file
//
// Returns:
//   - string: Hex-encoded SHA256 checksum
//   - error: Any error that occurred
func calculateChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// copyFile copies a file from src to dst.
//
// Parameters:
//   - src: Source file path
//   - dst: Destination file path
//
// Returns:
//   - error: Any error that occurred
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// outputJSON outputs the result as JSON.
//
// Parameters:
//   - result: The result to output
func outputJSON(result UpgradeResult) {
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}
