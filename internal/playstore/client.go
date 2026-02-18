// Package playstore provides a client for the Google Play Developer API.
//
// This package implements service account authentication and REST API calls
// for managing Android app distribution through the Google Play Console.
// It uses the Edits API workflow: insert edit -> upload AAB -> set track -> commit.
package playstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2/google"
)

const (
	// BaseURL is the Google Play Developer API base URL.
	BaseURL = "https://androidpublisher.googleapis.com/androidpublisher/v3"

	// UploadBaseURL is the upload endpoint for the Google Play Developer API.
	UploadBaseURL = "https://androidpublisher.googleapis.com/upload/androidpublisher/v3"

	// Scope is the required OAuth2 scope for the Google Play Developer API.
	Scope = "https://www.googleapis.com/auth/androidpublisher"

	// defaultTimeout is the default HTTP request timeout.
	defaultTimeout = 60 * time.Second
)

// Client is a Google Play Developer API client.
//
// It uses a service account for authentication and provides methods for
// uploading AABs and managing release tracks.
type Client struct {
	httpClient  *http.Client
	packageName string
}

// NewClient creates a new Google Play Developer API client.
//
// Parameters:
//   - serviceAccountPath: Path to the Google Cloud service account JSON key file
//   - packageName: The Android package name (e.g., "com.nof1.experiments")
//
// Returns:
//   - *Client: The configured client
//   - error: If the service account cannot be loaded or authenticated
func NewClient(ctx context.Context, serviceAccountPath, packageName string) (*Client, error) {
	data, err := os.ReadFile(serviceAccountPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read service account file: %w", err)
	}

	conf, err := google.JWTConfigFromJSON(data, Scope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service account: %w", err)
	}

	httpClient := conf.Client(ctx)
	httpClient.Timeout = defaultTimeout

	return &Client{
		httpClient:  httpClient,
		packageName: packageName,
	}, nil
}

// Edit represents a Google Play edit session.
type Edit struct {
	// ID is the edit session ID.
	ID string `json:"id"`

	// ExpiryTimeSeconds is the expiration time of the edit in seconds.
	ExpiryTimeSeconds string `json:"expiryTimeSeconds,omitempty"`
}

// Track represents a Google Play release track.
type Track struct {
	// Track is the track identifier (internal, alpha, beta, production).
	Track string `json:"track"`

	// Releases is the list of releases on this track.
	Releases []Release `json:"releases,omitempty"`
}

// Release represents a release on a track.
type Release struct {
	// Name is the release name (optional, for display only).
	Name string `json:"name,omitempty"`

	// VersionCodes is the list of version codes in this release.
	VersionCodes []string `json:"versionCodes,omitempty"`

	// Status is the release status (draft, inProgress, halted, completed).
	Status string `json:"status"`

	// ReleaseNotes is the list of localized release notes.
	ReleaseNotes []ReleaseNote `json:"releaseNotes,omitempty"`
}

// ReleaseNote represents localized release notes.
type ReleaseNote struct {
	// Language is the BCP-47 language tag (e.g., "en-US").
	Language string `json:"language"`

	// Text is the release note text.
	Text string `json:"text"`
}

// UploadResult contains the result of an AAB upload.
type UploadResult struct {
	// VersionCode is the version code of the uploaded AAB.
	VersionCode int64 `json:"versionCode"`
}

// InsertEdit creates a new edit session.
//
// An edit is required for all modifications. Changes are only visible
// after the edit is committed.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *Edit: The created edit session
//   - error: If the request fails
func (c *Client) InsertEdit(ctx context.Context) (*Edit, error) {
	url := fmt.Sprintf("%s/applications/%s/edits", BaseURL, c.packageName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("insert edit failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("insert edit failed (status %d): %s", resp.StatusCode, string(body))
	}

	var edit Edit
	if err := json.NewDecoder(resp.Body).Decode(&edit); err != nil {
		return nil, fmt.Errorf("failed to parse edit response: %w", err)
	}

	return &edit, nil
}

// UploadBundle uploads an AAB file to a specific edit.
//
// Parameters:
//   - ctx: Context for cancellation
//   - editID: The edit session ID
//   - aabPath: Local path to the .aab file
//
// Returns:
//   - *UploadResult: The upload result with version code
//   - error: If the upload fails
func (c *Client) UploadBundle(ctx context.Context, editID, aabPath string) (*UploadResult, error) {
	file, err := os.Open(aabPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open AAB file: %w", err)
	}
	defer file.Close()

	url := fmt.Sprintf("%s/applications/%s/edits/%s/bundles?uploadType=media",
		UploadBaseURL, c.packageName, editID)

	// Create multipart request
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filepath.Base(aabPath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("failed to copy AAB to form: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bundle upload failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bundle upload failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		VersionCode int64 `json:"versionCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse upload response: %w", err)
	}

	return &UploadResult{VersionCode: result.VersionCode}, nil
}

// SetTrack assigns a version to a release track.
//
// Parameters:
//   - ctx: Context for cancellation
//   - editID: The edit session ID
//   - track: The track name (internal, alpha, beta, production)
//   - versionCode: The version code from the uploaded AAB
//   - releaseNotes: Optional release notes (can be nil)
//
// Returns:
//   - error: If the request fails
func (c *Client) SetTrack(ctx context.Context, editID, track string, versionCode int64, releaseNotes []ReleaseNote) error {
	url := fmt.Sprintf("%s/applications/%s/edits/%s/tracks/%s",
		BaseURL, c.packageName, editID, track)

	trackBody := Track{
		Track: track,
		Releases: []Release{
			{
				VersionCodes: []string{fmt.Sprintf("%d", versionCode)},
				Status:       "completed",
				ReleaseNotes: releaseNotes,
			},
		},
	}

	bodyJSON, err := json.Marshal(trackBody)
	if err != nil {
		return fmt.Errorf("failed to marshal track body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("set track failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set track failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// CommitEdit commits an edit, making all changes live.
//
// Parameters:
//   - ctx: Context for cancellation
//   - editID: The edit session ID to commit
//
// Returns:
//   - error: If the commit fails
func (c *Client) CommitEdit(ctx context.Context, editID string) error {
	url := fmt.Sprintf("%s/applications/%s/edits/%s:commit",
		BaseURL, c.packageName, editID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create commit request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("commit edit failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("commit edit failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// UploadAndRelease performs the full upload-and-release workflow:
//  1. Create a new edit
//  2. Upload the AAB
//  3. Assign to the specified track
//  4. Commit the edit
//
// Parameters:
//   - ctx: Context for cancellation
//   - aabPath: Local path to the .aab file
//   - track: Release track (internal, alpha, beta, production)
//   - releaseNotes: Optional release notes (can be nil)
//
// Returns:
//   - int64: The version code of the uploaded AAB
//   - error: If any step fails
func (c *Client) UploadAndRelease(ctx context.Context, aabPath, track string, releaseNotes []ReleaseNote) (int64, error) {
	// Step 1: Create edit
	edit, err := c.InsertEdit(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to create edit: %w", err)
	}

	// Step 2: Upload AAB
	result, err := c.UploadBundle(ctx, edit.ID, aabPath)
	if err != nil {
		return 0, fmt.Errorf("failed to upload bundle: %w", err)
	}

	// Step 3: Set track
	if err := c.SetTrack(ctx, edit.ID, track, result.VersionCode, releaseNotes); err != nil {
		return 0, fmt.Errorf("failed to set track: %w", err)
	}

	// Step 4: Commit
	if err := c.CommitEdit(ctx, edit.ID); err != nil {
		return 0, fmt.Errorf("failed to commit edit: %w", err)
	}

	return result.VersionCode, nil
}
