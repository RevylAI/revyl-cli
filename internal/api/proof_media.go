package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/revyl/cli/internal/backendheaders"
)

// proofMediaPathFormat is the multipart publish endpoint. It is deliberately
// absent from the generated model surface (see scripts/openapi-excluded-paths.txt):
// multipart bodies produce no useful generated client, so the transport and the
// response model below are handwritten wrappers.
const proofMediaPathFormat = "/api/v1/reports-v3/sessions/%s/proof-media"

// SessionProofMediaResponse is the published location of one proof-media file.
//
// PublicURL serves the image bytes with no authentication, which is the point:
// it can be embedded in a pull request comment that reviewers read without a
// Revyl session.
type SessionProofMediaResponse struct {
	FileName  string `json:"file_name"`
	PublicURL string `json:"public_url"`
}

// PublishSessionProofMedia uploads an image for a device session and returns
// its public URL.
//
// Publishing is a disclosure: the returned URL, and the session's report, become
// readable by anyone who holds the link.
//
// Parameters:
//   - ctx: cancellation context.
//   - sessionID: the device session the media belongs to.
//   - filePath: a local PNG, JPEG, GIF, or WebP. Its base name becomes the
//     published name, so it must satisfy the backend's strict name rules.
//
// Returns:
//   - The published file name and public URL.
//   - An error when the file cannot be read, the session is not accessible to
//     the caller's organization, or the backend rejects the file.
func (c *Client) PublishSessionProofMedia(ctx context.Context, sessionID, filePath string) (*SessionProofMediaResponse, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filePath, err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("create upload form: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("read %s: %w", filePath, err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalize upload form: %w", err)
	}

	url := c.baseURL + fmt.Sprintf(proofMediaPathFormat, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if apiKey := strings.TrimSpace(c.GetAPIKey()); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	backendheaders.SetCloudAgentConversationContext(req)
	setCIHeaders(req)
	setAgentHeaders(req)

	client := c.uploadClient
	if client == nil {
		client = c.httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish proof media: %w", err)
	}

	var result SessionProofMediaResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
