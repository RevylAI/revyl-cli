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
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// DetailObject preserves structured `detail` payloads when the backend sends
	// machine-readable context, such as reports-v3 lookup hints.
	DetailObject map[string]interface{}
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

// DetailString returns a string field from a structured detail payload.
func (e *APIError) DetailString(key string) string {
	if e == nil || e.DetailObject == nil {
		return ""
	}
	value, ok := e.DetailObject[key]
	if !ok {
		return ""
	}
	str, _ := value.(string)
	return str
}

// DetailBool returns a boolean field from a structured detail payload.
func (e *APIError) DetailBool(key string) bool {
	if e == nil || e.DetailObject == nil {
		return false
	}
	value, ok := e.DetailObject[key]
	if !ok {
		return false
	}
	flag, _ := value.(bool)
	return flag
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
	if statusCode == 402 {
		return "No active billing plan. Add a payment method to unlock 30 free simulator minutes:\n  → revyl auth billing"
	}
	return ""
}

// extractHTMLError attempts to extract the meaningful error from an HTML debug page.
// Backend debug pages (e.g. Starlette/FastAPI) embed the exception in the page.
func extractHTMLError(html string) string {
	// Look for common patterns in Python traceback HTML pages
	// Starlette wraps the traceback in <pre> or <code> tags
	for _, marker := range []string{"<pre>", "<code>"} {
		idx := strings.LastIndex(html, marker)
		if idx >= 0 {
			end := marker[:1] + "/" + marker[1:]
			endIdx := strings.Index(html[idx:], end)
			if endIdx > 0 {
				content := html[idx+len(marker) : idx+endIdx]
				// Strip HTML tags from the extracted content
				content = stripHTMLTags(content)
				content = strings.TrimSpace(content)
				if len(content) > 500 {
					content = content[len(content)-500:]
				}
				if content != "" {
					return content
				}
			}
		}
	}
	// Fallback: strip all HTML and return a truncated version
	plain := stripHTMLTags(html)
	plain = strings.TrimSpace(plain)
	if len(plain) > 500 {
		plain = plain[:500] + "..."
	}
	if plain != "" {
		return plain
	}
	return "Server returned an HTML error page (check backend logs for details)"
}

func parseAPIErrorBody(statusCode int, body []byte) *APIError {
	var errResp struct {
		Error   string          `json:"error"`
		Detail  json.RawMessage `json:"detail"`
		Message string          `json:"message"`
	}
	_ = json.Unmarshal(body, &errResp)

	message := errResp.Error
	if message == "" {
		message = errResp.Message
	}

	var (
		detail       string
		detailObject map[string]interface{}
	)
	if len(errResp.Detail) > 0 && string(errResp.Detail) != "null" {
		var detailValue interface{}
		if err := json.Unmarshal(errResp.Detail, &detailValue); err == nil {
			switch value := detailValue.(type) {
			case string:
				detail = value
			case map[string]interface{}:
				detailObject = value
				if detailMessage, ok := value["message"].(string); ok {
					detail = detailMessage
				} else if rawDetail, marshalErr := json.Marshal(value); marshalErr == nil {
					detail = string(rawDetail)
				}
			default:
				if rawDetail, marshalErr := json.Marshal(value); marshalErr == nil {
					detail = string(rawDetail)
				}
			}
		}
	}

	if message == "" && detail == "" {
		bodyStr := string(body)
		if strings.Contains(bodyStr, "<html") || strings.Contains(bodyStr, "<!DOCTYPE") {
			detail = extractHTMLError(bodyStr)
		} else {
			if len(bodyStr) > 500 {
				bodyStr = bodyStr[:500] + "..."
			}
			if bodyStr != "" {
				detail = bodyStr
			}
		}
	}

	return &APIError{
		StatusCode:   statusCode,
		Message:      message,
		Detail:       detail,
		DetailObject: detailObject,
		Hint:         authHintForStatus(statusCode, message, detail),
	}
}

// stripHTMLTags removes HTML tags from a string.
func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// doRequest performs an HTTP request with authentication and retry logic.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	return c.doRequestWithRetry(ctx, method, path, body)
}

// doRequestOnce performs a single HTTP request without retries.
// Use this for endpoints where retrying is unlikely to help (e.g. deterministic failures).
func (c *Client) doRequestOnce(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
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
	req.Header.Set("X-Revyl-Client", "cli")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
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
	attempts := c.maxRetries + 1
	if attempts < 1 {
		// Defensive: invalid negative retry configuration should still execute once.
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
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
		// X-Revyl-Client identifies the client type for backend source classification.
		req.Header.Set("X-Revyl-Client", "cli")

		// Execute the request
		resp, err := c.httpClient.Do(req)

		// Check if we should retry
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}

		if !isRetryableError(err, statusCode) || attempt == attempts-1 {
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
	}

	// Defensive fallback; loop should always return on the final attempt.
	return nil, fmt.Errorf("request failed: retry loop ended unexpectedly")
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

		return parseAPIErrorBody(resp.StatusCode, body)
	}

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// parseResponseWithRaw parses the response body into target and returns the raw JSON bytes.
func parseResponseWithRaw(resp *http.Response, target interface{}) (json.RawMessage, error) {
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, &APIError{
				StatusCode: resp.StatusCode,
				Message:    "failed to read error response body",
				Detail:     readErr.Error(),
			}
		}

		return nil, parseAPIErrorBody(resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if target != nil {
		if err := json.Unmarshal(body, target); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return body, nil
}

// CLIRunConfig contains optional runtime configuration for test execution.
type CLIRunConfig struct {
	ExecutionMode *CLIExecutionMode `json:"execution_mode,omitempty"`
}

// CLIExecutionMode contains execution mode settings.
type CLIExecutionMode struct {
	InitialLocation    *CLILocation `json:"initial_location,omitempty"`
	InitialOrientation string       `json:"initial_orientation,omitempty"`
}

// CLILocation represents a GPS coordinate.
type CLILocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// ExecuteTestRequest represents a test execution request.
// Source tracking is handled via HTTP headers (X-Revyl-Client, X-CI-System).
type ExecuteTestRequest struct {
	TestID         string        `json:"test_id"`
	Retries        int           `json:"retries,omitempty"`
	BuildVersionID string        `json:"build_version_id,omitempty"`
	RunConfig      *CLIRunConfig `json:"run_config,omitempty"`
	// LaunchURL is the deep link URL for hot reload mode.
	// When provided, the test will launch the app via this URL instead of the normal app launch.
	LaunchURL string `json:"launch_url,omitempty"`
	// DeviceModel overrides the target device model (e.g. "iPhone 16", "Pixel 7").
	DeviceModel string `json:"device_model,omitempty"`
	// OsVersion overrides the target OS runtime (e.g. "iOS 18.5", "Android 14").
	OsVersion string `json:"os_version,omitempty"`
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
// BillingPlanResponse contains the org's current billing plan info.
type BillingPlanResponse struct {
	Plan            string  `json:"plan"`
	DisplayName     string  `json:"display_name"`
	MonthlyBase     float64 `json:"monthly_base"`
	FreeCreditLabel string  `json:"free_credit_label"`
	BillingExempt   bool    `json:"billing_exempt"`
}

// GetBillingPlan returns the org's current billing plan.
func (c *Client) GetBillingPlan(ctx context.Context) (*BillingPlanResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/execution/billing/plan", nil)
	if err != nil {
		return nil, err
	}

	var result BillingPlanResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

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
// BuildConfig and OverrideBuildConfig use the generated WorkflowAppConfig and
// PlatformApp types from generated.go.
type ExecuteWorkflowRequest struct {
	WorkflowID          string             `json:"workflow_id"`
	Retries             int                `json:"retries,omitempty"`
	BuildConfig         *WorkflowAppConfig `json:"build_config,omitempty"`
	OverrideBuildConfig bool               `json:"override_build_config,omitempty"`
	LocationConfig      *CLILocation       `json:"location_config,omitempty"`
	OverrideLocation    bool               `json:"override_location,omitempty"`
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

const maxUploadErrorBodyBytes = 16 * 1024

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
	fileInfo, err := os.Stat(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if err := c.uploadFileWithRetry(
		ctx,
		presignResult.UploadURL,
		presignResult.ContentType,
		req.FilePath,
		fileInfo.Size(),
	); err != nil {
		return nil, err
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

func isRetryableUploadStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func formatUploadStatusError(statusCode int, body []byte, readErr error) error {
	if readErr != nil {
		return fmt.Errorf("upload failed with status %d (failed to read response: %v)", statusCode, readErr)
	}

	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return fmt.Errorf("upload failed with status %d", statusCode)
	}
	return fmt.Errorf("upload failed with status %d: %s", statusCode, msg)
}

func (c *Client) uploadFileWithRetry(ctx context.Context, uploadURL, contentType, filePath string, fileSize int64) error {
	attempts := c.maxRetries + 1
	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		// Check context cancellation before each attempt.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Wait before retry (skip on first attempt).
		if attempt > 0 {
			delay := calculateBackoff(attempt-1, c.retryBaseDelay, c.retryMaxDelay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}

		uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, file)
		if err != nil {
			file.Close()
			return fmt.Errorf("failed to create upload request: %w", err)
		}
		uploadReq.Header.Set("Content-Type", contentType)
		uploadReq.ContentLength = fileSize

		uploadResp, err := c.uploadClient.Do(uploadReq)
		file.Close()
		if err != nil {
			lastErr = fmt.Errorf("upload failed: %w", err)
			if attempt == attempts-1 {
				return fmt.Errorf("upload failed after %d attempts: %w", attempts, lastErr)
			}
			continue
		}

		if uploadResp.StatusCode < 400 {
			uploadResp.Body.Close()
			return nil
		}

		body, readErr := io.ReadAll(io.LimitReader(uploadResp.Body, maxUploadErrorBodyBytes))
		uploadResp.Body.Close()
		statusErr := formatUploadStatusError(uploadResp.StatusCode, body, readErr)
		if !isRetryableUploadStatus(uploadResp.StatusCode) {
			return statusErr
		}

		lastErr = statusErr
		if attempt == attempts-1 {
			return fmt.Errorf("upload failed after %d attempts: %w", attempts, lastErr)
		}
	}

	if lastErr != nil {
		return fmt.Errorf("upload failed after %d attempts: %w", attempts, lastErr)
	}
	return fmt.Errorf("upload failed")
}

// BuildVersion represents a build version.
// Matches backend BuildVersionResponse schema.
type BuildVersion struct {
	ID          string                 `json:"id"`
	Version     string                 `json:"version"`
	UploadedAt  string                 `json:"uploaded_at"`
	PackageName string                 `json:"package_name,omitempty"`
	PackageID   string                 `json:"package_id,omitempty"`
	IsCurrent   bool                   `json:"is_current,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// BuildVersionsPage represents a paginated build versions response.
// Matches backend PaginatedBuildVersionsResponse schema.
type BuildVersionsPage struct {
	Items       []BuildVersion `json:"items"`
	Total       int            `json:"total"`
	Page        int            `json:"page"`
	PageSize    int            `json:"page_size"`
	TotalPages  int            `json:"total_pages"`
	HasNext     bool           `json:"has_next"`
	HasPrevious bool           `json:"has_previous"`
}

const (
	defaultBuildVersionsPageSize = 20
	maxBuildVersionsPageSize     = 100
)

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
	page, err := c.ListBuildVersionsPage(ctx, appID, 1, defaultBuildVersionsPageSize)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// ListBuildVersionsPage lists one page of build versions for an app.
//
// Parameters:
//   - ctx: Context for cancellation
//   - appID: The app ID
//   - page: 1-indexed page number
//   - pageSize: items per page (max 100)
//
// Returns:
//   - *BuildVersionsPage: Paginated build versions
//   - error: Any error that occurred
func (c *Client) ListBuildVersionsPage(ctx context.Context, appID string, page int, pageSize int) (*BuildVersionsPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultBuildVersionsPageSize
	}
	if pageSize > maxBuildVersionsPageSize {
		pageSize = maxBuildVersionsPageSize
	}

	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/builds/vars/%s/versions?page=%d&page_size=%d", appID, page, pageSize), nil)
	if err != nil {
		return nil, err
	}

	var result BuildVersionsPage
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.Items == nil {
		result.Items = []BuildVersion{}
	}

	return &result, nil
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
// MobileTargetEntry represents a saved device target from test_mobile_targets.
type MobileTargetEntry struct {
	DeviceModel string `json:"device_model"`
	OSVersion   string `json:"os_version"`
}

type Test struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Platform       string                 `json:"platform"`
	Description    string                 `json:"description,omitempty"`
	Tasks          interface{}            `json:"tasks"`
	Version        int                    `json:"version"`
	LastModifiedBy string                 `json:"last_modified_by,omitempty"`
	AppID          string                 `json:"app_id,omitempty"`
	PinnedVersion  string                 `json:"pinned_version,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	MobileTargets  []MobileTargetEntry    `json:"mobile_targets,omitempty"`
	Orientation    string                 `json:"orientation,omitempty"`
}

// UpdateTestRequest represents a test update request.
type UpdateTestRequest struct {
	TestID          string      `json:"-"`
	Name            string      `json:"name,omitempty"`
	Description     string      `json:"description,omitempty"`
	Tasks           interface{} `json:"tasks,omitempty"`
	AppID           string      `json:"app_id,omitempty"`
	PinnedVersionID string      `json:"pinned_version,omitempty"`
	ExpectedVersion int         `json:"expected_version,omitempty"`
	Force           bool        `json:"-"`
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
	normalizedReq := req
	if req != nil {
		normalizedReq = &CreateTestRequest{
			Name:     req.Name,
			Platform: normalizePlatform(req.Platform),
			Tasks:    req.Tasks,
			AppID:    req.AppID,
			OrgID:    req.OrgID,
		}
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/create", normalizedReq)
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

	// OrgName is the human-readable organization name from PropelAuth.
	OrgName string `json:"org_name"`

	// Email is the user's email address.
	Email string `json:"email"`

	// ConcurrencyLimit is the maximum number of concurrent test executions allowed.
	ConcurrencyLimit int `json:"concurrency_limit"`
}

// RevokeCLIAPIKeyRequest revokes a browser-generated CLI API key by ID.
type RevokeCLIAPIKeyRequest struct {
	APIKeyID string `json:"api_key_id"`
}

// RevokeCLIAPIKeyResponse represents the response from revoking a browser-generated CLI API key.
type RevokeCLIAPIKeyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
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

// RevokeCLIAPIKey revokes the current machine's browser-generated CLI API key.
func (c *Client) RevokeCLIAPIKey(ctx context.Context, apiKeyID string) error {
	resp, err := c.doRequestOnce(
		ctx,
		"POST",
		"/api/v1/entity/users/revoke_cli_api_key",
		&RevokeCLIAPIKeyRequest{APIKeyID: apiKeyID},
	)
	if err != nil {
		return err
	}

	var result RevokeCLIAPIKeyResponse
	if err := parseResponse(resp, &result); err != nil {
		return err
	}
	return nil
}

// StreamUploadBuild uploads a build using streaming (alternative to presigned URL).
//
// SimpleTest represents a lightweight test item for listing.
// Includes optional app and tag metadata used by the TUI browse/filter view.
type SimpleTest struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	AppID    string    `json:"app_id,omitempty"`
	AppName  string    `json:"app_name,omitempty"`
	Tags     []TestTag `json:"tags,omitempty"`
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

// ListAllOrgTests fetches all tests for the authenticated organization using
// offset pagination until exhaustion.
//
// Parameters:
//   - ctx: Context for cancellation
//   - pageSize: Page size for each request (default: 200, max: 500)
//
// Returns:
//   - []SimpleTest: All tests in the organization
//   - error: Any error that occurred
func (c *Client) ListAllOrgTests(ctx context.Context, pageSize int) ([]SimpleTest, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	if pageSize > 500 {
		pageSize = 500
	}

	var all []SimpleTest
	offset := 0

	for {
		resp, err := c.ListOrgTests(ctx, pageSize, offset)
		if err != nil {
			return nil, err
		}
		if resp == nil || len(resp.Tests) == 0 {
			break
		}

		all = append(all, resp.Tests...)
		offset += len(resp.Tests)

		// Stop when we've consumed the reported count.
		if resp.Count > 0 && offset >= resp.Count {
			break
		}

		// Defensive stop when page is short despite missing/incorrect count.
		if len(resp.Tests) < pageSize {
			break
		}
	}

	return all, nil
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
	SystemPrompt   string `json:"system_prompt,omitempty"`
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
	result.Items = c.hydrateAppVersionSummaries(ctx, result.Items)

	return &result, nil
}

const appVersionSummaryConcurrency = 8

func (c *Client) hydrateAppVersionSummaries(ctx context.Context, apps []App) []App {
	if len(apps) == 0 {
		return apps
	}

	hydrated := make([]App, len(apps))
	copy(hydrated, apps)

	var wg sync.WaitGroup
	sem := make(chan struct{}, appVersionSummaryConcurrency)

	for i := range hydrated {
		if !appNeedsVersionSummaryHydration(hydrated[i]) {
			continue
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			hydrated[idx] = c.hydrateSingleAppVersionSummary(ctx, hydrated[idx])
		}(i)
	}

	wg.Wait()
	return hydrated
}

func (c *Client) hydrateSingleAppVersionSummary(ctx context.Context, app App) App {
	if !appNeedsVersionSummaryHydration(app) {
		return app
	}

	page, err := c.ListBuildVersionsPage(ctx, app.ID, 1, 1)
	if err != nil {
		return app
	}

	app.VersionsCount = page.Total
	if app.LatestVersion == "" && len(page.Items) > 0 {
		app.LatestVersion = strings.TrimSpace(page.Items[0].Version)
	}

	return app
}

func appNeedsVersionSummaryHydration(app App) bool {
	if strings.TrimSpace(app.ID) == "" {
		return false
	}
	return app.VersionsCount == 0 || strings.TrimSpace(app.LatestVersion) == ""
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
	result = c.hydrateSingleAppVersionSummary(ctx, result)

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
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Tests               []string               `json:"tests,omitempty"`
	Schedule            string                 `json:"schedule,omitempty"`
	LocationConfig      map[string]interface{} `json:"location_config,omitempty"`
	OverrideLocation    bool                   `json:"override_location,omitempty"`
	BuildConfig         map[string]interface{} `json:"build_config,omitempty"`
	OverrideBuildConfig bool                   `json:"override_build_config,omitempty"`
	RunConfig           *WorkflowRunConfig     `json:"run_config,omitempty"`
}

// CLIWorkflowLastExecution holds the last execution metadata returned by the
// get_with_last_status endpoint.
type CLIWorkflowLastExecution struct {
	Status    string   `json:"status"`
	LastRun   *string  `json:"last_run,omitempty"`
	Duration  *float64 `json:"duration,omitempty"`
	StartedAt *string  `json:"started_at,omitempty"`
	ID        *string  `json:"id,omitempty"`
}

// SimpleWorkflow represents a workflow returned by the list endpoint,
// including last-execution metadata and test count.
type SimpleWorkflow struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	TestCount     int                       `json:"test_count"`
	LastExecution *CLIWorkflowLastExecution `json:"last_execution,omitempty"`
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
	return c.ListWorkflowsPage(ctx, 200, 0)
}

// ListWorkflowsPage retrieves workflows for the current organization using
// explicit limit/offset pagination.
//
// Parameters:
//   - ctx: Context for cancellation
//   - limit: Maximum workflows to fetch in this call (default: 200)
//   - offset: Number of workflows to skip (default: 0)
//
// Returns:
//   - *CLIWorkflowListResponse: The paginated workflow list
//   - error: Any error that occurred
func (c *Client) ListWorkflowsPage(ctx context.Context, limit, offset int) (*CLIWorkflowListResponse, error) {
	if limit <= 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	path := fmt.Sprintf("/api/v1/workflows/get_with_last_status?limit=%d&offset=%d", limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIWorkflowListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ListAllWorkflows retrieves all workflows for the current organization using
// limit/offset pagination until exhaustion.
//
// Parameters:
//   - ctx: Context for cancellation
//   - pageSize: Page size per call (default: 200, max: 500)
//
// Returns:
//   - []SimpleWorkflow: All workflows
//   - error: Any error that occurred
func (c *Client) ListAllWorkflows(ctx context.Context, pageSize int) ([]SimpleWorkflow, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	if pageSize > 500 {
		pageSize = 500
	}

	var all []SimpleWorkflow
	offset := 0

	for {
		resp, err := c.ListWorkflowsPage(ctx, pageSize, offset)
		if err != nil {
			return nil, err
		}
		if resp == nil || len(resp.Workflows) == 0 {
			break
		}

		all = append(all, resp.Workflows...)
		offset += len(resp.Workflows)

		if resp.Count > 0 && offset >= resp.Count {
			break
		}
		if len(resp.Workflows) < pageSize {
			break
		}
	}

	return all, nil
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
		fmt.Sprintf("/api/v1/workflows/get_workflow_info?workflow_id=%s", workflowID), nil)
	if err != nil {
		return nil, err
	}

	var result Workflow
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// WorkflowInfoTestItem contains per-test metadata returned by the lightweight
// workflow info endpoint (name, platform, last execution status).
type WorkflowInfoTestItem struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Platform          string   `json:"platform"`
	LastStatus        *string  `json:"last_status,omitempty"`
	LastExecutionTime *string  `json:"last_execution_time,omitempty"`
	LastDuration      *float64 `json:"last_duration,omitempty"`
}

// WorkflowInfoResponse is the parsed response from the lightweight workflow
// info endpoint, returning test_info with names and platforms.
type WorkflowInfoResponse struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	TestInfo []WorkflowInfoTestItem `json:"test_info"`
}

// GetWorkflowInfo retrieves lightweight workflow data including resolved test
// names, platforms, and per-test last execution status.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowID: The workflow UUID
//
// Returns:
//   - *WorkflowInfoResponse: Workflow info with test details
//   - error: Any error that occurred
func (c *Client) GetWorkflowInfo(ctx context.Context, workflowID string) (*WorkflowInfoResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/workflows/get_info/%s", workflowID), nil)
	if err != nil {
		return nil, err
	}

	var wrapper struct {
		Data WorkflowInfoResponse `json:"data"`
	}
	if err := parseResponse(resp, &wrapper); err != nil {
		return nil, err
	}

	return &wrapper.Data, nil
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

// BuildVersionDetail represents a build version with an optional download URL.
// Returned by GetBuildVersionDownloadURL.
type BuildVersionDetail struct {
	ID          string                 `json:"id"`
	Version     string                 `json:"version"`
	DownloadURL string                 `json:"download_url,omitempty"`
	PackageName string                 `json:"package_name,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// GetBuildVersionDownloadURL retrieves a build version with a presigned download URL.
//
// Parameters:
//   - ctx: Context for cancellation
//   - versionID: The build version ID
//
// Returns:
//   - *BuildVersionDetail: The build version with download URL
//   - error: Any error that occurred (404 if not found, 403 if not authorized)
func (c *Client) GetBuildVersionDownloadURL(ctx context.Context, versionID string) (*BuildVersionDetail, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/builds/builds/%s?include_download_url=true", versionID), nil)
	if err != nil {
		return nil, err
	}

	var result BuildVersionDetail
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if result.DownloadURL == "" {
		return nil, fmt.Errorf("build version %s has no downloadable artifact", versionID)
	}

	return &result, nil
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

	// AppID is the CogniSim apps table UUID (latest build is resolved server-side).
	AppID string `json:"app_id,omitempty"`

	// AppURL is a downloadable app artifact URL (.apk/.ipa/.zip).
	AppURL string `json:"app_url,omitempty"`

	// AppLink is a presigned or direct URL to the app binary artifact.
	AppLink string `json:"app_link,omitempty"`

	// IsSimulation enables simulation mode (streaming without test execution).
	IsSimulation bool `json:"is_simulation,omitempty"`

	// IdleTimeoutSeconds controls how long a session can be idle before auto-timeout.
	// Defaults to 300 seconds when omitted.
	IdleTimeoutSeconds int `json:"idle_timeout_seconds,omitempty"`

	// DeviceModel overrides the target device model (e.g. "iPhone 16", "Pixel 7").
	DeviceModel string `json:"device_model,omitempty"`

	// OsVersion overrides the target OS runtime (e.g. "iOS 18.5", "Android 14").
	OsVersion string `json:"os_version,omitempty"`

	// SessionID allows callers to pre-generate the canonical device session identifier.
	SessionID string `json:"session_id,omitempty"`

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

// GroundElementRequest is the payload for the grounding proxy endpoint.
type GroundElementRequest struct {
	// Target is a natural language description of the UI element to locate.
	Target string `json:"target"`

	// ImageBase64 is the base64-encoded screenshot (PNG or JPEG).
	ImageBase64 string `json:"image_base64"`

	// Width is the screenshot width in pixels.
	Width int `json:"width"`

	// Height is the screenshot height in pixels.
	Height int `json:"height"`

	// Platform is the device platform (android or ios).
	Platform string `json:"platform,omitempty"`

	// SessionID is the device session ID for cost tracking.
	SessionID string `json:"session_id,omitempty"`
}

// GroundElementResponse is the response from the grounding proxy endpoint.
type GroundElementResponse struct {
	// X is the absolute X pixel coordinate.
	X int `json:"x"`

	// Y is the absolute Y pixel coordinate.
	Y int `json:"y"`

	// Found indicates whether the element was successfully located.
	Found bool `json:"found"`

	// Error is the error message if grounding failed.
	Error string `json:"error,omitempty"`
}

// GroundElement locates a UI element in a screenshot via the backend grounding
// proxy, which routes the request through the Hatchet grounder-only workflow.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - req: The grounding request with target description and screenshot.
//
// Returns:
//   - *GroundElementResponse: The grounding result with coordinates.
//   - error: Any error that occurred during the API call.
func (c *Client) GroundElement(ctx context.Context, req *GroundElementRequest) (*GroundElementResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/execution/ground", req)
	if err != nil {
		return nil, err
	}

	var result GroundElementResponse
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

// GetActiveDeviceSessions retrieves all active device sessions for an organization.
// Returns sessions with status IN ('starting', 'running').
//
// Parameters:
//   - ctx: Context for cancellation
//   - orgID: The organization ID to query sessions for
//
// Returns:
//   - *ActiveDeviceSessionsResponse: List of active sessions
//   - error: Any error that occurred
func (c *Client) GetActiveDeviceSessions(ctx context.Context, orgID string) (*ActiveDeviceSessionsResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/execution/device-sessions/active?org_id=%s", orgID), nil)
	if err != nil {
		return nil, err
	}

	var result ActiveDeviceSessionsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeviceSessionDetail is the response shape for GET /device-sessions/{session_id}.
type DeviceSessionDetail struct {
	ID            string  `json:"id"`
	OrgID         string  `json:"org_id"`
	Source        *string `json:"source"`
	Platform      *string `json:"platform"`
	DeviceModel   *string `json:"device_model"`
	OsVersion     *string `json:"os_version"`
	Status        string  `json:"status"`
	WhepURL       *string `json:"whep_url"`
	WorkflowRunID *string `json:"workflow_run_id"`
	CreatedAt     *string `json:"created_at"`
	StartedAt     *string `json:"started_at"`
	EndedAt       *string `json:"ended_at"`
	ErrorMessage  *string `json:"error_message"`
	TraceID       *string `json:"trace_id"`
	ScreenWidth   *int    `json:"screen_width"`
	ScreenHeight  *int    `json:"screen_height"`
	ReportID      *string `json:"report_id"`
	HasVideo      bool    `json:"has_video"`
	StepCount     int     `json:"step_count"`
	TestID        *string `json:"test_id"`
	TestName      *string `json:"test_name"`
	UserEmail     *string `json:"user_email"`
	CanCancel     bool    `json:"can_cancel"`
}

// GetDeviceSessionByID retrieves a single device session by its ID.
func (c *Client) GetDeviceSessionByID(ctx context.Context, sessionID string) (*DeviceSessionDetail, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/execution/device-sessions/%s", sessionID), nil)
	if err != nil {
		return nil, err
	}

	var result DeviceSessionDetail
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
	Version     int           `json:"version"`
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
	Name            *string        `json:"name,omitempty"`
	Description     *string        `json:"description,omitempty"`
	Blocks          *[]interface{} `json:"blocks,omitempty"`
	ExpectedVersion *int           `json:"expected_version,omitempty"`
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

// --- Module lifecycle API methods ---

// GetModuleVersions lists version history for a module.
//
// Parameters:
//   - ctx: Context for cancellation
//   - moduleID: The module UUID
//   - limit: Maximum number of versions to return
//   - offset: Number of versions to skip (for pagination)
//
// Returns:
//   - *ModuleVersionListResponse: Paginated list of module versions
//   - error: Any error that occurred
func (c *Client) GetModuleVersions(ctx context.Context, moduleID string, limit, offset int) (*ModuleVersionListResponse, error) {
	path := fmt.Sprintf("/api/v1/modules/%s/versions?limit=%d&offset=%d", moduleID, limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result ModuleVersionListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// RestoreModuleVersion restores a module to a specific historical version.
//
// Parameters:
//   - ctx: Context for cancellation
//   - moduleID: The module UUID
//   - version: The version number to restore to
//
// Returns:
//   - error: Any error that occurred (404 if version not found)
func (c *Client) RestoreModuleVersion(ctx context.Context, moduleID string, version int) error {
	body := map[string]int{"version": version}
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/modules/%s/restore", moduleID), body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// GetModuleUsage lists the tests that reference a specific module.
//
// Parameters:
//   - ctx: Context for cancellation
//   - moduleID: The module UUID
//
// Returns:
//   - *ModuleUsageResponse: List of tests that use the module
//   - error: Any error that occurred
func (c *Client) GetModuleUsage(ctx context.Context, moduleID string) (*ModuleUsageResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/modules/%s/tests", moduleID), nil)
	if err != nil {
		return nil, err
	}

	var result ModuleUsageResponse
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

// --- Enhanced History API types ---

// CLIEnhancedTask represents the task details within an enhanced history item.
type CLIEnhancedTask struct {
	ID                   string  `json:"id"`
	TestID               string  `json:"test_id"`
	Success              *bool   `json:"success"`
	Progress             float64 `json:"progress"`
	CurrentStep          string  `json:"current_step,omitempty"`
	StepsCompleted       int     `json:"steps_completed"`
	TotalSteps           int     `json:"total_steps"`
	ErrorMessage         string  `json:"error_message,omitempty"`
	Status               string  `json:"status"`
	StartedAt            string  `json:"started_at,omitempty"`
	CompletedAt          string  `json:"completed_at,omitempty"`
	ExecutionTimeSeconds float64 `json:"execution_time_seconds,omitempty"`
}

// CLIEnhancedHistoryItem represents a single execution in the enhanced history.
type CLIEnhancedHistoryItem struct {
	ID            string           `json:"id"`
	TestUID       string           `json:"test_uid"`
	ExecutionTime string           `json:"execution_time"`
	Status        string           `json:"status"`
	Duration      *float64         `json:"duration"`
	EnhancedTask  *CLIEnhancedTask `json:"enhanced_task"`
	HasReport     bool             `json:"has_report"`
}

// CLIEnhancedHistoryResponse represents the response from the enhanced history endpoint.
type CLIEnhancedHistoryResponse struct {
	Items          []CLIEnhancedHistoryItem `json:"items"`
	TotalCount     int                      `json:"total_count"`
	RequestedCount int                      `json:"requested_count"`
	FoundCount     int                      `json:"found_count"`
}

// GetDashboardMetrics fetches org-level dashboard metrics including total tests,
// workflows, test runs, failure rate, and average duration with week-over-week deltas.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *DashboardMetrics: The dashboard metrics (type from generated.go)
//   - error: Any error that occurred
func (c *Client) GetDashboardMetrics(ctx context.Context) (*DashboardMetrics, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/entity/users/get_dashboard_metrics", nil)
	if err != nil {
		return nil, err
	}

	var result DashboardMetrics
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetTestEnhancedHistory retrieves the enhanced execution history for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test ID
//   - limit: Maximum number of items to return
//   - offset: Number of items to skip for pagination
//
// Returns:
//   - *CLIEnhancedHistoryResponse: The enhanced history
//   - error: Any error that occurred
func (c *Client) GetTestEnhancedHistory(ctx context.Context, testID string, limit, offset int) (*CLIEnhancedHistoryResponse, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	path := fmt.Sprintf("/api/v1/tests/get_test_enhanced_history?test_id=%s&limit=%d&offset=%d", testID, limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIEnhancedHistoryResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// --- Report V3 API types ---

// CLIReportTLDRKeyMoment mirrors the typed TLDR key moment payload.
type CLIReportTLDRKeyMoment struct {
	StepReference string `json:"step_reference"`
	Description   string `json:"description"`
	Importance    string `json:"importance"`
}

// CLIReportTLDR mirrors the typed TLDR payload returned by reports-v3 context.
type CLIReportTLDR struct {
	TestCase    string                   `json:"test_case"`
	KeyMoments  []CLIReportTLDRKeyMoment `json:"key_moments"`
	Insights    []string                 `json:"insights"`
	LlmMetadata map[string]interface{}   `json:"llm_metadata,omitempty"`
}

// CLIReportContextAction mirrors the backend's high-context action payload.
type CLIReportContextAction struct {
	ID                       *string                `json:"id,omitempty"`
	StepID                   *string                `json:"step_id,omitempty"`
	ActionIndex              int                    `json:"action_index"`
	ActionType               *string                `json:"action_type,omitempty"`
	LlmCallID                *string                `json:"llm_call_id,omitempty"`
	ReflectionLlmCallID      *string                `json:"reflection_llm_call_id,omitempty"`
	AgentDescription         *string                `json:"agent_description,omitempty"`
	Reasoning                *string                `json:"reasoning,omitempty"`
	ReflectionDecision       *string                `json:"reflection_decision,omitempty"`
	ReflectionReasoning      *string                `json:"reflection_reasoning,omitempty"`
	ReflectionSuggestion     *string                `json:"reflection_suggestion,omitempty"`
	IsTerminal               bool                   `json:"is_terminal,omitempty"`
	ScreenshotBeforeURL      *string                `json:"screenshot_before_url,omitempty"`
	ScreenshotBeforeCleanURL *string                `json:"screenshot_before_clean_url,omitempty"`
	ScreenshotAfterURL       *string                `json:"screenshot_after_url,omitempty"`
	VideoTimestampStart      *float64               `json:"video_timestamp_start,omitempty"`
	VideoTimestampEnd        *float64               `json:"video_timestamp_end,omitempty"`
	StartedAt                *string                `json:"started_at,omitempty"`
	CompletedAt              *string                `json:"completed_at,omitempty"`
	CreatedAt                *string                `json:"created_at,omitempty"`
	TypeData                 map[string]interface{} `json:"type_data,omitempty"`
	LlmCall                  map[string]interface{} `json:"llm_call,omitempty"`
	ReflectionLlmCall        map[string]interface{} `json:"reflection_llm_call,omitempty"`
}

// CLIReportContextStep mirrors the backend's high-context step payload.
type CLIReportContextStep struct {
	ID                    string                   `json:"id"`
	ReportID              *string                  `json:"report_id,omitempty"`
	ParentStepID          *string                  `json:"parent_step_id,omitempty"`
	ExecutionOrder        int                      `json:"execution_order"`
	NodeID                *string                  `json:"node_id,omitempty"`
	StepType              string                   `json:"step_type"`
	StepDescription       *string                  `json:"step_description,omitempty"`
	Success               *bool                    `json:"success,omitempty"`
	ErrorMessage          *string                  `json:"error_message,omitempty"`
	StartedAt             *string                  `json:"started_at,omitempty"`
	CompletedAt           *string                  `json:"completed_at,omitempty"`
	LlmCallID             *string                  `json:"llm_call_id,omitempty"`
	SourceModuleID        *string                  `json:"source_module_id,omitempty"`
	SourceModuleName      *string                  `json:"source_module_name,omitempty"`
	VideoTimestampStart   *float64                 `json:"video_timestamp_start,omitempty"`
	VideoTimestampEnd     *float64                 `json:"video_timestamp_end,omitempty"`
	CreatedAt             *string                  `json:"created_at,omitempty"`
	Status                *string                  `json:"status,omitempty"`
	StatusReason          *string                  `json:"status_reason,omitempty"`
	EffectiveStatus       *string                  `json:"effective_status,omitempty"`
	EffectiveStatusReason *string                  `json:"effective_status_reason,omitempty"`
	ValidationResult      *bool                    `json:"validation_result,omitempty"`
	ValidationReasoning   *string                  `json:"validation_reasoning,omitempty"`
	TypeData              map[string]interface{}   `json:"type_data,omitempty"`
	Actions               []CLIReportContextAction `json:"actions,omitempty"`
	LlmCall               map[string]interface{}   `json:"llm_call,omitempty"`
}

// CLIReportContextResponse mirrors the backend's high-context report payload.
type CLIReportContextResponse struct {
	ID                    string                 `json:"id"`
	ReportURL             *string                `json:"report_url,omitempty"`
	ExecutionID           *string                `json:"execution_id,omitempty"`
	SessionID             *string                `json:"session_id,omitempty"`
	TestID                *string                `json:"test_id,omitempty"`
	TestVersionID         *string                `json:"test_version_id,omitempty"`
	OrgID                 string                 `json:"org_id"`
	StartedAt             *string                `json:"started_at,omitempty"`
	CompletedAt           *string                `json:"completed_at,omitempty"`
	TotalSteps            *int                   `json:"total_steps,omitempty"`
	PassedSteps           *int                   `json:"passed_steps,omitempty"`
	WarningSteps          *int                   `json:"warning_steps,omitempty"`
	FailedSteps           *int                   `json:"failed_steps,omitempty"`
	TotalValidations      *int                   `json:"total_validations,omitempty"`
	ValidationsPassed     *int                   `json:"validations_passed,omitempty"`
	EffectivePassedSteps  *int                   `json:"effective_passed_steps,omitempty"`
	EffectiveWarningSteps *int                   `json:"effective_warning_steps,omitempty"`
	EffectiveFailedSteps  *int                   `json:"effective_failed_steps,omitempty"`
	EffectiveRunningSteps *int                   `json:"effective_running_steps,omitempty"`
	EffectivePendingSteps *int                   `json:"effective_pending_steps,omitempty"`
	TestGoalSummary       *string                `json:"test_goal_summary,omitempty"`
	Tldr                  *CLIReportTLDR         `json:"tldr,omitempty"`
	CreatedAt             *string                `json:"created_at,omitempty"`
	UpdatedAt             *string                `json:"updated_at,omitempty"`
	TestName              *string                `json:"test_name,omitempty"`
	Platform              *string                `json:"platform,omitempty"`
	SessionStatus         *string                `json:"session_status,omitempty"`
	DeviceModel           *string                `json:"device_model,omitempty"`
	OsVersion             *string                `json:"os_version,omitempty"`
	WhepURL               *string                `json:"whep_url,omitempty"`
	TraceID               *string                `json:"trace_id,omitempty"`
	DeviceMetadata        *DeviceMetadata        `json:"device_metadata,omitempty"`
	ScreenWidth           *int                   `json:"screen_width,omitempty"`
	ScreenHeight          *int                   `json:"screen_height,omitempty"`
	Success               *bool                  `json:"success,omitempty"`
	WorkflowExecutionID   *string                `json:"workflow_execution_id,omitempty"`
	AppName               *string                `json:"app_name,omitempty"`
	SystemPrompt          *string                `json:"system_prompt,omitempty"`
	BuildVersion          *string                `json:"build_version,omitempty"`
	TestVersionNumber     *int                   `json:"test_version_number,omitempty"`
	VideoURL              *string                `json:"video_url,omitempty"`
	PerfettoTraceURL      *string                `json:"perfetto_trace_url,omitempty"`
	HardwareMetricsURL    *string                `json:"hardware_metrics_url,omitempty"`
	Steps                 []CLIReportContextStep `json:"steps,omitempty"`
}

// CLIReportContextEnvelope preserves both the raw response bytes and the typed report.
type CLIReportContextEnvelope struct {
	Raw    json.RawMessage
	Report *CLIReportContextResponse
}

// GetReportContextByExecution retrieves the canonical high-context report for an execution.
func (c *Client) GetReportContextByExecution(ctx context.Context, executionID string, includeSteps, includeActions, includeLLMCalls bool) (*CLIReportContextEnvelope, error) {
	path := fmt.Sprintf(
		"/api/v1/reports-v3/reports/by-execution/%s/context?include_steps=%t&include_actions=%t&include_llm_calls=%t",
		executionID,
		includeSteps,
		includeActions,
		includeLLMCalls,
	)
	resp, err := c.doRequestOnce(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIReportContextResponse
	raw, err := parseResponseWithRaw(resp, &result)
	if err != nil {
		return nil, err
	}

	return &CLIReportContextEnvelope{
		Raw:    raw,
		Report: &result,
	}, nil
}

// GetReportBySession retrieves the high-context report for a device session.
func (c *Client) GetReportBySession(ctx context.Context, sessionID string, includeSteps, includeActions, includeLLMCalls bool) (*CLIReportContextEnvelope, error) {
	path := fmt.Sprintf(
		"/api/v1/reports-v3/reports/by-session/%s/context?include_steps=%t&include_actions=%t&include_llm_calls=%t",
		sessionID,
		includeSteps,
		includeActions,
		includeLLMCalls,
	)
	resp, err := c.doRequestOnce(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIReportContextResponse
	raw, err := parseResponseWithRaw(resp, &result)
	if err != nil {
		return nil, err
	}

	return &CLIReportContextEnvelope{
		Raw:    raw,
		Report: &result,
	}, nil
}

// --- Shareable Link API types ---

// CLIShareableLinkRequest represents a request to generate a shareable report link.
type CLIShareableLinkRequest struct {
	TaskID          string `json:"task_id"`
	ExpirationHours *int   `json:"expiration_hours"`
}

// CLIShareableLinkResponse represents the response containing a shareable link.
type CLIShareableLinkResponse struct {
	ShareableLink string `json:"shareable_link"`
}

// GenerateShareableLink creates a shareable link for a test execution report.
//
// Parameters:
//   - ctx: Context for cancellation
//   - taskID: The execution task ID
//
// Returns:
//   - *CLIShareableLinkResponse: The shareable link
//   - error: Any error that occurred
func (c *Client) GenerateShareableLink(ctx context.Context, taskID string) (*CLIShareableLinkResponse, error) {
	req := &CLIShareableLinkRequest{
		TaskID:          taskID,
		ExpirationHours: nil,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/report/async-run/generate_shareable_report_link_by_task", req)
	if err != nil {
		return nil, err
	}

	var result CLIShareableLinkResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// --- Workflow Status/History/Report API types ---

// CLIWorkflowStatusResponse represents a workflow execution status.
type CLIWorkflowStatusResponse struct {
	ExecutionID         string  `json:"execution_id,omitempty"`
	WorkflowID          string  `json:"workflow_id"`
	Status              string  `json:"status"`
	Progress            float64 `json:"progress"`
	CompletedTests      int     `json:"completed_tests"`
	TotalTests          int     `json:"total_tests"`
	PassedTests         int     `json:"passed_tests"`
	FailedTests         int     `json:"failed_tests"`
	StartedAt           string  `json:"started_at,omitempty"`
	EstimatedCompletion string  `json:"estimated_completion,omitempty"`
	Duration            string  `json:"duration,omitempty"`
	ErrorMessage        string  `json:"error_message,omitempty"`
}

// CLIWorkflowHistoryResponse represents workflow execution history.
type CLIWorkflowHistoryResponse struct {
	WorkflowID      string                      `json:"workflow_id"`
	Executions      []CLIWorkflowStatusResponse `json:"executions"`
	TotalCount      int                         `json:"total_count"`
	SuccessRate     float64                     `json:"success_rate"`
	AverageDuration *float64                    `json:"average_duration,omitempty"`
}

// CLIWorkflowTaskInfo contains workflow execution snapshot data.
type CLIWorkflowTaskInfo struct {
	TaskID         string   `json:"task_id"`
	WorkflowID     string   `json:"workflow_id,omitempty"`
	Status         string   `json:"status,omitempty"`
	Success        *bool    `json:"success,omitempty"`
	Duration       *float64 `json:"duration,omitempty"`
	TotalTests     *int     `json:"total_tests,omitempty"`
	CompletedTests *int     `json:"completed_tests,omitempty"`
	TaskIDs        []string `json:"task_ids"`
	StartedAt      string   `json:"started_at,omitempty"`
	UpdatedAt      string   `json:"updated_at,omitempty"`
	CreatedAt      string   `json:"created_at,omitempty"`
	TriggeredBy    string   `json:"triggered_by,omitempty"`
}

// CLIWorkflowDetailInfo contains workflow definition data.
type CLIWorkflowDetailInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Tests       []string `json:"tests"`
}

// CLIWorkflowTestInfo contains test name/platform info for a workflow.
type CLIWorkflowTestInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
}

// CLIChildTaskReportInfo contains individual test execution data within a workflow.
type CLIChildTaskReportInfo struct {
	TaskID               string   `json:"task_id"`
	TestID               string   `json:"test_id,omitempty"`
	TestName             string   `json:"test_name,omitempty"`
	Platform             string   `json:"platform,omitempty"`
	Status               string   `json:"status,omitempty"`
	Success              *bool    `json:"success,omitempty"`
	StartedAt            string   `json:"started_at,omitempty"`
	CompletedAt          string   `json:"completed_at,omitempty"`
	Duration             *float64 `json:"duration,omitempty"`
	ExecutionTimeSeconds *float64 `json:"execution_time_seconds,omitempty"`
	StepsCompleted       *int     `json:"steps_completed,omitempty"`
	TotalSteps           *int     `json:"total_steps,omitempty"`
	Progress             *float64 `json:"progress,omitempty"`
	ErrorMessage         string   `json:"error_message,omitempty"`
}

// CLIUnifiedWorkflowReportResponse represents a comprehensive workflow report.
type CLIUnifiedWorkflowReportResponse struct {
	WorkflowTask   CLIWorkflowTaskInfo      `json:"workflow_task"`
	WorkflowDetail *CLIWorkflowDetailInfo   `json:"workflow_detail,omitempty"`
	TestInfo       []CLIWorkflowTestInfo    `json:"test_info"`
	ChildTasks     []CLIChildTaskReportInfo `json:"child_tasks"`
}

// GetWorkflowStatus retrieves the real-time status of a workflow execution.
func (c *Client) GetWorkflowStatus(ctx context.Context, taskID string) (*CLIWorkflowStatusResponse, error) {
	path := fmt.Sprintf("/api/v1/workflows/status/%s", taskID)
	resp, err := c.doRequestOnce(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIWorkflowStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetWorkflowHistory retrieves execution history for a workflow.
func (c *Client) GetWorkflowHistory(ctx context.Context, workflowID string, limit, offset int) (*CLIWorkflowHistoryResponse, error) {
	path := fmt.Sprintf("/api/v1/workflows/status/history/%s?limit=%d&offset=%d",
		workflowID, limit, offset)
	resp, err := c.doRequestOnce(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIWorkflowHistoryResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetWorkflowUnifiedReport retrieves a comprehensive workflow report.
func (c *Client) GetWorkflowUnifiedReport(ctx context.Context, workflowTaskID string) (*CLIUnifiedWorkflowReportResponse, error) {
	body := map[string]interface{}{
		"workflow_task_id": workflowTaskID,
	}
	resp, err := c.doRequestOnce(ctx, "POST", "/api/v1/workflows/share/unified-report", body)
	if err != nil {
		return nil, err
	}

	var result CLIUnifiedWorkflowReportResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// --- Env Var API methods ---

// EnvVar represents an app launch environment variable.
type EnvVar struct {
	ID        string `json:"id"`
	TestID    string `json:"test_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// EnvVarsResponse represents the response from listing env vars.
type EnvVarsResponse struct {
	Message string   `json:"message"`
	Result  []EnvVar `json:"result"`
}

// EnvVarResponse represents the response from a single env var operation.
type EnvVarResponse struct {
	Message string `json:"message"`
	Result  EnvVar `json:"result"`
}

// ListEnvVars retrieves all env vars for a test.
func (c *Client) ListEnvVars(ctx context.Context, testID string) (*EnvVarsResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/app_launch_env/read?test_id=%s", testID)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result EnvVarsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// AddEnvVar adds an env var to a test.
func (c *Client) AddEnvVar(ctx context.Context, testID, key, value string) (*EnvVarResponse, error) {
	body := map[string]string{
		"test_id": testID,
		"key":     key,
		"value":   value,
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/variables/app_launch_env/add", body)
	if err != nil {
		return nil, err
	}

	var result EnvVarResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateEnvVar updates an existing env var.
func (c *Client) UpdateEnvVar(ctx context.Context, envVarID, key, value string) (*EnvVarResponse, error) {
	body := map[string]string{
		"env_var_id": envVarID,
		"key":        key,
		"value":      value,
	}
	resp, err := c.doRequest(ctx, "PUT", "/api/v1/variables/app_launch_env/update", body)
	if err != nil {
		return nil, err
	}

	var result EnvVarResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteEnvVar deletes a single env var by ID.
func (c *Client) DeleteEnvVar(ctx context.Context, envVarID string) error {
	path := fmt.Sprintf("/api/v1/variables/app_launch_env/delete?env_var_id=%s", envVarID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// DeleteAllEnvVars deletes all env vars for a test.
func (c *Client) DeleteAllEnvVars(ctx context.Context, testID string) error {
	path := fmt.Sprintf("/api/v1/variables/app_launch_env/delete_all?test_id=%s", testID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// UpdateDeviceTarget updates the saved device target for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - deviceModel: Target device model (empty string or "AUTO" for auto)
//   - osVersion: Target OS version (empty string or "AUTO" for auto)
//   - orientation: Device orientation ("portrait" or "landscape", empty to skip)
//
// Returns:
//   - error: Any error that occurred
func (c *Client) UpdateDeviceTarget(ctx context.Context, testID, deviceModel, osVersion, orientation string) error {
	path := fmt.Sprintf("/api/v1/tests/%s/device-target", testID)
	body := map[string]*string{}
	if deviceModel != "" {
		body["device_model"] = &deviceModel
	}
	if osVersion != "" {
		body["os_version"] = &osVersion
	}
	if orientation != "" {
		body["orientation"] = &orientation
	}
	resp, err := c.doRequest(ctx, "PATCH", path, body)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Workflow Settings API methods ---

// UpdateWorkflowName renames a workflow while preserving its ID/history.
func (c *Client) UpdateWorkflowName(ctx context.Context, workflowID, name string) error {
	body := map[string]string{
		"name": name,
	}
	path := fmt.Sprintf("/api/v1/workflows/update_name/%s", workflowID)
	resp, err := c.doRequest(ctx, "PUT", path, body)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// UpdateWorkflowLocationConfig updates the stored location config for a workflow.
func (c *Client) UpdateWorkflowLocationConfig(ctx context.Context, workflowID string, locationConfig map[string]interface{}, override bool) error {
	body := map[string]interface{}{
		"location_config":   locationConfig,
		"override_location": override,
	}
	path := fmt.Sprintf("/api/v1/workflows/update_location_config/%s", workflowID)
	resp, err := c.doRequest(ctx, "PUT", path, body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// UpdateWorkflowBuildConfig updates the stored build/app config for a workflow.
func (c *Client) UpdateWorkflowBuildConfig(ctx context.Context, workflowID string, buildConfig map[string]interface{}, override bool) error {
	body := map[string]interface{}{
		"build_config":          buildConfig,
		"override_build_config": override,
	}
	path := fmt.Sprintf("/api/v1/workflows/update_build_config/%s", workflowID)
	resp, err := c.doRequest(ctx, "PUT", path, body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// WorkflowRunConfig represents a workflow's run configuration (parallelism, retries).
type WorkflowRunConfig struct {
	Parallelism int `json:"parallelism,omitempty"`
	MaxRetries  int `json:"max_retries,omitempty"`
}

// UpdateWorkflowRunConfig updates the run configuration for a workflow.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowID: The workflow UUID
//   - config: The run configuration to apply
//
// Returns:
//   - error: Any error that occurred
func (c *Client) UpdateWorkflowRunConfig(ctx context.Context, workflowID string, config *WorkflowRunConfig) error {
	path := fmt.Sprintf("/api/v1/workflows/update_run_config/%s", workflowID)
	resp, err := c.doRequest(ctx, "PUT", path, config)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// DeviceSessionHistoryResponse represents paginated device session history.
type DeviceSessionHistoryResponse struct {
	Sessions []DeviceSessionHistoryItem `json:"sessions"`
	Total    int                        `json:"total"`
}

// DeviceSessionHistoryItem represents a single device session in the history list.
type DeviceSessionHistoryItem struct {
	ID        string  `json:"id"`
	Platform  string  `json:"platform"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	Duration  float64 `json:"duration_seconds,omitempty"`
}

// GetDeviceSessionHistory retrieves paginated device session history.
//
// Parameters:
//   - ctx: Context for cancellation
//   - limit: Maximum number of sessions to return
//   - offset: Number of sessions to skip for pagination
//
// Returns:
//   - *DeviceSessionHistoryResponse: The paginated session history
//   - error: Any error that occurred
func (c *Client) GetDeviceSessionHistory(ctx context.Context, limit, offset int) (*DeviceSessionHistoryResponse, error) {
	path := fmt.Sprintf("/api/v1/execution/device-sessions/history?limit=%d&offset=%d", limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result DeviceSessionHistoryResponse
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

// ---------------------------------------------------------------------------
// Worker Control Relay
// ---------------------------------------------------------------------------

// ProxyWorkerRequest forwards a device action request through the backend.
// CLI/MCP device control uses this relay as the canonical transport so
// sandboxed environments do not depend on direct worker DNS reachability.
// The relay infers the underlying HTTP method from the action name.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - workflowRunID: The Hatchet workflow run powering the session.
//   - action: Worker endpoint name (e.g. "tap", "swipe", "health").
//   - body: JSON-serializable request body (nil for GET-like actions).
//
// Returns:
//   - []byte: Raw response body from the worker.
//   - int: HTTP status code from the worker.
//   - error: Any error from the proxy call itself.
//
// proxyNoRetryActions lists worker actions that are not idempotent.
// Retrying these creates duplicate side-effects (e.g. duplicate agent steps).
var proxyNoRetryActions = map[string]bool{
	"execute_step": true,
}

func (c *Client) ProxyWorkerRequest(ctx context.Context, workflowRunID, action string, body interface{}) ([]byte, int, error) {
	path := fmt.Sprintf("/api/v1/execution/device-proxy/%s/%s", workflowRunID, action)

	method := proxyWorkerMethodForAction(action)
	if method == http.MethodGet {
		body = nil
	}

	var resp *http.Response
	var err error
	if proxyNoRetryActions[action] {
		resp, err = c.doRequestOnce(ctx, method, path, body)
	} else {
		resp, err = c.doRequest(ctx, method, path, body)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("proxy request failed for %s: %w", action, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read proxy response: %w", err)
	}

	return data, resp.StatusCode, nil
}

func proxyWorkerMethodForAction(action string) string {
	// Extract base action for compound paths (e.g. "step_status/{id}").
	base := action
	if idx := strings.Index(action, "/"); idx >= 0 {
		base = action[:idx]
	}
	switch base {
	case "screenshot", "health", "device_info", "step_status", "hierarchy":
		return http.MethodGet
	default:
		return http.MethodPost
	}
}

// ProxyScreenshot retrieves a device screenshot through the backend proxy.
// Returns the raw PNG bytes.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - workflowRunID: The Hatchet workflow run powering the session.
//
// Returns:
//   - []byte: PNG image data.
//   - error: Any error that occurred.
func (c *Client) ProxyScreenshot(ctx context.Context, workflowRunID string) ([]byte, error) {
	data, status, err := c.ProxyWorkerRequest(ctx, workflowRunID, "screenshot", nil)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("proxy screenshot failed (HTTP %d): %s", status, string(data))
	}
	return data, nil
}

// --- Script API methods ---

// CLIScriptInfo represents a script for CLI display.
type CLIScriptInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Code        string  `json:"code"`
	Runtime     string  `json:"runtime"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// CLIScriptListResponse represents the response from listing scripts.
type CLIScriptListResponse struct {
	Scripts []CLIScriptInfo `json:"scripts"`
	Count   int             `json:"count"`
}

// CLICreateScriptRequest represents a script creation request.
type CLICreateScriptRequest struct {
	Name        string  `json:"name"`
	Code        string  `json:"code"`
	Runtime     string  `json:"runtime"`
	Description *string `json:"description,omitempty"`
}

// CLIUpdateScriptRequest represents a script update request.
type CLIUpdateScriptRequest struct {
	Name        *string `json:"name,omitempty"`
	Code        *string `json:"code,omitempty"`
	Runtime     *string `json:"runtime,omitempty"`
	Description *string `json:"description,omitempty"`
}

// CLIScriptUsageResponse represents the response from checking script usage.
type CLIScriptUsageResponse struct {
	Tests []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"tests"`
	Total int `json:"total"`
}

// ListScripts fetches all scripts for the authenticated user's organization.
func (c *Client) ListScripts(ctx context.Context, runtime string, limit, offset int) (*CLIScriptListResponse, error) {
	path := "/api/v1/tests/scripts?"
	params := url.Values{}
	if runtime != "" {
		params.Set("runtime", runtime)
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		params.Set("offset", fmt.Sprintf("%d", offset))
	}
	path += params.Encode()

	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIScriptListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetScript retrieves a script by ID.
func (c *Client) GetScript(ctx context.Context, scriptID string) (*CLIScriptInfo, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/tests/scripts/%s", scriptID), nil)
	if err != nil {
		return nil, err
	}

	var result CLIScriptInfo
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateScript creates a new script.
func (c *Client) CreateScript(ctx context.Context, req *CLICreateScriptRequest) (*CLIScriptInfo, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/scripts", req)
	if err != nil {
		return nil, err
	}

	var result CLIScriptInfo
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateScript updates an existing script.
func (c *Client) UpdateScript(ctx context.Context, scriptID string, req *CLIUpdateScriptRequest) (*CLIScriptInfo, error) {
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/tests/scripts/%s", scriptID), req)
	if err != nil {
		return nil, err
	}

	var result CLIScriptInfo
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteScript deletes a script by ID.
func (c *Client) DeleteScript(ctx context.Context, scriptID string) error {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/tests/scripts/%s", scriptID), nil)
	if err != nil {
		return err
	}

	// 204 No Content means success - body is empty
	if resp.StatusCode == 204 {
		resp.Body.Close()
		return nil
	}

	return parseResponse(resp, nil)
}

// GetScriptUsage retrieves all tests that use a specific script.
func (c *Client) GetScriptUsage(ctx context.Context, scriptID string) (*CLIScriptUsageResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/tests/scripts/%s/tests", scriptID), nil)
	if err != nil {
		return nil, err
	}

	var result CLIScriptUsageResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// --- Custom Variable API methods ---

// CustomVariable represents a test variable used in {{variable-name}} or
// {{variable_name}} syntax.
//
// Fields:
//   - ID: Unique identifier of the variable
//   - TestUID: Test UUID this variable belongs to
//   - VariableName: The variable name
//   - VariableValue: The variable value (may be empty for extraction-defined vars)
type CustomVariable struct {
	ID            string `json:"id"`
	TestUID       string `json:"test_uid"`
	VariableName  string `json:"variable_name"`
	VariableValue string `json:"variable_value"`
}

// CustomVariablesResponse represents the response from listing custom variables.
type CustomVariablesResponse struct {
	Message string           `json:"message"`
	Result  []CustomVariable `json:"result"`
}

// CustomVariableResponse represents the response from a single custom variable operation.
type CustomVariableResponse struct {
	Message string         `json:"message"`
	Result  CustomVariable `json:"result"`
}

// ListCustomVariables retrieves all custom variables for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//
// Returns:
//   - *CustomVariablesResponse: List of variables
//   - error: Any error that occurred
func (c *Client) ListCustomVariables(ctx context.Context, testID string) (*CustomVariablesResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/custom/read_variables?test_uid=%s", testID)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CustomVariablesResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// AddCustomVariable adds a new custom variable to a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - name: Variable name
//   - value: Variable value (may be empty)
//
// Returns:
//   - *CustomVariableResponse: The created variable
//   - error: Any error that occurred (409 if duplicate name)
func (c *Client) AddCustomVariable(ctx context.Context, testID, name, value string) (*CustomVariableResponse, error) {
	body := map[string]string{
		"test_uid":       testID,
		"variable_name":  name,
		"variable_value": value,
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/variables/custom/add", body)
	if err != nil {
		return nil, err
	}

	var result CustomVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateCustomVariableValue updates the value of an existing custom variable.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - variableID: The variable UUID
//   - newValue: The new value
//
// Returns:
//   - error: Any error that occurred
func (c *Client) UpdateCustomVariableValue(ctx context.Context, testID, variableID, newValue string) error {
	body := map[string]string{
		"test_uid":    testID,
		"variable_id": variableID,
		"new_value":   newValue,
	}
	resp, err := c.doRequest(ctx, "PUT", "/api/v1/variables/custom/update_value", body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// RenameCustomVariable updates the name of an existing custom variable.
//
// Parameters:
//   - ctx: Context for cancellation
//   - variableID: The variable UUID
//   - newName: The new variable name
//
// Returns:
//   - error: Any error that occurred
func (c *Client) RenameCustomVariable(ctx context.Context, variableID, newName string) error {
	body := map[string]string{
		"variable_name": newName,
	}
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/variables/custom/update/%s", variableID), body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// DeleteCustomVariable deletes a custom variable by name.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - name: Variable name to delete
//
// Returns:
//   - error: Any error that occurred
func (c *Client) DeleteCustomVariable(ctx context.Context, testID, name string) error {
	path := fmt.Sprintf("/api/v1/variables/custom/delete?test_uid=%s&variable_name=%s", testID, name)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// DeleteAllCustomVariables deletes all custom variables for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//
// Returns:
//   - error: Any error that occurred
func (c *Client) DeleteAllCustomVariables(ctx context.Context, testID string) error {
	path := fmt.Sprintf("/api/v1/variables/custom/delete_all?test_uid=%s", testID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// CheckCustomVariableExists checks whether a variable with the given name exists for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - name: Variable name to check
//
// Returns:
//   - *CheckVariableExistsResponse: Whether the variable exists and its ID
//   - error: Any error that occurred
func (c *Client) CheckCustomVariableExists(ctx context.Context, testID, name string) (*CheckVariableExistsResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/custom/check_exists?test_id=%s&variable_name=%s", testID, name)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CheckVariableExistsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// --- Test Duplication ---

// DuplicateTest creates a copy of an existing test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The duplication request (uses generated DuplicateTestRequest)
//
// Returns:
//   - *DuplicateTestResponse: The newly created duplicate test
//   - error: Any error that occurred
func (c *Client) DuplicateTest(ctx context.Context, req *DuplicateTestRequest) (*DuplicateTestResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/duplicate", req)
	if err != nil {
		return nil, err
	}
	var result DuplicateTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Test Versioning ---

// GetTestVersions lists version history for a test.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//
// Returns:
//   - *TestVersionListResponse: The version history (uses generated type)
//   - error: Any error that occurred
func (c *Client) GetTestVersions(ctx context.Context, testID string) (*TestVersionListResponse, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/tests/%s/versions", testID), nil)
	if err != nil {
		return nil, err
	}
	var result TestVersionListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RestoreTestVersion restores a test to a specific version.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testID: The test UUID
//   - version: The version number to restore
//
// Returns:
//   - error: Any error that occurred
func (c *Client) RestoreTestVersion(ctx context.Context, testID string, version int) error {
	body := TestRestoreVersionRequest{Version: version}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/v1/tests/%s/restore", testID), body)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Workflow Test Management ---

// WorkflowTestWithPolicy represents a workflow-test attachment with failure policy.
type WorkflowTestWithPolicy struct {
	WorkflowTestID string `json:"id"`
	TestID         string `json:"test_id"`
	TestName       string `json:"test_name,omitempty"`
	Platform       string `json:"platform,omitempty"`
	FailurePolicy  string `json:"failure_policy,omitempty"`
}

// GetWorkflowTestsWithPolicy lists workflow tests with their failure policies.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowID: The workflow UUID
//
// Returns:
//   - []WorkflowTestWithPolicy: The workflow tests with policies
//   - error: Any error that occurred
func (c *Client) GetWorkflowTestsWithPolicy(ctx context.Context, workflowID string) ([]WorkflowTestWithPolicy, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/workflows/workflow/%s/tests", workflowID), nil)
	if err != nil {
		return nil, err
	}

	var wrapper struct {
		Data []WorkflowTestWithPolicy `json:"data"`
	}
	if err := parseResponse(resp, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Data, nil
}

// UpdateWorkflowTestFailurePolicy updates the failure policy for a test in a workflow.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowTestID: The workflow-test attachment UUID
//   - policy: The failure policy ("fail_workflow" or "ignore_failure")
//
// Returns:
//   - error: Any error that occurred
func (c *Client) UpdateWorkflowTestFailurePolicy(ctx context.Context, workflowTestID string, policy string) error {
	body := map[string]string{"failure_policy": policy}
	resp, err := c.doRequest(ctx, "PATCH", fmt.Sprintf("/api/v1/workflows/workflow-tests/%s", workflowTestID), body)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// UpdateWorkflowTests atomically replaces the test list for a workflow.
//
// Parameters:
//   - ctx: Context for cancellation
//   - workflowID: The workflow UUID
//   - testIDs: Full replacement list of test UUIDs
//
// Returns:
//   - error: Any error that occurred (400 if invalid test IDs, 404 if workflow not found)
func (c *Client) UpdateWorkflowTests(ctx context.Context, workflowID string, testIDs []string) error {
	path := fmt.Sprintf("/api/v1/workflows/update_tests/%s", workflowID)
	resp, err := c.doRequest(ctx, "PUT", path, testIDs)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// ---------------------------------------------------------------------------
// Org Files
// ---------------------------------------------------------------------------

// CLIOrgFile represents a file in the organization.
type CLIOrgFile struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id"`
	UserID      string `json:"user_id"`
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// CLIOrgFileListResponse represents the response from listing org files.
type CLIOrgFileListResponse struct {
	Files []CLIOrgFile `json:"files"`
	Count int          `json:"count"`
}

// CLIOrgFileUploadRequest represents the request to get a presigned upload URL.
// Also reused for replace-url (same request shape).
type CLIOrgFileUploadRequest struct {
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

// CLIOrgFileUploadResponse represents the response from getting an upload URL.
type CLIOrgFileUploadResponse struct {
	File        CLIOrgFile `json:"file"`
	UploadURL   string     `json:"upload_url"`
	S3Key       string     `json:"s3_key,omitempty"`
	ExpiresIn   int        `json:"expires_in"`
	ContentType string     `json:"content_type"`
}

// CLIOrgFileDownloadResponse represents the response from getting a download URL.
type CLIOrgFileDownloadResponse struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	ExpiresIn int    `json:"expires_in"`
}

// CLIOrgFileCompleteUploadRequest represents the request to confirm a file upload.
type CLIOrgFileCompleteUploadRequest struct {
	S3Key       string `json:"s3_key,omitempty"`
	Filename    string `json:"filename,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
}

// CLIOrgFileUpdateRequest represents the request to update file metadata.
type CLIOrgFileUpdateRequest struct {
	Filename    *string `json:"filename,omitempty"`
	Description *string `json:"description,omitempty"`
}

// ListOrgFiles fetches all files for the authenticated user's organization.
func (c *Client) ListOrgFiles(ctx context.Context, limit, offset int) (*CLIOrgFileListResponse, error) {
	path := fmt.Sprintf("/api/v1/files/?limit=%d&offset=%d", limit, offset)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result CLIOrgFileListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetOrgFileUploadURL gets a presigned URL for uploading a new file.
func (c *Client) GetOrgFileUploadURL(ctx context.Context, req *CLIOrgFileUploadRequest) (*CLIOrgFileUploadResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/files/upload-url", req)
	if err != nil {
		return nil, err
	}
	var result CLIOrgFileUploadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetOrgFileDownloadURL gets a presigned URL for downloading a file.
func (c *Client) GetOrgFileDownloadURL(ctx context.Context, fileID string) (*CLIOrgFileDownloadResponse, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/files/%s/download-url", fileID), nil)
	if err != nil {
		return nil, err
	}
	var result CLIOrgFileDownloadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateOrgFile updates file metadata (filename and/or description).
func (c *Client) UpdateOrgFile(ctx context.Context, fileID string, req *CLIOrgFileUpdateRequest) (*CLIOrgFile, error) {
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/files/%s", fileID), req)
	if err != nil {
		return nil, err
	}
	var result CLIOrgFile
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetOrgFileReplaceURL gets a presigned URL for replacing a file's content.
// The file ID is preserved so revyl-file:// references remain valid.
func (c *Client) GetOrgFileReplaceURL(ctx context.Context, fileID string, req *CLIOrgFileUploadRequest) (*CLIOrgFileUploadResponse, error) {
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/files/%s/replace-url", fileID), req)
	if err != nil {
		return nil, err
	}
	var result CLIOrgFileUploadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteOrgFile deletes a file.
func (c *Client) DeleteOrgFile(ctx context.Context, fileID string) error {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/files/%s", fileID), nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == 204 {
		resp.Body.Close()
		return nil
	}
	return parseResponse(resp, nil)
}

// CompleteOrgFileUpload confirms that an S3 upload completed successfully.
func (c *Client) CompleteOrgFileUpload(ctx context.Context, fileID string, req *CLIOrgFileCompleteUploadRequest) (*CLIOrgFile, error) {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/files/%s/complete-upload", fileID), req)
	if err != nil {
		return nil, err
	}
	var result CLIOrgFile
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UploadOrgFile uploads a file using the presigned URL flow.
// It stats the file, infers content type, gets a presigned URL, and uploads to S3.
func (c *Client) UploadOrgFile(ctx context.Context, filePath, displayName, description string) (*CLIOrgFile, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	filename := filepath.Base(filePath)
	contentType := mime.TypeByExtension(filepath.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	dn := displayName
	if dn == "" {
		dn = filename
	}

	presign, err := c.GetOrgFileUploadURL(ctx, &CLIOrgFileUploadRequest{
		Filename:    filename,
		FileSize:    info.Size(),
		DisplayName: dn,
		Description: description,
		ContentType: contentType,
	})
	if err != nil {
		return nil, err
	}

	if err := c.uploadFileWithRetry(ctx, presign.UploadURL, presign.ContentType, filePath, info.Size()); err != nil {
		return nil, err
	}

	confirmed, err := c.CompleteOrgFileUpload(ctx, presign.File.ID, &CLIOrgFileCompleteUploadRequest{})
	if err != nil {
		return nil, fmt.Errorf("file uploaded to storage but failed to confirm: %w", err)
	}
	return confirmed, nil
}

// ReplaceOrgFileContent replaces a file's content using the presigned URL flow.
// The file ID is preserved so revyl-file:// references remain valid.
func (c *Client) ReplaceOrgFileContent(ctx context.Context, fileID, filePath, displayName, description string) (*CLIOrgFile, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	filename := filepath.Base(filePath)
	contentType := mime.TypeByExtension(filepath.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	dn := displayName
	if dn == "" {
		dn = filename
	}

	presign, err := c.GetOrgFileReplaceURL(ctx, fileID, &CLIOrgFileUploadRequest{
		Filename:    filename,
		FileSize:    info.Size(),
		DisplayName: dn,
		Description: description,
		ContentType: contentType,
	})
	if err != nil {
		return nil, err
	}

	if err := c.uploadFileWithRetry(ctx, presign.UploadURL, presign.ContentType, filePath, info.Size()); err != nil {
		return nil, err
	}

	confirmed, err := c.CompleteOrgFileUpload(ctx, fileID, &CLIOrgFileCompleteUploadRequest{
		S3Key:       presign.S3Key,
		Filename:    dn,
		FileSize:    info.Size(),
		ContentType: contentType,
		Description: description,
	})
	if err != nil {
		return nil, fmt.Errorf("file uploaded to storage but failed to confirm replacement: %w", err)
	}
	return confirmed, nil
}

// DownloadFileFromURL downloads a file from a URL to a local path.
// It uses atomic writes (temp file + rename) and cleans up on failure.
func (c *Client) DownloadFileFromURL(ctx context.Context, fileURL, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := c.uploadClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxUploadErrorBodyBytes))
		return formatUploadStatusError(resp.StatusCode, body, nil)
	}

	// Write to temp file in same directory for atomic rename.
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".revyl-download-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	_, copyErr := io.Copy(tmp, resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write file: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to move file to destination: %w", err)
	}

	return nil
}

// --- Global Variable API methods ---

// Global variable response models are generated in generated.go from OpenAPI.

// ListGlobalVariables retrieves all global variables for the authenticated user's org.
func (c *Client) ListGlobalVariables(ctx context.Context) (*GlobalVariablesResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/variables/global", nil)
	if err != nil {
		return nil, err
	}

	var result GlobalVariablesResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// AddGlobalVariable creates a new global variable for the authenticated user's org.
func (c *Client) AddGlobalVariable(ctx context.Context, name, value string) (*GlobalVariableResponse, error) {
	body := map[string]interface{}{
		"variable_name":  name,
		"variable_value": value,
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/variables/global", body)
	if err != nil {
		return nil, err
	}

	var result GlobalVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateGlobalVariable updates an existing global variable by its UUID.
func (c *Client) UpdateGlobalVariable(ctx context.Context, variableID, name, value string) (*GlobalVariableResponse, error) {
	body := map[string]interface{}{
		"variable_name":  name,
		"variable_value": value,
	}
	path := fmt.Sprintf("/api/v1/variables/global/%s", variableID)
	resp, err := c.doRequest(ctx, "PUT", path, body)
	if err != nil {
		return nil, err
	}

	var result GlobalVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteGlobalVariable deletes a global variable by its UUID.
func (c *Client) DeleteGlobalVariable(ctx context.Context, variableID string) error {
	path := fmt.Sprintf("/api/v1/variables/global/%s", variableID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

// ---------------------------------------------------------------------------
// Session artifact upload (ephemeral presigned URLs for dev-push deltas)
// ---------------------------------------------------------------------------

// GetSessionArtifactUploadURL generates presigned S3 URLs for an ephemeral
// artifact scoped to the given device session. No database record is created.
//
// Parameters:
//   - ctx: cancellation context
//   - sessionID: the device session UUID
//   - req: file size and content type
//
// Returns:
//   - SessionArtifactUploadResponse with upload and download URLs
//   - error: API or network failure
func (c *Client) GetSessionArtifactUploadURL(ctx context.Context, sessionID string, req *SessionArtifactUploadRequest) (*SessionArtifactUploadResponse, error) {
	path := fmt.Sprintf("/api/v1/execution/device-sessions/%s/artifacts/upload-url", sessionID)
	resp, err := c.doRequest(ctx, "POST", path, req)
	if err != nil {
		return nil, err
	}
	var result SessionArtifactUploadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
