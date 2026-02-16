package asc

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UploadIPA uploads an IPA file to App Store Connect using the buildUploads REST API.
//
// This follows the multi-step upload protocol:
//  1. Create a buildUpload reservation (POST /v1/buildUploads)
//  2. Create a buildUploadFile with file metadata (POST /v1/buildUploadFiles)
//  3. Upload file chunks to presigned URLs returned in step 2
//  4. Mark the file as uploaded (PATCH /v1/buildUploadFiles/{id})
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The App Store Connect numeric app ID
//   - ipaPath: Local path to the .ipa file
//   - version: The CFBundleShortVersionString (e.g., "1.0.0")
//   - buildNumber: The CFBundleVersion (e.g., "42")
//
// Returns:
//   - string: The build upload ID (can be used to track processing)
//   - error: If any step of the upload fails
func (c *Client) UploadIPA(ctx context.Context, appID, ipaPath, version, buildNumber string) (string, error) {
	// Validate the file exists and is readable
	fileInfo, err := os.Stat(ipaPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat IPA file: %w", err)
	}
	if fileInfo.IsDir() {
		return "", fmt.Errorf("IPA path is a directory, not a file: %s", ipaPath)
	}

	fileSize := fileInfo.Size()
	fileName := filepath.Base(ipaPath)

	// Step 1: Create build upload reservation
	uploadResp, err := c.createBuildUpload(ctx, appID, version, buildNumber)
	if err != nil {
		return "", fmt.Errorf("failed to create build upload: %w", err)
	}
	uploadID := uploadResp.ID

	// Step 2: Create build upload file (get presigned URLs)
	fileResp, err := c.createBuildUploadFile(ctx, uploadID, fileName, fileSize)
	if err != nil {
		return "", fmt.Errorf("failed to create build upload file: %w", err)
	}

	// Step 3: Upload file chunks to presigned URLs
	if err := uploadFileChunks(ctx, ipaPath, fileSize, fileResp.UploadOperations); err != nil {
		return "", fmt.Errorf("failed to upload file chunks: %w", err)
	}

	// Step 4: Compute checksum and mark file as uploaded
	checksum, err := computeMD5(ipaPath)
	if err != nil {
		return "", fmt.Errorf("failed to compute file checksum: %w", err)
	}

	if err := c.commitBuildUploadFile(ctx, fileResp.ID, checksum); err != nil {
		return "", fmt.Errorf("failed to commit upload: %w", err)
	}

	return uploadID, nil
}

// buildUploadReservation holds the response from creating a build upload.
type buildUploadReservation struct {
	ID string
}

// buildUploadFileReservation holds the response from creating a build upload file.
type buildUploadFileReservation struct {
	ID               string
	UploadOperations []uploadOperation
}

// uploadOperation represents a presigned upload URL with offset/length.
type uploadOperation struct {
	Method         string       `json:"method"`
	URL            string       `json:"url"`
	Length         int64        `json:"length"`
	Offset         int64        `json:"offset"`
	RequestHeaders []httpHeader `json:"requestHeaders,omitempty"`
}

// httpHeader represents an HTTP header key-value pair.
type httpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// createBuildUpload creates a build upload reservation.
func (c *Client) createBuildUpload(ctx context.Context, appID, version, buildNumber string) (*buildUploadReservation, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "buildUploads",
			"attributes": map[string]interface{}{
				"cfBundleShortVersionString": version,
				"cfBundleVersion":            buildNumber,
			},
			"relationships": map[string]interface{}{
				"app": map[string]interface{}{
					"data": map[string]interface{}{
						"type": "apps",
						"id":   appID,
					},
				},
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	data, err := c.doRequest(ctx, http.MethodPost, "/buildUploads", strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse build upload response: %w", err)
	}

	return &buildUploadReservation{ID: resp.Data.ID}, nil
}

// createBuildUploadFile creates a file reservation for a build upload.
func (c *Client) createBuildUploadFile(ctx context.Context, uploadID, fileName string, fileSize int64) (*buildUploadFileReservation, error) {
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "buildUploadFiles",
			"attributes": map[string]interface{}{
				"fileName": fileName,
				"fileSize": fileSize,
				"uti":      "com.apple.itunes.ipa",
			},
			"relationships": map[string]interface{}{
				"buildUpload": map[string]interface{}{
					"data": map[string]interface{}{
						"type": "buildUploads",
						"id":   uploadID,
					},
				},
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	data, err := c.doRequest(ctx, http.MethodPost, "/buildUploadFiles", strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				UploadOperations []uploadOperation `json:"uploadOperations"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse build upload file response: %w", err)
	}

	return &buildUploadFileReservation{
		ID:               resp.Data.ID,
		UploadOperations: resp.Data.Attributes.UploadOperations,
	}, nil
}

// uploadFileChunks uploads file chunks to presigned URLs.
func uploadFileChunks(ctx context.Context, filePath string, fileSize int64, operations []uploadOperation) error {
	if len(operations) == 0 {
		return fmt.Errorf("no upload operations returned by API")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	client := &http.Client{Timeout: 5 * time.Minute}

	for i, op := range operations {
		method := strings.ToUpper(strings.TrimSpace(op.Method))
		if method == "" {
			method = http.MethodPut
		}

		if op.Offset+op.Length > fileSize {
			return fmt.Errorf("upload operation %d exceeds file size (offset=%d, length=%d, fileSize=%d)", i, op.Offset, op.Length, fileSize)
		}

		reader := io.NewSectionReader(file, op.Offset, op.Length)
		req, err := http.NewRequestWithContext(ctx, method, op.URL, reader)
		if err != nil {
			return fmt.Errorf("upload operation %d: failed to create request: %w", i, err)
		}

		req.ContentLength = op.Length
		for _, header := range op.RequestHeaders {
			req.Header.Set(header.Name, header.Value)
		}

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("upload operation %d: request failed: %w", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("upload operation %d: failed with status %d", i, resp.StatusCode)
		}
	}

	return nil
}

// commitBuildUploadFile marks the file as uploaded with its checksum.
func (c *Client) commitBuildUploadFile(ctx context.Context, fileID, md5Checksum string) error {
	uploaded := true
	body := map[string]interface{}{
		"data": map[string]interface{}{
			"type": "buildUploadFiles",
			"id":   fileID,
			"attributes": map[string]interface{}{
				"uploaded": uploaded,
				"sourceFileChecksums": map[string]interface{}{
					"file": map[string]interface{}{
						"hash":      md5Checksum,
						"algorithm": "MD5",
					},
				},
			},
		},
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return err
	}

	_, err = c.doRequest(ctx, http.MethodPatch, fmt.Sprintf("/buildUploadFiles/%s", fileID), strings.NewReader(string(bodyJSON)))
	return err
}

// computeMD5 computes the MD5 hash of a file.
func computeMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
