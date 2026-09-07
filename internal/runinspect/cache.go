package runinspect

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/privatefs"
)

func runCachePath(cacheDir, taskID, filename string) (string, bool) {
	if cacheDir == "" || !isSingleCachePathComponent(taskID) ||
		!isSingleCachePathComponent(filename) {
		return "", false
	}
	return path.Join(taskID, filename), true
}

func isSingleCachePathComponent(value string) bool {
	return value != "" && value != "." && value != ".." &&
		!filepath.IsAbs(value) && filepath.VolumeName(value) == "" &&
		!strings.ContainsAny(value, "/\\") && !strings.ContainsRune(value, '\x00')
}

func readRunCacheFile(cacheDir, taskID, filename string) ([]byte, bool) {
	relativePath, ok := runCachePath(cacheDir, taskID, filename)
	if !ok {
		return nil, false
	}
	cacheRoot, err := openOrCreateRunCacheDirectory(cacheDir, false)
	if err != nil {
		return nil, false
	}
	defer func() { _ = cacheRoot.Close() }()

	taskRoot, err := privatefs.OpenDirectoryInRoot(cacheRoot, path.Dir(relativePath))
	if err != nil {
		return nil, false
	}
	defer func() { _ = taskRoot.Close() }()
	data, err := taskRoot.ReadFile(filename)
	if err != nil {
		return nil, false
	}
	return data, true
}

func writeRunCacheFile(cacheDir, taskID, filename string, data []byte) {
	relativePath, ok := runCachePath(cacheDir, taskID, filename)
	if !ok {
		return
	}
	cacheRoot, err := openOrCreateRunCacheDirectory(cacheDir, true)
	if err != nil {
		return
	}
	defer func() { _ = cacheRoot.Close() }()

	taskRoot, err := privatefs.CreateDirectoryInRoot(cacheRoot, path.Dir(relativePath))
	if err != nil {
		return
	}
	defer func() { _ = taskRoot.Close() }()
	file, err := taskRoot.OpenFile(
		filename,
		os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return
	}
	if err := file.Truncate(0); err != nil {
		return
	}
	if _, err := file.Write(data); err != nil {
		return
	}
}

func openOrCreateRunCacheDirectory(cacheDir string, create bool) (*os.Root, error) {
	cacheDir = filepath.Clean(cacheDir)
	trustedRoot, relativePath := filepath.Dir(cacheDir), filepath.Base(cacheDir)
	if cacheDir == DefaultCacheDir() {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		trustedRoot, relativePath = home, filepath.Join(".revyl", "run-cache")
	}
	if create {
		return privatefs.CreateDirectory(trustedRoot, relativePath)
	}
	return privatefs.OpenDirectory(trustedRoot, relativePath)
}
