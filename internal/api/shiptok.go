package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/revyl/cli/internal/backendheaders"
)

type ShiptokVideoUploadTarget struct {
	GithubRepository        string
	GithubPullRequestNumber int
}

func (c *Client) UploadShiptokVideo(ctx context.Context, file *os.File, target ShiptokVideoUploadTarget) (*ShiptokVideoUploadResponse, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect video: %w", err)
	}
	var framing bytes.Buffer
	writer := multipart.NewWriter(&framing)
	fields := [][2]string{{"github_repository", target.GithubRepository}}
	if target.GithubPullRequestNumber > 0 {
		fields = append(fields, [2]string{"github_pull_request_number", strconv.Itoa(target.GithubPullRequestNumber)})
	}
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, fmt.Errorf("write upload field: %w", err)
		}
	}
	contentType := "video/mp4"
	if strings.EqualFold(filepath.Ext(file.Name()), ".mov") {
		contentType = "video/quicktime"
	}
	partHeaders := make(textproto.MIMEHeader)
	partHeaders.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name": "file", "filename": filepath.Base(file.Name()),
	}))
	partHeaders.Set("Content-Type", contentType)
	if _, err := writer.CreatePart(partHeaders); err != nil {
		return nil, fmt.Errorf("create upload form: %w", err)
	}
	fileOffset := framing.Len()
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish upload form: %w", err)
	}
	body := io.MultiReader(
		bytes.NewReader(framing.Bytes()[:fileOffset]),
		io.NewSectionReader(file, 0, info.Size()),
		bytes.NewReader(framing.Bytes()[fileOffset:]),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/shiptok/videos", body)
	if err != nil {
		return nil, fmt.Errorf("create video upload request: %w", err)
	}
	req.ContentLength = int64(framing.Len()) + info.Size()
	if apiKey := strings.TrimSpace(c.GetAPIKey()); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	backendheaders.SetCloudAgentConversationContext(req)
	backendheaders.SetRuntimeTraceContext(req)
	setCIHeaders(req)
	setAgentHeaders(req)
	client := c.uploadClient
	if client == nil {
		client = c.httpClient
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send video upload: %w", err)
	}
	recordUpcomingMinimumVersion(response)
	var video ShiptokVideoUploadResponse
	if err := parseResponse(response, &video); err != nil {
		return nil, err
	}
	if strings.TrimSpace(video.Url) == "" {
		return nil, fmt.Errorf("server returned no video URL")
	}
	return &video, nil
}
