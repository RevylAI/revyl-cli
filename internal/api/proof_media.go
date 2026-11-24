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

// PublishSessionProofMedia uploads a local PNG, JPEG, GIF, or WebP for a device session and returns
// its published name and unauthenticated public URL. Publishing discloses the media URL and session
// report to anyone holding the link; the backend may re-encode the image and assign a digest-bound name.
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
	recordUpcomingMinimumVersion(resp)

	var result SessionProofMediaResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
