// Package build provides build execution and artifact management utilities.
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

// ResolveArtifactPath resolves the artifact path, supporting glob patterns.
//
// Parameters:
//   - workDir: The working directory to resolve relative paths from
//   - output: The output path pattern (may contain globs like *.apk)
//
// Returns:
//   - string: The resolved absolute path to the artifact
//   - error: Any error that occurred during resolution
//
// If the output contains a glob pattern, the most recently modified matching file is returned.
func ResolveArtifactPath(workDir, output string) (string, error) {
	// Handle absolute paths
	if filepath.IsAbs(output) {
		if _, err := os.Stat(output); err != nil {
			// Try glob matching
			matches, err := filepath.Glob(output)
			if err != nil || len(matches) == 0 {
				return "", fmt.Errorf("artifact not found: %s", output)
			}
			return getMostRecentFile(matches)
		}
		return output, nil
	}

	// Handle relative paths
	fullPath := filepath.Join(workDir, output)

	// Check if it's a direct path
	if _, err := os.Stat(fullPath); err == nil {
		return fullPath, nil
	}

	// Try glob matching
	matches, err := filepath.Glob(fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid glob pattern: %w", err)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("artifact not found: %s", output)
	}

	return getMostRecentFile(matches)
}

// getMostRecentFile returns the most recently modified file from a list of paths.
//
// Parameters:
//   - paths: List of file paths to check
//
// Returns:
//   - string: Path to the most recently modified file
//   - error: Any error that occurred
func getMostRecentFile(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("no files provided")
	}

	var mostRecent string
	var mostRecentTime int64

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.ModTime().Unix() > mostRecentTime {
			mostRecentTime = info.ModTime().Unix()
			mostRecent = path
		}
	}

	if mostRecent == "" {
		return paths[0], nil
	}

	return mostRecent, nil
}

// IsTarGz checks if the file is a tar.gz archive.
//
// Parameters:
//   - path: The file path to check
//
// Returns:
//   - bool: True if the file is a tar.gz archive
func IsTarGz(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".tar.gz") ||
		strings.HasSuffix(strings.ToLower(path), ".tgz")
}

// IsAppBundle checks if the path is a .app bundle directory.
//
// Parameters:
//   - path: The path to check
//
// Returns:
//   - bool: True if the path is a .app bundle directory
func IsAppBundle(path string) bool {
	if !strings.HasSuffix(path, ".app") {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// ExtractAppFromTarGz extracts a .app bundle from a tar.gz archive and zips it.
//
// Parameters:
//   - tarGzPath: Path to the tar.gz archive
//
// Returns:
//   - string: Path to the created zip file
//   - error: Any error that occurred during extraction
//
// The function searches for a .app directory within the archive and creates
// a zip file containing it. The caller is responsible for cleaning up the
// returned zip file.
func ExtractAppFromTarGz(tarGzPath string) (string, error) {
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

	// Create temp directory for extraction
	tempDir, err := os.MkdirTemp("", "revyl-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Extract all files
	var appPath string
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Determine the target path
		targetPath := filepath.Join(tempDir, header.Name)

		// Check if this is part of a .app bundle
		if strings.Contains(header.Name, ".app") {
			parts := strings.Split(header.Name, ".app")
			if len(parts) > 0 {
				potentialAppPath := filepath.Join(tempDir, parts[0]+".app")
				if appPath == "" || len(potentialAppPath) < len(appPath) {
					appPath = potentialAppPath
				}
			}
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
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return "", fmt.Errorf("failed to create parent directory: %w", err)
			}
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				// Ignore symlink errors on some systems
				continue
			}
		}
	}

	if appPath == "" {
		return "", fmt.Errorf("no .app bundle found in archive")
	}

	// Verify the .app exists
	if _, err := os.Stat(appPath); err != nil {
		return "", fmt.Errorf(".app bundle not found after extraction: %w", err)
	}

	// Zip the .app bundle
	return ZipAppBundle(appPath)
}

// ZipAppBundle creates a zip archive from a .app bundle directory.
//
// Parameters:
//   - appPath: Path to the .app bundle directory
//
// Returns:
//   - string: Path to the created zip file
//   - error: Any error that occurred during zipping
//
// The caller is responsible for cleaning up the returned zip file.
func ZipAppBundle(appPath string) (string, error) {
	// Create temp zip file
	zipFile, err := os.CreateTemp("", "revyl-*.zip")
	if err != nil {
		return "", fmt.Errorf("failed to create temp zip file: %w", err)
	}
	zipPath := zipFile.Name()

	// Create zip writer
	zipWriter := zip.NewWriter(zipFile)

	// Walk the .app directory and add files
	appName := filepath.Base(appPath)
	err = filepath.Walk(appPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path within the .app
		relPath, err := filepath.Rel(filepath.Dir(appPath), path)
		if err != nil {
			return err
		}

		// Handle symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return nil // Skip symlinks we can't read
			}

			header := &zip.FileHeader{
				Name:   relPath,
				Method: zip.Deflate,
			}
			header.SetMode(info.Mode())

			writer, err := zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}

			_, err = writer.Write([]byte(linkTarget))
			return err
		}

		// Handle directories
		if info.IsDir() {
			if relPath != appName {
				_, err := zipWriter.Create(relPath + "/")
				return err
			}
			return nil
		}

		// Handle regular files
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

	if err != nil {
		zipWriter.Close()
		zipFile.Close()
		os.Remove(zipPath)
		return "", fmt.Errorf("failed to create zip: %w", err)
	}

	if err := zipWriter.Close(); err != nil {
		zipFile.Close()
		os.Remove(zipPath)
		return "", fmt.Errorf("failed to close zip writer: %w", err)
	}

	if err := zipFile.Close(); err != nil {
		os.Remove(zipPath)
		return "", fmt.Errorf("failed to close zip file: %w", err)
	}

	return zipPath, nil
}
