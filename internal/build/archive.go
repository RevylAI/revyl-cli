// Package build provides build-related utilities for the Revyl CLI.
package build

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IsAppBundle checks if a path is a .app directory (iOS app bundle).
//
// Parameters:
//   - path: The file path to check
//
// Returns:
//   - bool: True if the path is a .app directory
func IsAppBundle(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".app")
}

// ZipAppBundle creates a .zip file from a .app directory.
//
// This is needed because the backend expects iOS simulator builds as .zip files
// containing the .app bundle.
//
// Parameters:
//   - appPath: Path to the .app directory
//
// Returns:
//   - string: Path to the created .zip file (caller should clean up with os.Remove)
//   - error: Any error that occurred
func ZipAppBundle(appPath string) (string, error) {
	// Validate input
	if !IsAppBundle(appPath) {
		return "", fmt.Errorf("path is not a .app directory: %s", appPath)
	}

	// Create the zip file
	// Use case-insensitive suffix removal to handle .app, .APP, .App, etc.
	appName := filepath.Base(appPath)
	lowerName := strings.ToLower(appName)
	if strings.HasSuffix(lowerName, ".app") {
		appName = appName[:len(appName)-4] // Remove last 4 chars (.app)
	}
	zipName := appName + ".zip"
	zipPath := filepath.Join(os.TempDir(), "revyl-upload-"+zipName)

	zipFile, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to create zip file: %w", err)
	}

	zipWriter := zip.NewWriter(zipFile)

	// Add all files from .app directory to zip
	appParentDir := filepath.Dir(appPath)
	err = filepath.Walk(appPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from parent of .app (so .app is at root of zip)
		relPath, err := filepath.Rel(appParentDir, path)
		if err != nil {
			return err
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			// Create symlink entry in zip
			header := &zip.FileHeader{
				Name:   relPath,
				Method: zip.Store,
			}
			header.SetMode(info.Mode())
			writer, err := zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}
			_, err = writer.Write([]byte(linkTarget))
			return err
		}

		if info.IsDir() {
			// Add directory entry
			_, err := zipWriter.Create(relPath + "/")
			return err
		}

		// Add file
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})

	// Close writers and check for errors
	// zipWriter.Close() is critical - it writes the central directory
	if closeErr := zipWriter.Close(); closeErr != nil {
		zipFile.Close()
		os.Remove(zipPath)
		if err != nil {
			return "", fmt.Errorf("failed to create zip: %w (also failed to close: %v)", err, closeErr)
		}
		return "", fmt.Errorf("failed to finalize zip archive: %w", closeErr)
	}

	if closeErr := zipFile.Close(); closeErr != nil {
		os.Remove(zipPath)
		if err != nil {
			return "", fmt.Errorf("failed to create zip: %w (also failed to close file: %v)", err, closeErr)
		}
		return "", fmt.Errorf("failed to close zip file: %w", closeErr)
	}

	if err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("failed to create zip: %w", err)
	}

	return zipPath, nil
}

// ExtractAppFromTarGz extracts a .app directory from a .tar.gz file and creates a .zip.
//
// This is needed for iOS builds from EAS Build, which output .tar.gz files containing
// .app bundles. The backend expects .zip files for iOS simulator builds.
//
// Parameters:
//   - tarGzPath: Path to the .tar.gz file
//
// Returns:
//   - string: Path to the created .zip file (caller should clean up with os.Remove)
//   - error: Any error that occurred
func ExtractAppFromTarGz(tarGzPath string) (string, error) {
	// Create temp directory for extraction
	tempDir, err := os.MkdirTemp("", "revyl-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir) // Clean up extraction dir

	// Open the tar.gz file
	file, err := os.Open(tarGzPath)
	if err != nil {
		return "", fmt.Errorf("failed to open tar.gz: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Extract all files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Construct the full path
		targetPath := filepath.Join(tempDir, header.Name)

		// Ensure the path is within tempDir (security check)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(tempDir)) {
			return "", fmt.Errorf("invalid tar entry path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return "", fmt.Errorf("failed to create parent directory: %w", err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return "", fmt.Errorf("failed to create file: %w", err)
			}

			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return "", fmt.Errorf("failed to write file: %w", err)
			}
			outFile.Close()
		case tar.TypeSymlink:
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				// Ignore symlink errors on Windows
				if !strings.Contains(err.Error(), "not permitted") {
					return "", fmt.Errorf("failed to create symlink: %w", err)
				}
			}
		}
	}

	// Find the .app directory
	var appDir string
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && strings.HasSuffix(info.Name(), ".app") {
			appDir = path
			return filepath.SkipAll // Stop walking
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return "", fmt.Errorf("failed to find .app directory: %w", err)
	}

	if appDir == "" {
		return "", fmt.Errorf("no .app directory found in tar.gz")
	}

	// Use ZipAppBundle to create the zip file
	return ZipAppBundle(appDir)
}

// IsTarGz checks if a file path is a .tar.gz or .tgz file.
//
// Parameters:
//   - path: The file path to check
//
// Returns:
//   - bool: True if the file is a tar.gz archive
func IsTarGz(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}
