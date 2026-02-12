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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/revyl/cli/internal/config"
)

// defaultVersion is the package-level CLI version applied to every new Client.
// Set it once at startup via SetDefaultVersion so all API clients inherit it
// automatically without callers needing to remember SetVersion on each instance.
var defaultVersion string

// SetDefaultVersion sets the CLI version string that every newly created Client
// will use in its User-Agent header. Call this once during startup (e.g. in
// PersistentPreRun) before any API clients are created.
//
// Parameters:
//   - version: The CLI version (e.g. "1.2.3" or "dev")
func SetDefaultVersion(version string) {
	defaultVersion = version
}

const (
	// DefaultBaseURL is the default Revyl API base URL.
	DefaultBaseURL = "https://backend.revyl.ai"

	// DefaultTimeout is the default HTTP request timeout.
	DefaultTimeout = 30 * time.Second

	// UploadTimeout is the timeout for large file uploads (APKs, IPAs).
	UploadTimeout = 10 * time.Minute

	// DefaultMaxRetries is the default number of retry attempts for transient failures.
	DefaultMaxRetries = 3

	// DefaultRetryBaseDelay is the base delay for exponential backoff.
	DefaultRetryBaseDelay = 500 * time.Millisecond

	// DefaultRetryMaxDelay is the maximum delay between retries.
	DefaultRetryMaxDelay = 10 * time.Second
)

// Client is the Revyl API client.
type Client struct {
	baseURL        string
	apiKey         string
	version        string // CLI version string for User-Agent header
	httpClient     *http.Client
	uploadClient   *http.Client // Separate client with longer timeout for file uploads
	maxRetries     int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration
}

// NewClient creates a new API client using the resolved backend URL.
// Respects REVYL_BACKEND_URL environment variable for custom environments.
//
// Parameters:
//   - apiKey: The API key for authentication
//
// Returns:
//   - *Client: A new client instance
func NewClient(apiKey string) *Client {
	return &Client{
		baseURL: config.GetBackendURL(false),
		apiKey:  apiKey,
		version: defaultVersion,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		uploadClient: &http.Client{
			Timeout: UploadTimeout,
		},
		maxRetries:     DefaultMaxRetries,
		retryBaseDelay: DefaultRetryBaseDelay,
		retryMaxDelay:  DefaultRetryMaxDelay,
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
		version: defaultVersion,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		uploadClient: &http.Client{
			Timeout: UploadTimeout,
		},
		maxRetries:     DefaultMaxRetries,
		retryBaseDelay: DefaultRetryBaseDelay,
		retryMaxDelay:  DefaultRetryMaxDelay,
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
		version: defaultVersion,
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
		},
		uploadClient: &http.Client{
			Timeout: UploadTimeout,
		},
		maxRetries:     DefaultMaxRetries,
		retryBaseDelay: DefaultRetryBaseDelay,
		retryMaxDelay:  DefaultRetryMaxDelay,
	}
}

// SetVersion sets the CLI version string used in the User-Agent header.
//
// Parameters:
//   - version: The CLI version (e.g. "1.2.3" or "dev")
func (c *Client) SetVersion(version string) {
	c.version = version
}

// userAgent returns the User-Agent header value including the CLI version.
func (c *Client) userAgent() string {
	v := c.version
	if v == "" {
		v = "dev"
	}
	return "revyl-cli/" + v
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
	// Hint is an optional user-facing suggestion (e.g., "Run 'revyl auth login' to re-authenticate").
	Hint string
}

// Error returns a human-readable error message.
// If a hint is available (e.g., for expired sessions), it is appended.
//
// Returns:
//   - string: The error message, with fallback to HTTP status if no message available
func (e *APIError) Error() string {
	var base string
	if e.Message != "" && e.Detail != "" {
		base = fmt.Sprintf("%s: %s", e.Message, e.Detail)
	} else if e.Message != "" {
		base = e.Message
	} else if e.Detail != "" {
		base = e.Detail
	} else {
		base = fmt.Sprintf("HTTP %d: %s", e.StatusCode, http.StatusText(e.StatusCode))
	}

	if e.Hint != "" {
		return base + "\n" + e.Hint
	}
	return base
}

// authHintForStatus returns a user-facing hint for authentication errors.
// For 401 responses that indicate an expired or invalid token, it suggests
// re-authenticating so the user doesn't see a confusing "Invalid API key" message.
//
// Parameters:
//   - statusCode: The HTTP status code
//   - message: The parsed error message from the response
//   - detail: The parsed error detail from the response
//
// Returns:
//   - string: A hint message, or empty string if no hint is applicable
func authHintForStatus(statusCode int, message, detail string) string {
	if statusCode == 401 {
		return "Session may have expired. Run 'revyl auth login' to re-authenticate."
	}
	return ""
}

// doRequest performs an HTTP request with authentication.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	return c.doRequestWithRetry(ctx, method, path, body)
}

// isRetryableError checks if an error or status code should trigger a retry.
//
// Parameters:
//   - err: The error from the HTTP request (may be nil)
//   - statusCode: The HTTP status code (0 if request failed)
//
// Returns:
//   - bool: True if the request should be retried
func isRetryableError(err error, statusCode int) bool {
	// Retry on network errors
	if err != nil {
		return true
	}

	// Retry on server errors (5xx) and rate limiting (429)
	if statusCode >= 500 || statusCode == 429 {
		return true
	}

	return false
}

// calculateBackoff calculates the delay for the next retry attempt using exponential backoff.
//
// Parameters:
//   - attempt: The current attempt number (0-indexed)
//   - baseDelay: The base delay duration
//   - maxDelay: The maximum delay duration
//
// Returns:
//   - time.Duration: The delay before the next retry
func calculateBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	// Exponential backoff: baseDelay * 2^attempt
	delay := baseDelay * time.Duration(1<<uint(attempt))
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

// doRequestWithRetry performs an HTTP request with retry logic for transient failures.
//
// Parameters:
//   - ctx: Context for cancellation
//   - method: HTTP method (GET, POST, etc.)
//   - path: API path (appended to base URL)
//   - body: Request body (will be JSON marshaled)
//
// Returns:
//   - *http.Response: The HTTP response
//   - error: Any error that occurred
func (c *Client) doRequestWithRetry(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// Check context cancellation before each attempt
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// Wait before retry (skip on first attempt)
		if attempt > 0 {
			delay := calculateBackoff(attempt-1, c.retryBaseDelay, c.retryMaxDelay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		// Build the request
		reqURL := c.baseURL + path

		var bodyReader io.Reader
		if body != nil {
			jsonBody, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(jsonBody)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", c.userAgent())

		// Set source tracking header
		// X-Revyl-Client identifies the client type (cli maps to "api" source in DB)
		req.Header.Set("X-Revyl-Client", "cli")

		// Execute the request
		resp, err := c.httpClient.Do(req)

		// Check if we should retry
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}

		if !isRetryableError(err, statusCode) || attempt == c.maxRetries {
			// Return the response (success or non-retryable error)
			if err != nil {
				return nil, fmt.Errorf("request failed: %w", err)
			}
			return resp, nil
		}

		// Close the response body before retrying to avoid resource leaks
		if resp != nil {
			resp.Body.Close()
		}

		lastErr = err
		lastResp = resp
	}

	// All retries exhausted
	if lastErr != nil {
		return nil, fmt.Errorf("request failed after %d retries: %w", c.maxRetries, lastErr)
	}
	return lastResp, nil
}

// parseResponse parses the response body into the target struct.
func parseResponse(resp *http.Response, target interface{}) error {
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return &APIError{
				StatusCode: resp.StatusCode,
				Message:    "failed to read error response body",
				Detail:     readErr.Error(),
			}
		}

		// Try to parse structured error response
		// Supports multiple common error field names
		var errResp struct {
			Error   string `json:"error"`
			Detail  string `json:"detail"`
			Message string `json:"message"`
		}
		// Ignore JSON parse errors - we'll fall back to raw body
		_ = json.Unmarshal(body, &errResp)

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
			Hint:       authHintForStatus(resp.StatusCode, message, detail),
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
	// LaunchURL is the deep link URL for hot reload mode.
	// When provided, the test will launch the app via this URL instead of the normal app launch.
	LaunchURL string `json:"launch_url,omitempty"`
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
	AppID        string                 `json:"build_var_id"`
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
		req.AppID,
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

	uploadResp, err := c.uploadClient.Do(uploadReq)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode >= 400 {
		body, readErr := io.ReadAll(uploadResp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("upload failed with status %d (failed to read response: %v)", uploadResp.StatusCode, readErr)
		}
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

	// Backend complete-upload doesn't return version_id; use the presign value.
	if completeResult.VersionID == "" {
		completeResult.VersionID = presignResult.VersionID
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

// ListBuildVersions lists build versions for an app.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The app ID
//
// Returns:
//   - []BuildVersion: List of build versions
//   - error: Any error that occurred
func (c *Client) ListBuildVersions(ctx context.Context, appID string) ([]BuildVersion, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/builds/vars/%s/versions", appID), nil)
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
	AppID          string                 `json:"app_id,omitempty"`
	PinnedVersion  string                 `json:"pinned_version,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// UpdateTestRequest represents a test update request.
type UpdateTestRequest struct {
	TestID          string      `json:"-"`
	Tasks           interface{} `json:"tasks,omitempty"`
	AppID           string      `json:"app_id,omitempty"`
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
	Name     string      `json:"name"`
	Platform string      `json:"platform"`
	Tasks    interface{} `json:"tasks"`
	AppID    string      `json:"app_id,omitempty"`
	OrgID    string      `json:"org_id,omitempty"`
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

// TestWithTags represents a test with its associated tags.
// Used by the full get_tests endpoint which includes tag data.
type TestWithTags struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	Tags     []TestTag `json:"tags"`
}

// TestTag represents a tag on a test (subset of CLITagResponse).
type TestTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// CLITestListWithTagsResponse represents the response from the full test list endpoint.
type CLITestListWithTagsResponse struct {
	Tests []TestWithTags `json:"tests"`
	Count int            `json:"count"`
}

// ListOrgTestsWithTags fetches all tests with their tags.
// Uses the full get_tests endpoint which includes tag data per test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - limit: Maximum number of tests to return (default: 150)
//   - offset: Number of tests to skip for pagination (default: 0)
//
// Returns:
//   - *CLITestListWithTagsResponse: List of tests with tags
//   - error: Any error that occurred
func (c *Client) ListOrgTestsWithTags(ctx context.Context, limit, offset int) (*CLITestListWithTagsResponse, error) {
	if limit <= 0 {
		limit = 150
	}
	if offset < 0 {
		offset = 0
	}

	path := fmt.Sprintf("/api/v1/tests/get_tests?limit=%d&offset=%d", limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLITestListWithTagsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// App represents an app in the organization.
// This is a CLI-specific type that's simpler than the generated API response types.
type App struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Platform       string `json:"platform"`
	Description    string `json:"description,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
	LatestVersion  string `json:"latest_version,omitempty"`
	VersionsCount  int    `json:"versions_count"`
}

// CLIPaginatedAppsResponse represents a paginated list of apps.
// This is a CLI-specific type that uses App instead of the generated type.
type CLIPaginatedAppsResponse struct {
	Items       []App `json:"items"`
	Total       int   `json:"total"`
	Page        int   `json:"page"`
	PageSize    int   `json:"page_size"`
	TotalPages  int   `json:"total_pages"`
	HasNext     bool  `json:"has_next"`
	HasPrevious bool  `json:"has_previous"`
}

// ListApps fetches all apps for the authenticated user's organization.
//
// Parameters:
//   - ctx: Context for cancellation
//   - platform: Optional platform filter (android, ios, or empty for all)
//   - page: Page number (1-indexed, default: 1)
//   - pageSize: Number of items per page (default: 50, max: 100)
//
// Returns:
//   - *CLIPaginatedAppsResponse: Paginated list of apps
//   - error: Any error that occurred
func (c *Client) ListApps(ctx context.Context, platform string, page, pageSize int) (*CLIPaginatedAppsResponse, error) {
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
		path += "&platform=" + normalizePlatform(platform)
	}

	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIPaginatedAppsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetApp retrieves an app by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The app ID
//
// Returns:
//   - *App: The app data
//   - error: Any error that occurred
func (c *Client) GetApp(ctx context.Context, appID string) (*App, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/builds/vars/%s", appID), nil)
	if err != nil {
		return nil, err
	}

	var result App
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateAppRequest represents a request to create a new app.
type CreateAppRequest struct {
	// Name is the display name for the app.
	Name string `json:"name"`

	// Platform is the target platform (ios or android).
	Platform string `json:"platform"`

	// Description is an optional description.
	Description string `json:"description,omitempty"`
}

// CreateAppResponse represents the response from creating an app.
type CreateAppResponse struct {
	// ID is the unique identifier for the created app.
	ID string `json:"id"`

	// Name is the display name.
	Name string `json:"name"`

	// Platform is the target platform.
	Platform string `json:"platform"`
}

// CreateApp creates a new app in the organization.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The creation request with name, platform, and optional description
//
// Returns:
//   - *CreateAppResponse: The created app
//   - error: Any error that occurred
func (c *Client) CreateApp(ctx context.Context, req *CreateAppRequest) (*CreateAppResponse, error) {
	// Normalize platform to match backend enum (iOS, Android)
	normalizedReq := &CreateAppRequest{
		Name:        req.Name,
		Platform:    normalizePlatform(req.Platform),
		Description: req.Description,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/builds/vars", normalizedReq)
	if err != nil {
		return nil, err
	}

	var result CreateAppResponse
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

// CLITestStatusResponse represents the status of a test execution.
// This is a CLI-specific type that matches the backend TestStatusResponse schema.
type CLITestStatusResponse struct {
	// ID is the execution task ID.
	ID string `json:"id"`

	// TestID is the test definition ID.
	TestID string `json:"test_id"`

	// Status is the current execution status (queued, running, completed, failed, etc.).
	Status string `json:"status"`

	// Progress is the completion percentage (0-100).
	Progress float64 `json:"progress"`

	// CurrentStep is the description of the current step being executed.
	CurrentStep string `json:"current_step,omitempty"`

	// CurrentStepIndex is the 0-based index of the current step.
	CurrentStepIndex int `json:"current_step_index"`

	// TotalSteps is the total number of steps in the test.
	TotalSteps int `json:"total_steps"`

	// StepsCompleted is the number of steps that have been completed.
	StepsCompleted int `json:"steps_completed"`

	// ErrorMessage contains the error message if the test failed.
	ErrorMessage string `json:"error_message,omitempty"`

	// Success indicates whether the test passed (nil if not yet complete).
	Success *bool `json:"success,omitempty"`

	// WorkflowRunID is the parent workflow run ID if this test is part of a workflow.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	// StartedAt is when the test execution started.
	StartedAt string `json:"started_at,omitempty"`

	// CompletedAt is when the test execution completed.
	CompletedAt string `json:"completed_at,omitempty"`

	// ExecutionTimeSeconds is the total execution time in seconds.
	ExecutionTimeSeconds float64 `json:"execution_time_seconds,omitempty"`
}

// Workflow represents a workflow definition.
type Workflow struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Tests    []string `json:"tests,omitempty"`
	Schedule string   `json:"schedule,omitempty"`
}

// SimpleWorkflow represents a minimal workflow definition for listing.
type SimpleWorkflow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CLIWorkflowListResponse represents the response from the list workflows endpoint.
type CLIWorkflowListResponse struct {
	Workflows []SimpleWorkflow `json:"data"`
	Count     int              `json:"count"`
}

// ListWorkflows retrieves all workflows for the current organization.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *CLIWorkflowListResponse: The list of workflows
//   - error: Any error that occurred
func (c *Client) ListWorkflows(ctx context.Context) (*CLIWorkflowListResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/workflows/get_with_last_status?limit=200&offset=0", nil)
	if err != nil {
		return nil, err
	}

	var result CLIWorkflowListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetWorkflow retrieves a workflow by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowID: The workflow ID
//
// Returns:
//   - *Workflow: The workflow data
//   - error: Any error that occurred
func (c *Client) GetWorkflow(ctx context.Context, workflowID string) (*Workflow, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/workflows/get_workflow_by_id/%s", workflowID), nil)
	if err != nil {
		return nil, err
	}

	var result Workflow
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetTestStatus retrieves the current status of a test execution.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The execution task ID
//
// Returns:
//   - *CLITestStatusResponse: The current test status
//   - error: Any error that occurred
func (c *Client) GetTestStatus(ctx context.Context, taskID string) (*CLITestStatusResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/tests/get_test_execution_task?task_id=%s", taskID), nil)
	if err != nil {
		return nil, err
	}

	var result CLITestStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CancelTest cancels a running test execution.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The execution task ID to cancel
//
// Returns:
//   - *CancelTestResponse: The cancellation response
//   - error: Any error that occurred
func (c *Client) CancelTest(ctx context.Context, taskID string) (*CancelTestResponse, error) {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/execution/tests/status/cancel/%s", taskID), nil)
	if err != nil {
		return nil, err
	}

	var result CancelTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CancelWorkflow cancels a running workflow execution.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The workflow task ID to cancel
//
// Returns:
//   - *WorkflowCancelResponse: The cancellation response
//   - error: Any error that occurred
func (c *Client) CancelWorkflow(ctx context.Context, taskID string) (*WorkflowCancelResponse, error) {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/workflows/status/cancel/%s", taskID), nil)
	if err != nil {
		return nil, err
	}

	var result WorkflowCancelResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetCloudflareCredentials fetches scoped tunnel credentials from the backend.
//
// Security notes:
//   - Requires valid Revyl API key (user must be authenticated)
//   - Credentials are scoped to tunnel operations only
//   - Credentials expire after 1 hour
//   - Credentials are NOT cached locally
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *CloudflareCredentials: Scoped credentials for tunnel creation
//   - error: Any error that occurred
func (c *Client) GetCloudflareCredentials(ctx context.Context) (*CloudflareCredentials, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/tunnels/credentials", nil)
	if err != nil {
		return nil, err
	}

	var result CloudflareCredentials
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// FindDevClientBuilds searches for development client builds in the organization.
// Looks for apps with names containing "dev" or "development".
//
// Parameters:
//   - ctx: Context for cancellation
//   - platform: Platform filter ("ios", "android", or empty for all)
//
// Returns:
//   - []App: List of matching apps
//   - error: Any error that occurred
func (c *Client) FindDevClientBuilds(ctx context.Context, platform string) ([]App, error) {
	resp, err := c.ListApps(ctx, platform, 1, 100)
	if err != nil {
		return nil, err
	}

	var devBuilds []App
	for _, app := range resp.Items {
		nameLower := strings.ToLower(app.Name)
		if strings.Contains(nameLower, "dev") ||
			strings.Contains(nameLower, "development") {
			devBuilds = append(devBuilds, app)
		}
	}

	return devBuilds, nil
}

// GetLatestBuildVersion retrieves the latest version for an app.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The app ID
//
// Returns:
//   - *BuildVersion: The latest build version, or nil if none exist
//   - error: Any error that occurred
func (c *Client) GetLatestBuildVersion(ctx context.Context, appID string) (*BuildVersion, error) {
	versions, err := c.ListBuildVersions(ctx, appID)
	if err != nil {
		return nil, err
	}

	if len(versions) == 0 {
		return nil, nil
	}

	// Return the first version (API returns sorted by most recent)
	return &versions[0], nil
}

// StartDeviceRequest represents a request to start a device session.
// Used for interactive test creation mode.
type StartDeviceRequest struct {
	// Platform is the target platform (ios or android).
	Platform string `json:"platform"`

	// TestID is the test ID to associate with this device session.
	// Required unless IsSimulation is true.
	TestID string `json:"test_id,omitempty"`

	// AppPackage is the bundle ID / package name of the app.
	AppPackage string `json:"app_package,omitempty"`

	// IsSimulation enables simulation mode (streaming without test execution).
	IsSimulation bool `json:"is_simulation,omitempty"`

	// RunConfig contains optional execution configuration.
	RunConfig *TestRunConfig `json:"run_config,omitempty"`
}

// TestRunConfig contains optional execution configuration.
type TestRunConfig struct {
	// MaxRetries is the maximum number of retries for failed steps.
	MaxRetries int `json:"max_retries,omitempty"`

	// TimeoutSeconds is the maximum execution time in seconds.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// StartDevice starts a device session for interactive test creation.
// Returns the generated StartDeviceResponse type.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The device start request
//
// Returns:
//   - *StartDeviceResponse: The device start response with workflow run ID
//   - error: Any error that occurred
func (c *Client) StartDevice(ctx context.Context, req *StartDeviceRequest) (*StartDeviceResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/execution/start_device", req)
	if err != nil {
		return nil, err
	}

	var result StartDeviceResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetWorkerWSURL retrieves the worker WebSocket URL for a workflow run.
// The URL may not be immediately available after starting a device.
// Poll this endpoint until status is "ready".
// Returns the generated WorkerConnectionResponse type.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowRunID: The workflow run ID from StartDevice
//
// Returns:
//   - *WorkerConnectionResponse: The worker connection info
//   - error: Any error that occurred
func (c *Client) GetWorkerWSURL(ctx context.Context, workflowRunID string) (*WorkerConnectionResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/execution/streaming/worker-connection/%s", workflowRunID), nil)
	if err != nil {
		return nil, err
	}

	var result WorkerConnectionResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CancelDeviceResponse represents the response from cancelling a device session.
type CancelDeviceResponse struct {
	// Success indicates whether the cancellation was successful.
	Success bool `json:"success"`

	// Message contains additional information about the cancellation.
	Message string `json:"message,omitempty"`

	// WorkflowRunID is the workflow run that was cancelled.
	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	// DBUpdated indicates whether the database was updated.
	DBUpdated bool `json:"db_updated,omitempty"`
}

// CancelDevice cancels a running device session.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowRunID: The workflow run ID to cancel
//
// Returns:
//   - *CancelDeviceResponse: The cancellation response
//   - error: Any error that occurred
func (c *Client) CancelDevice(ctx context.Context, workflowRunID string) (*CancelDeviceResponse, error) {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/execution/device/status/cancel/%s", workflowRunID), nil)
	if err != nil {
		return nil, err
	}

	var result CancelDeviceResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteTestResponse represents the response from deleting a test.
type DeleteTestResponse struct {
	// ID is the ID of the deleted test.
	ID string `json:"id"`

	// Message is a success message.
	Message string `json:"message"`
}

// DeleteTest deletes a test by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test ID to delete
//
// Returns:
//   - *DeleteTestResponse: The deletion response
//   - error: Any error that occurred (404 if not found, 403 if not authorized)
func (c *Client) DeleteTest(ctx context.Context, testID string) (*DeleteTestResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/tests/delete/%s", testID), nil)
	if err != nil {
		return nil, err
	}

	var result DeleteTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CLIDeleteWorkflowResponse represents the response from deleting a workflow.
// This is a CLI-specific type that simplifies the generated DeleteWorkflowResponse.
type CLIDeleteWorkflowResponse struct {
	// ID is the ID of the deleted workflow.
	ID string `json:"id"`

	// Message is a success message.
	Message string `json:"message"`
}

// DeleteWorkflow deletes a workflow by ID (soft delete).
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowID: The workflow ID to delete
//
// Returns:
//   - *CLIDeleteWorkflowResponse: The deletion response
//   - error: Any error that occurred (404 if not found, 403 if not authorized)
func (c *Client) DeleteWorkflow(ctx context.Context, workflowID string) (*CLIDeleteWorkflowResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/workflows/delete/%s", workflowID), nil)
	if err != nil {
		return nil, err
	}

	// Parse the full response first
	var fullResult DeleteWorkflowResponse
	if err := parseResponse(resp, &fullResult); err != nil {
		return nil, err
	}

	// Convert to CLI-friendly response
	return &CLIDeleteWorkflowResponse{
		ID:      fullResult.Data.Id,
		Message: fullResult.Message,
	}, nil
}

// CLIDeleteAppResponse represents the response from deleting an app.
type CLIDeleteAppResponse struct {
	// Message is a success message.
	Message string `json:"message"`

	// DetachedTests is the number of tests that were detached from this app.
	DetachedTests int `json:"detached_tests,omitempty"`
}

// DeleteApp deletes an app and all its versions.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The app ID to delete
//
// Returns:
//   - *CLIDeleteAppResponse: The deletion response
//   - error: Any error that occurred (404 if not found, 403 if not authorized)
func (c *Client) DeleteApp(ctx context.Context, appID string) (*CLIDeleteAppResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/builds/vars/%s", appID), nil)
	if err != nil {
		return nil, err
	}

	var result CLIDeleteAppResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteBuildVersionResponse represents the response from deleting a build version.
type DeleteBuildVersionResponse struct {
	// Message is a success message.
	Message string `json:"message"`
}

// --- Module API methods ---

// CLIModuleResponse represents a module for CLI display.
type CLIModuleResponse struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Blocks      []interface{} `json:"blocks"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
	OrgID       string        `json:"org_id"`
}

// CLIModulesListResponse represents the response from listing modules.
type CLIModulesListResponse struct {
	Message string              `json:"message"`
	Result  []CLIModuleResponse `json:"result"`
}

// CLIModuleSingleResponse represents the response from a single module operation.
type CLIModuleSingleResponse struct {
	Message string            `json:"message"`
	Result  CLIModuleResponse `json:"result"`
}

// CLICreateModuleRequest represents a module creation request.
type CLICreateModuleRequest struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Blocks      []interface{} `json:"blocks"`
}

// CLIUpdateModuleRequest represents a module update request.
type CLIUpdateModuleRequest struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	Blocks      *[]interface{} `json:"blocks,omitempty"`
}

// CLIDeleteModuleResponse represents the response from deleting a module.
type CLIDeleteModuleResponse struct {
	Message string `json:"message"`
}

// ListModules fetches all modules for the authenticated user's organization.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *CLIModulesListResponse: List of modules
//   - error: Any error that occurred
func (c *Client) ListModules(ctx context.Context) (*CLIModulesListResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/modules/list", nil)
	if err != nil {
		return nil, err
	}

	var result CLIModulesListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetModule retrieves a module by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - moduleID: The module UUID
//
// Returns:
//   - *CLIModuleSingleResponse: The module data
//   - error: Any error that occurred
func (c *Client) GetModule(ctx context.Context, moduleID string) (*CLIModuleSingleResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/modules/%s", moduleID), nil)
	if err != nil {
		return nil, err
	}

	var result CLIModuleSingleResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateModule creates a new module.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The creation request
//
// Returns:
//   - *CLIModuleSingleResponse: The created module
//   - error: Any error that occurred
func (c *Client) CreateModule(ctx context.Context, req *CLICreateModuleRequest) (*CLIModuleSingleResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/modules/create", req)
	if err != nil {
		return nil, err
	}

	var result CLIModuleSingleResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateModule updates an existing module.
//
// Parameters:
//   - ctx: Context for cancellation
//   - moduleID: The module UUID
//   - req: The update request
//
// Returns:
//   - *CLIModuleSingleResponse: The updated module
//   - error: Any error that occurred
func (c *Client) UpdateModule(ctx context.Context, moduleID string, req *CLIUpdateModuleRequest) (*CLIModuleSingleResponse, error) {
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/modules/update/%s", moduleID), req)
	if err != nil {
		return nil, err
	}

	var result CLIModuleSingleResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteModule deletes a module by ID.
//
// Parameters:
//   - ctx: Context for cancellation
//   - moduleID: The module UUID
//
// Returns:
//   - *CLIDeleteModuleResponse: The deletion response
//   - error: Any error that occurred (409 if module is in use)
func (c *Client) DeleteModule(ctx context.Context, moduleID string) (*CLIDeleteModuleResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/modules/delete/%s", moduleID), nil)
	if err != nil {
		return nil, err
	}

	var result CLIDeleteModuleResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// --- Tag API methods ---

// CLITagResponse represents a tag for CLI display.
type CLITagResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	TestCount   int    `json:"test_count,omitempty"`
}

// CLITagListResponse represents the response from listing tags.
type CLITagListResponse struct {
	Tags []CLITagResponse `json:"tags"`
}

// CLICreateTagRequest represents a tag creation request.
type CLICreateTagRequest struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// CLIUpdateTagRequest represents a tag update request.
type CLIUpdateTagRequest struct {
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
}

// CLISyncTagsRequest represents a request to sync (replace) tags on a test.
type CLISyncTagsRequest struct {
	TagNames []string `json:"tag_names"`
}

// CLISyncTagsResponse represents the response from syncing tags on a test.
type CLISyncTagsResponse struct {
	TestID string             `json:"test_id"`
	Tags   []CLITagSyncResult `json:"tags"`
}

// CLITagSyncResult represents the result of syncing a single tag.
type CLITagSyncResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color,omitempty"`
	Created bool   `json:"created"`
}

// CLIBulkSyncTagsRequest represents a request to add/remove tags on multiple tests.
type CLIBulkSyncTagsRequest struct {
	TestIDs      []string `json:"test_ids"`
	TagsToAdd    []string `json:"tags_to_add,omitempty"`
	TagsToRemove []string `json:"tags_to_remove,omitempty"`
}

// CLIBulkSyncTagsResponse represents the response from bulk syncing tags.
type CLIBulkSyncTagsResponse struct {
	Results      []CLIBulkSyncResult `json:"results"`
	SuccessCount int                 `json:"success_count"`
	ErrorCount   int                 `json:"error_count"`
}

// CLIBulkSyncResult represents the result for a single test in a bulk sync.
type CLIBulkSyncResult struct {
	TestID  string  `json:"test_id"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

// CLIDeleteTagResponse represents the response from deleting a tag.
type CLIDeleteTagResponse struct {
	Deleted bool   `json:"deleted"`
	TagID   string `json:"tag_id"`
}

// ListTags fetches all tags for the authenticated user's organization.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *CLITagListResponse: List of tags with test counts
//   - error: Any error that occurred
func (c *Client) ListTags(ctx context.Context) (*CLITagListResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/tests/tags", nil)
	if err != nil {
		return nil, err
	}

	var result CLITagListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateTag creates a new tag (upserts if name exists).
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The creation request
//
// Returns:
//   - *CLITagResponse: The created tag
//   - error: Any error that occurred
func (c *Client) CreateTag(ctx context.Context, req *CLICreateTagRequest) (*CLITagResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/tags", req)
	if err != nil {
		return nil, err
	}

	var result CLITagResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateTag updates an existing tag.
//
// Parameters:
//   - ctx: Context for cancellation
//   - tagID: The tag UUID
//   - req: The update request
//
// Returns:
//   - *CLITagResponse: The updated tag
//   - error: Any error that occurred
func (c *Client) UpdateTag(ctx context.Context, tagID string, req *CLIUpdateTagRequest) (*CLITagResponse, error) {
	resp, err := c.doRequest(ctx, "PATCH",
		fmt.Sprintf("/api/v1/tests/tags/%s", tagID), req)
	if err != nil {
		return nil, err
	}

	var result CLITagResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteTag deletes a tag by ID (cascades from all tests).
//
// Parameters:
//   - ctx: Context for cancellation
//   - tagID: The tag UUID
//
// Returns:
//   - error: Any error that occurred
func (c *Client) DeleteTag(ctx context.Context, tagID string) error {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/tests/tags/%s", tagID), nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// GetTestTags retrieves tags for a specific test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//
// Returns:
//   - []CLITagResponse: List of tags on the test
//   - error: Any error that occurred
func (c *Client) GetTestTags(ctx context.Context, testID string) ([]CLITagResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/tests/tags/tests/%s", testID), nil)
	if err != nil {
		return nil, err
	}

	var result []CLITagResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// SyncTestTags replaces all tags on a test with the given tag names.
// Tags are auto-created if they don't exist.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - req: The sync request with tag names
//
// Returns:
//   - *CLISyncTagsResponse: The sync result
//   - error: Any error that occurred
func (c *Client) SyncTestTags(ctx context.Context, testID string, req *CLISyncTagsRequest) (*CLISyncTagsResponse, error) {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/tests/tags/tests/%s/sync", testID), req)
	if err != nil {
		return nil, err
	}

	var result CLISyncTagsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// BulkSyncTestTags adds/removes tags on multiple tests.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The bulk sync request
//
// Returns:
//   - *CLIBulkSyncTagsResponse: The bulk sync result
//   - error: Any error that occurred
func (c *Client) BulkSyncTestTags(ctx context.Context, req *CLIBulkSyncTagsRequest) (*CLIBulkSyncTagsResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/tags/tests/bulk-sync", req)
	if err != nil {
		return nil, err
	}

	var result CLIBulkSyncTagsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteBuildVersion deletes a specific build version.
//
// Parameters:
//   - ctx: Context for cancellation
//   - versionID: The build version ID to delete
//
// Returns:
//   - *DeleteBuildVersionResponse: The deletion response
//   - error: Any error that occurred (404 if not found, 403 if not authorized)
func (c *Client) DeleteBuildVersion(ctx context.Context, versionID string) (*DeleteBuildVersionResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/builds/versions/%s", versionID), nil)
	if err != nil {
		return nil, err
	}

	var result DeleteBuildVersionResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}
