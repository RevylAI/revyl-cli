// Package api provides the HTTP client for the Revyl API.
//
// This package handles all communication with the Revyl backend,
// including authentication, request/response handling, and error management.
//
// Type Strategy:
// - generated.go contains types auto-generated from the backend OpenAPI spec
// - client.go contains CLI-specific wrapper types that are more ergonomic
// - CLI types use simple strings instead of pointers/UUIDs for ease of use
// - For new endpoints, prefer using generated types when they match CLI needs
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revyl/cli/internal/config"
)

const (
	// DefaultBaseURL is the default Revyl API base URL.
	DefaultBaseURL = "https://backend.revyl.ai"

	// DefaultTimeout is the default HTTP request timeout.
	DefaultTimeout = 30 * time.Second
)

// Client is the Revyl API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new API client using production URLs.
//
// Parameters:
//   - apiKey: The API key for authentication
//
// Returns:
//   - *Client: A new client instance
func NewClient(apiKey string) *Client {
	return &Client{
		baseURL: DefaultBaseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// NewClientWithDevMode creates a new API client with dev mode support.
// When devMode is true, the client uses localhost URLs read from .env files.
//
// Parameters:
//   - apiKey: The API key for authentication
//   - devMode: If true, use local development server URLs
//
// Returns:
//   - *Client: A new client instance
func NewClientWithDevMode(apiKey string, devMode bool) *Client {
	return &Client{
		baseURL: config.GetBackendURL(devMode),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// NewClientWithBaseURL creates a new API client with a custom base URL.
//
// Parameters:
//   - apiKey: The API key for authentication
//   - baseURL: The base URL for the API
//
// Returns:
//   - *Client: A new client instance
func NewClientWithBaseURL(apiKey, baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

// GetAPIKey returns the API key used by this client.
//
// Returns:
//   - string: The API key
func (c *Client) GetAPIKey() string {
	return c.apiKey
}

// APIError represents an error response from the API.
type APIError struct {
	StatusCode int
	Message    string
	Detail     string
}

// Error returns a human-readable error message.
//
// Returns:
//   - string: The error message, with fallback to HTTP status if no message available
func (e *APIError) Error() string {
	if e.Message != "" && e.Detail != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Detail)
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, http.StatusText(e.StatusCode))
}

// doRequest performs an HTTP request with authentication.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "revyl-cli/1.0")

	// Set source tracking header
	// X-Revyl-Client identifies the client type (cli maps to "api" source in DB)
	req.Header.Set("X-Revyl-Client", "cli")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// parseResponse parses the response body into the target struct.
func parseResponse(resp *http.Response, target interface{}) error {
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)

		// Try to parse structured error response
		// Supports multiple common error field names
		var errResp struct {
			Error   string `json:"error"`
			Detail  string `json:"detail"`
			Message string `json:"message"`
		}
		json.Unmarshal(body, &errResp)

		// Build error message from available fields
		message := errResp.Error
		if message == "" {
			message = errResp.Message
		}
		detail := errResp.Detail

		// Fallback to raw body if no structured error found
		if message == "" && detail == "" {
			bodyStr := string(body)
			if len(bodyStr) > 200 {
				bodyStr = bodyStr[:200] + "..."
			}
			if bodyStr != "" {
				detail = bodyStr
			}
		}

		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    message,
			Detail:     detail,
		}
	}

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// ExecuteTestRequest represents a test execution request.
// Source tracking is handled via HTTP headers (X-Revyl-Client, X-CI-System).
type ExecuteTestRequest struct {
	TestID         string `json:"test_id"`
	Retries        int    `json:"retries,omitempty"`
	BuildVersionID string `json:"build_version_id,omitempty"`
}

// ExecuteTestResponse represents a test execution response.
type ExecuteTestResponse struct {
	ID      string `json:"id"`
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ExecuteTest starts a test execution.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The execution request
//
// Returns:
//   - *ExecuteTestResponse: The execution response with task ID
//   - error: Any error that occurred
func (c *Client) ExecuteTest(ctx context.Context, req *ExecuteTestRequest) (*ExecuteTestResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/execution/api/execute_test_id_async", req)
	if err != nil {
		return nil, err
	}

	var result ExecuteTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	// Use ID if TaskID is empty (backwards compatibility)
	if result.TaskID == "" && result.ID != "" {
		result.TaskID = result.ID
	}

	return &result, nil
}

// ExecuteWorkflowRequest represents a workflow execution request.
// Source tracking is handled via HTTP headers (X-Revyl-Client, X-CI-System).
type ExecuteWorkflowRequest struct {
	WorkflowID string `json:"workflow_id"`
	Retries    int    `json:"retries,omitempty"`
}

// ExecuteWorkflowResponse represents a workflow execution response.
// Success is not included because it's not known at queue time - it's determined
// later via SSE monitoring when the workflow completes.
type ExecuteWorkflowResponse struct {
	TaskID string `json:"task_id"`
}

// ExecuteWorkflow starts a workflow execution.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The execution request
//
// Returns:
//   - *ExecuteWorkflowResponse: The execution response with task ID
//   - error: Any error that occurred
func (c *Client) ExecuteWorkflow(ctx context.Context, req *ExecuteWorkflowRequest) (*ExecuteWorkflowResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/execution/api/execute_workflow_id_async", req)
	if err != nil {
		return nil, err
	}

	var result ExecuteWorkflowResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UploadBuildRequest represents a build upload request.
type UploadBuildRequest struct {
	BuildVarID   string                 `json:"build_var_id"`
	Version      string                 `json:"version"`
	FilePath     string                 `json:"-"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
	SetAsCurrent bool                   `json:"set_as_current,omitempty"`
}

// UploadBuildResponse represents a build upload response.
type UploadBuildResponse struct {
	VersionID string `json:"version_id"`
	Version   string `json:"version"`
	PackageID string `json:"package_id,omitempty"`
}

// UploadBuild uploads a build artifact.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The upload request
//
// Returns:
//   - *UploadBuildResponse: The upload response
//   - error: Any error that occurred
func (c *Client) UploadBuild(ctx context.Context, req *UploadBuildRequest) (*UploadBuildResponse, error) {
	// Get file name from path
	fileName := filepath.Base(req.FilePath)

	// Build URL with query parameters (backend expects version and file_name as query params)
	uploadURLPath := fmt.Sprintf(
		"/api/v1/builds/vars/%s/versions/upload-url?version=%s&file_name=%s",
		req.BuildVarID,
		url.QueryEscape(req.Version),
		url.QueryEscape(fileName),
	)

	// POST with empty body to get presigned URL
	presignResp, err := c.doRequest(ctx, "POST", uploadURLPath, nil)
	if err != nil {
		return nil, err
	}

	var presignResult struct {
		VersionID   string `json:"version_id"`
		Version     string `json:"version"`
		UploadURL   string `json:"upload_url"`
		ContentType string `json:"content_type"`
	}
	if err := parseResponse(presignResp, &presignResult); err != nil {
		return nil, err
	}

	// Upload the file to S3
	file, err := os.Open(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	uploadReq, err := http.NewRequestWithContext(ctx, "PUT", presignResult.UploadURL, file)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload request: %w", err)
	}

	uploadReq.Header.Set("Content-Type", presignResult.ContentType)
	uploadReq.ContentLength = fileInfo.Size()

	uploadResp, err := c.httpClient.Do(uploadReq)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode >= 400 {
		body, _ := io.ReadAll(uploadResp.Body)
		return nil, fmt.Errorf("upload failed with status %d: %s", uploadResp.StatusCode, string(body))
	}

	// Complete the upload
	completeResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/builds/versions/%s/complete-upload", presignResult.VersionID),
		map[string]interface{}{
			"version_id": presignResult.VersionID,
			"metadata":   req.Metadata,
		})
	if err != nil {
		return nil, err
	}

	var completeResult UploadBuildResponse
	if err := parseResponse(completeResp, &completeResult); err != nil {
		return nil, err
	}

	return &completeResult, nil
}

// BuildVersion represents a build version.
// Matches backend BuildVersionResponse schema.
type BuildVersion struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	UploadedAt  string `json:"uploaded_at"`
	PackageName string `json:"package_name,omitempty"`
	PackageID   string `json:"package_id,omitempty"`
	IsCurrent   bool   `json:"is_current,omitempty"`
}

// ListBuildVersions lists build versions for a build variable.
//
// Parameters:
//   - ctx: Context for cancellation
//   - buildVarID: The build variable ID
//
// Returns:
//   - []BuildVersion: List of build versions
//   - error: Any error that occurred
func (c *Client) ListBuildVersions(ctx context.Context, buildVarID string) ([]BuildVersion, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/builds/vars/%s/versions", buildVarID), nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Versions []BuildVersion `json:"versions"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result.Versions, nil
}

// GetTest retrieves a test by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test ID
//
// Returns:
//   - *Test: The test data
//   - error: Any error that occurred
func (c *Client) GetTest(ctx context.Context, testID string) (*Test, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/tests/get_test_by_id/%s", testID), nil)
	if err != nil {
		return nil, err
	}

	var result Test
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Test represents a test definition.
type Test struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Platform       string                 `json:"platform"`
	Tasks          interface{}            `json:"tasks"`
	Version        int                    `json:"version"`
	LastModifiedBy string                 `json:"last_modified_by,omitempty"`
	BuildVarID     string                 `json:"build_var_id,omitempty"`
	PinnedVersion  string                 `json:"pinned_version,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// UpdateTestRequest represents a test update request.
type UpdateTestRequest struct {
	TestID          string      `json:"-"`
	Tasks           interface{} `json:"tasks,omitempty"`
	BuildVarID      string      `json:"build_var_id,omitempty"`
	ExpectedVersion int         `json:"expected_version,omitempty"`
	Force           bool        `json:"-"` // Client-side only, not sent to server
}

// UpdateTestResponse represents a test update response.
type UpdateTestResponse struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// UpdateTest updates a test definition.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The update request
//
// Returns:
//   - *UpdateTestResponse: The update response
//   - error: Any error that occurred
func (c *Client) UpdateTest(ctx context.Context, req *UpdateTestRequest) (*UpdateTestResponse, error) {
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/tests/update/%s", req.TestID), req)
	if err != nil {
		return nil, err
	}

	var result UpdateTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateTestRequest represents a test creation request.
type CreateTestRequest struct {
	Name       string      `json:"name"`
	Platform   string      `json:"platform"`
	Tasks      interface{} `json:"tasks"`
	BuildVarID string      `json:"build_var_id,omitempty"`
	OrgID      string      `json:"org_id,omitempty"`
}

// CreateTestResponse represents a test creation response.
// The backend returns the full Test object, but we only need id and version.
type CreateTestResponse struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Name    string `json:"name,omitempty"`
}

// CreateTest creates a new test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The creation request
//
// Returns:
//   - *CreateTestResponse: The creation response
//   - error: Any error that occurred
func (c *Client) CreateTest(ctx context.Context, req *CreateTestRequest) (*CreateTestResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/create", req)
	if err != nil {
		return nil, err
	}

	var result CreateTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ValidateAPIKeyResponse represents the response from API key validation.
// Contains user information returned when an API key is successfully validated.
type ValidateAPIKeyResponse struct {
	// UserID is the unique identifier for the authenticated user.
	UserID string `json:"user_id"`

	// OrgID is the organization ID the user belongs to.
	OrgID string `json:"org_id"`

	// Email is the user's email address.
	Email string `json:"email"`

	// ConcurrencyLimit is the maximum number of concurrent test executions allowed.
	ConcurrencyLimit int `json:"concurrency_limit"`
}

// ValidateAPIKey validates the client's API key against the backend.
// Returns user information if the API key is valid.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *ValidateAPIKeyResponse: User information if API key is valid
//   - error: APIError with StatusCode 401 if invalid, or other errors
func (c *Client) ValidateAPIKey(ctx context.Context) (*ValidateAPIKeyResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/entity/users/get_user_uuid", nil)
	if err != nil {
		return nil, err
	}

	var result ValidateAPIKeyResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// StreamUploadBuild uploads a build using streaming (alternative to presigned URL).
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The upload request
//
// Returns:
//   - *UploadBuildResponse: The upload response
//   - error: Any error that occurred
func (c *Client) StreamUploadBuild(ctx context.Context, req *UploadBuildRequest) (*UploadBuildResponse, error) {
	file, err := os.Open(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	part, err := writer.CreateFormFile("file", filepath.Base(req.FilePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("failed to copy file: %w", err)
	}

	// Add version
	if err := writer.WriteField("version", req.Version); err != nil {
		return nil, fmt.Errorf("failed to write version field: %w", err)
	}

	// Add metadata
	if req.Metadata != nil {
		metadataJSON, _ := json.Marshal(req.Metadata)
		if err := writer.WriteField("metadata", string(metadataJSON)); err != nil {
			return nil, fmt.Errorf("failed to write metadata field: %w", err)
		}
	}

	writer.Close()

	url := fmt.Sprintf("%s/api/v1/builds/vars/%s/versions/stream-upload", c.baseURL, req.BuildVarID)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	var result UploadBuildResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// SimpleTest represents a lightweight test item for listing.
// This is a CLI-specific type that's simpler than the generated SimpleTestItem.
type SimpleTest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// CLISimpleTestListResponse represents the response from listing simple tests.
// This is a CLI-specific type that uses SimpleTest instead of the generated type.
type CLISimpleTestListResponse struct {
	Tests []SimpleTest `json:"tests"`
	Count int          `json:"count"`
}

// ListOrgTests fetches all tests for the authenticated user's organization.
// Returns a lightweight list with just id, name, and platform.
//
// Parameters:
//   - ctx: Context for cancellation
//   - limit: Maximum number of tests to return (default: 100)
//   - offset: Number of tests to skip for pagination (default: 0)
//
// Returns:
//   - *CLISimpleTestListResponse: List of tests with count
//   - error: Any error that occurred
func (c *Client) ListOrgTests(ctx context.Context, limit, offset int) (*CLISimpleTestListResponse, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	path := fmt.Sprintf("/api/v1/tests/get_simple_tests?limit=%d&offset=%d", limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLISimpleTestListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// BuildVar represents a build variable in the organization.
// This is a CLI-specific type that's simpler than the generated BuildVarResponse.
type BuildVar struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	Description    string `json:"description,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
	LatestVersion  string `json:"latest_version,omitempty"`
	VersionsCount  int    `json:"versions_count"`
}

// CLIPaginatedBuildVarsResponse represents a paginated list of build variables.
// This is a CLI-specific type that uses BuildVar instead of the generated type.
type CLIPaginatedBuildVarsResponse struct {
	Items       []BuildVar `json:"items"`
	Total       int        `json:"total"`
	Page        int        `json:"page"`
	PageSize    int        `json:"page_size"`
	TotalPages  int        `json:"total_pages"`
	HasNext     bool       `json:"has_next"`
	HasPrevious bool       `json:"has_previous"`
}

// ListOrgBuildVars fetches all build variables for the authenticated user's organization.
//
// Parameters:
//   - ctx: Context for cancellation
//   - platform: Optional platform filter (android, ios, or empty for all)
//   - page: Page number (1-indexed, default: 1)
//   - pageSize: Number of items per page (default: 50, max: 100)
//
// Returns:
//   - *CLIPaginatedBuildVarsResponse: Paginated list of build variables
//   - error: Any error that occurred
func (c *Client) ListOrgBuildVars(ctx context.Context, platform string, page, pageSize int) (*CLIPaginatedBuildVarsResponse, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	path := fmt.Sprintf("/api/v1/builds/vars?page=%d&page_size=%d", page, pageSize)
	if platform != "" {
		path += "&platform=" + platform
	}

	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIPaginatedBuildVarsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetBuildVar retrieves a build variable by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - buildVarID: The build variable ID
//
// Returns:
//   - *BuildVar: The build variable data
//   - error: Any error that occurred
func (c *Client) GetBuildVar(ctx context.Context, buildVarID string) (*BuildVar, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/builds/vars/%s", buildVarID), nil)
	if err != nil {
		return nil, err
	}

	var result BuildVar
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateBuildVarRequest represents a request to create a new build variable.
type CreateBuildVarRequest struct {
	// Name is the display name for the build variable.
	Name string `json:"name"`

	// Platform is the target platform (ios or android).
	Platform string `json:"platform"`

	// Description is an optional description.
	Description string `json:"description,omitempty"`
}

// CreateBuildVarResponse represents the response from creating a build variable.
type CreateBuildVarResponse struct {
	// ID is the unique identifier for the created build variable.
	ID string `json:"id"`

	// Name is the display name.
	Name string `json:"name"`

	// Platform is the target platform.
	Platform string `json:"platform"`
}

// CreateBuildVar creates a new build variable in the organization.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The creation request with name, platform, and optional description
//
// Returns:
//   - *CreateBuildVarResponse: The created build variable
//   - error: Any error that occurred
func (c *Client) CreateBuildVar(ctx context.Context, req *CreateBuildVarRequest) (*CreateBuildVarResponse, error) {
	// Normalize platform to match backend enum (iOS, Android)
	normalizedReq := &CreateBuildVarRequest{
		Name:        req.Name,
		Platform:    normalizePlatform(req.Platform),
		Description: req.Description,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/builds/vars", normalizedReq)
	if err != nil {
		return nil, err
	}

	var result CreateBuildVarResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// normalizePlatform converts platform strings to backend enum format.
//
// Parameters:
//   - platform: The platform string (e.g., "ios", "android", "iOS", "Android")
//
// Returns:
//   - string: The normalized platform ("iOS" or "Android")
func normalizePlatform(platform string) string {
	switch strings.ToLower(platform) {
	case "ios":
		return "iOS"
	case "android":
		return "Android"
	default:
		return platform
	}
}

// CLICreateWorkflowRequest represents a workflow creation request.
// This is a CLI-specific type for creating workflows.
// Matches backend WorkflowData schema.
type CLICreateWorkflowRequest struct {
	// Name is the workflow name.
	Name string `json:"name"`

	// Tests is an optional list of test IDs to include in the workflow.
	Tests []string `json:"tests"`

	// Schedule is the workflow schedule (defaults to "No Schedule").
	Schedule string `json:"schedule"`

	// Owner is the user ID who owns this workflow (required by backend).
	Owner string `json:"owner"`

	// OrgID is the organization ID (optional).
	OrgID string `json:"org_id,omitempty"`
}

// CLICreateWorkflowResponse represents a workflow creation response.
// This is a CLI-specific type that matches the backend CreateWorkflowResponse.
type CLICreateWorkflowResponse struct {
	// Data contains the created workflow record.
	Data struct {
		// ID is the unique identifier for the created workflow.
		ID string `json:"id"`

		// Name is the workflow name.
		Name string `json:"name"`
	} `json:"data"`
}

// GetID returns the workflow ID from the response.
func (r *CLICreateWorkflowResponse) GetID() string {
	return r.Data.ID
}

// CreateWorkflow creates a new workflow.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The creation request with name and optional test IDs
//
// Returns:
//   - *CLICreateWorkflowResponse: The creation response with workflow ID
//   - error: Any error that occurred
func (c *Client) CreateWorkflow(ctx context.Context, req *CLICreateWorkflowRequest) (*CLICreateWorkflowResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/workflows/create", req)
	if err != nil {
		return nil, err
	}

	var result CLICreateWorkflowResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}
