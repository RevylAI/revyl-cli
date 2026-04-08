package devpush

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/mcp"
)

// ProxyS3Transport delivers artifacts via S3 presigned URLs and sends install
// commands through the backend device-proxy. This is the default transport
// until the relay service is available.
type ProxyS3Transport struct {
	apiClient  *api.Client
	sessionMgr *mcp.DeviceSessionManager
}

// PushArtifact uploads data to a temporary S3 location via the session
// artifacts presigned-URL endpoint and returns a download URL the worker
// can fetch from. This uses a single API call (no database records).
//
// Parameters:
//   - ctx: cancellation context
//   - session: target device session (SessionID used for S3 key scoping)
//   - data: raw bytes to upload (delta zip)
//
// Returns:
//   - ArtifactRef: S3 presigned download URL
//   - error: upload failure
func (t *ProxyS3Transport) PushArtifact(ctx context.Context, session *mcp.DeviceSession, data []byte) (ArtifactRef, error) {
	ct := "application/zip"
	presign, err := t.apiClient.GetSessionArtifactUploadURL(ctx, session.SessionID, &api.SessionArtifactUploadRequest{
		FileSize:    len(data),
		ContentType: &ct,
	})
	if err != nil {
		return ArtifactRef{}, fmt.Errorf("failed to get upload URL: %w", err)
	}

	if err := putBytesToPresignedURL(ctx, presign.UploadUrl, ct, data); err != nil {
		return ArtifactRef{}, fmt.Errorf("failed to upload delta: %w", err)
	}

	return ArtifactRef{URL: presign.DownloadUrl}, nil
}

// SendInstall sends an install request to the worker via the backend proxy
// with the extended install_mode field for delta/fast install.
//
// Parameters:
//   - ctx: cancellation context
//   - session: target device session
//   - ref: artifact reference from PushArtifact
//   - opts: install mode, bundle ID, platform
//
// Returns:
//   - InstallResult: worker response
//   - error: proxy or worker error
func (t *ProxyS3Transport) SendInstall(ctx context.Context, session *mcp.DeviceSession, ref ArtifactRef, opts InstallOpts) (*InstallResult, error) {
	body := map[string]interface{}{
		"app_url":      ref.URL,
		"install_mode": opts.Mode,
	}
	if ref.Path != "" {
		body["app_path"] = ref.Path
	}
	if opts.BundleID != "" {
		body["bundle_id"] = opts.BundleID
	}
	if opts.Platform != "" {
		body["platform"] = opts.Platform
	}
	if len(opts.DeletedFiles) > 0 {
		body["deleted_files"] = opts.DeletedFiles
	}

	respBody, err := t.sessionMgr.WorkerRequestForSession(ctx, session.Index, "/install", body)
	if err != nil {
		return nil, fmt.Errorf("install request failed: %w", err)
	}

	return parseInstallResponse(respBody)
}

// parseInstallResponse unmarshals the worker JSON and enforces the success
// flag. A 200 response with success:false in the body is surfaced as an error
// so callers don't need a separate validation step.
//
// Returns:
//   - *InstallResult: parsed response (non-nil even on logical failure so
//     callers can inspect metadata like install_method)
//   - error: non-nil when JSON is malformed or the worker reported failure
func parseInstallResponse(respBody []byte) (*InstallResult, error) {
	var result InstallResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse install response: %w", err)
	}
	if !result.Success {
		errMsg := result.Error
		if errMsg == "" {
			errMsg = "install action failed"
		}
		return &result, fmt.Errorf("worker install failed: %s", errMsg)
	}
	return &result, nil
}

// putBytesToPresignedURL uploads raw bytes to an S3 presigned PUT URL.
func putBytesToPresignedURL(ctx context.Context, uploadURL, contentType string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(data))

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upload returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
