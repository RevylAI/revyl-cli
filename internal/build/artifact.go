// Package build provides build execution and artifact management utilities.
package build

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"howett.net/plist"
)

// ResolveArtifactPath resolves a path or the most recently modified glob match.
func ResolveArtifactPath(workDir, output string) (string, error) {
	if filepath.IsAbs(output) {
		if _, err := os.Stat(output); err != nil {
			matches, err := filepath.Glob(output)
			if err != nil || len(matches) == 0 {
				return "", fmt.Errorf("artifact not found: %s", output)
			}
			return getMostRecentFile(matches)
		}
		return output, nil
	}

	fullPath := filepath.Join(workDir, output)

	if _, err := os.Stat(fullPath); err == nil {
		return fullPath, nil
	}

	matches, err := filepath.Glob(fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid glob pattern: %w", err)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("artifact not found: %s", output)
	}

	return getMostRecentFile(matches)
}

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

// IsTarGz recognizes gzip archive filenames, including EAS artifacts named .gz.
func IsTarGz(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".gz") ||
		strings.HasSuffix(strings.ToLower(path), ".tgz")
}

// IsAppBundle reports whether the path is an .app directory.
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

// ExtractAppFromArchive selects an app from ZIP or gzip-tar contents and returns a ZIP.
// The caller must remove the returned file.
func ExtractAppFromArchive(archivePath string) (string, error) {
	return extractAppFromArchive(archivePath, defaultArchiveLimits())
}

func extractAppFromArchive(archivePath string, limits archiveLimits) (string, error) {
	isZip, err := isZipFile(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to detect archive format: %w", err)
	}

	if isZip {
		return extractAppFromZip(archivePath, limits)
	}

	return extractAppFromTarGz(archivePath, limits)
}

// ExtractAppFromTarGz is kept for backward compatibility; it delegates to ExtractAppFromArchive.
func ExtractAppFromTarGz(tarGzPath string) (string, error) {
	return ExtractAppFromArchive(tarGzPath)
}

func isZipFile(path string) (bool, error) {
	f, err := os.Open(path) // #nosec G304 -- The caller selects this local input archive.
	if err != nil {
		return false, err
	}
	defer f.Close()

	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return false, nil // Can't read magic bytes, assume not zip
	}

	return magic[0] == 'P' && magic[1] == 'K' && magic[2] == 0x03 && magic[3] == 0x04, nil
}

func extractAppFromZip(zipPath string, limits archiveLimits) (resultPath string, resultErr error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("failed to open zip: %w", err)
	}
	defer func() {
		if r != nil {
			resultErr = errors.Join(resultErr, r.Close())
		}
	}()
	layout, err := validateZipArchive(&r.Reader)
	if err != nil {
		return "", err
	}
	tempDir, err := os.MkdirTemp("", "revyl-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer cleanupArchiveExtraction(tempDir, &resultPath, &resultErr, os.RemoveAll)
	budget := &archiveWorkspaceBudget{limitBytes: limits.workspaceBytes}
	extractor, err := newArchiveExtractor(tempDir, layout, budget)
	if err != nil {
		return "", err
	}
	for index, entry := range r.File {
		member := layout.members[index]
		if member.isLink || member.isDir {
			if err := extractor.extractMember(member, nil); err != nil {
				return "", err
			}
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open zip entry: %w", err)
		}
		if err := errors.Join(extractor.extractMember(member, reader), reader.Close()); err != nil {
			return "", err
		}
	}
	closeErr := r.Close()
	r = nil
	if closeErr != nil {
		return "", fmt.Errorf("failed to close source zip: %w", closeErr)
	}
	if err := extractor.stageLinks(); err != nil {
		return "", err
	}
	appPath, err := preferredIOSAppBundle(tempDir, layout)
	if err != nil {
		return "", err
	}
	return zipAppBundle(appPath, extractor.metadata, budget, limits.artifactBytes)
}

func extractAppFromTarGz(tarGzPath string, limits archiveLimits) (resultPath string, resultErr error) {
	tempDir, err := os.MkdirTemp("", "revyl-extract-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer cleanupArchiveExtraction(tempDir, &resultPath, &resultErr, os.RemoveAll)
	budget := &archiveWorkspaceBudget{limitBytes: limits.workspaceBytes}
	if err := budget.reserve(archiveFilesystemEntryBytes); err != nil {
		return "", err
	}
	// The caller explicitly selects the local source archive to upload.
	file, err := os.Open(tarGzPath) // #nosec G304
	if err != nil {
		return "", fmt.Errorf("failed to open tar.gz: %w", err)
	}
	stagedTar, layout, stageErr := stageValidatedTar(file, tempDir, budget)
	closeErr := file.Close()
	if stagedTar != nil {
		defer func() {
			if stagedTar != nil {
				resultErr = errors.Join(resultErr, stagedTar.Close())
			}
		}()
	}
	if err := errors.Join(stageErr, closeErr); err != nil {
		return "", err
	}
	stagedInfo, err := stagedTar.Stat()
	if err != nil {
		return "", fmt.Errorf("failed to inspect temporary tar: %w", err)
	}
	extractionDir := filepath.Join(tempDir, "app")
	if err := os.Mkdir(extractionDir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create extraction directory: %w", err)
	}
	metadata, extractErr := extractValidatedTar(tar.NewReader(stagedTar), extractionDir, layout, budget)
	closeErr = stagedTar.Close()
	removeErr := os.Remove(stagedTar.Name())
	stagedTar = nil
	if err := errors.Join(extractErr, closeErr, removeErr); err != nil {
		return "", fmt.Errorf("failed to extract temporary tar: %w", err)
	}
	budget.usedBytes -= stagedInfo.Size() + archiveFilesystemEntryBytes
	appPath, err := preferredIOSAppBundle(extractionDir, layout)
	if err != nil {
		return "", err
	}
	return zipAppBundle(appPath, metadata, budget, limits.artifactBytes)
}

func extractValidatedTar(reader *tar.Reader, tempDir string, layout *archiveLayout, budget *archiveWorkspaceBudget) (extractedArchiveMetadata, error) {
	extractor, err := newArchiveExtractor(tempDir, layout, budget)
	if err != nil {
		return nil, err
	}
	for _, member := range layout.members {
		if _, err := reader.Next(); err != nil {
			return nil, fmt.Errorf("failed to read validated tar entry: %w", err)
		}
		if err := extractor.extractMember(member, reader); err != nil {
			return nil, err
		}
	}
	if err := extractor.stageLinks(); err != nil {
		return nil, err
	}
	return extractor.metadata, nil
}

type iosAppBundleCandidate struct {
	path              string
	normalizedPath    string
	isAppClip         bool
	appBundleCount    int
	pathDepth         int
	malformedMetadata bool
}

func preferredIOSAppBundle(rootDir string, layout *archiveLayout) (string, error) {
	var candidates []iosAppBundleCandidate
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
			return nil
		}
		relativePath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		plistPath, err := resolveArchiveMember(filepath.ToSlash(filepath.Join(relativePath, "Info.plist")), layout.links)
		if err != nil {
			return err
		}
		// Bundle paths and links were validated inside the private extraction root.
		data, err := os.ReadFile(filepath.Join(rootDir, filepath.FromSlash(plistPath))) // #nosec G304
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// Plists are open dictionaries; only app-role keys affect local selection.
		var metadata map[string]any
		_, parseErr := plist.Unmarshal(data, &metadata)
		if parseErr != nil {
			metadata = nil
		}
		platform, _ := metadata["DTPlatformName"].(string)
		companionID, _ := metadata["WKCompanionAppBundleIdentifier"].(string)
		if metadata["WKWatchKitApp"] == true || metadata["WKApplication"] == true || companionID != "" || strings.HasPrefix(strings.ToLower(platform), "watch") {
			return nil
		}
		_, hasAppClipMetadata := metadata["NSAppClip"]
		candidate := iosAppBundleCandidate{
			path: path, normalizedPath: strings.ToLower(filepath.ToSlash(relativePath)),
			isAppClip: hasAppClipMetadata, malformedMetadata: parseErr != nil,
		}
		parts := strings.Split(candidate.normalizedPath, "/")
		candidate.pathDepth = len(parts)
		for _, part := range parts {
			if strings.HasSuffix(part, ".app") {
				candidate.appBundleCount++
			}
			if part == "appclips" {
				candidate.isAppClip = true
			}
		}
		if candidate.isAppClip && (!hasAppClipMetadata || candidate.appBundleCount != 1) {
			return nil
		}
		// Preserve malformed hosts for API validation instead of substituting a Clip.
		candidates = append(candidates, candidate)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to inspect app bundles: %w", err)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no supported iOS .app bundle found in archive; include an iPhone app or a standalone App Clip with NSAppClip metadata")
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.isAppClip != b.isAppClip {
			return !a.isAppClip
		}
		if a.appBundleCount != b.appBundleCount {
			return a.appBundleCount < b.appBundleCount
		}
		if a.pathDepth != b.pathDepth {
			return a.pathDepth < b.pathDepth
		}
		if a.malformedMetadata != b.malformedMetadata {
			return !a.malformedMetadata
		}
		return a.normalizedPath < b.normalizedPath
	})
	return candidates[0].path, nil
}

// PlatformFromFilePath infers the device platform ("ios" or "android") from an
// artifact file extension. Returns an empty string when the extension is
// ambiguous or unrecognized.
func PlatformFromFilePath(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".apk"), strings.HasSuffix(lower, ".aab"):
		return "android"
	case strings.HasSuffix(lower, ".ipa"),
		strings.HasSuffix(lower, ".app"),
		strings.HasSuffix(lower, ".app.zip"),
		strings.HasSuffix(lower, ".gz"),
		strings.HasSuffix(lower, ".tgz"):
		return "ios"
	default:
		return ""
	}
}

// ZipAppBundle packages an .app directory. The caller must remove the returned ZIP.
func ZipAppBundle(appPath string) (string, error) {
	return zipAppBundle(appPath, nil, &archiveWorkspaceBudget{limitBytes: archiveWorkspaceLimitBytes}, archiveUploadLimitBytes)
}

func zipAppBundle(appPath string, metadata extractedArchiveMetadata, budget *archiveWorkspaceBudget, artifactLimitBytes int64) (string, error) {
	if err := budget.reserve(archiveFilesystemEntryBytes); err != nil {
		return "", err
	}
	zipFile, err := os.CreateTemp("", "revyl-*.zip")
	if err != nil {
		return "", fmt.Errorf("failed to create temp zip file: %w", err)
	}
	zipPath := zipFile.Name()

	output := &archiveBudgetWriter{target: zipFile, workspace: budget, limitBytes: artifactLimitBytes}
	zipWriter := zip.NewWriter(output)
	var repackedBytes int64

	appName := filepath.Base(appPath)
	err = filepath.Walk(appPath, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(filepath.Dir(appPath), filePath)
		if err != nil {
			return err
		}

		relPath = filepath.ToSlash(relPath)
		if info.IsDir() && relPath == appName {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate
		var original archiveFileMetadata
		if metadata != nil {
			var found bool
			original, found, err = metadata.lookup(filePath)
			if err != nil {
				return err
			}
			if found {
				header.SetMode(original.mode)
			} else if info.IsDir() {
				header.SetMode(os.ModeDir | 0o755)
			} else {
				return fmt.Errorf("missing archive metadata for %q", relPath)
			}
		}
		if info.IsDir() {
			header.Name += "/"
			header.Method = zip.Store
		}

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget := original.linkTarget
			if metadata == nil {
				linkTarget, err = os.Readlink(filePath)
				if err != nil {
					return fmt.Errorf("failed to read app bundle symlink: %w", err)
				}
			}
			written, err := copyArchiveContent(writer, strings.NewReader(filepath.ToSlash(linkTarget)), budget.limitBytes-repackedBytes)
			repackedBytes += written
			return err
		}

		// Walk does not follow symlinks; link entries are handled above, and the app root is caller-selected or validated.
		file, err := os.Open(filePath) // #nosec G304
		if err != nil {
			return err
		}
		written, copyErr := copyArchiveContent(writer, file, budget.limitBytes-repackedBytes)
		repackedBytes += written
		return errors.Join(copyErr, file.Close())
	})

	if err == nil && metadata != nil {
		err = writeArchiveSymlinks(zipWriter, appPath, metadata, budget.limitBytes-repackedBytes)
	}
	if err != nil {
		return "", fmt.Errorf("failed to create zip: %w", errors.Join(err, zipWriter.Close(), zipFile.Close(), os.Remove(zipPath)))
	}

	if err := zipWriter.Close(); err != nil {
		return "", fmt.Errorf("failed to close zip writer: %w", errors.Join(err, zipFile.Close(), os.Remove(zipPath)))
	}

	if err := zipFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close zip file: %w", errors.Join(err, os.Remove(zipPath)))
	}

	return zipPath, nil
}
