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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// debugLogf emits verbose upload diagnostics (part counts, per-part retries).
// A no-op unless the CLI wires a logger via SetDebugLogger at startup.
var debugLogf = func(format string, args ...interface{}) {}

// SetDebugLogger routes api-package debug output to the given printf-style
// logger (typically ui.PrintDebug, which itself gates on --debug).
func SetDebugLogger(fn func(format string, args ...interface{})) {
	if fn != nil {
		debugLogf = fn
	}
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
	// uploadStateDir overrides where resumable-upload records are stored.
	// Empty resolves lazily to ~/.revyl/uploads; tests point it at a temp dir.
	uploadStateDir string
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

// BaseURL returns the resolved backend base URL for this client.
func (c *Client) BaseURL() string {
	return c.baseURL
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

// HotReloadRelayCreateParams provisions a new backend-owned hot reload relay.
type HotReloadRelayCreateParams struct {
	Provider string `json:"provider,omitempty"`
	Platform string `json:"platform,omitempty"`
}

// HotReloadRelaySession describes a backend-owned hot reload relay session.
type HotReloadRelaySession struct {
	RelayID      string `json:"relay_id"`
	PublicURL    string `json:"public_url"`
	ConnectURL   string `json:"connect_url"`
	ConnectToken string `json:"connect_token"`
	Transport    string `json:"transport"`
	ExpiresAt    string `json:"expires_at"`
}

// ConnectWebSocketURL returns the relay websocket URL without embedding auth material.
func (s *HotReloadRelaySession) ConnectWebSocketURL() (string, error) {
	if s == nil {
		return "", fmt.Errorf("relay session is nil")
	}
	connectURL := strings.TrimSpace(s.ConnectURL)
	if connectURL == "" {
		return "", fmt.Errorf("relay connect url is empty")
	}
	if _, err := url.Parse(connectURL); err != nil {
		return "", fmt.Errorf("invalid relay connect url: %w", err)
	}
	return connectURL, nil
}

// ConnectAuthHeader returns the Authorization header value for relay websocket authentication.
func (s *HotReloadRelaySession) ConnectAuthHeader() string {
	if s == nil {
		return ""
	}
	token := strings.TrimSpace(s.ConnectToken)
	if token == "" {
		return ""
	}
	return "Bearer " + token
}

// HotReloadRelayHeartbeatStatus is returned by the relay heartbeat endpoint.
type HotReloadRelayHeartbeatStatus struct {
	RelayID   string `json:"relay_id"`
	ExpiresAt string `json:"expires_at"`
	Active    bool   `json:"active"`
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
		return "Your free device time is used up. Add a payment method for more free time or choose a plan:\n  → revyl auth billing"
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
		Errors  []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
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

	if len(errResp.Errors) > 0 {
		var parts []string
		for _, validationErr := range errResp.Errors {
			part := strings.TrimSpace(validationErr.Message)
			field := strings.TrimSpace(validationErr.Field)
			if field != "" && part != "" {
				part = fmt.Sprintf("%s: %s", field, part)
			} else if field != "" {
				part = field
			}
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			validationDetail := strings.Join(parts, "; ")
			if detail == "" || detail == "null" {
				detail = validationDetail
			} else if !strings.Contains(detail, validationDetail) {
				detail = strings.TrimSpace(detail + "; " + validationDetail)
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
	return c.doRequestWithRetry(ctx, method, path, body, nil)
}

// doRequestOnce performs a single HTTP request without retries.
// Use this for endpoints where retrying is unlikely to help (e.g. deterministic failures).
func (c *Client) doRequestOnce(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	return c.doRequestOnceWithClient(ctx, method, path, body, c.httpClient)
}

func (c *Client) doRequestOnceWithClient(ctx context.Context, method, path string, body interface{}, client *http.Client) (*http.Response, error) {
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

	if strings.TrimSpace(c.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	setCIHeaders(req)

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
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
func (c *Client) doRequestWithRetry(ctx context.Context, method, path string, body interface{}, extraHeaders ...map[string]string) (*http.Response, error) {
	headers := map[string]string(nil)
	if len(extraHeaders) > 0 {
		headers = extraHeaders[0]
	}
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

		if strings.TrimSpace(c.apiKey) != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", c.userAgent())

		// Set source tracking header
		// X-Revyl-Client identifies the client type for backend source classification.
		req.Header.Set("X-Revyl-Client", "cli")
		setCIHeaders(req)
		for key, value := range headers {
			if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
				req.Header.Set(key, value)
			}
		}

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
	// FailFast halts the run on the first failed step or validation when set.
	// Pointer so a nil value is omitted from the request and the backend
	// falls back to the test's stored run_config.
	FailFast *bool `json:"fail_fast,omitempty"`
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
	// LaunchEnvVars are inline launch environment variables (KEY=VALUE) applied to
	// the app's launch environment; they override org launch vars attached to the test.
	LaunchEnvVars map[string]string `json:"launch_env_vars,omitempty"`
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
	VersionID string   `json:"version_id"`
	Version   string   `json:"version"`
	PackageID string   `json:"package_id,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

const maxUploadErrorBodyBytes = 16 * 1024

const androidSplitAPKError = "Android split APKs and APK Set archives are not supported. Upload one installable APK instead, preferably a universal/fat APK or an x86_64-specific APK"

// uploadSessionResponse mirrors the backend BuildUploadSessionResponse.
type uploadSessionResponse struct {
	UploadID    string `json:"upload_id"`
	UploadURL   string `json:"upload_url"`
	ContentType string `json:"content_type"`
}

// buildCreatedResponse mirrors the subset of BuildVersionResponse the CLI uses.
type buildCreatedResponse struct {
	ID          string                 `json:"id"`
	Version     string                 `json:"version"`
	PackageName string                 `json:"package_name,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type extractBuildPackageIDResponse struct {
	PackageID string `json:"package_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// UploadBuild uploads a build artifact. Files at or under
// multipartUploadThresholdBytes go through the staging upload-session flow
// (single presigned PUT); larger files use an S3 multipart upload so a
// mid-transfer failure only retries the affected part instead of the whole
// artifact.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The upload request
//
// Returns:
//   - *UploadBuildResponse: The upload response
//   - error: Any error that occurred
func (c *Client) UploadBuild(ctx context.Context, req *UploadBuildRequest) (*UploadBuildResponse, error) {
	if err := validateLocalBuildArtifact(req.FilePath, req.Metadata); err != nil {
		return nil, err
	}

	fileName := filepath.Base(req.FilePath)
	fileInfo, err := os.Stat(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	if fileInfo.Size() > multipartUploadThresholdBytes {
		return c.uploadBuildMultipart(ctx, req, fileName, fileInfo.Size())
	}
	return c.uploadBuildViaSession(ctx, req, fileName, fileInfo.Size())
}

// uploadBuildViaSession uploads via the staging upload-session flow: open a
// session, PUT to the presigned staging URL, then finalize via
// createBuildFromStagedUpload.
func (c *Client) uploadBuildViaSession(ctx context.Context, req *UploadBuildRequest, fileName string, fileSize int64) (*UploadBuildResponse, error) {
	sessionResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds/upload-session?source=cli_upload", req.AppID),
		map[string]interface{}{
			"version":   req.Version,
			"file_name": fileName,
		})
	if err != nil {
		return nil, err
	}
	var session uploadSessionResponse
	if err := parseResponse(sessionResp, &session); err != nil {
		if isUploadSessionUnsupported(err) {
			return c.uploadBuildLegacyPresigned(ctx, req, fileName)
		}
		return nil, err
	}

	if err := c.uploadFileWithRetry(
		ctx,
		session.UploadURL,
		session.ContentType,
		req.FilePath,
		fileSize,
	); err != nil {
		return nil, err
	}

	return c.createBuildFromStagedUpload(ctx, req, session.UploadID)
}

// createBuildFromStagedUpload finalizes a staging upload (single PUT or
// multipart): one create call validates the staged artifact server-side and
// inserts the build row. No build row exists until validation passes, so
// there is nothing to clean up on failure — abandoned staging objects are
// swept server-side on later session starts.
func (c *Client) createBuildFromStagedUpload(ctx context.Context, req *UploadBuildRequest, uploadID string) (*UploadBuildResponse, error) {
	metadata := map[string]interface{}{"source": "cli_upload"}
	for key, value := range req.Metadata {
		metadata[key] = value
	}

	createResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds", req.AppID),
		map[string]interface{}{
			"upload_id":      uploadID,
			"version":        req.Version,
			"metadata":       metadata,
			"set_as_current": req.SetAsCurrent,
		})
	if err != nil {
		return nil, err
	}
	var created buildCreatedResponse
	if err := parseResponse(createResp, &created); err != nil {
		return nil, err
	}

	return &UploadBuildResponse{
		VersionID: created.ID,
		Version:   created.Version,
		PackageID: created.PackageName,
		Warnings:  artifactValidationWarnings(created.Metadata),
	}, nil
}

func isUploadSessionUnsupported(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	if apiErr.StatusCode == http.StatusMethodNotAllowed {
		return true
	}
	if apiErr.StatusCode != http.StatusNotFound {
		return false
	}
	// A 404 can also be a matched route reporting a missing resource ("App
	// not found"), which no fallback endpoint can fix — without this check a
	// bad app id would cascade through every legacy flow and surface its
	// error from the wrong endpoint.
	detail := strings.ToLower(apiErr.Detail + " " + apiErr.Message)
	return !strings.Contains(detail, "app not found")
}

func artifactValidationWarnings(metadata map[string]interface{}) []string {
	validation, ok := metadata["artifact_validation"].(map[string]interface{})
	if !ok {
		return nil
	}
	rawWarnings, ok := validation["warnings"].([]interface{})
	if !ok {
		return nil
	}
	warnings := make([]string, 0, len(rawWarnings))
	for _, raw := range rawWarnings {
		if warning, ok := raw.(string); ok && strings.TrimSpace(warning) != "" {
			warnings = append(warnings, warning)
		}
	}
	return warnings
}

func (c *Client) uploadBuildLegacyPresigned(ctx context.Context, req *UploadBuildRequest, fileName string) (*UploadBuildResponse, error) {
	uploadURLPath := fmt.Sprintf(
		"/api/v1/builds/vars/%s/versions/upload-url?version=%s&file_name=%s&source=cli_upload",
		req.AppID,
		url.QueryEscape(req.Version),
		url.QueryEscape(fileName),
	)

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
		c.bestEffortDeleteBuildVersion(presignResult.VersionID)
		return nil, err
	}

	return c.finalizeRowBackedUpload(ctx, req, presignResult.VersionID)
}

// finalizeRowBackedUpload validates an already-uploaded artifact server-side
// (extract-package-id) and finalizes its pending build row (complete-upload)
// for the legacy presigned PUT flow, which creates the build row before
// uploading; on any failure the pending row is deleted best-effort.
func (c *Client) finalizeRowBackedUpload(ctx context.Context, req *UploadBuildRequest, versionID string) (*UploadBuildResponse, error) {
	extractResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/builds/versions/%s/extract-package-id", versionID),
		nil)
	if err != nil {
		c.bestEffortDeleteBuildVersion(versionID)
		return nil, err
	}
	var extractResult extractBuildPackageIDResponse
	if err := parseResponse(extractResp, &extractResult); err != nil {
		c.bestEffortDeleteBuildVersion(versionID)
		return nil, err
	}
	if extractResult.Error != "" {
		c.bestEffortDeleteBuildVersion(versionID)
		return nil, fmt.Errorf("failed to validate uploaded build: %s", extractResult.Error)
	}
	if extractResult.PackageID == "" {
		c.bestEffortDeleteBuildVersion(versionID)
		return nil, fmt.Errorf("failed to validate uploaded build: package ID could not be extracted")
	}

	metadata := map[string]interface{}{"source": "cli_upload"}
	for key, value := range req.Metadata {
		metadata[key] = value
	}
	metadata["package_id"] = extractResult.PackageID

	completeResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/builds/versions/%s/complete-upload", versionID),
		map[string]interface{}{
			"version_id":     versionID,
			"metadata":       metadata,
			"package_name":   extractResult.PackageID,
			"set_as_current": req.SetAsCurrent,
		})
	if err != nil {
		c.bestEffortDeleteBuildVersion(versionID)
		return nil, err
	}

	var completeResult UploadBuildResponse
	if err := parseResponse(completeResp, &completeResult); err != nil {
		c.bestEffortDeleteBuildVersion(versionID)
		return nil, err
	}
	if completeResult.VersionID == "" {
		completeResult.VersionID = versionID
	}
	if completeResult.PackageID == "" {
		completeResult.PackageID = extractResult.PackageID
	}
	return &completeResult, nil
}

// multipartUploadThresholdBytes is the file size above which UploadBuild
// switches from a single presigned PUT to an S3 multipart upload. A var so
// tests can lower it.
var multipartUploadThresholdBytes int64 = 64 * 1024 * 1024

// defaultMultipartUploadConcurrency is how many parts upload in parallel when
// REVYL_UPLOAD_CONCURRENCY is unset.
const defaultMultipartUploadConcurrency = 4

// maxMultipartUploadConcurrency caps REVYL_UPLOAD_CONCURRENCY so a runaway value
// can't open thousands of simultaneous connections.
const maxMultipartUploadConcurrency = 64

// uploadMaxStallPasses is how many consecutive part-upload passes may make zero
// progress while already at a single connection before the run gives up and
// leaves the rest to be resumed later.
const uploadMaxStallPasses = 3

// uploadConcurrency resolves how many parts upload in parallel, honoring
// REVYL_UPLOAD_CONCURRENCY so users on a constrained or proxied uplink can cap
// (or serialize with =1) the simultaneous PUTs that otherwise saturate the link
// and cause connection resets.
func uploadConcurrency() int {
	if v := strings.TrimSpace(os.Getenv("REVYL_UPLOAD_CONCURRENCY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			// Clamp the upper end: an unbounded value would spawn thousands of
			// simultaneous PUTs/goroutines and worsen the saturated-uplink
			// failure this exists to fix.
			if n > maxMultipartUploadConcurrency {
				n = maxMultipartUploadConcurrency
			}
			return n
		}
		debugLogf("ignoring invalid REVYL_UPLOAD_CONCURRENCY=%q", v)
	}
	return defaultMultipartUploadConcurrency
}

// maxSinglePutUploadBytes is S3's hard cap on a single (non-multipart) PUT.
// A var so tests can lower it.
var maxSinglePutUploadBytes int64 = 5 * 1024 * 1024 * 1024

// multipartUploadPartURL mirrors the backend BuildMultipartUploadPartURL.
type multipartUploadPartURL struct {
	PartNumber int    `json:"part_number"`
	UploadURL  string `json:"upload_url"`
}

// multipartUploadStartResponse mirrors the backend
// BuildMultipartUploadStartResponse.
type multipartUploadStartResponse struct {
	UploadID    string                   `json:"upload_id"`
	S3UploadID  string                   `json:"s3_upload_id"`
	PartSize    int64                    `json:"part_size"`
	Parts       []multipartUploadPartURL `json:"parts"`
	CompleteURL string                   `json:"complete_url"`
	AbortURL    string                   `json:"abort_url"`
}

// multipartResumeResponse mirrors the backend
// BuildMultipartUploadResumeResponse.
type multipartResumeResponse struct {
	UploadID      string                   `json:"upload_id"`
	S3UploadID    string                   `json:"s3_upload_id"`
	PartSize      int64                    `json:"part_size"`
	Parts         []multipartUploadPartURL `json:"parts"`
	UploadedParts []struct {
		PartNumber int    `json:"part_number"`
		ETag       string `json:"etag"`
		Size       int64  `json:"size"`
	} `json:"uploaded_parts"`
	CompleteURL string `json:"complete_url"`
	AbortURL    string `json:"abort_url"`
}

// multipartCompletedPart is the ETag collected for one uploaded part.
type multipartCompletedPart struct {
	PartNumber int
	ETag       string
}

// multipartPlan is everything driveMultipartUpload needs to finish an upload,
// whether it was freshly started or resumed from an earlier interruption.
type multipartPlan struct {
	uploadID    string
	partSize    int64
	parts       []multipartUploadPartURL
	uploaded    map[int]string // part number -> ETag for parts already in S3
	completeURL string
	abortURL    string
}

// uploadInterruptedError marks a part-upload run that stopped before finishing
// without an unrecoverable cause: the parts so far are safe in S3 and the
// resume record is kept, so a re-run continues. The driver detects it to append
// the "re-run to resume" hint exactly once.
type uploadInterruptedError struct{ err error }

func (e *uploadInterruptedError) Error() string { return e.err.Error() }
func (e *uploadInterruptedError) Unwrap() error { return e.err }

// permanentUploadError marks a part failure that re-uploading cannot fix — a
// non-retryable S3 status, a missing ETag (a header-stripping proxy), or a part
// out of range. The driver abandons the upload instead of looping or keeping
// resume state, because no re-run would do better.
type permanentUploadError struct{ err error }

func (e *permanentUploadError) Error() string { return e.err.Error() }
func (e *permanentUploadError) Unwrap() error { return e.err }

// uploadBuildMultipart uploads a large artifact via S3 multipart upload into
// the staging area. If an earlier attempt on this exact artifact was
// interrupted, it resumes — asking S3 which parts it already holds and
// uploading only the rest; otherwise it starts a fresh upload. Either way it
// records resume state before transferring any part, so a dropped connection,
// crash, or Ctrl-C never throws away completed parts: the next run continues
// from where it stopped.
func (c *Client) uploadBuildMultipart(ctx context.Context, req *UploadBuildRequest, fileName string, fileSize int64) (*UploadBuildResponse, error) {
	fileHash, err := fileSHA256(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	stateKey := uploadStateKey(req.AppID, req.Version, req.FilePath, fileSize)

	if st := c.loadResumableUploadState(stateKey); st != nil {
		if !st.matches(fileSize, fileHash) {
			// The artifact was rebuilt; the parts in S3 belong to a stale
			// build, so abandon them (lifecycle rule sweeps them) and restart.
			debugLogf("multipart upload: artifact changed since saved upload; starting fresh")
			c.deleteResumableUploadState(stateKey)
		} else if st.Assembled {
			// A prior run finished assembling the object but failed at the
			// create call; the multipart session is gone, so skip straight to
			// finalize rather than re-listing parts (which would 410) and
			// re-uploading the whole artifact.
			debugLogf("multipart upload: object already assembled; finalizing")
			return c.finalizeStagedUpload(ctx, req, stateKey, st.UploadID)
		} else if plan, rerr := c.resumeMultipartPlan(ctx, req, fileName, fileSize, st); rerr == nil {
			debugLogf(
				"multipart upload: resuming, %d/%d parts already in S3 (concurrency %d)",
				len(plan.uploaded), len(plan.parts), uploadConcurrency(),
			)
			return c.driveMultipartUpload(ctx, req, fileSize, stateKey, plan)
		} else if resumeSessionUnusable(rerr) {
			// The stored upload can't be resumed (gone/expired, a version
			// conflict, or parts inconsistent with the file) — discarding it and
			// starting fresh either succeeds or surfaces the same clear error at
			// start, instead of looping the same failure on every re-run.
			debugLogf("multipart upload: stored upload unusable (%v); starting fresh", rerr)
			c.deleteResumableUploadState(stateKey)
		} else {
			// Transient resume failure (network/5xx). Keep the state so the
			// user can simply re-run rather than restart from zero.
			return nil, rerr
		}
	}

	startResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds/multipart-upload/start?source=cli_upload", req.AppID),
		map[string]interface{}{
			"version":   req.Version,
			"file_name": fileName,
			"file_size": fileSize,
		})
	if err != nil {
		return nil, err
	}
	var start multipartUploadStartResponse
	if err := parseResponse(startResp, &start); err != nil {
		if isUploadSessionUnsupported(err) {
			// Older backends without multipart support: the single PUT still
			// works below S3's 5 GiB PUT cap, it is just less resilient for
			// large artifacts. Above the cap the PUT can never succeed, so
			// fail fast with the real reason instead of an opaque S3 error
			// after a long dead-end transfer.
			if fileSize > maxSinglePutUploadBytes {
				return nil, fmt.Errorf(
					"this backend does not support multipart uploads and the artifact (%d bytes) exceeds S3's 5 GiB single-upload limit; upgrade the backend to upload artifacts this large",
					fileSize,
				)
			}
			return c.uploadBuildViaSession(ctx, req, fileName, fileSize)
		}
		return nil, err
	}
	if start.PartSize <= 0 || len(start.Parts) == 0 || start.CompleteURL == "" ||
		int64(len(start.Parts))*start.PartSize < fileSize {
		c.bestEffortAbortMultipartUpload(start.AbortURL)
		return nil, fmt.Errorf(
			"multipart upload start returned %d parts of %d bytes for a %d byte file",
			len(start.Parts), start.PartSize, fileSize,
		)
	}

	debugLogf(
		"multipart upload: %d parts of %d bytes (file %d bytes, concurrency %d)",
		len(start.Parts), start.PartSize, fileSize, uploadConcurrency(),
	)

	// Persist the resume record before uploading any part so an interruption at
	// any point is recoverable. s3_upload_id lets resume re-presign the upload.
	c.saveResumableUploadState(stateKey, &resumableUploadState{
		UploadID:   start.UploadID,
		S3UploadID: start.S3UploadID,
		AppID:      req.AppID,
		Version:    req.Version,
		FileName:   fileName,
		FilePath:   req.FilePath,
		FileSize:   fileSize,
		FileHash:   fileHash,
		PartSize:   start.PartSize,
	})

	return c.driveMultipartUpload(ctx, req, fileSize, stateKey, &multipartPlan{
		uploadID:    start.UploadID,
		partSize:    start.PartSize,
		parts:       start.Parts,
		completeURL: start.CompleteURL,
		abortURL:    start.AbortURL,
	})
}

// resumeMultipartPlan asks the backend to re-presign an interrupted upload and
// report which parts S3 already holds. A listed part is trusted only when its
// stored size matches what this artifact expects for that part number, so a
// mismatched or partial part is simply re-uploaded.
func (c *Client) resumeMultipartPlan(ctx context.Context, req *UploadBuildRequest, fileName string, fileSize int64, st *resumableUploadState) (*multipartPlan, error) {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds/multipart-upload/resume", req.AppID),
		map[string]interface{}{
			"upload_id":    st.UploadID,
			"s3_upload_id": st.S3UploadID,
			"version":      req.Version,
			"file_name":    fileName,
			"file_size":    fileSize,
		})
	if err != nil {
		return nil, err
	}
	var resume multipartResumeResponse
	if err := parseResponse(resp, &resume); err != nil {
		return nil, err
	}
	if resume.PartSize <= 0 || len(resume.Parts) == 0 || resume.CompleteURL == "" ||
		int64(len(resume.Parts))*resume.PartSize < fileSize {
		return nil, fmt.Errorf(
			"multipart upload resume returned %d parts of %d bytes for a %d byte file",
			len(resume.Parts), resume.PartSize, fileSize,
		)
	}

	// Trust a part S3 reports only when it has a non-empty ETag and its stored
	// size matches what this artifact expects for that part number; a mismatched,
	// partial, or ETag-less part is dropped here so it gets re-uploaded rather
	// than poisoning the final CompleteMultipartUpload with a bad ETag.
	uploaded := make(map[int]string, len(resume.UploadedParts))
	for _, p := range resume.UploadedParts {
		if p.ETag == "" {
			continue
		}
		if expected := partLength(p.PartNumber, resume.PartSize, fileSize); expected > 0 && p.Size == expected {
			uploaded[p.PartNumber] = p.ETag
		}
	}

	return &multipartPlan{
		uploadID:    resume.UploadID,
		partSize:    resume.PartSize,
		parts:       resume.Parts,
		uploaded:    uploaded,
		completeURL: resume.CompleteURL,
		abortURL:    resume.AbortURL,
	}, nil
}

// driveMultipartUpload uploads the parts S3 doesn't already have, assembles the
// object, finalizes the build, and clears the resume record on success. Transfer
// failures route through classifyUploadFailure: a transient interruption keeps
// the parts and resume record so the next run continues, while an unrecoverable
// failure abandons the upload outright.
func (c *Client) driveMultipartUpload(ctx context.Context, req *UploadBuildRequest, fileSize int64, stateKey string, plan *multipartPlan) (*UploadBuildResponse, error) {
	completed, err := c.uploadMultipartParts(ctx, req.FilePath, fileSize, plan)
	if err != nil {
		return nil, c.classifyUploadFailure(stateKey, plan, err)
	}

	if err := c.completeMultipartUploadAtS3(ctx, plan.completeURL, completed); err != nil {
		return nil, c.classifyUploadFailure(stateKey, plan, err)
	}

	// The object is assembled; record that so a create failure resumes at
	// finalize instead of re-uploading everything.
	c.markUploadAssembled(stateKey)
	return c.finalizeStagedUpload(ctx, req, stateKey, plan.uploadID)
}

// finalizeStagedUpload turns the assembled staging object into a build and
// clears the resume record. A failure here keeps the record (the object is
// safe) so a re-run retries finalize directly — see the Assembled branch in
// uploadBuildMultipart.
func (c *Client) finalizeStagedUpload(ctx context.Context, req *UploadBuildRequest, stateKey, uploadID string) (*UploadBuildResponse, error) {
	resp, err := c.createBuildFromStagedUpload(ctx, req, uploadID)
	if err != nil {
		return nil, fmt.Errorf("%w; re-run the same command to resume from where it stopped", err)
	}
	c.deleteResumableUploadState(stateKey)
	return resp, nil
}

// markUploadAssembled flips the persisted resume record to the assembled phase
// (best effort: if it can't be written, a re-run simply re-uploads as before).
func (c *Client) markUploadAssembled(stateKey string) {
	st := c.loadResumableUploadState(stateKey)
	if st == nil {
		return
	}
	st.Assembled = true
	c.saveResumableUploadState(stateKey, st)
}

// classifyUploadFailure decides whether a failed transfer is resumable. A
// permanent failure (a part or the assembly step S3 will reject on every
// attempt) is checked first — even under cancellation — and abandons the upload
// so a re-run starts clean instead of looping on a transfer that can never
// finish. Anything else (a transient stall, a dropped connection, Ctrl-C) keeps
// the parts and resume record and tells the user to re-run.
func (c *Client) classifyUploadFailure(stateKey string, plan *multipartPlan, err error) error {
	var permanent *permanentUploadError
	if errors.As(err, &permanent) {
		c.bestEffortAbortMultipartUpload(plan.abortURL)
		c.deleteResumableUploadState(stateKey)
		return err
	}
	return fmt.Errorf("%w; re-run the same command to resume from where it stopped", err)
}

// resumeSessionUnusable reports whether a resume failed for a reason that means
// the stored upload can never be resumed: 409 (a version conflict, or parts
// inconsistent with the file) or 410 (gone/expired). Only then should the client
// discard its saved state and start fresh. Every other failure — a transient 4xx
// like 401/403 (token expired mid-upload), 408/429 (timeout/rate limit), or any
// 5xx/network error — keeps the state so a plain re-run resumes instead of
// re-uploading from zero.
func resumeSessionUnusable(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	return apiErr.StatusCode == http.StatusConflict || apiErr.StatusCode == http.StatusGone
}

// partLength returns the byte length of one part, accounting for the short
// final part, or 0 if the part number is out of range for the file.
func partLength(partNumber int, partSize, fileSize int64) int64 {
	offset := int64(partNumber-1) * partSize
	if offset >= fileSize {
		return 0
	}
	if remaining := fileSize - offset; remaining < partSize {
		return remaining
	}
	return partSize
}

// multipartCompletePartXML and multipartCompleteRequestXML form the S3
// CompleteMultipartUpload request body.
type multipartCompletePartXML struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type multipartCompleteRequestXML struct {
	XMLName xml.Name                   `xml:"CompleteMultipartUpload"`
	Parts   []multipartCompletePartXML `xml:"Part"`
}

// completeMultipartUploadAtS3 assembles the uploaded parts by POSTing the
// part ETags to the presigned CompleteMultipartUpload URL. Quirks handled:
// S3 may return 200 with an <Error> document in the body (retryable), and a
// retry whose earlier attempt already assembled the object gets NoSuchUpload
// — treated as success, because the subsequent create call fails cleanly if
// the staged object genuinely doesn't exist. A first-attempt NoSuchUpload
// means the upload was aborted or expired out-of-band, and a non-retryable
// status (e.g. InvalidPart) means the parts list S3 holds can never assemble;
// both are wrapped as *permanentUploadError so the caller abandons the upload
// instead of looping a re-run that re-completes the same broken parts.
func (c *Client) completeMultipartUploadAtS3(ctx context.Context, completeURL string, parts []multipartCompletedPart) error {
	xmlParts := make([]multipartCompletePartXML, len(parts))
	for i, part := range parts {
		xmlParts[i] = multipartCompletePartXML{PartNumber: part.PartNumber, ETag: part.ETag}
	}
	body, err := xml.Marshal(multipartCompleteRequestXML{Parts: xmlParts})
	if err != nil {
		return &permanentUploadError{err: fmt.Errorf("failed to encode complete multipart request: %w", err)}
	}

	var permanent bool
	err = c.retryUploadAttempts(ctx, "complete multipart upload", func(attempt int) (bool, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, completeURL, bytes.NewReader(body))
		if err != nil {
			permanent = true
			return true, fmt.Errorf("failed to create complete multipart request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/xml")

		resp, err := c.uploadClient.Do(httpReq)
		if err != nil {
			return false, fmt.Errorf("complete multipart upload failed: %w", err)
		}
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUploadErrorBodyBytes))
		resp.Body.Close()
		if readErr != nil {
			return false, fmt.Errorf("complete multipart upload failed: %w", readErr)
		}

		payload := string(respBody)
		switch {
		case resp.StatusCode < 400 && strings.Contains(payload, "<CompleteMultipartUploadResult"):
			return true, nil
		case strings.Contains(payload, "NoSuchUpload"):
			if attempt > 0 {
				return true, nil
			}
			permanent = true
			return true, fmt.Errorf(
				"multipart upload no longer exists; it may have been aborted or expired before completion",
			)
		case resp.StatusCode >= 400:
			statusErr := formatUploadStatusError(resp.StatusCode, respBody, nil)
			if !isRetryableUploadStatus(resp.StatusCode) {
				permanent = true
				return true, statusErr
			}
			return false, statusErr
		default:
			return false, fmt.Errorf(
				"complete multipart upload failed: %s",
				strings.TrimSpace(payload),
			)
		}
	})
	if err != nil && permanent {
		return &permanentUploadError{err: err}
	}
	return err
}

// uploadMultipartParts uploads every part not already in S3, retrying across
// passes so a transient drop costs one part, not the whole artifact. Within a
// pass each missing part uploads concurrently with its own retry budget and,
// unlike a fail-fast pass, a sibling failure no longer cancels the rest — so a
// pass makes as much forward progress as the link allows. A pass that makes no
// progress drops to a single connection (the usual cause is a constrained or
// proxied uplink that cannot sustain parallel PUTs); only after repeated
// no-progress passes at one connection does the run give up, returning an
// *uploadInterruptedError so the caller keeps the resume record. Returns all
// parts' ETags (freshly uploaded plus pre-existing) sorted by part number.
func (c *Client) uploadMultipartParts(ctx context.Context, filePath string, fileSize int64, plan *multipartPlan) ([]multipartCompletedPart, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	completed := make(map[int]string, len(plan.parts))
	for partNumber, etag := range plan.uploaded {
		completed[partNumber] = etag
	}

	concurrency := uploadConcurrency()
	stalls := 0
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		missing := make([]multipartUploadPartURL, 0, len(plan.parts))
		for _, part := range plan.parts {
			if _, done := completed[part.PartNumber]; !done {
				missing = append(missing, part)
			}
		}
		if len(missing) == 0 {
			break
		}

		before := len(completed)
		if passErr := c.uploadPartsPass(ctx, file, fileSize, plan.partSize, missing, concurrency, completed); passErr != nil {
			lastErr = passErr
			// A part that can never succeed dooms the artifact; surface it now
			// so the caller abandons the upload rather than looping or saving
			// resume state for a transfer no re-run could finish.
			var pe *permanentUploadError
			if errors.As(passErr, &pe) {
				return nil, passErr
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		if len(completed) > before {
			stalls = 0
			continue
		}

		// No part landed this pass. Shed parallelism first — a single
		// connection is far likelier to survive a saturated uplink — and only
		// give up once even that makes no progress repeatedly.
		if concurrency > 1 {
			concurrency = 1
			debugLogf("multipart upload: no progress, dropping to a single connection")
			continue
		}
		stalls++
		if stalls >= uploadMaxStallPasses {
			if lastErr == nil {
				lastErr = fmt.Errorf("upload stalled with %d of %d parts remaining", len(missing), len(plan.parts))
			}
			return nil, &uploadInterruptedError{err: lastErr}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(calculateBackoff(stalls-1, c.retryBaseDelay, c.retryMaxDelay)):
		}
	}

	result := make([]multipartCompletedPart, 0, len(completed))
	for partNumber, etag := range completed {
		result = append(result, multipartCompletedPart{PartNumber: partNumber, ETag: etag})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PartNumber < result[j].PartNumber })
	return result, nil
}

// uploadPartsPass uploads the given missing parts with the given concurrency,
// recording each success into completed (guarded by mu). It attempts every part
// regardless of sibling failures and returns the last part error seen (nil if
// all succeeded), leaving the caller to decide whether to retry the stragglers.
func (c *Client) uploadPartsPass(ctx context.Context, file *os.File, fileSize, partSize int64, missing []multipartUploadPartURL, concurrency int, completed map[int]string) error {
	if concurrency > len(missing) {
		concurrency = len(missing)
	}

	jobs := make(chan multipartUploadPartURL)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var lastErr error
	var permErr error

	record := func(err error) {
		mu.Lock()
		lastErr = err
		var pe *permanentUploadError
		if permErr == nil && errors.As(err, &pe) {
			permErr = err
		}
		mu.Unlock()
	}

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for part := range jobs {
				if ctx.Err() != nil {
					return
				}
				length := partLength(part.PartNumber, partSize, fileSize)
				if length <= 0 {
					record(&permanentUploadError{err: fmt.Errorf("part %d is out of range for a %d byte file", part.PartNumber, fileSize)})
					continue
				}
				offset := int64(part.PartNumber-1) * partSize
				etag, err := c.uploadPartWithRetry(ctx, file, part, offset, length)
				if err != nil {
					record(err)
					continue
				}
				mu.Lock()
				completed[part.PartNumber] = etag
				mu.Unlock()
			}
		}()
	}

dispatch:
	for _, part := range missing {
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- part:
		}
	}
	close(jobs)
	wg.Wait()
	// A permanent failure takes precedence: it dooms the whole upload, so the
	// caller must see it even if a later transient failure overwrote lastErr.
	if permErr != nil {
		return permErr
	}
	return lastErr
}

// partUploadAttemptTimeout bounds one attempt of one part PUT. The deadline
// scales with part size at a conservative floor rate so slow links can still
// finish within the 24h presigned-URL window — a fixed whole-request timeout
// would put a hard bandwidth floor under every part that no retry can clear.
func partUploadAttemptTimeout(length int64) time.Duration {
	const floorBytesPerSecond = 32 * 1024 // ~0.25 Mbps per concurrent part
	timeout := 2*time.Minute + time.Duration(length/floorBytesPerSecond)*time.Second
	if timeout < UploadTimeout {
		return UploadTimeout
	}
	return timeout
}

// uploadPartWithRetry PUTs one part to its presigned URL and returns the ETag
// S3 responds with. Each part retries independently using the same retryable
// status / backoff rules as single-shot uploads, so one broken pipe costs one
// part, not the whole artifact.
func (c *Client) uploadPartWithRetry(ctx context.Context, file *os.File, part multipartUploadPartURL, offset, length int64) (string, error) {
	var etag string
	var permanent bool
	err := c.retryUploadAttempts(ctx, fmt.Sprintf("part %d upload", part.PartNumber), func(int) (bool, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, partUploadAttemptTimeout(length))
		defer cancel()

		// ReadAt is safe for concurrent use, so all workers share one *os.File.
		reader := io.NewSectionReader(file, offset, length)
		uploadReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPut, part.UploadURL, reader)
		if err != nil {
			permanent = true
			return true, fmt.Errorf("failed to create part upload request: %w", err)
		}
		uploadReq.ContentLength = length

		// A copy of uploadClient without its whole-request timeout: attemptCtx
		// bounds the attempt instead, scaled to the part size. The copy shares
		// the transport, so connection pooling is unaffected.
		partClient := *c.uploadClient
		partClient.Timeout = 0
		uploadResp, err := partClient.Do(uploadReq)
		if err != nil {
			return false, fmt.Errorf("part %d upload failed: %w", part.PartNumber, err)
		}

		if uploadResp.StatusCode < 400 {
			etag = uploadResp.Header.Get("ETag")
			_, _ = io.Copy(io.Discard, uploadResp.Body)
			uploadResp.Body.Close()
			if etag == "" {
				// Deterministic: S3 always returns an ETag on a successful
				// part PUT, so a missing header (e.g. stripped by a proxy)
				// will be missing on every attempt — retrying just re-uploads
				// the part for the same outcome.
				permanent = true
				return true, fmt.Errorf(
					"part %d upload returned no ETag (a proxy may be stripping response headers)",
					part.PartNumber,
				)
			}
			return true, nil
		}

		body, readErr := io.ReadAll(io.LimitReader(uploadResp.Body, maxUploadErrorBodyBytes))
		uploadResp.Body.Close()
		statusErr := fmt.Errorf("part %d: %w", part.PartNumber, formatUploadStatusError(uploadResp.StatusCode, body, readErr))
		if !isRetryableUploadStatus(uploadResp.StatusCode) {
			permanent = true
			return true, statusErr
		}
		return false, statusErr
	})
	if err != nil {
		if permanent {
			return "", &permanentUploadError{err: err}
		}
		return "", err
	}
	return etag, nil
}

// bestEffortAbortMultipartUpload promptly frees the parts of a failed upload
// via the presigned AbortMultipartUpload URL. Purely a courtesy: no build row
// exists yet, and S3 lifecycle rules expire abandoned multipart uploads
// regardless, so failures here are only worth a debug line.
func (c *Client) bestEffortAbortMultipartUpload(abortURL string) {
	if abortURL == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, abortURL, nil)
	if err != nil {
		return
	}
	resp, err := c.uploadClient.Do(httpReq)
	if err != nil {
		debugLogf("multipart upload: abort failed: %v", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func (c *Client) bestEffortDeleteBuildVersion(versionID string) {
	if versionID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := c.DeleteBuildVersion(cleanupCtx, versionID); err != nil {
		fmt.Fprintf(
			os.Stderr,
			"warning: failed to clean up orphaned build version %s: %v\n",
			versionID, err,
		)
	}
}

// validateLocalBuildArtifact runs the same hard pre-flight checks that
// AppUploadField applies in the web UI. Bad artifacts are rejected before any
// HTTP work — no presigned URL, no S3 upload, no orphaned build row.
//
// Hard rejections (regardless of platform):
//   - .aab (Android App Bundles aren't supported)
//   - .ipa (real-device builds; we only run simulators)
//   - .apks / split APK archives (we install one APK artifact)
//   - .zip with an IPA-shaped Payload/*.app/ tree
//
// Platform-aware structural checks for .zip files:
//   - iOS:     must contain a *.app/Info.plist
//   - Android: must contain exactly one non-split .apk
//   - unknown: must look like one of the two
//
// The deeper iOS simulator check (DTPlatformName != iphoneos) and the Android
// ABI check still run server-side when the build is created; this function
// exists so obvious mistakes fail locally with a clear error instead of after
// upload.
func validateLocalBuildArtifact(filePath string, metadata map[string]interface{}) error {
	fileName := strings.ToLower(filepath.Base(filePath))

	if strings.HasSuffix(fileName, ".aab") {
		return fmt.Errorf(
			"Android App Bundles (.aab) are not supported. Please use an APK build instead",
		)
	}
	if strings.HasSuffix(fileName, ".apks") {
		return fmt.Errorf(androidSplitAPKError)
	}
	if strings.HasSuffix(fileName, ".ipa") {
		return fmt.Errorf(
			"IPA files are not supported. Please upload a simulator-built .app bundle (zipped). " +
				"IPAs are for real devices, not simulators",
		)
	}

	platform := resolveUploadPlatform(metadata)

	if strings.HasSuffix(fileName, ".zip") {
		return validateLocalZipArtifact(filePath, platform)
	}

	if strings.HasSuffix(fileName, ".apk") {
		if platform == "ios" {
			return fmt.Errorf(
				"%q has an .apk extension but the iOS platform was selected; "+
					"upload a simulator-built .app bundle (zipped) instead",
				filepath.Base(filePath),
			)
		}
		if looksLikeLocalSplitAPKName(filePath) {
			return fmt.Errorf(androidSplitAPKError)
		}
		return nil
	}

	// Other extensions fall through to the backend's _validate_file_extension
	// gate, which is the canonical source of accepted extensions.
	return nil
}

func looksLikeLocalSplitAPKName(path string) bool {
	baseName := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(baseName, "split_") ||
		strings.HasPrefix(baseName, "split.") ||
		strings.HasPrefix(baseName, "config.")
}

// validateLocalZipArtifact opens a .zip and inspects its top-level contents to
// decide whether it looks like a wrapped iOS .app or a wrapped Android APK.
func validateLocalZipArtifact(filePath string, platform string) error {
	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return fmt.Errorf("failed to read .zip artifact: %w", err)
	}
	defer reader.Close()

	hasPayload := false
	hasAppInfoPlist := false
	var apkPaths []string

	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := file.Name
		lower := strings.ToLower(name)

		if strings.HasPrefix(name, "Payload/") && strings.Contains(name, ".app/") {
			hasPayload = true
		}
		if strings.HasSuffix(name, ".app/Info.plist") && !strings.Contains(name, "__MACOSX") {
			hasAppInfoPlist = true
		}
		if strings.HasSuffix(lower, ".apk") {
			apkPaths = append(apkPaths, name)
		}
	}

	if hasPayload {
		return fmt.Errorf(
			"IPA files are not supported. Please upload a simulator-built .app bundle (zipped). " +
				"IPAs are for real devices, not simulators",
		)
	}

	androidZipErr := validateLocalAndroidAPKPaths(apkPaths)

	switch platform {
	case "ios":
		if !hasAppInfoPlist {
			return fmt.Errorf(
				"no valid .app bundle found in the .zip. " +
					"Please upload a zipped simulator-built .app bundle",
			)
		}
	case "android":
		if androidZipErr != nil {
			return androidZipErr
		}
	default:
		if len(apkPaths) > 0 {
			if androidZipErr != nil {
				return androidZipErr
			}
			return nil
		}
		if !hasAppInfoPlist {
			return fmt.Errorf(
				"no supported build found in this .zip. " +
					"Upload a .zip containing an .apk or a simulator-built .app bundle",
			)
		}
	}

	return nil
}

func validateLocalAndroidAPKPaths(apkPaths []string) error {
	if len(apkPaths) == 0 {
		return fmt.Errorf("Android ZIP artifacts must contain exactly one .apk file")
	}
	if len(apkPaths) > 1 {
		return fmt.Errorf(androidSplitAPKError)
	}
	if looksLikeLocalSplitAPKName(apkPaths[0]) {
		return fmt.Errorf(androidSplitAPKError)
	}
	return nil
}

// resolveUploadPlatform reads metadata.platform and lowercases it. Returns ""
// when the caller didn't pin a platform — in which case the structural check
// accepts either iOS or Android shapes.
func resolveUploadPlatform(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata["platform"]
	if !ok {
		return ""
	}
	platform, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(platform))
}

// CreateBuildFromURLRequest represents a server-side URL-based build ingestion request.
// The backend downloads the artifact directly from the provided URL, so the CLI
// never needs the binary on disk.
type CreateBuildFromURLRequest struct {
	// AppID is the target Revyl app to store the build under.
	AppID string `json:"-"`

	// FromURL is the remote artifact URL (e.g. Artifactory, S3, GCS, EAS).
	FromURL string `json:"from_url"`

	// Headers are optional HTTP headers forwarded when fetching FromURL (e.g. Authorization).
	Headers map[string]string `json:"headers,omitempty"`

	// Version is the version label for the build (must be unique within the app).
	Version string `json:"version"`

	// Metadata is optional key-value metadata attached to the build record.
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// SetAsCurrent marks this version as the current/default version for the app.
	SetAsCurrent bool `json:"set_as_current,omitempty"`
}

// CreateBuildFromURLResponse represents the server response after a URL-based build ingestion.
type CreateBuildFromURLResponse struct {
	// ID is the unique identifier of the created build version.
	ID string `json:"id"`

	// AppID is the app the build was stored under.
	AppID string `json:"app_id"`

	// Version is the version label that was assigned.
	Version string `json:"version"`

	// ArtifactURL is the S3 key where the artifact was stored.
	ArtifactURL string `json:"artifact_url"`

	// PackageName is the extracted package identifier (bundle ID / package name), if any.
	PackageName string `json:"package_name,omitempty"`

	// WasReused indicates the version already existed and was returned as-is (idempotent).
	WasReused bool `json:"was_reused,omitempty"`
}

// CreateBuildFromURL ingests a build artifact from a remote URL server-side.
// The backend streams the download, uploads to S3, extracts package metadata,
// and creates the build record. The CLI does not need the binary locally.
//
// Parameters:
//   - ctx: Context for cancellation
//   - req: The URL-based build request (AppID, FromURL, optional Headers/Version/Metadata)
//
// Returns:
//   - *CreateBuildFromURLResponse: The created build version info
//   - error: Any error (502 if the backend cannot fetch the URL, 409 if version exists and is not idempotent)
func (c *Client) CreateBuildFromURL(ctx context.Context, req *CreateBuildFromURLRequest) (*CreateBuildFromURLResponse, error) {
	path := fmt.Sprintf("/api/v1/builds/%s/builds/from-url", req.AppID)

	resp, err := c.doRequest(ctx, "POST", path, req)
	if err != nil {
		return nil, err
	}

	var result CreateBuildFromURLResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
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

// retryUploadAttempts runs attemptFn with the client's retry budget and
// exponential backoff, stopping early on context cancellation. attemptFn
// returns done=true to stop immediately (err nil = success, err non-nil =
// permanent failure) or done=false with a retryable error; when the budget is
// exhausted the last retryable error is wrapped as
// "<operation> failed after N attempts".
func (c *Client) retryUploadAttempts(ctx context.Context, operation string, attemptFn func(attempt int) (bool, error)) error {
	attempts := c.maxRetries + 1
	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {
			debugLogf(
				"retrying %s (attempt %d/%d): %v",
				operation, attempt+1, attempts, lastErr,
			)
			delay := calculateBackoff(attempt-1, c.retryBaseDelay, c.retryMaxDelay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		done, err := attemptFn(attempt)
		if done {
			return err
		}
		lastErr = err
	}

	return fmt.Errorf("%s failed after %d attempts: %w", operation, attempts, lastErr)
}

func (c *Client) uploadFileWithRetry(ctx context.Context, uploadURL, contentType, filePath string, fileSize int64) error {
	return c.retryUploadAttempts(ctx, "upload", func(int) (bool, error) {
		file, err := os.Open(filePath)
		if err != nil {
			return true, fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()

		uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, file)
		if err != nil {
			return true, fmt.Errorf("failed to create upload request: %w", err)
		}
		uploadReq.Header.Set("Content-Type", contentType)
		uploadReq.ContentLength = fileSize

		uploadResp, err := c.uploadClient.Do(uploadReq)
		if err != nil {
			return false, fmt.Errorf("upload failed: %w", err)
		}

		if uploadResp.StatusCode < 400 {
			uploadResp.Body.Close()
			return true, nil
		}

		body, readErr := io.ReadAll(io.LimitReader(uploadResp.Body, maxUploadErrorBodyBytes))
		uploadResp.Body.Close()
		statusErr := formatUploadStatusError(uploadResp.StatusCode, body, readErr)
		if !isRetryableUploadStatus(uploadResp.StatusCode) {
			return true, statusErr
		}
		return false, statusErr
	})
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
//   - *TestSummary: The test data
//   - error: Any error that occurred
func (c *Client) GetTest(ctx context.Context, testID string) (*TestSummary, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/tests/get_test_by_id/%s", testID), nil)
	if err != nil {
		return nil, err
	}

	var result TestSummary
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// MobileTargetEntry represents a saved device target from test_mobile_targets.
type MobileTargetEntry struct {
	DeviceModel string `json:"device_model"`
	OSVersion   string `json:"os_version"`
}

// TestSummary represents the high-level shape returned by GetTest.
// Renamed from `Test` to avoid colliding with the generated
// ActionBlockVariableScope enum constant `Test`.
type TestSummary struct {
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
	// RunConfig is the test's persisted run configuration (TestRunConfig
	// from cognisim_schemas). Kept as a raw map so the CLI can edit
	// individual keys without binding to every nested field type.
	RunConfig map[string]interface{} `json:"run_config,omitempty"`
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
	// RunConfig persists the test's run configuration. Send the merged
	// map (read-modify-write) — the backend stores it as-is, replacing
	// any previous run_config.
	RunConfig map[string]interface{} `json:"run_config,omitempty"`
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
	// Every test must be associated with an app. This is the single chokepoint
	// all CLI test creation funnels through, so enforcing here guarantees no
	// command path (create, hot-reload, interactive, init) can create an
	// app-less test. Mirrors the backend guard in create_test_supabase.
	if req == nil || strings.TrimSpace(req.AppID) == "" {
		return nil, fmt.Errorf(
			"a test must be associated with an app: provide an app_id before creating a test",
		)
	}

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

// CreateTestFromBlocksRequest represents a test creation request backed by
// already-compiled editor blocks.
type CreateTestFromBlocksRequest struct {
	Blocks   []map[string]interface{} `json:"blocks"`
	Metadata map[string]interface{}   `json:"metadata"`
	Options  map[string]interface{}   `json:"options,omitempty"`
}

// CreateTestFromBlocksResponse represents the response from /tests/yaml/from-blocks.
type CreateTestFromBlocksResponse struct {
	Success      bool     `json:"success"`
	TestID       string   `json:"test_id"`
	BlocksCount  int      `json:"blocks_count"`
	GeneratedIDs []string `json:"generated_ids"`
	Errors       []string `json:"errors,omitempty"`
	CreatedAt    *string  `json:"created_at,omitempty"`
}

type YAMLValidationMessage struct {
	Severity     string `json:"severity"`
	Code         string `json:"code,omitempty"`
	Message      string `json:"message"`
	FieldPath    string `json:"field_path,omitempty"`
	Line         int    `json:"line,omitempty"`
	LineNumber   int    `json:"line_number,omitempty"`
	Column       int    `json:"column,omitempty"`
	ColumnNumber int    `json:"column_number,omitempty"`
	Suggestion   string `json:"suggestion,omitempty"`
}

type ValidateYAMLRequest struct {
	YAMLContent    string `json:"yaml_content"`
	ValidationType string `json:"validation_type,omitempty"`
	Platform       string `json:"platform,omitempty"`
}

type ValidateYAMLResponse struct {
	IsValid        bool                    `json:"is_valid"`
	ValidationType string                  `json:"validation_type"`
	Errors         int                     `json:"errors"`
	Warnings       int                     `json:"warnings"`
	Messages       []YAMLValidationMessage `json:"messages"`
}

func (c *Client) ValidateYAML(ctx context.Context, req *ValidateYAMLRequest) (*ValidateYAMLResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/yaml/validate-yaml", req)
	if err != nil {
		return nil, err
	}

	var result ValidateYAMLResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// CreateTestFromBlocks creates a test from editor blocks using the YAML v2 API.
func (c *Client) CreateTestFromBlocks(ctx context.Context, req *CreateTestFromBlocksRequest) (*CreateTestFromBlocksResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/yaml/from-blocks", req)
	if err != nil {
		return nil, err
	}

	var result CreateTestFromBlocksResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// StartRecordingCompile starts a recording/session compilation job.
func (c *Client) StartRecordingCompile(ctx context.Context, req *CompileRecordingRequest) (*CompileRecordingStartResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/recordings/compile", req)
	if err != nil {
		return nil, err
	}

	var result CompileRecordingStartResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetRecordingCompileStatus fetches the current status and result for a compile job.
func (c *Client) GetRecordingCompileStatus(ctx context.Context, jobID string) (*CompileRecordingStatusResponse, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf(
		"/api/v1/recordings/compile/%s",
		url.PathEscape(jobID),
	), nil)
	if err != nil {
		return nil, err
	}

	var result CompileRecordingStatusResponse
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

// AtlasQuery carries the shared filters supported by Atlas inspection endpoints.
type AtlasQuery struct {
	AppID               string
	BuildID             string
	ReportID            string
	TestID              string
	WorkflowExecutionID string
	SourceKind          string
	FromTime            string
	ToTime              string
	SurfaceScope        string
	Visibility          string
	IncludeVariants     bool
	IncludeDetails      *bool
	IncludeFlows        *bool
	Limit               int
	Query               string
	Direction           string
	LeftEntityID        string
	RightEntityID       string
	IncludeScreenshots  bool
}

// AtlasResponse is a generic JSON object returned by Atlas inspection endpoints.
type AtlasResponse map[string]interface{}

func (q AtlasQuery) values() url.Values {
	values := url.Values{}
	if q.BuildID != "" {
		values.Set("build_id", q.BuildID)
	}
	if q.ReportID != "" {
		values.Set("report_id", q.ReportID)
	}
	if q.TestID != "" {
		values.Set("test_id", q.TestID)
	}
	if q.WorkflowExecutionID != "" {
		values.Set("workflow_execution_id", q.WorkflowExecutionID)
	}
	if q.SourceKind != "" {
		values.Set("source_kind", q.SourceKind)
	}
	if q.FromTime != "" {
		values.Set("from_time", q.FromTime)
	}
	if q.ToTime != "" {
		values.Set("to_time", q.ToTime)
	}
	if q.SurfaceScope != "" {
		values.Set("surface_scope", q.SurfaceScope)
	}
	if q.Visibility != "" {
		values.Set("visibility", q.Visibility)
	}
	if q.IncludeVariants {
		values.Set("include_variants", "true")
	}
	if q.IncludeDetails != nil {
		values.Set("include_details", fmt.Sprintf("%t", *q.IncludeDetails))
	}
	if q.IncludeFlows != nil {
		values.Set("include_flows", fmt.Sprintf("%t", *q.IncludeFlows))
	}
	if q.Limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", q.Limit))
	}
	if q.Query != "" {
		values.Set("q", q.Query)
	}
	if q.Direction != "" {
		values.Set("direction", q.Direction)
	}
	if q.LeftEntityID != "" {
		values.Set("left_entity_id", q.LeftEntityID)
	}
	if q.RightEntityID != "" {
		values.Set("right_entity_id", q.RightEntityID)
	}
	if q.IncludeScreenshots {
		values.Set("include_screenshots", "true")
	}
	return values
}

func (c *Client) getAtlas(ctx context.Context, path string, query AtlasQuery) (AtlasResponse, error) {
	values := query.values()
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = AtlasResponse{}
	}
	return result, nil
}

func (c *Client) GetAtlasOverview(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/overview", url.PathEscape(query.AppID)), query)
}

func (c *Client) GetAtlasGraph(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/graph", url.PathEscape(query.AppID)), query)
}

func (c *Client) GetAtlasStructure(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/structure", url.PathEscape(query.AppID)), query)
}

func (c *Client) GetAtlasEntity(ctx context.Context, query AtlasQuery, entityID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/entities/%s", url.PathEscape(query.AppID), url.PathEscape(entityID)), query)
}

func (c *Client) GetAtlasEntityObservations(ctx context.Context, query AtlasQuery, entityID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/entities/%s/observations", url.PathEscape(query.AppID), url.PathEscape(entityID)), query)
}

func (c *Client) GetAtlasEntityNeighbors(ctx context.Context, query AtlasQuery, entityID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/entities/%s/neighbors", url.PathEscape(query.AppID), url.PathEscape(entityID)), query)
}

func (c *Client) GetAtlasEntityCandidates(ctx context.Context, query AtlasQuery, entityID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/entities/%s/candidates", url.PathEscape(query.AppID), url.PathEscape(entityID)), query)
}

func (c *Client) GetAtlasObservation(ctx context.Context, query AtlasQuery, observationID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/observations/%s", url.PathEscape(query.AppID), url.PathEscape(observationID)), query)
}

func (c *Client) GetAtlasFlows(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/flows", url.PathEscape(query.AppID)), query)
}

func (c *Client) SearchAtlas(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/search", url.PathEscape(query.AppID)), query)
}

func (c *Client) CompareAtlasEntities(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/compare", url.PathEscape(query.AppID)), query)
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
	return c.listApps(ctx, platform, "", page, pageSize, true)
}

func (c *Client) SearchApps(ctx context.Context, query string, platform string, pageSize int) (*CLIPaginatedAppsResponse, error) {
	return c.listApps(ctx, platform, query, 1, pageSize, false)
}

func (c *Client) listApps(ctx context.Context, platform string, search string, page, pageSize int, hydrateVersions bool) (*CLIPaginatedAppsResponse, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", page))
	values.Set("page_size", fmt.Sprintf("%d", pageSize))
	if platform != "" {
		values.Set("platform", normalizePlatform(platform))
	}
	if strings.TrimSpace(search) != "" {
		values.Set("search", strings.TrimSpace(search))
	}
	path := "/api/v1/builds/vars?" + values.Encode()

	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result CLIPaginatedAppsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if hydrateVersions {
		result.Items = c.hydrateAppVersionSummaries(ctx, result.Items)
	}

	return &result, nil
}

// ListAllApps retrieves all apps for the authenticated user's organization by
// following page-based pagination until exhaustion.
func (c *Client) ListAllApps(ctx context.Context, platform string, pageSize int) ([]App, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 100 {
		pageSize = 100
	}

	page := 1
	all := make([]App, 0, pageSize)
	for {
		result, err := c.ListApps(ctx, platform, page, pageSize)
		if err != nil {
			return nil, err
		}

		all = append(all, result.Items...)
		if !result.HasNext {
			break
		}
		page++
	}

	return all, nil
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

// CreateHotReloadRelay provisions a backend-owned relay session.
func (c *Client) CreateHotReloadRelay(
	ctx context.Context,
	req HotReloadRelayCreateParams,
) (*HotReloadRelaySession, error) {
	resp, err := c.doRequestWithRetry(ctx, "POST", "/api/v1/hotreload/relays", req, traceHandoffHeadersFromContext(ctx))
	if err != nil {
		return nil, err
	}

	var result HotReloadRelaySession
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HeartbeatHotReloadRelay refreshes a relay session TTL.
func (c *Client) HeartbeatHotReloadRelay(
	ctx context.Context,
	relayID string,
) (*HotReloadRelayHeartbeatStatus, error) {
	resp, err := c.doRequest(
		ctx,
		"POST",
		fmt.Sprintf("/api/v1/hotreload/relays/%s/heartbeat", url.PathEscape(strings.TrimSpace(relayID))),
		nil,
	)
	if err != nil {
		return nil, err
	}

	var result HotReloadRelayHeartbeatStatus
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RevokeHotReloadRelay tears down a relay session on the backend.
func (c *Client) RevokeHotReloadRelay(ctx context.Context, relayID string) error {
	resp, err := c.doRequest(
		ctx,
		"DELETE",
		fmt.Sprintf("/api/v1/hotreload/relays/%s", url.PathEscape(strings.TrimSpace(relayID))),
		nil,
	)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
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
	AppID       string                 `json:"app_id,omitempty"`
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

	// BuildID is the CogniSim builds table UUID for the installed artifact.
	BuildID string `json:"build_id,omitempty"`

	// AppURL is a downloadable app artifact URL (.apk/.ipa/.zip).
	AppURL string `json:"app_url,omitempty"`

	// AppLink is a presigned or direct URL to the app binary artifact.
	AppLink string `json:"app_link,omitempty"`

	// LaunchEnvVarIds are org launch variable IDs selected for a raw device session.
	LaunchEnvVarIds []string `json:"launch_env_var_ids,omitempty"`

	// EnvVars are inline launch environment variables (KEY=VALUE) applied to the
	// app's launch environment. Merged over LaunchEnvVarIds; inline takes precedence.
	EnvVars map[string]string `json:"env_vars,omitempty"`

	// IsSimulation enables simulation mode (streaming without test execution).
	IsSimulation bool `json:"is_simulation,omitempty"`

	// IdleTimeoutSeconds controls how long a session can be idle before auto-timeout.
	// Defaults to 300 seconds when omitted.
	IdleTimeoutSeconds int `json:"idle_timeout_seconds,omitempty"`

	// DeviceModel overrides the target device model (e.g. "iPhone 16", "Pixel 7").
	DeviceModel string `json:"device_model,omitempty"`

	// OsVersion overrides the target OS runtime (e.g. "iOS 18.5", "Android 14").
	OsVersion string `json:"os_version,omitempty"`

	// DeviceRunnerID pins the session to a specific worker DEVICE_ID label.
	DeviceRunnerID string `json:"device_runner_id,omitempty"`

	// SessionID allows callers to pre-generate the canonical device session identifier.
	SessionID string `json:"session_id,omitempty"`

	// RunConfig contains optional execution configuration.
	RunConfig *DeviceRunConfig `json:"run_config,omitempty"`
}

// DeviceRunConfig contains optional execution configuration for device sessions.
type DeviceRunConfig struct {
	// MaxRetries is the maximum number of retries for failed steps.
	MaxRetries int `json:"max_retries,omitempty"`

	// TimeoutSeconds is the maximum execution time in seconds.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// ExecutionMode contains execution-mode flags understood by the backend.
	ExecutionMode *DeviceExecutionModeConfig `json:"execution_mode,omitempty"`
}

// DeviceExecutionModeConfig contains execution behavior flags for device sessions.
type DeviceExecutionModeConfig struct {
	// SkipAppInstall prevents the backend boot path from installing the app.
	// Dev-loop flows use this so the CLI-owned install/relaunch step is the
	// single source of truth.
	SkipAppInstall bool `json:"skip_app_install,omitempty"`

	// InitialOrientation sets the device orientation before the session app
	// launches.
	InitialOrientation string `json:"initial_orientation,omitempty"`

	// InitialLocale sets the device locale before the session app launches.
	InitialLocale string `json:"initial_locale,omitempty"`
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
	if req == nil {
		return nil, fmt.Errorf("start device request is nil")
	}
	requestID := newTraceRequestID()
	headers := map[string]string{traceRequestIDHeader: requestID}
	if handoff, ok := TraceHandoffFromContext(ctx); ok {
		if handoffHeaders := handoff.Headers(); len(handoffHeaders) > 0 {
			headers = handoffHeaders
		}
	} else if handoff, err := c.exportStartDeviceTrace(ctx, requestID); err == nil {
		headers[cliTraceparentHeader] = handoff.Traceparent
		headers[cliTraceHandoffHeader] = handoff.HandoffToken
	}

	resp, err := c.doRequestWithRetry(ctx, "POST", "/api/v1/execution/start_device", req, headers)
	if err != nil {
		return nil, err
	}

	var result StartDeviceResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func traceHandoffHeadersFromContext(ctx context.Context) map[string]string {
	handoff, ok := TraceHandoffFromContext(ctx)
	if !ok {
		return nil
	}
	return handoff.Headers()
}

// GetDeviceTargets fetches the backend's current device target matrix.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - *AllPlatformTargets: The current device target matrix
//   - error: Any error that occurred
func (c *Client) GetDeviceTargets(ctx context.Context) (*AllPlatformTargets, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/execution/device-targets", nil)
	if err != nil {
		return nil, err
	}

	var result AllPlatformTargets
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
	ID              string                 `json:"id"`
	OrgID           string                 `json:"org_id"`
	Source          *string                `json:"source"`
	SourceMetadata  map[string]interface{} `json:"source_metadata,omitempty"`
	Platform        *string                `json:"platform"`
	DeviceModel     *string                `json:"device_model"`
	OsVersion       *string                `json:"os_version"`
	Status          string                 `json:"status"`
	WhepURL         *string                `json:"whep_url"`
	WorkflowRunID   *string                `json:"workflow_run_id"`
	CreatedAt       *string                `json:"created_at"`
	StartedAt       *string                `json:"started_at"`
	EndedAt         *string                `json:"ended_at"`
	ErrorMessage    *string                `json:"error_message"`
	TraceID         *string                `json:"trace_id"`
	ScreenWidth     *int                   `json:"screen_width"`
	ScreenHeight    *int                   `json:"screen_height"`
	ReportID        *string                `json:"report_id"`
	HasVideo        bool                   `json:"has_video"`
	StepCount       int                    `json:"step_count"`
	ActionCount     int                    `json:"action_count"`
	DurationSeconds *float64               `json:"duration_seconds"`
	TestID          *string                `json:"test_id"`
	TestName        *string                `json:"test_name"`
	UserEmail       *string                `json:"user_email"`
	CanCancel       bool                   `json:"can_cancel"`
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

// CLIReportContextEnvelope preserves both the raw response bytes and the
// typed report. Report is the generated ReportContextResponse — the single
// source of truth regenerated from the backend OpenAPI, so a backend field
// can never silently fail to round-trip into the CLI (the drift that hid
// the live-artifact *_partial bug).
type CLIReportContextEnvelope struct {
	Raw    json.RawMessage
	Report *ReportContextResponse
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

	var result ReportContextResponse
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

	var result ReportContextResponse
	raw, err := parseResponseWithRaw(resp, &result)
	if err != nil {
		return nil, err
	}

	return &CLIReportContextEnvelope{
		Raw:    raw,
		Report: &result,
	}, nil
}

// GetDeviceLogsDownloadURL fetches a presigned URL for the device-logs
// artifact attached to the given report. Device logs are not embedded
// in the context envelope (unlike network / perf / trace / device-state) —
// they live behind a dedicated endpoint. Returns a 404 (mapped to
// APIError) when no device logs were uploaded for the run.
func (c *Client) GetDeviceLogsDownloadURL(ctx context.Context, reportID string) (*DeviceLogsDownloadResponse, error) {
	path := fmt.Sprintf("/api/v1/reports-v3/reports/%s/device-logs", reportID)
	resp, err := c.doRequestOnce(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result DeviceLogsDownloadResponse
	if _, err := parseResponseWithRaw(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
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

// --- Test Launch Var Attachment API methods ---

// ListTestLaunchEnvVarAttachments retrieves all org launch vars attached to a test.
func (c *Client) ListTestLaunchEnvVarAttachments(ctx context.Context, testID string) (*OrgLaunchVariablesResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/org_launch_env/test-attachments?test_id=%s", testID)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariablesResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ReplaceTestLaunchEnvVarAttachments replaces the launch vars attached to a test.
func (c *Client) ReplaceTestLaunchEnvVarAttachments(ctx context.Context, testID string, envVarIDs []string) (*OrgLaunchVariablesResponse, error) {
	body := map[string]interface{}{
		"test_id":     testID,
		"env_var_ids": envVarIDs,
	}
	resp, err := c.doRequest(ctx, "PUT", "/api/v1/variables/org_launch_env/test-attachments", body)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariablesResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
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

var proxyLongRunningActions = map[string]bool{
	"install": true,
}

func (c *Client) ProxyWorkerRequest(ctx context.Context, workflowRunID, action string, body interface{}) ([]byte, int, error) {
	path := fmt.Sprintf("/api/v1/execution/device-proxy/%s/%s", workflowRunID, action)

	method := proxyWorkerMethodForAction(action)
	if method == http.MethodGet {
		body = nil
	}

	var resp *http.Response
	var err error
	if proxyLongRunningActions[action] {
		resp, err = c.doRequestOnceWithClient(ctx, method, path, body, c.uploadClient)
	} else if proxyNoRetryActions[action] {
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
	if idx := strings.Index(action, "?"); idx >= 0 {
		action = action[:idx]
	}
	// Read-only device_state sub-paths route GET; the rest go POST.
	// (``userdefaults`` is POST despite being read-only so the proxy
	// doesn't have to thread query strings — body carries the args.)
	switch action {
	case "device_state", "device_state/list":
		return http.MethodGet
	case "device_state/snapshot",
		"device_state/diff",
		"device_state/userdefaults",
		"device_state/sqlite/query":
		return http.MethodPost
	}
	// Extract base action for compound paths (e.g. "step_status/{id}").
	base := action
	if idx := strings.Index(action, "/"); idx >= 0 {
		base = action[:idx]
	}
	switch base {
	case "screenshot", "health", "device_info", "step_status", "hierarchy", "performance_metrics", "network_requests", "device_logs":
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

// OrgLaunchVariable represents an org-scoped reusable launch variable.
type OrgLaunchVariable struct {
	ID                string `json:"id"`
	OrgID             string `json:"org_id"`
	Key               string `json:"key"`
	Value             string `json:"value"`
	Description       string `json:"description,omitempty"`
	CreatedBy         string `json:"created_by,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	AttachedTestCount int    `json:"attached_test_count,omitempty"`
}

// OrgLaunchVariablesResponse represents the response from listing org launch variables.
type OrgLaunchVariablesResponse struct {
	Message string              `json:"message"`
	Result  []OrgLaunchVariable `json:"result"`
}

// OrgLaunchVariableResponse represents the response from a single org launch variable operation.
type OrgLaunchVariableResponse struct {
	Message string            `json:"message"`
	Result  OrgLaunchVariable `json:"result"`
}

// OrgLaunchVariableDeleteResponse represents the response from deleting an org launch variable.
type OrgLaunchVariableDeleteResponse struct {
	Message           string            `json:"message"`
	Result            OrgLaunchVariable `json:"result"`
	DetachedTestCount int               `json:"detached_test_count"`
}

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

// GlobalVariableWriteOptions controls optional global variable write fields.
type GlobalVariableWriteOptions struct {
	IsSecret *bool
}

// AddGlobalVariable creates a new global variable for the authenticated user's org.
func (c *Client) AddGlobalVariable(ctx context.Context, name, value string, opts GlobalVariableWriteOptions) (*GlobalVariableResponse, error) {
	body := map[string]interface{}{
		"variable_name":  name,
		"variable_value": value,
	}
	if opts.IsSecret != nil {
		body["is_secret"] = *opts.IsSecret
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
func (c *Client) UpdateGlobalVariable(ctx context.Context, variableID, name string, value *string, opts GlobalVariableWriteOptions) (*GlobalVariableResponse, error) {
	body := map[string]interface{}{
		"variable_name": name,
	}
	if value != nil {
		body["variable_value"] = *value
	}
	if opts.IsSecret != nil {
		body["is_secret"] = *opts.IsSecret
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

// ListOrgLaunchVariables retrieves all org launch variables for the authenticated user's org.
func (c *Client) ListOrgLaunchVariables(ctx context.Context) (*OrgLaunchVariablesResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/variables/org_launch_env", nil)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariablesResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// AddOrgLaunchVariable creates a new org launch variable for the authenticated user's org.
func (c *Client) AddOrgLaunchVariable(ctx context.Context, key, value string, description *string) (*OrgLaunchVariableResponse, error) {
	body := map[string]interface{}{
		"key":   key,
		"value": value,
	}
	if description != nil {
		body["description"] = *description
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/variables/org_launch_env", body)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// UpdateOrgLaunchVariable updates an existing org launch variable by UUID.
func (c *Client) UpdateOrgLaunchVariable(ctx context.Context, variableID string, key, value, description *string) (*OrgLaunchVariableResponse, error) {
	body := map[string]interface{}{}
	if key != nil {
		body["key"] = *key
	}
	if value != nil {
		body["value"] = *value
	}
	if description != nil {
		body["description"] = *description
	}

	path := fmt.Sprintf("/api/v1/variables/org_launch_env/%s", variableID)
	resp, err := c.doRequest(ctx, "PUT", path, body)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// DeleteOrgLaunchVariable deletes an org launch variable by UUID.
func (c *Client) DeleteOrgLaunchVariable(ctx context.Context, variableID string) (*OrgLaunchVariableDeleteResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/org_launch_env/%s", variableID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariableDeleteResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
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

// ---------------------------------------------------------------------------
// Remote Build
// ---------------------------------------------------------------------------

// GetRemoteBuildUploadURL obtains a presigned S3 URL for uploading a source
// archive used by a remote build.
//
// Parameters:
//   - ctx: cancellation context
//   - appID: UUID of the target app
//   - filename: name of the archive file (e.g. "source.tar.gz")
//   - fileSize: size of the archive in bytes
//
// Returns:
//   - *RemoteBuildSourceUploadResponse containing upload URL and S3 key
//   - error on API or network failure
func (c *Client) GetRemoteBuildUploadURL(ctx context.Context, appID, filename string, fileSize int64) (*RemoteBuildSourceUploadResponse, error) {
	req := &RemoteBuildSourceUploadRequest{
		AppId:    appID,
		Filename: &filename,
		FileSize: func() *int { v := int(fileSize); return &v }(),
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/apps/remote/upload-url", req)
	if err != nil {
		return nil, err
	}
	var result RemoteBuildSourceUploadResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// TriggerRemoteBuild dispatches a remote build job via the backend.
//
// Parameters:
//   - ctx: cancellation context
//   - req: build parameters including source key and build command
//
// Returns:
//   - *RemoteBuildTriggerResponse with the build_job_id for status polling
//   - error on API or network failure
func (c *Client) TriggerRemoteBuild(ctx context.Context, req *RemoteBuildRequest) (*RemoteBuildTriggerResponse, error) {
	resp, err := c.doRequest(ctx, "POST", "/api/v1/apps/remote", req)
	if err != nil {
		return nil, err
	}
	var result RemoteBuildTriggerResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRemoteBuildStatus polls the current status of a remote build job.
//
// Parameters:
//   - ctx: cancellation context
//   - buildJobID: UUID returned by TriggerRemoteBuild
//
// Returns:
//   - *RemoteBuildStatusResponse with current phase, logs tail, and result
//   - error on API or network failure
func (c *Client) GetRemoteBuildStatus(ctx context.Context, buildJobID string) (*RemoteBuildStatusResponse, error) {
	path := fmt.Sprintf("/api/v1/apps/remote/%s/status", buildJobID)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result RemoteBuildStatusResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BuildRunnerStatus is defined in generated.go from the OpenAPI schema.

// CheckBuildRunnersAvailable queries the backend for active build capacity
// assigned to the caller's organisation. Used as a pre-flight check before
// uploading source to avoid wasting time when no capacity is available.
//
// Parameters:
//   - ctx: cancellation context
//   - platform: build platform ("ios" or "android")
//
// Returns:
//   - *BuildRunnerStatus with availability flag and capacity count
//   - error on API or network failure
func (c *Client) CheckBuildRunnersAvailable(ctx context.Context, platform string) (*BuildRunnerStatus, error) {
	path := "/api/v1/apps/remote/runners/available"
	values := url.Values{}
	if strings.TrimSpace(platform) != "" {
		values.Set("platform", strings.TrimSpace(platform))
	}
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result BuildRunnerStatus
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UploadFileToPresignedURL uploads a file to a presigned S3 PUT URL with retry.
//
// Parameters:
//   - ctx: cancellation context
//   - uploadURL: presigned S3 PUT URL
//   - contentType: MIME type for the upload
//   - filePath: local file path to upload
//   - fileSize: size of the file in bytes
//
// Returns:
//   - error on upload failure after retries
func (c *Client) UploadFileToPresignedURL(ctx context.Context, uploadURL, contentType, filePath string, fileSize int64) error {
	return c.uploadFileWithRetry(ctx, uploadURL, contentType, filePath, fileSize)
}

// UploadFileToPresignedPost uploads a file via a presigned S3 POST policy
// (multipart form). This enforces server-side content-length-range limits
// that presigned PUT URLs cannot enforce.
//
// Parameters:
//   - ctx: cancellation context
//   - postURL: presigned POST URL (S3 bucket endpoint)
//   - fields: form fields from the presigned POST policy
//   - filePath: local file path to upload
//
// Returns:
//   - error on upload failure after retries
func (c *Client) UploadFileToPresignedPost(ctx context.Context, postURL string, fields map[string]string, filePath string) error {
	attempts := c.maxRetries + 1
	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {
			delay := calculateBackoff(attempt-1, c.retryBaseDelay, c.retryMaxDelay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		err := c.doPresignedPostUpload(ctx, postURL, fields, filePath)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("upload failed after %d attempts: %w", attempts, lastErr)
}

func (c *Client) doPresignedPostUpload(ctx context.Context, postURL string, fields map[string]string, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return fmt.Errorf("write field %s: %w", k, err)
		}
	}

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("copy file data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", postURL, &body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.uploadClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("S3 POST returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// CancelRemoteBuild requests cancellation of a running remote build.
// The backend releases the concurrency slot and marks the build as cancelled.
//
// Parameters:
//   - ctx: cancellation context
//   - buildJobID: UUID returned by TriggerRemoteBuild
//
// Returns:
//   - error on API or network failure
func (c *Client) CancelRemoteBuild(ctx context.Context, buildJobID string) error {
	path := fmt.Sprintf("/api/v1/apps/remote/%s", buildJobID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}
