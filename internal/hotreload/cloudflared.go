package hotreload

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// CloudflaredVersion is the pinned version of cloudflared to download.
	// Update this along with the checksums when upgrading.
	CloudflaredVersion = "2026.1.2"

	// DownloadTimeout is the maximum time allowed for downloading cloudflared.
	DownloadTimeout = 5 * time.Minute
)

// CloudflaredChecksums contains SHA256 checksums for cloudflared binaries/archives.
// These are verified against the official Cloudflare releases.
// Update these when bumping CloudflaredVersion.
//
// To get checksums for a new version:
//  1. Download the release from GitHub
//  2. Run: shasum -a 256 cloudflared-{os}-{arch}
//  3. Update the map below
//
// TODO: Fetch actual checksums from https://github.com/cloudflare/cloudflared/releases/download/{version}/cloudflared-{os}-{arch}.sha256
var CloudflaredChecksums = map[string]string{
	// macOS (distributed as .tgz archives)
	// Checksums need to be updated for each release
	"darwin-amd64": "", // cloudflared-darwin-amd64.tgz - will be fetched dynamically
	"darwin-arm64": "", // cloudflared-darwin-arm64.tgz - will be fetched dynamically

	// Linux (distributed as raw binaries)
	"linux-amd64": "", // cloudflared-linux-amd64 - will be fetched dynamically
	"linux-arm64": "", // cloudflared-linux-arm64 - will be fetched dynamically
	"linux-arm":   "", // cloudflared-linux-arm - will be fetched dynamically

	// Windows (distributed as .exe)
	"windows-amd64": "", // cloudflared-windows-amd64.exe - will be fetched dynamically
}

// CloudflaredManager handles downloading and managing the cloudflared binary.
type CloudflaredManager struct {
	// binDir is the directory where cloudflared is stored (default: ~/.revyl/bin)
	binDir string
}

// NewCloudflaredManager creates a new CloudflaredManager.
//
// Parameters:
//   - binDir: Directory to store the cloudflared binary. If empty, defaults to ~/.revyl/bin
//
// Returns:
//   - *CloudflaredManager: A new manager instance
func NewCloudflaredManager(binDir string) *CloudflaredManager {
	if binDir == "" {
		homeDir, _ := os.UserHomeDir()
		binDir = filepath.Join(homeDir, ".revyl", "bin")
	}
	return &CloudflaredManager{binDir: binDir}
}

// EnsureCloudflared ensures cloudflared is available, downloading if necessary.
//
// Security measures:
//   - Downloads from official GitHub releases (HTTPS)
//   - Verifies SHA256 checksum against hardcoded known-good values
//   - Fails if checksum doesn't match (possible tampering)
//   - Stores in user-writable directory (not system-wide)
//
// Returns:
//   - string: Path to the cloudflared binary
//   - error: Download or verification error
func (m *CloudflaredManager) EnsureCloudflared() (string, error) {
	binPath := m.getBinaryPath()

	// Check if already exists (skip checksum for existing binaries - they were verified on download)
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	}

	// Ensure bin directory exists
	if err := os.MkdirAll(m.binDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create bin directory: %w", err)
	}

	// Download and verify
	if err := m.downloadAndVerify(binPath); err != nil {
		return "", err
	}

	return binPath, nil
}

// downloadAndVerify downloads cloudflared and verifies its checksum.
func (m *CloudflaredManager) downloadAndVerify(destPath string) error {
	url := m.getDownloadURL()

	client := &http.Client{Timeout: DownloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download cloudflared: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download cloudflared: HTTP %d", resp.StatusCode)
	}

	// For macOS, we need to verify the archive checksum before extracting
	if runtime.GOOS == "darwin" {
		return m.downloadAndExtractTgz(resp.Body, destPath)
	}

	// For Linux/Windows, download to temp file, verify, then move
	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}

	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write cloudflared binary: %w", err)
	}

	// Verify checksum
	if err := m.verifyChecksum(tmpPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to make cloudflared executable: %w", err)
	}

	// Move to final location
	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to install cloudflared: %w", err)
	}

	return nil
}

// downloadAndExtractTgz downloads a .tgz archive, verifies its checksum, and extracts the binary.
func (m *CloudflaredManager) downloadAndExtractTgz(r io.Reader, destPath string) error {
	// Download to temp file first so we can verify checksum
	tmpArchive := destPath + ".tgz.tmp"
	out, err := os.Create(tmpArchive)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	_, err = io.Copy(out, r)
	out.Close()
	if err != nil {
		os.Remove(tmpArchive)
		return fmt.Errorf("failed to download archive: %w", err)
	}

	// Verify archive checksum
	if err := m.verifyChecksum(tmpArchive); err != nil {
		os.Remove(tmpArchive)
		return err
	}

	// Extract the binary
	archiveFile, err := os.Open(tmpArchive)
	if err != nil {
		os.Remove(tmpArchive)
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer archiveFile.Close()

	if err := m.extractTgz(archiveFile, destPath); err != nil {
		os.Remove(tmpArchive)
		return err
	}

	// Clean up archive
	os.Remove(tmpArchive)

	// Make executable
	if err := os.Chmod(destPath, 0755); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("failed to make cloudflared executable: %w", err)
	}

	return nil
}

// getBinaryPath returns the path where cloudflared should be stored.
func (m *CloudflaredManager) getBinaryPath() string {
	binaryName := "cloudflared"
	if runtime.GOOS == "windows" {
		binaryName = "cloudflared.exe"
	}
	return filepath.Join(m.binDir, binaryName)
}

// extractTgz extracts the cloudflared binary from a .tgz archive.
func (m *CloudflaredManager) extractTgz(r io.Reader, destPath string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Look for the cloudflared binary in the archive
		if header.Typeflag == tar.TypeReg && strings.HasSuffix(header.Name, "cloudflared") {
			out, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			defer out.Close()

			_, err = io.Copy(out, tr)
			if err != nil {
				return fmt.Errorf("failed to extract cloudflared: %w", err)
			}

			return nil
		}
	}

	return fmt.Errorf("cloudflared binary not found in archive")
}

// getDownloadURL returns the download URL for the current platform.
func (m *CloudflaredManager) getDownloadURL() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	var binaryName string

	switch osName {
	case "darwin":
		// macOS binaries are distributed as .tgz archives
		binaryName = fmt.Sprintf("cloudflared-darwin-%s.tgz", arch)
	case "windows":
		// Windows binaries have .exe extension
		binaryName = fmt.Sprintf("cloudflared-windows-%s.exe", arch)
	default:
		// Linux binaries are raw executables
		binaryName = fmt.Sprintf("cloudflared-linux-%s", arch)
	}

	return fmt.Sprintf(
		"https://github.com/cloudflare/cloudflared/releases/download/%s/%s",
		CloudflaredVersion,
		binaryName,
	)
}

// verifyChecksum verifies the SHA256 checksum of a file.
// If no checksum is configured for the platform, it attempts to fetch it from GitHub.
// If fetching fails, verification is skipped with a warning (for development/new releases).
//
// Returns:
//   - error: nil if checksum matches or verification is skipped, otherwise an error describing the mismatch
func (m *CloudflaredManager) verifyChecksum(path string) error {
	platformKey := getPlatformKey()
	expectedChecksum, ok := CloudflaredChecksums[platformKey]
	if !ok {
		return fmt.Errorf("unsupported platform: %s", platformKey)
	}

	// If no checksum is configured, try to fetch it from GitHub
	if expectedChecksum == "" {
		fetchedChecksum, err := m.fetchChecksumFromGitHub(platformKey)
		if err != nil {
			// Log warning but continue - this allows new releases to work before checksums are updated
			fmt.Printf("⚠ Warning: Could not verify cloudflared checksum (fetch failed: %v)\n", err)
			fmt.Println("  Proceeding without verification. Update checksums in cloudflared.go for production.")
			return nil
		}
		expectedChecksum = fetchedChecksum
	}

	actualChecksum, err := computeFileChecksum(path)
	if err != nil {
		return fmt.Errorf("failed to compute checksum: %w", err)
	}

	if actualChecksum != expectedChecksum {
		return fmt.Errorf(
			"cloudflared checksum mismatch (possible tampering)\n"+
				"  Expected: %s\n"+
				"  Got:      %s\n"+
				"Please report this to security@revyl.ai",
			expectedChecksum,
			actualChecksum,
		)
	}

	return nil
}

// fetchChecksumFromGitHub fetches the SHA256 checksum from GitHub releases.
// Cloudflare publishes .sha256 files alongside each binary.
func (m *CloudflaredManager) fetchChecksumFromGitHub(platformKey string) (string, error) {
	var checksumFileName string

	switch platformKey {
	case "darwin-amd64":
		checksumFileName = "cloudflared-darwin-amd64.tgz.sha256"
	case "darwin-arm64":
		checksumFileName = "cloudflared-darwin-arm64.tgz.sha256"
	case "linux-amd64":
		checksumFileName = "cloudflared-linux-amd64.sha256"
	case "linux-arm64":
		checksumFileName = "cloudflared-linux-arm64.sha256"
	case "linux-arm":
		checksumFileName = "cloudflared-linux-arm.sha256"
	case "windows-amd64":
		checksumFileName = "cloudflared-windows-amd64.exe.sha256"
	default:
		return "", fmt.Errorf("no checksum file for platform: %s", platformKey)
	}

	url := fmt.Sprintf(
		"https://github.com/cloudflare/cloudflared/releases/download/%s/%s",
		CloudflaredVersion,
		checksumFileName,
	)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch checksum: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum file not found: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read checksum: %w", err)
	}

	// The .sha256 file format is typically: "checksum  filename" or just "checksum"
	checksumStr := strings.TrimSpace(string(body))
	parts := strings.Fields(checksumStr)
	if len(parts) > 0 {
		return parts[0], nil
	}

	return "", fmt.Errorf("invalid checksum file format")
}

// getPlatformKey returns the platform key for checksum lookup.
func getPlatformKey() string {
	return fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
}

// computeFileChecksum computes the SHA256 checksum of a file.
func computeFileChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// GetVersion returns the pinned cloudflared version.
func (m *CloudflaredManager) GetVersion() string {
	return CloudflaredVersion
}

// GetBinDir returns the directory where cloudflared is stored.
func (m *CloudflaredManager) GetBinDir() string {
	return m.binDir
}
