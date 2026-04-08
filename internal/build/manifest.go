package build

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// AppManifest records file metadata for a built .app or .apk so subsequent
// rebuilds can diff against it and produce a minimal delta zip.
type AppManifest struct {
	AppPath string                   `json:"app_path"`
	Hash    string                   `json:"hash"`
	Files   map[string]ManifestEntry `json:"files"`
}

// ManifestEntry stores size and modification time for a single file inside the
// app bundle.
type ManifestEntry struct {
	Size  int64 `json:"size"`
	Mtime int64 `json:"mtime"`
}

// BuildError represents a single compiler error extracted from build output.
type BuildError struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// BuildManifest walks an .app directory (or single .apk file) and records the
// size and modification time of every regular file. The overall Hash is a
// deterministic SHA-256 over all (relativePath, size, mtime) tuples sorted by
// path.
//
// Parameters:
//   - appPath: root of the .app directory or path to an .apk file
//
// Returns:
//   - *AppManifest: populated manifest
//   - error: filesystem error
func BuildManifest(appPath string) (*AppManifest, error) {
	m := &AppManifest{
		AppPath: appPath,
		Files:   make(map[string]ManifestEntry),
	}

	info, err := os.Stat(appPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", appPath, err)
	}

	if !info.IsDir() {
		m.Files[filepath.Base(appPath)] = ManifestEntry{
			Size:  info.Size(),
			Mtime: info.ModTime().Unix(),
		}
		m.Hash = hashManifestFiles(m.Files)
		return m, nil
	}

	err = filepath.Walk(appPath, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(appPath, path)
		if relErr != nil {
			return relErr
		}
		m.Files[rel] = ManifestEntry{
			Size:  fi.Size(),
			Mtime: fi.ModTime().Unix(),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", appPath, err)
	}

	m.Hash = hashManifestFiles(m.Files)
	return m, nil
}

// ManifestDiff holds the result of comparing two app manifests.
type ManifestDiff struct {
	Changed []string // new or modified relative paths (sorted)
	Deleted []string // paths present in old but absent in cur (sorted)
}

// DiffManifest compares two manifests and returns changed/added files and
// deleted files. A file is considered changed when its size or mtime differs.
//
// Parameters:
//   - old: previous manifest (nil treats every file as new)
//   - cur: current manifest
//
// Returns:
//   - ManifestDiff with Changed and Deleted paths, both sorted
func DiffManifest(old, cur *AppManifest) ManifestDiff {
	if old == nil {
		keys := make([]string, 0, len(cur.Files))
		for k := range cur.Files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return ManifestDiff{Changed: keys}
	}

	var changed []string
	for path, entry := range cur.Files {
		prev, exists := old.Files[path]
		if !exists || prev.Size != entry.Size || prev.Mtime != entry.Mtime {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)

	var deleted []string
	for path := range old.Files {
		if _, exists := cur.Files[path]; !exists {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(deleted)

	return ManifestDiff{Changed: changed, Deleted: deleted}
}

// CreateDeltaZip builds an in-memory zip containing only the specified files
// from appPath. Relative paths inside the zip mirror their position in the
// .app bundle so the worker can extract directly on top of its cached copy.
//
// Parameters:
//   - appPath: root directory of the .app bundle
//   - changedFiles: relative paths to include (from DiffManifest)
//
// Returns:
//   - []byte: zip archive bytes
//   - error: filesystem or zip error
func CreateDeltaZip(appPath string, changedFiles []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, rel := range changedFiles {
		full := filepath.Join(appPath, rel)
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}

		fi, err := os.Stat(full)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", rel, err)
		}

		header, err := zip.FileInfoHeader(fi)
		if err != nil {
			return nil, fmt.Errorf("header %s: %w", rel, err)
		}
		header.Name = rel
		header.Method = zip.Deflate

		w, err := zw.CreateHeader(header)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", rel, err)
		}
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("write %s: %w", rel, err)
		}
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// DeltaSize returns the total size of the changed files in bytes without
// creating the zip. Useful for deciding whether to use the delta path or fall
// back to a full upload.
//
// Parameters:
//   - appPath: root directory of the .app bundle
//   - changedFiles: relative paths from DiffManifest
//
// Returns:
//   - int64: sum of file sizes
func DeltaSize(appPath string, changedFiles []string) int64 {
	var total int64
	for _, rel := range changedFiles {
		fi, err := os.Stat(filepath.Join(appPath, rel))
		if err == nil {
			total += fi.Size()
		}
	}
	return total
}

// LoadManifest reads a persisted manifest from disk.
//
// Parameters:
//   - path: filesystem path to the JSON manifest file
//
// Returns:
//   - *AppManifest: loaded manifest, or nil if the file does not exist
//   - error: parse error (missing file is not an error)
func LoadManifest(path string) (*AppManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m AppManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// SaveManifest writes a manifest to disk atomically (write-to-temp then rename).
//
// Parameters:
//   - m: manifest to persist
//   - path: destination file path
//
// Returns:
//   - error: filesystem error
func SaveManifest(m *AppManifest, path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// hashManifestFiles computes a deterministic SHA-256 over sorted (path, size,
// mtime) tuples.
func hashManifestFiles(files map[string]ManifestEntry) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		e := files[k]
		fmt.Fprintf(h, "%s:%d:%d\n", k, e.Size, e.Mtime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// xcodeBuildErrorRe matches Xcode compiler errors in the format:
// /path/to/File.swift:42:15: error: Cannot convert...
var xcodeBuildErrorRe = regexp.MustCompile(`^(.+?):(\d+):(\d+):\s+(error|warning|note):\s+(.+)$`)

// ParseXcodeBuildErrors extracts structured errors from xcodebuild output.
//
// Parameters:
//   - output: raw xcodebuild stdout+stderr
//
// Returns:
//   - []BuildError: parsed errors (may be empty)
func ParseXcodeBuildErrors(output string) []BuildError {
	var errors []BuildError
	for _, line := range strings.Split(output, "\n") {
		m := xcodeBuildErrorRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNum, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		errors = append(errors, BuildError{
			File:     m[1],
			Line:     lineNum,
			Column:   col,
			Severity: m[4],
			Message:  m[5],
		})
	}
	return errors
}

// gradleBuildErrorRe matches Kotlin/Java compiler errors in the format:
// e: file:///path/File.kt:42:15 Cannot convert...
var gradleBuildErrorRe = regexp.MustCompile(`^e:\s+file://(.+?):(\d+):(\d+)\s+(.+)$`)

// ParseGradleBuildErrors extracts structured errors from Gradle build output.
//
// Parameters:
//   - output: raw Gradle stdout+stderr
//
// Returns:
//   - []BuildError: parsed errors (may be empty)
func ParseGradleBuildErrors(output string) []BuildError {
	var errors []BuildError
	for _, line := range strings.Split(output, "\n") {
		m := gradleBuildErrorRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNum, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		errors = append(errors, BuildError{
			File:     m[1],
			Line:     lineNum,
			Column:   col,
			Severity: "error",
			Message:  m[4],
		})
	}
	return errors
}
