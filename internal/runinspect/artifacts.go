// Generic artifact-download helpers for the run-inspector — fetch a
// presigned S3 URL, optionally gunzip, optionally cache to disk.

package runinspect

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/revyl/cli/internal/api"
)

// ErrArtifactNotAvailable signals the report exists but the requested
// artifact wasn't uploaded for this run.
var ErrArtifactNotAvailable = errors.New("artifact not available for this run")

// FetchArtifactBytes downloads a presigned-URL artifact and returns its
// decompressed contents (or raw bytes when `gunzip` is false). When
// `cacheDir` is set, the payload is cached at
// `<cacheDir>/<taskID>/<filename>` and reused on subsequent calls.
// Caps decompressed size at 256 MiB.
func FetchArtifactBytes(
	ctx context.Context,
	url string,
	httpClient *http.Client,
	cacheDir, taskID, cacheFilename string,
	gunzip bool,
) ([]byte, error) {
	if url == "" {
		return nil, ErrArtifactNotAvailable
	}
	// Never cache a still-growing live object under the finalized key — see
	// FetchDeviceStateLines. Centralized here so callers can't poison the cache.
	if IsLiveURL(url) {
		cacheDir = ""
	}
	if cached, ok := readCachedBytes(cacheDir, taskID, cacheFilename); ok {
		return cached, nil
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"download artifact: HTTP %d (presigned URL may have expired — re-fetch report)",
			resp.StatusCode,
		)
	}

	const maxDecompressedBytes = 256 * 1024 * 1024
	var reader io.Reader = resp.Body
	if gunzip {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gunzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	limited := io.LimitReader(reader, maxDecompressedBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read artifact: %w", err)
	}
	if int64(len(raw)) > maxDecompressedBytes {
		return nil, fmt.Errorf(
			"artifact exceeds %d byte ceiling — likely corrupt or unexpectedly large",
			maxDecompressedBytes,
		)
	}
	writeCachedBytes(cacheDir, taskID, cacheFilename, raw)
	return raw, nil
}

// DownloadArtifactToFile streams `url` to `outPath` (no decompression).
// Writes to a tempfile and renames on success so a partial download
// never leaves a half-written destination.
func DownloadArtifactToFile(
	ctx context.Context,
	url string,
	httpClient *http.Client,
	outPath string,
) (int64, error) {
	if url == "" {
		return 0, ErrArtifactNotAvailable
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf(
			"download artifact: HTTP %d (presigned URL may have expired — re-fetch report)",
			resp.StatusCode,
		)
	}

	dir := filepath.Dir(outPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, fmt.Errorf("create output dir: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".revyl-download-*")
	if err != nil {
		return 0, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	written, err := io.Copy(tmp, resp.Body)
	if err != nil {
		_ = tmp.Close()
		return 0, fmt.Errorf("write artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return 0, fmt.Errorf("rename to %s: %w", outPath, err)
	}
	return written, nil
}

// ResolveDeviceLogsURL hits the dedicated /reports/{report_id}/device-logs
// endpoint to obtain a presigned URL. Returns ErrArtifactNotAvailable
// when the backend says no device logs were uploaded.
func ResolveDeviceLogsURL(
	ctx context.Context,
	client *api.Client,
	reportID string,
) (*api.DeviceLogsDownloadResponse, error) {
	if reportID == "" {
		return nil, errors.New("report id missing — re-fetch the report first")
	}
	resp, err := client.GetDeviceLogsDownloadURL(ctx, reportID)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrArtifactNotAvailable
		}
		return nil, fmt.Errorf("fetch device-logs URL: %w", err)
	}
	if resp == nil || resp.DownloadUrl == "" {
		return nil, ErrArtifactNotAvailable
	}
	return resp, nil
}

func readCachedBytes(cacheDir, taskID, filename string) ([]byte, bool) {
	return readRunCacheFile(cacheDir, taskID, filename)
}

func writeCachedBytes(cacheDir, taskID, filename string, data []byte) {
	writeRunCacheFile(cacheDir, taskID, filename, data)
}
