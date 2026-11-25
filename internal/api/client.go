// Package api provides the HTTP and WebSocket clients for the Revyl backend,
// combining OpenAPI-generated types in generated.go with CLI wrapper DTOs.
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
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/revyl/cli/internal/backendheaders"
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

var debugLogf = func(format string, args ...interface{}) {}

func SetDebugLogger(fn func(format string, args ...interface{})) {
	if fn != nil {
		debugLogf = fn
	}
}

const (
	DefaultBaseURL = "https://backend.revyl.ai"

	DefaultTimeout = 30 * time.Second

	BuildFinalizationTimeout = 90 * time.Second

	LaunchTimeout = 120 * time.Second

	UploadTimeout = 10 * time.Minute

	DefaultMaxRetries = 3

	DefaultRetryMaxDelay = 10 * time.Second

	historyFiltersCapabilityHeader = "X-Revyl-History-Filters"
)

var DefaultRetryBaseDelay = 500 * time.Millisecond

type Client struct {
	baseURL        string
	apiKeyMu       sync.RWMutex
	apiKey         string
	version        string
	httpClient     *http.Client
	uploadClient   *http.Client
	maxRetries     int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration
	uploadStateDir string
}

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

// NewClientWithDevMode creates a Client using config.GetBackendURL(devMode),
// where explicit backend overrides take precedence over local dev discovery.
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

func (c *Client) SetVersion(version string) {
	c.version = version
}

func (c *Client) userAgent() string {
	v := c.version
	if v == "" {
		v = "dev"
	}
	return "revyl-cli/" + v
}

func (c *Client) GetAPIKey() string {
	c.apiKeyMu.RLock()
	defer c.apiKeyMu.RUnlock()
	return c.apiKey
}

func (c *Client) SetAPIKey(apiKey string) {
	c.apiKeyMu.Lock()
	defer c.apiKeyMu.Unlock()
	c.apiKey = apiKey
}

// SetHTTPClient replaces the client's ordinary httpClient (not uploadClient); nil is ignored.
func (c *Client) SetHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	c.httpClient = client
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

type APIError struct {
	StatusCode       int
	Code             string
	Message          string
	Detail           string
	ValidationIssues []APIValidationIssue
	DetailObject     map[string]interface{}
	Hint             string
	RetryAfter       time.Duration
	FilePath         string
	FileLine         int
	BillingURL       string
	BillingAction    BillingAction
	UpgradeBenefits  *UpgradeBenefits
	UnlocksWithCard  bool
	// BillingReported marks that the caller took ownership of reporting this refusal's billing fields in --json output.
	BillingReported bool
}

// ParseRetryAfter parses an HTTP Retry-After header (delay seconds or HTTP date) into a non-negative
// delay, returning 0 when empty, invalid, or elapsed; very large integer-second values can overflow time.Duration.
func ParseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return max(time.Until(retryAt), 0)
}

type APIValidationIssue struct {
	Field   string
	Message string
	Type    string
}

const maxDebugErrorBodyBytes = 4096

var validationRequestLocations = []string{"body.", "query.", "path.", "header.", "cookie."}

const pydanticValueErrorPrefix = "Value error, "

func (i APIValidationIssue) readableText() string {
	field := i.Field
	for _, location := range validationRequestLocations {
		if trimmed, found := strings.CutPrefix(field, location); found && trimmed != "" {
			field = trimmed
			break
		}
	}
	message := i.Message
	if i.Type == "value_error" {
		message = strings.TrimPrefix(message, pydanticValueErrorPrefix)
	}
	switch {
	case field != "" && message != "":
		return field + ": " + message
	case field != "":
		return field
	default:
		return message
	}
}

type HotReloadRelayCreateParams struct {
	Provider string `json:"provider,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type HotReloadRelaySession struct {
	RelayID      string `json:"relay_id"`
	PublicURL    string `json:"public_url"`
	ConnectURL   string `json:"connect_url"`
	ConnectToken string `json:"connect_token"`
	Transport    string `json:"transport"`
	ExpiresAt    string `json:"expires_at"`
}

// ConnectWebSocketURL validates and returns the trimmed relay ConnectURL without appending the session token.
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

type HotReloadRelayHeartbeatStatus struct {
	RelayID   string `json:"relay_id"`
	ExpiresAt string `json:"expires_at"`
	Active    bool   `json:"active"`
}

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
	if e.FilePath != "" && e.FileLine > 0 {
		base = fmt.Sprintf("%s:%d: %s", e.FilePath, e.FileLine, base)
	} else if e.FilePath != "" {
		base = fmt.Sprintf("%s: %s", e.FilePath, base)
	}

	if e.Hint != "" && !strings.Contains(base, e.Hint) {
		return base + "\n" + e.Hint
	}
	return base
}

// IsTestVersionConflict reports whether a test update was rejected because the
// remote version advanced past the caller's expected version. Other 409s on the
// same route (for example renaming a test to a name that already exists) carry a
// plain-text detail and are not resolved by forcing the push.
func (e *APIError) IsTestVersionConflict() bool {
	return e != nil &&
		e.StatusCode == http.StatusConflict &&
		e.DetailString("error") == "version_conflict"
}

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

const EdgeBlockedCode = "edge_blocked"

var cfRayPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// edgeBlockedError returns an edge_blocked error for an HTML 403 that
// Cloudflare served instead of the API, or nil for any other response. The
// HTML body is never included in the error.
func edgeBlockedError(response *http.Response, body []byte) *APIError {
	contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	ray := strings.TrimSpace(response.Header.Get("cf-ray"))
	if response.StatusCode != http.StatusForbidden || !strings.EqualFold(contentType, "text/html") || ray == "" || json.Valid(body) {
		return nil
	}
	hint := "Contact Revyl support to review the edge security rule; signing in again will not resolve this block."
	if cfRayPattern.MatchString(ray) {
		hint = "Send cf-ray " + ray + " to Revyl support; signing in again will not resolve this block."
	}
	return &APIError{
		StatusCode: response.StatusCode,
		Code:       EdgeBlockedCode,
		Message:    "blocked at Revyl's edge, not a client error",
		Hint:       hint,
	}
}

func authHintForStatus(statusCode int, serverReason string) string {
	if statusCode == 401 {
		return "Session may have expired. Run 'revyl auth login' to re-authenticate."
	}
	if statusCode == 402 {
		if strings.TrimSpace(serverReason) != "" {
			return "Review billing: " + BillingSettingsURL(false)
		}
		return "Billing needs attention: " + BillingSettingsURL(false)
	}
	return ""
}

func extractHTMLError(html string) string {
	for _, marker := range []string{"<pre>", "<code>"} {
		idx := strings.LastIndex(html, marker)
		if idx >= 0 {
			end := marker[:1] + "/" + marker[1:]
			endIdx := strings.Index(html[idx:], end)
			if endIdx > 0 {
				content := html[idx+len(marker) : idx+endIdx]
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
		Code    string          `json:"code"`
		Error   string          `json:"error"`
		Detail  json.RawMessage `json:"detail"`
		Message string          `json:"message"`
		Path    string          `json:"path"`
		Line    int             `json:"line"`
		Errors  []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &errResp)
	debugBody := string(body)
	if len(debugBody) > maxDebugErrorBodyBytes {
		debugBody = debugBody[:maxDebugErrorBodyBytes] + "..."
	}
	debugLogf("API error response (HTTP %d): %s", statusCode, debugBody)

	message := errResp.Error
	if message == "" {
		message = errResp.Message
	}

	var (
		detail       string
		detailObject map[string]interface{}
		detailIssues []APIValidationIssue
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
				rawIssues, _ := value["issues"].([]interface{})
				for _, rawIssue := range rawIssues {
					issueFields, ok := rawIssue.(map[string]interface{})
					if !ok {
						continue
					}
					field, _ := issueFields["field_path"].(string)
					issueMessage, _ := issueFields["message"].(string)
					code, _ := issueFields["code"].(string)
					if suggestion, _ := issueFields["suggestion"].(string); strings.TrimSpace(suggestion) != "" {
						issueMessage = fmt.Sprintf("%s (%s)", strings.TrimSpace(issueMessage), strings.TrimSpace(suggestion))
					}
					detailIssues = append(detailIssues, APIValidationIssue{
						Field:   strings.TrimSpace(field),
						Message: strings.TrimSpace(issueMessage),
						Type:    strings.TrimSpace(code),
					})
				}
			default:
				if rawDetail, marshalErr := json.Marshal(value); marshalErr == nil {
					detail = string(rawDetail)
				}
			}
		}
	}

	serverReason := message + detail
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

	validationIssues := make([]APIValidationIssue, 0, len(errResp.Errors)+len(detailIssues))
	if len(detailIssues) > 0 {
		validationIssues = append(validationIssues, detailIssues...)
		parts := make([]string, 0, len(detailIssues))
		for _, issue := range detailIssues {
			if part := issue.readableText(); part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			detail = strings.TrimPrefix(detail+": "+strings.Join(parts, "; "), ": ")
		}
	}
	if len(errResp.Errors) > 0 {
		var parts []string
		for _, validationErr := range errResp.Errors {
			issue := APIValidationIssue{
				Field:   strings.TrimSpace(validationErr.Field),
				Message: strings.TrimSpace(validationErr.Message),
				Type:    strings.TrimSpace(validationErr.Type),
			}
			validationIssues = append(validationIssues, issue)
			if part := issue.readableText(); part != "" {
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

	apiError := &APIError{
		StatusCode:       statusCode,
		Code:             errResp.Code,
		Message:          message,
		Detail:           detail,
		ValidationIssues: validationIssues,
		DetailObject:     detailObject,
		Hint:             authHintForStatus(statusCode, serverReason),
	}
	if TestDefinitionRunErrorResponseCode(errResp.Code).Valid() {
		apiError.FilePath = errResp.Path
		apiError.FileLine = errResp.Line
	}
	billing := readBillingResponseFields(body)
	apiError.BillingURL = billing.BillingURL
	apiError.BillingAction = billing.BillingAction
	apiError.UpgradeBenefits = billing.UpgradeBenefits
	if statusCode == http.StatusPaymentRequired && apiError.BillingURL != "" {
		apiError.Hint = ""
		if !strings.Contains(serverReason, apiError.BillingURL) {
			apiError.Hint = BillingNextStep(apiError.BillingAction, apiError.BillingURL)
		}
	}
	if (statusCode == http.StatusTooManyRequests || statusCode == http.StatusConflict) &&
		(isConcurrencyLimitMessage(message+" "+detail) || isConcurrencyLimitCode(apiError.Code) ||
			isConcurrencyLimitCode(apiError.DetailString("code")) || isConcurrencyLimitCode(apiError.DetailString("error_type"))) {
		apiError.Hint = concurrencyUpgradeHint(apiError.BillingURL)
	}
	return apiError
}

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

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return c.doRequestWithRetry(ctx, method, path, body)
	default:
		return c.doRequestOnce(ctx, method, path, body)
	}
}

func (c *Client) doRequestOnce(ctx context.Context, method, path string, body interface{}, extraHeaders ...map[string]string) (*http.Response, error) {
	return c.doRequestOnceWithClient(ctx, method, path, body, c.httpClient, extraHeaders...)
}

func (c *Client) doRequestOnceWithClient(ctx context.Context, method, path string, body interface{}, client *http.Client, extraHeaders ...map[string]string) (*http.Response, error) {
	reqURL := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		switch payload := body.(type) {
		case []byte:
			bodyReader = bytes.NewReader(payload)
		default:
			jsonBody, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(jsonBody)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	apiKey := c.GetAPIKey()
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("X-Revyl-Client", "cli")
	backendheaders.SetCloudAgentConversationContext(req)
	backendheaders.SetRuntimeTraceContext(req)
	setCIHeaders(req)
	setAgentHeaders(req)
	if len(extraHeaders) > 0 {
		for key, value := range extraHeaders[0] {
			req.Header.Set(key, value)
		}
	}

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	recordUpcomingMinimumVersion(resp)
	return resp, nil
}

func isRetryableError(err error, statusCode int) bool {
	if err != nil {
		return true
	}

	if statusCode >= 500 || statusCode == 429 {
		return true
	}

	return false
}

func isTransientTransportError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func calculateBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	delay := baseDelay * time.Duration(1<<uint(attempt))
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func (c *Client) doRequestWithRetry(ctx context.Context, method, path string, body interface{}, extraHeaders ...map[string]string) (*http.Response, error) {
	return c.doRequestWithRetryClient(ctx, method, path, body, c.httpClient, extraHeaders...)
}

func (c *Client) doRequestWithRetryClient(ctx context.Context, method, path string, body interface{}, client *http.Client, extraHeaders ...map[string]string) (*http.Response, error) {
	headers := map[string]string(nil)
	if len(extraHeaders) > 0 {
		headers = extraHeaders[0]
	}
	if client == nil {
		client = http.DefaultClient
	}
	attempts := c.maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt > 0 {
			delay := calculateBackoff(attempt-1, c.retryBaseDelay, c.retryMaxDelay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

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

		apiKey := c.GetAPIKey()
		if strings.TrimSpace(apiKey) != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", c.userAgent())

		req.Header.Set("X-Revyl-Client", "cli")
		backendheaders.SetCloudAgentConversationContext(req)
		backendheaders.SetRuntimeTraceContext(req)
		setCIHeaders(req)
		setAgentHeaders(req)
		for key, value := range headers {
			if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
				req.Header.Set(key, value)
			}
		}

		resp, err := client.Do(req)
		recordUpcomingMinimumVersion(resp)

		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}

		if !isRetryableError(err, statusCode) || attempt == attempts-1 {
			if err != nil {
				return nil, fmt.Errorf("request failed: %w", err)
			}
			return resp, nil
		}

		if resp != nil {
			resp.Body.Close()
		}
	}

	return nil, fmt.Errorf("request failed: retry loop ended unexpectedly")
}

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

		if edgeErr := edgeBlockedError(resp, body); edgeErr != nil {
			return edgeErr
		}
		apiErr := parseAPIErrorBody(resp.StatusCode, body)
		apiErr.RetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"))
		return apiErr
	}

	if target != nil {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

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

		if edgeErr := edgeBlockedError(resp, body); edgeErr != nil {
			return nil, edgeErr
		}
		apiErr := parseAPIErrorBody(resp.StatusCode, body)
		apiErr.RetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, apiErr
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

type CLIRunConfig struct {
	ExecutionMode *CLIExecutionMode `json:"execution_mode,omitempty"`
	FailFast      *bool             `json:"fail_fast,omitempty"`
}

type CLIExecutionMode struct {
	InitialLocation    *CLILocation `json:"initial_location,omitempty"`
	InitialOrientation string       `json:"initial_orientation,omitempty"`
}

type CLILocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type ExecuteTestRequest struct {
	GitBranchName   string             `json:"git_branch_name,omitempty"`
	BranchID        string             `json:"branch_id,omitempty"`
	BranchName      string             `json:"branch_name,omitempty"`
	BuildSelection  BuildSelectionMode `json:"build_selection,omitempty"`
	PinnedBuildID   string             `json:"pinned_build_id,omitempty"`
	TestID          string             `json:"test_id"`
	Retries         int                `json:"retries,omitempty"`
	BuildVersionID  string             `json:"build_version_id,omitempty"`
	RunConfig       *CLIRunConfig      `json:"run_config,omitempty"`
	LaunchURL       string             `json:"launch_url,omitempty"`
	LaunchEnvVarIds []string           `json:"launch_env_var_ids,omitempty"`
	DeviceModel     string             `json:"device_model,omitempty"`
	OsVersion       string             `json:"os_version,omitempty"`
	// LaunchEnvVars are inline launch environment variables (KEY=VALUE) applied to
	// the app's launch environment; they override org launch vars attached to the test.
	LaunchEnvVars map[string]string `json:"launch_env_vars,omitempty"`
	// LaunchArguments are non-secret inline iOS app arguments in exact token order.
	LaunchArguments     []string               `json:"launch_arguments,omitempty"`
	SessionProfile      *SessionProfileRequest `json:"session_profile,omitempty"`
	VariableOverrides   map[string]string      `json:"variable_overrides,omitempty"`
	TestDefinitionFiles *TestDefinitionFiles   `json:"test_definition_files,omitempty"`
}

type TestDefinitionFiles struct {
	ProjectID                       *string              `json:"project_id"`
	GitCommitSHA                    *string              `json:"git_commit_sha"`
	HasUncommittedDefinitionChanges bool                 `json:"has_uncommitted_definition_changes"`
	Files                           []TestDefinitionFile `json:"files"`
}

type TestDefinitionFile struct {
	ProjectRelativePath string `json:"project_relative_path"`
	Content             string `json:"content"`
}

type TestDefinitionSource string

const (
	TestDefinitionSourceCLIUpload     TestDefinitionSource = "cli_upload"
	TestDefinitionSourceGitCommitRead TestDefinitionSource = "git_commit_read"
	TestDefinitionSourceSavedTest     TestDefinitionSource = "saved_test"
)

type TestDefinitionFallbackReason string

const (
	TestDefinitionFallbackReasonMissingGitBranch               TestDefinitionFallbackReason = "missing_git_branch"
	TestDefinitionFallbackReasonForkPullRequest                TestDefinitionFallbackReason = "fork_pull_request"
	TestDefinitionFallbackReasonMissingTestFile                TestDefinitionFallbackReason = "missing_test_file"
	TestDefinitionFallbackReasonMissingTestFileOnDefaultBranch TestDefinitionFallbackReason = "missing_test_file_on_default_branch"
)

type TestDefinitionGitRead struct {
	TestDefinitionGitBranchName        string `json:"test_definition_git_branch_name,omitempty"`
	TestDefinitionGitCommitSHA         string `json:"test_definition_git_commit_sha,omitempty"`
	TestDefinitionMissingGitBranchName string `json:"test_definition_missing_git_branch_name,omitempty"`
}

type ExecuteTestResponse struct {
	AutoGitBranchEnabled         *bool                        `json:"auto_git_branch_enabled,omitempty"`
	BranchSource                 BranchSource                 `json:"branch_source,omitempty"`
	GitBranchName                string                       `json:"git_branch_name,omitempty"`
	BranchName                   string                       `json:"branch_name,omitempty"`
	ID                           string                       `json:"id"`
	TaskID                       string                       `json:"task_id"`
	Status                       string                       `json:"status"`
	Message                      string                       `json:"message"`
	TestDefinitionSource         TestDefinitionSource         `json:"test_definition_source,omitempty"`
	TestDefinitionFallbackReason TestDefinitionFallbackReason `json:"test_definition_fallback_reason,omitempty"`
	TestDefinitionSnapshotID     string                       `json:"test_definition_snapshot_id,omitempty"`
	UsageNotice                  *UsageNotice                 `json:"billing_notice,omitempty"`
	QueueNotice                  *DeviceQueueNotice           `json:"queue_notice,omitempty"`
	TestDefinitionGitRead
}

type BillingPlanResponse struct {
	Plan            string  `json:"plan"`
	DisplayName     string  `json:"display_name"`
	MonthlyBase     float64 `json:"monthly_base"`
	FreeCreditLabel string  `json:"free_credit_label"`
	PaygEnrolled    bool    `json:"payg_enrolled"`
}

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

func (c *Client) ExecuteTest(ctx context.Context, req *ExecuteTestRequest) (*ExecuteTestResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("test execution request is nil")
	}
	req.Retries = 1
	if err := validateGitBranchName(req.GitBranchName); err != nil {
		return nil, err
	}
	if gitBranchNameFor(req.BuildSelection, req.GitBranchName) != req.GitBranchName {
		withoutGitBranch := *req
		withoutGitBranch.GitBranchName = ""
		req = &withoutGitBranch
	}
	resp, err := c.doRequestOnce(ctx, "POST", "/api/v1/execution/api/execute_test_id_async", req)
	if err != nil {
		return nil, err
	}

	var result ExecuteTestResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if result.TaskID == "" && result.ID != "" {
		result.TaskID = result.ID
	}

	return &result, nil
}

type ExecuteWorkflowRequest struct {
	ConcurrencyLimitOverride *int                 `json:"concurrency_limit_override,omitempty"`
	GitBranchName            string               `json:"git_branch_name,omitempty"`
	BranchName               string               `json:"branch_name,omitempty"`
	BuildSelection           BuildSelectionMode   `json:"build_selection,omitempty"`
	WorkflowID               string               `json:"workflow_id"`
	Retries                  int                  `json:"retries,omitempty"`
	BuildConfig              *WorkflowAppConfig   `json:"build_config,omitempty"`
	OverrideBuildConfig      bool                 `json:"override_build_config,omitempty"`
	LocationConfig           *CLILocation         `json:"location_config,omitempty"`
	OverrideLocation         bool                 `json:"override_location,omitempty"`
	VariableOverrides        map[string]string    `json:"variable_overrides,omitempty"`
	LaunchEnvVarIds          []string             `json:"launch_env_var_ids,omitempty"`
	LaunchEnvVars            map[string]string    `json:"launch_env_vars,omitempty"`
	LaunchArguments          []string             `json:"launch_arguments,omitempty"`
	TestDefinitionFiles      *TestDefinitionFiles `json:"test_definition_files,omitempty"`
}

type ExecuteWorkflowResponse struct {
	TaskID                              string                               `json:"task_id"`
	TestCountByDefinitionSource         map[TestDefinitionSource]int         `json:"test_count_by_definition_source,omitempty"`
	TestCountByDefinitionFallbackReason map[TestDefinitionFallbackReason]int `json:"test_count_by_definition_fallback_reason,omitempty"`
	UsageNotice                         *UsageNotice                         `json:"billing_notice,omitempty"`
	TestDefinitionGitRead
}

func (c *Client) ExecuteWorkflow(ctx context.Context, req *ExecuteWorkflowRequest) (*ExecuteWorkflowResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("workflow execution request is nil")
	}
	req.Retries = 1
	if err := validateGitBranchName(req.GitBranchName); err != nil {
		return nil, err
	}
	if gitBranchNameFor(req.BuildSelection, req.GitBranchName) != req.GitBranchName {
		withoutGitBranch := *req
		withoutGitBranch.GitBranchName = ""
		req = &withoutGitBranch
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/workflow-executions", req)
	if err != nil {
		return nil, err
	}

	var result ExecuteWorkflowResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

type UploadBuildRequest struct {
	GitBranchName string                 `json:"git_branch_name,omitempty"`
	BranchID      string                 `json:"branch_id,omitempty"`
	BranchName    string                 `json:"branch_name,omitempty"`
	AppID         string                 `json:"build_var_id"`
	Version       string                 `json:"version"`
	FilePath      string                 `json:"-"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	Pin           bool                   `json:"pin,omitempty"`
}

type UploadBuildResponse struct {
	BranchID    string       `json:"branch_id,omitempty"`
	BranchName  string       `json:"branch_name,omitempty"`
	VersionID   string       `json:"version_id"`
	Version     string       `json:"version"`
	PackageID   string       `json:"package_id,omitempty"`
	Warnings    []string     `json:"warnings,omitempty"`
	UsageNotice *UsageNotice `json:"billing_notice,omitempty"`
}

const maxUploadErrorBodyBytes = 16 * 1024

const androidSplitAPKError = "Android split APKs and APK Set archives are not supported. Upload one standalone installable APK instead"

type uploadSessionResponse struct {
	BranchID    string `json:"branch_id,omitempty"`
	BranchName  string `json:"branch_name,omitempty"`
	UploadID    string `json:"upload_id"`
	UploadURL   string `json:"upload_url"`
	ContentType string `json:"content_type"`
}

type buildCreatedResponse struct {
	BranchID    string                 `json:"branch_id,omitempty"`
	BranchName  string                 `json:"branch_name,omitempty"`
	ID          string                 `json:"id"`
	Version     string                 `json:"version"`
	PackageName string                 `json:"package_name,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	UsageNotice *UsageNotice           `json:"billing_notice,omitempty"`
}

type extractBuildPackageIDResponse struct {
	PackageID string `json:"package_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (c *Client) UploadBuild(ctx context.Context, req *UploadBuildRequest) (*UploadBuildResponse, error) {
	if err := validateGitBranchName(req.GitBranchName); err != nil {
		return nil, err
	}
	if err := validateBranchSelector(req.BranchID, req.BranchName); err != nil {
		return nil, err
	}
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

func (c *Client) uploadBuildViaSession(ctx context.Context, req *UploadBuildRequest, fileName string, fileSize int64) (*UploadBuildResponse, error) {
	sessionResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds/upload-session?source=cli_upload", req.AppID),
		uploadDestinationPayload(req, map[string]interface{}{
			"version":   req.Version,
			"file_name": fileName,
		}))
	if err != nil {
		return nil, err
	}
	var session uploadSessionResponse
	if err := parseResponse(sessionResp, &session); err != nil {
		if isUploadSessionUnsupported(err) {
			if req.BranchID != "" || req.BranchName != "" || req.GitBranchName != "" {
				return nil, fmt.Errorf("this backend does not support branch upload destinations: %w", err)
			}
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

func withUploadPin(payload map[string]interface{}, pin bool) map[string]interface{} {
	if pin {
		payload["pin"] = true
	}
	return payload
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

	finalizationClient := *c.httpClient
	finalizationClient.Timeout = BuildFinalizationTimeout
	createResp, err := c.doRequestWithRetryClient(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds", req.AppID),
		withUploadPin(map[string]interface{}{
			"upload_id": uploadID,
			"version":   req.Version,
			"metadata":  metadata,
		}, req.Pin), &finalizationClient)
	if err != nil {
		return nil, err
	}
	var created buildCreatedResponse
	if err := parseResponse(createResp, &created); err != nil {
		return nil, err
	}

	return &UploadBuildResponse{
		BranchID:    created.BranchID,
		BranchName:  created.BranchName,
		VersionID:   created.ID,
		Version:     created.Version,
		PackageID:   created.PackageName,
		Warnings:    artifactValidationWarnings(created.Metadata),
		UsageNotice: created.UsageNotice,
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
		"/api/v1/apps/%s/builds/upload-url?version=%s&file_name=%s&source=cli_upload",
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

func (c *Client) finalizeRowBackedUpload(ctx context.Context, req *UploadBuildRequest, versionID string) (*UploadBuildResponse, error) {
	extractResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/builds/%s/extract-package-id", versionID),
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
		fmt.Sprintf("/api/v1/apps/builds/%s/complete-upload", versionID),
		withUploadPin(map[string]interface{}{
			"version_id":   versionID,
			"metadata":     metadata,
			"package_name": extractResult.PackageID,
		}, req.Pin))
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

var multipartUploadThresholdBytes int64 = 64 * 1024 * 1024

const defaultMultipartUploadConcurrency = 4

const maxMultipartUploadConcurrency = 64

const uploadMaxStallPasses = 3

func uploadConcurrency() int {
	if v := strings.TrimSpace(os.Getenv("REVYL_UPLOAD_CONCURRENCY")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
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

type multipartUploadPartURL struct {
	PartNumber int    `json:"part_number"`
	UploadURL  string `json:"upload_url"`
}

type multipartUploadStartResponse struct {
	UploadID    string                   `json:"upload_id"`
	S3UploadID  string                   `json:"s3_upload_id"`
	PartSize    int64                    `json:"part_size"`
	Parts       []multipartUploadPartURL `json:"parts"`
	CompleteURL string                   `json:"complete_url"`
	AbortURL    string                   `json:"abort_url"`
}

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

type multipartCompletedPart struct {
	PartNumber int
	ETag       string
}

type multipartPlan struct {
	uploadID    string
	partSize    int64
	parts       []multipartUploadPartURL
	uploaded    map[int]string
	completeURL string
	abortURL    string
}

type uploadInterruptedError struct{ err error }

func (e *uploadInterruptedError) Error() string { return e.err.Error() }
func (e *uploadInterruptedError) Unwrap() error { return e.err }

type permanentUploadError struct{ err error }

func (e *permanentUploadError) Error() string { return e.err.Error() }
func (e *permanentUploadError) Unwrap() error { return e.err }

func (c *Client) uploadBuildMultipart(ctx context.Context, req *UploadBuildRequest, fileName string, fileSize int64) (*UploadBuildResponse, error) {
	fileHash, err := fileSHA256(req.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	stateKey := uploadDestinationStateKeyWithContext(req.AppID, req.Version, req.FilePath, fileSize, req.BranchID, req.BranchName, req.GitBranchName)

	if st := c.loadResumableUploadState(stateKey); st != nil {
		if !st.matches(fileSize, fileHash) {
			// The artifact was rebuilt; the parts in S3 belong to a stale
			// build, so abandon them (lifecycle rule sweeps them) and restart.
			debugLogf("multipart upload: artifact changed since saved upload; starting fresh")
			c.deleteResumableUploadState(stateKey)
		} else if st.Assembled {
			debugLogf("multipart upload: object already assembled; finalizing")
			return c.finalizeStagedUpload(ctx, req, stateKey, st.UploadID)
		} else if plan, rerr := c.resumeMultipartPlan(ctx, req, fileName, fileSize, st); rerr == nil {
			debugLogf(
				"multipart upload: resuming, %d/%d parts already in S3 (concurrency %d)",
				len(plan.uploaded), len(plan.parts), uploadConcurrency(),
			)
			return c.driveMultipartUpload(ctx, req, fileSize, stateKey, plan)
		} else if resumeSessionUnusable(rerr) {
			debugLogf("multipart upload: stored upload unusable (%v); starting fresh", rerr)
			c.deleteResumableUploadState(stateKey)
		} else {
			return nil, rerr
		}
	}

	startResp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/apps/%s/builds/multipart-upload/start?source=cli_upload", req.AppID),
		uploadDestinationPayload(req, map[string]interface{}{
			"version":   req.Version,
			"file_name": fileName,
			"file_size": fileSize,
		}))
	if err != nil {
		return nil, err
	}
	var start multipartUploadStartResponse
	if err := parseResponse(startResp, &start); err != nil {
		if isUploadSessionUnsupported(err) {
			if req.BranchID != "" || req.BranchName != "" || req.GitBranchName != "" {
				return nil, fmt.Errorf("this backend does not support branch upload destinations: %w", err)
			}
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

func (c *Client) driveMultipartUpload(ctx context.Context, req *UploadBuildRequest, fileSize int64, stateKey string, plan *multipartPlan) (*UploadBuildResponse, error) {
	completed, err := c.uploadMultipartParts(ctx, req.FilePath, fileSize, plan)
	if err != nil {
		return nil, c.classifyUploadFailure(stateKey, plan, err)
	}

	if err := c.completeMultipartUploadAtS3(ctx, plan.completeURL, completed); err != nil {
		return nil, c.classifyUploadFailure(stateKey, plan, err)
	}

	c.markUploadAssembled(stateKey)
	return c.finalizeStagedUpload(ctx, req, stateKey, plan.uploadID)
}

func (c *Client) finalizeStagedUpload(ctx context.Context, req *UploadBuildRequest, stateKey, uploadID string) (*UploadBuildResponse, error) {
	resp, err := c.createBuildFromStagedUpload(ctx, req, uploadID)
	if err != nil {
		return nil, fmt.Errorf("%w; re-run the same command to resume from where it stopped", err)
	}
	c.deleteResumableUploadState(stateKey)
	return resp, nil
}

func (c *Client) markUploadAssembled(stateKey string) {
	st := c.loadResumableUploadState(stateKey)
	if st == nil {
		return
	}
	st.Assembled = true
	c.saveResumableUploadState(stateKey, st)
}

func (c *Client) classifyUploadFailure(stateKey string, plan *multipartPlan, err error) error {
	var permanent *permanentUploadError
	if errors.As(err, &permanent) {
		c.bestEffortAbortMultipartUpload(plan.abortURL)
		c.deleteResumableUploadState(stateKey)
		return err
	}
	return fmt.Errorf("%w; re-run the same command to resume from where it stopped", err)
}

func resumeSessionUnusable(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	return apiErr.StatusCode == http.StatusConflict || apiErr.StatusCode == http.StatusGone
}

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

type multipartCompletePartXML struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type multipartCompleteRequestXML struct {
	XMLName xml.Name                   `xml:"CompleteMultipartUpload"`
	Parts   []multipartCompletePartXML `xml:"Part"`
}

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
	const floorBytesPerSecond = 32 * 1024
	timeout := 2*time.Minute + time.Duration(length/floorBytesPerSecond)*time.Second
	if timeout < UploadTimeout {
		return UploadTimeout
	}
	return timeout
}

func (c *Client) uploadPartWithRetry(ctx context.Context, file *os.File, part multipartUploadPartURL, offset, length int64) (string, error) {
	var etag string
	var permanent bool
	err := c.retryUploadAttempts(ctx, fmt.Sprintf("part %d upload", part.PartNumber), func(int) (bool, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, partUploadAttemptTimeout(length))
		defer cancel()

		reader := io.NewSectionReader(file, offset, length)
		uploadReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPut, part.UploadURL, reader)
		if err != nil {
			permanent = true
			return true, fmt.Errorf("failed to create part upload request: %w", err)
		}
		uploadReq.ContentLength = length

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

	return nil
}

func looksLikeLocalSplitAPKName(path string) bool {
	baseName := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(baseName, "split_") ||
		strings.HasPrefix(baseName, "split.") ||
		strings.HasPrefix(baseName, "config.")
}

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

type CreateBuildFromURLRequest struct {
	GitBranchName string `json:"git_branch_name,omitempty"`
	BranchID      string `json:"branch_id,omitempty"`
	BranchName    string `json:"branch_name,omitempty"`
	AppID         string `json:"-"`

	FromURL string `json:"from_url"`

	Headers map[string]string `json:"headers,omitempty"`

	Version string `json:"version"`

	Metadata map[string]interface{} `json:"metadata,omitempty"`

	Pin bool `json:"pin,omitempty"`
}

type CreateBuildFromURLResponse struct {
	BranchID   string `json:"branch_id,omitempty"`
	BranchName string `json:"branch_name,omitempty"`
	ID         string `json:"id"`

	AppID string `json:"app_id"`

	Version string `json:"version"`

	ArtifactURL string `json:"artifact_url"`

	PackageName string `json:"package_name,omitempty"`

	WasReused bool `json:"was_reused,omitempty"`
}

func (c *Client) CreateBuildFromURL(ctx context.Context, req *CreateBuildFromURLRequest) (*CreateBuildFromURLResponse, error) {
	if err := validateGitBranchName(req.GitBranchName); err != nil {
		return nil, err
	}
	if err := validateBranchSelector(req.BranchID, req.BranchName); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("/api/v1/apps/%s/builds/from-url", req.AppID)

	// A timeout can leave the backend ingest running, so repeating this request
	// can create concurrent work for the same artifact and version.
	resp, err := c.doRequestOnceWithClient(ctx, http.MethodPost, path, req, c.uploadClient)
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

type UploadHTTPError struct {
	StatusCode int
	Code       string
}

func (e *UploadHTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("upload failed with status %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("upload failed with status %d", e.StatusCode)
}

func safeUploadProviderCode(body []byte) string {
	var payload struct {
		Code string `xml:"Code"`
	}
	if err := xml.Unmarshal(body, &payload); err != nil {
		return ""
	}
	code := strings.TrimSpace(payload.Code)
	if code == "" || len(code) > 64 {
		return ""
	}
	for _, char := range code {
		if (char < 'a' || char > 'z') &&
			(char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') &&
			char != '_' && char != '-' && char != '.' {
			return ""
		}
	}
	return code
}

func formatUploadStatusError(statusCode int, body []byte, readErr error) error {
	if readErr != nil {
		return fmt.Errorf("%w (response could not be read)", &UploadHTTPError{StatusCode: statusCode})
	}
	return &UploadHTTPError{
		StatusCode: statusCode,
		Code:       safeUploadProviderCode(body),
	}
}

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
	return c.uploadFileWithHeadersAndRetry(
		ctx,
		uploadURL,
		map[string]string{"Content-Type": contentType},
		filePath,
		fileSize,
	)
}

func (c *Client) uploadFileWithHeadersAndRetry(ctx context.Context, uploadURL string, headers map[string]string, filePath string, fileSize int64) error {
	return c.uploadFileWithClientAndRetry(ctx, c.uploadClient, uploadURL, headers, filePath, fileSize)
}

func (c *Client) uploadFileWithClientAndRetry(ctx context.Context, uploadClient *http.Client, uploadURL string, headers map[string]string, filePath string, fileSize int64) error {
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
		for name, value := range headers {
			if strings.EqualFold(name, "Content-Length") {
				declaredSize, err := strconv.ParseInt(value, 10, 64)
				if err != nil || declaredSize != fileSize {
					return true, fmt.Errorf("signed upload Content-Length does not match the file size")
				}
				continue
			}
			uploadReq.Header.Set(name, value)
		}
		uploadReq.ContentLength = fileSize

		uploadResp, err := uploadClient.Do(uploadReq)
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

type BuildVersion struct {
	ArtifactKind string                 `json:"artifact_kind,omitempty"`
	BranchID     string                 `json:"branch_id,omitempty"`
	BranchName   string                 `json:"branch_name,omitempty"`
	ID           string                 `json:"id"`
	Version      string                 `json:"version"`
	UploadedAt   string                 `json:"uploaded_at"`
	PackageName  string                 `json:"package_name,omitempty"`
	PackageID    string                 `json:"package_id,omitempty"`
	IsCurrent    bool                   `json:"is_current,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

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

func (c *Client) ListBuildVersions(ctx context.Context, appID string) ([]BuildVersion, error) {
	page, err := c.ListBuildVersionsPage(ctx, appID, 1, defaultBuildVersionsPageSize)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (c *Client) ListBuildVersionsPage(ctx context.Context, appID string, page int, pageSize int) (*BuildVersionsPage, error) {
	return c.ListBuildVersionsWithOptions(ctx, appID, BuildListOptions{Page: page, PageSize: pageSize})
}

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

// GetExistingTestsByIDs retrieves many tests in one request, omitting tests
// that are missing or belong to another organization.
func (c *Client) GetExistingTestsByIDs(ctx context.Context, testIDs []string) ([]Test, error) {
	omitMissing := true
	resp, err := c.doRequest(ctx, "POST", "/api/v1/tests/get_test_info_batch",
		BatchTestInfoRequest{TestIds: testIDs, OmitMissing: &omitMissing})
	if err != nil {
		return nil, err
	}

	var result []Test
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return result, nil
}

type MobileTargetEntry struct {
	DeviceModel string `json:"device_model"`
	OSVersion   string `json:"os_version"`
}

type TestSummary struct {
	BranchID       string                 `json:"branch_id,omitempty"`
	BuildSelection BuildSelectionMode     `json:"build_selection,omitempty"`
	PinnedBuildID  string                 `json:"pinned_build_id,omitempty"`
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
	RunConfig      map[string]interface{} `json:"run_config,omitempty"`
}

type UpdateTestRequest struct {
	*SavedBuildSelection
	TestID          string                 `json:"-"`
	Name            string                 `json:"name,omitempty"`
	Description     string                 `json:"description,omitempty"`
	Platform        string                 `json:"platform,omitempty"`
	Tasks           interface{}            `json:"tasks,omitempty"`
	AppID           string                 `json:"app_id,omitempty"`
	PinnedVersionID string                 `json:"pinned_version,omitempty"`
	ExpectedVersion int                    `json:"expected_version,omitempty"`
	Force           bool                   `json:"-"`
	RunConfig       map[string]interface{} `json:"run_config,omitempty"`
}

type UpdateTestResponse struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

func (c *Client) UpdateTest(ctx context.Context, req *UpdateTestRequest) (*UpdateTestResponse, error) {
	if err := req.SavedBuildSelection.Validate(); err != nil {
		return nil, err
	}
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

type CreateTestRequest struct {
	*SavedBuildSelection
	PinnedVersionID string                 `json:"pinned_version,omitempty"`
	Name            string                 `json:"name"`
	Platform        string                 `json:"platform"`
	Tasks           interface{}            `json:"tasks"`
	AppID           string                 `json:"app_id,omitempty"`
	OrgID           string                 `json:"org_id,omitempty"`
	RunConfig       map[string]interface{} `json:"run_config,omitempty"`
}

type CreateTestResponse struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Name    string `json:"name,omitempty"`
}

func (c *Client) CreateTest(ctx context.Context, req *CreateTestRequest) (*CreateTestResponse, error) {
	if req == nil || strings.TrimSpace(req.AppID) == "" {
		return nil, fmt.Errorf(
			"a test must be associated with an app: provide an app_id before creating a test",
		)
	}

	if err := req.SavedBuildSelection.Validate(); err != nil {
		return nil, err
	}
	normalizedReq := &CreateTestRequest{
		SavedBuildSelection: req.SavedBuildSelection,
		PinnedVersionID:     req.PinnedVersionID,
		Name:                req.Name,
		Platform:            normalizePlatform(req.Platform),
		Tasks:               req.Tasks,
		AppID:               req.AppID,
		OrgID:               req.OrgID,
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

type CreateTestFromBlocksRequest struct {
	Blocks   []map[string]interface{} `json:"blocks"`
	Metadata map[string]interface{}   `json:"metadata"`
	Options  map[string]interface{}   `json:"options,omitempty"`
}

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

type ValidateAPIKeyResponse struct {
	UserID string `json:"user_id"`

	OrgID string `json:"org_id"`

	OrgName string `json:"org_name"`

	Email string `json:"email"`

	ConcurrencyLimit int `json:"concurrency_limit"`
}

type RevokeCLIAPIKeyRequest struct {
	APIKeyID string `json:"api_key_id"`
}

type RevokeCLIAPIKeyResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

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

// RevokeCLIAPIKey revokes the specified browser-generated CLI API key ID.
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

type GithubInstallURLResponse struct {
	// StateToken is the CSRF token bound to the user+org for this install.
	StateToken string `json:"state_token"`

	InstallURL string `json:"install_url"`
}

type GithubOrgRepository struct {
	Owner string `json:"owner"`

	Repo string `json:"repo"`

	InstallationID int64 `json:"installation_id"`
}

type GithubOrgInstallation struct {
	ID string `json:"id"`

	PropelOrgID string `json:"propel_org_id"`

	InstallationID int64 `json:"installation_id"`

	Status string `json:"status"`

	CreatedAt string `json:"created_at"`

	GHInstallationOwnerID string `json:"gh_installation_owner_id,omitempty"`

	VerifiedAt string `json:"verified_at,omitempty"`
}

type GithubRepositoriesResponse struct {
	Repositories []GithubOrgRepository `json:"repositories"`

	Installation *GithubOrgInstallation `json:"installation"`

	HasAccess bool `json:"has_access"`
}

func (r *GithubRepositoriesResponse) IsConnected() bool {
	if r == nil {
		return false
	}
	return r.Installation != nil && r.HasAccess
}

func (c *Client) GetGithubInstallURL(ctx context.Context) (*GithubInstallURLResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/integrations/github/install-url", nil)
	if err != nil {
		return nil, err
	}

	var result GithubInstallURLResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetGithubRepositories(ctx context.Context) (*GithubRepositoriesResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/integrations/github/repositories", nil)
	if err != nil {
		return nil, err
	}

	var result GithubRepositoriesResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type SimpleTest struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	AppID    string    `json:"app_id,omitempty"`
	AppName  string    `json:"app_name,omitempty"`
	Tags     []TestTag `json:"tags,omitempty"`
}

type CLISimpleTestListResponse struct {
	Tests []SimpleTest `json:"tests"`
	Count int          `json:"count"`
}

// ListOrgTests fetches one paginated slice of lightweight organization tests (including optional app and tag metadata).
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

func (c *Client) ListAllOrgTests(ctx context.Context, pageSize int) ([]SimpleTest, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	if pageSize > 500 {
		pageSize = 500
	}

	var all []SimpleTest
	offset := 0
	var prefetchedPages []*CLISimpleTestListResponse
	prefetched := false

	for {
		var resp *CLISimpleTestListResponse
		if len(prefetchedPages) > 0 {
			resp, prefetchedPages = prefetchedPages[0], prefetchedPages[1:]
		} else {
			var err error
			resp, err = c.ListOrgTests(ctx, pageSize, offset)
			if err != nil {
				return nil, err
			}
		}
		if resp == nil || len(resp.Tests) == 0 {
			break
		}

		all = append(all, resp.Tests...)
		offset += len(resp.Tests)

		if resp.Count > 0 && offset >= resp.Count {
			break
		}

		if len(resp.Tests) < pageSize {
			break
		}

		if !prefetched && resp.Count > 0 {
			prefetched = true
			var err error
			prefetchedPages, err = c.listOrgTestPages(ctx, pageSize, offset, resp.Count)
			if err != nil {
				return nil, err
			}
		}
	}

	return all, nil
}

const listAllOrgTestsConcurrency = 4

func (c *Client) listOrgTestPages(ctx context.Context, pageSize, startOffset, totalCount int) ([]*CLISimpleTestListResponse, error) {
	var offsets []int
	for offset := startOffset; offset < totalCount; offset += pageSize {
		offsets = append(offsets, offset)
	}
	pages := make([]*CLISimpleTestListResponse, len(offsets))
	errs := make([]error, len(offsets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, listAllOrgTestsConcurrency)
	for i, offset := range offsets {
		wg.Add(1)
		go func(i, offset int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pages[i], errs[i] = c.ListOrgTests(ctx, pageSize, offset)
		}(i, offset)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return pages, nil
}

type TestWithTags struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	Tags     []TestTag `json:"tags"`
}

type TestTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type CLITestListWithTagsResponse struct {
	Tests      []TestWithTags `json:"tests"`
	Count      int            `json:"count"`
	TotalCount *int           `json:"total_count,omitempty"`
}

// ListOrgTestsWithTags fetches one limit/offset page of organization tests with tags.
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

type App struct {
	AutoGitBranchEnabled       *bool  `json:"auto_git_branch_enabled,omitempty"`
	DefaultBranchID            string `json:"default_branch_id,omitempty"`
	DefaultBranchName          string `json:"default_branch_name,omitempty"`
	DefaultBranchPinnedBuildID string `json:"default_branch_pinned_build_id,omitempty"`
	ID                         string `json:"id"`
	Name                       string `json:"name"`
	Platform                   string `json:"platform"`
	Description                string `json:"description,omitempty"`
	SystemPrompt               string `json:"system_prompt,omitempty"`
	CurrentVersion             string `json:"current_version,omitempty"`
	LatestVersion              string `json:"latest_version,omitempty"`
	VersionsCount              int    `json:"versions_count"`
}

func (app App) AutomaticGitBranchEnabled() bool {
	return app.AutoGitBranchEnabled == nil || *app.AutoGitBranchEnabled
}

type AtlasQuery struct {
	BranchID            string
	RecentBuildLimit    int
	AppID               string
	BuildID             string
	ReportID            string
	TestID              string
	WorkflowExecutionID string
	SourceKind          string
	DeviceModel         string
	DeviceRuntime       string
	FromTime            string
	ToTime              string
	Visibility          string
	IncludeVariants     *bool
	IncludeDetails      *bool
	IncludeFlows        *bool
	Limit               int
	IncludeScreenshots  bool
	// LaunchTerms are <key>:<value> launch-variable filters. They travel only in
	// the launch-terms header so values, including secrets, never reach a URL.
	LaunchTerms []string
}

const AtlasLaunchTermsHeader = "X-Atlas-Launch-Terms"

func atlasLaunchTermsHeaders(terms []string) []map[string]string {
	if len(terms) == 0 {
		return nil
	}
	encoded := make([]string, len(terms))
	for index, term := range terms {
		encoded[index] = url.PathEscape(term)
	}
	return []map[string]string{{AtlasLaunchTermsHeader: strings.Join(encoded, ",")}}
}

type AtlasFilterQuery struct {
	AppID            string
	Group            string
	Key              string
	Prefix           string
	BranchID         string
	BuildID          string
	FromTime         string
	ToTime           string
	RecentBuildLimit int
}

func (c *Client) GetAtlasGraphFilters(ctx context.Context, query AtlasFilterQuery) (AtlasResponse, error) {
	values := url.Values{}
	for name, value := range map[string]string{
		"group":     query.Group,
		"key":       query.Key,
		"prefix":    query.Prefix,
		"branch_id": query.BranchID,
		"build_id":  query.BuildID,
		"from_time": query.FromTime,
		"to_time":   query.ToTime,
	} {
		if value != "" {
			values.Set(name, value)
		}
	}
	if query.RecentBuildLimit > 0 {
		values.Set("recent_build_limit", fmt.Sprintf("%d", query.RecentBuildLimit))
	}
	path := fmt.Sprintf("/api/v1/atlas/v2/apps/%s/graph/filters", url.PathEscape(query.AppID))
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
	return result, nil
}

type AtlasResponse map[string]interface{}

func (q AtlasQuery) values() url.Values {
	values := url.Values{}
	if q.BranchID != "" {
		values.Set("branch_id", q.BranchID)
	}
	if q.RecentBuildLimit > 0 {
		values.Set("recent_build_limit", fmt.Sprintf("%d", q.RecentBuildLimit))
	}
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
	if q.DeviceModel != "" {
		values.Set("device_model", q.DeviceModel)
	}
	if q.DeviceRuntime != "" {
		values.Set("device_runtime", q.DeviceRuntime)
	}
	if q.FromTime != "" {
		values.Set("from_time", q.FromTime)
	}
	if q.ToTime != "" {
		values.Set("to_time", q.ToTime)
	}
	if q.Visibility != "" {
		values.Set("visibility", q.Visibility)
	}
	if q.IncludeVariants != nil {
		values.Set("include_variants", fmt.Sprintf("%t", *q.IncludeVariants))
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
	resp, err := c.doRequestWithRetry(ctx, "GET", path, nil, atlasLaunchTermsHeaders(query.LaunchTerms)...)
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

func (c *Client) GetAtlasGraph(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/graph", url.PathEscape(query.AppID)), query)
}

func (c *Client) GetAtlasIndex(ctx context.Context, search string, limit, offset int, contentOnly, olapReady bool) (AtlasResponse, error) {
	values := url.Values{}
	values.Set("summary_first", "true")
	if search != "" {
		values.Set("search", search)
	}
	values.Set("limit", fmt.Sprintf("%d", limit))
	values.Set("offset", fmt.Sprintf("%d", offset))
	values.Set("content_only", fmt.Sprintf("%t", contentOnly))
	if olapReady {
		values.Set("olap_ready", "true")
	}
	path := "/api/v1/atlas/v2/index?" + values.Encode()
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GetAtlasEdgeRuns(
	ctx context.Context,
	query AtlasQuery,
	sourceID, targetID, actionType, actionLabel string,
	limit int,
) (AtlasResponse, error) {
	values := query.values()
	for _, key := range []string{
		"include_variants",
		"include_details",
		"include_flows",
		"include_screenshots",
		"limit",
	} {
		values.Del(key)
	}
	values.Set("source", sourceID)
	values.Set("target", targetID)
	values.Set("action_type", actionType)
	if actionLabel != "" {
		values.Set("action_label", actionLabel)
	}
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := fmt.Sprintf(
		"/api/v1/atlas/v2/apps/%s/edge-runs?%s",
		url.PathEscape(query.AppID),
		values.Encode(),
	)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result AtlasResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) GetAtlasStructure(ctx context.Context, query AtlasQuery) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/structure", url.PathEscape(query.AppID)), query)
}

func (c *Client) GetAtlasEntityObservations(ctx context.Context, query AtlasQuery, entityID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/entities/%s/observations", url.PathEscape(query.AppID), url.PathEscape(entityID)), query)
}

func (c *Client) GetAtlasNodeDetails(ctx context.Context, query AtlasQuery, nodeID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/nodes/%s/details", url.PathEscape(query.AppID), url.PathEscape(nodeID)), query)
}

func (c *Client) GetAtlasObservation(ctx context.Context, query AtlasQuery, observationID string) (AtlasResponse, error) {
	return c.getAtlas(ctx, fmt.Sprintf("/api/v1/atlas/v2/apps/%s/observations/%s", url.PathEscape(query.AppID), url.PathEscape(observationID)), query)
}

type CLIPaginatedAppsResponse struct {
	Items       []App `json:"items"`
	Total       int   `json:"total"`
	Page        int   `json:"page"`
	PageSize    int   `json:"page_size"`
	TotalPages  int   `json:"total_pages"`
	HasNext     bool  `json:"has_next"`
	HasPrevious bool  `json:"has_previous"`
}

// ListApps fetches one paginated page of organization apps.
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
	path := "/api/v1/apps/?" + values.Encode()

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
	if app.DefaultBranchID != "" {
		return false
	}
	if strings.TrimSpace(app.ID) == "" {
		return false
	}
	return app.VersionsCount == 0 || strings.TrimSpace(app.LatestVersion) == ""
}

func (c *Client) GetApp(ctx context.Context, appID string) (*App, error) {
	return c.getApp(ctx, appID, true)
}

func (c *Client) getApp(ctx context.Context, appID string, hydrate bool) (*App, error) {
	resp, err := c.doRequest(ctx, "GET",
		fmt.Sprintf("/api/v1/apps/%s", appID), nil)
	if err != nil {
		return nil, err
	}

	var result App
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if hydrate {
		result = c.hydrateSingleAppVersionSummary(ctx, result)
	}

	return &result, nil
}

type CreateAppRequest struct {
	Name string `json:"name"`

	Platform string `json:"platform"`

	Description string `json:"description,omitempty"`
}

type CreateAppResponse struct {
	DefaultBranchID   string `json:"default_branch_id,omitempty"`
	DefaultBranchName string `json:"default_branch_name,omitempty"`
	ID                string `json:"id"`

	Name string `json:"name"`

	Platform string `json:"platform"`
}

func (c *Client) CreateApp(ctx context.Context, req *CreateAppRequest) (*CreateAppResponse, error) {
	normalizedReq := &CreateAppRequest{
		Name:        req.Name,
		Platform:    normalizePlatform(req.Platform),
		Description: req.Description,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/apps/", normalizedReq)
	if err != nil {
		return nil, err
	}

	var result CreateAppResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func normalizePlatform(platform string) string {
	switch strings.ToLower(platform) {
	case "ios":
		return "iOS"
	case "android":
		return "Android"
	case "web":
		return "web"
	default:
		return platform
	}
}

type CLICreateWorkflowRequest struct {
	Name string `json:"name"`

	Tests []string `json:"tests"`

	// Schedule is the workflow schedule (defaults to "No Schedule").
	Schedule string `json:"schedule"`

	Owner string `json:"owner"`

	OrgID string `json:"org_id,omitempty"`
}

type CLICreateWorkflowResponse struct {
	Data struct {
		ID string `json:"id"`

		Name string `json:"name"`
	} `json:"data"`
}

func (r *CLICreateWorkflowResponse) GetID() string {
	return r.Data.ID
}

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

type CLITestStatusResponse struct {
	ID string `json:"id"`

	TestID string `json:"test_id"`

	Status string `json:"status"`

	Progress float64 `json:"progress"`

	CurrentStep string `json:"current_step,omitempty"`

	CurrentStepIndex int `json:"current_step_index"`

	TotalSteps int `json:"total_steps"`

	StepsCompleted int `json:"steps_completed"`

	ErrorMessage string `json:"error_message,omitempty"`

	Success *bool `json:"success,omitempty"`

	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	StartedAt string `json:"started_at,omitempty"`

	CompletedAt string `json:"completed_at,omitempty"`

	ExecutionTimeSeconds float64 `json:"execution_time_seconds,omitempty"`
}

type Workflow struct {
	DefaultConcurrencyLimit *int                   `json:"default_concurrency_limit"`
	ID                      string                 `json:"id"`
	Name                    string                 `json:"name"`
	Tests                   []string               `json:"tests,omitempty"`
	Schedule                string                 `json:"schedule,omitempty"`
	LocationConfig          map[string]interface{} `json:"location_config,omitempty"`
	OverrideLocation        bool                   `json:"override_location,omitempty"`
	BuildConfig             map[string]interface{} `json:"build_config,omitempty"`
	OverrideBuildConfig     bool                   `json:"override_build_config,omitempty"`
	RunConfig               map[string]interface{} `json:"run_config,omitempty"`
	OverrideRunConfig       bool                   `json:"override_run_config,omitempty"`
}

// RunConfigRetries returns the stored run_config.execution_mode.retries count regardless of OverrideRunConfig.
func (w *Workflow) RunConfigRetries() (int, bool) {
	executionMode, ok := w.RunConfig["execution_mode"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	switch retries := executionMode["retries"].(type) {
	case float64:
		return int(retries), true
	case int:
		return retries, true
	default:
		return 0, false
	}
}

func (w *Workflow) SetRunConfigRetries(retries int) {
	runConfig := make(map[string]interface{}, len(w.RunConfig)+1)
	for key, value := range w.RunConfig {
		runConfig[key] = value
	}
	executionMode := map[string]interface{}{}
	if stored, ok := w.RunConfig["execution_mode"].(map[string]interface{}); ok {
		for key, value := range stored {
			executionMode[key] = value
		}
	}
	executionMode["retries"] = retries
	runConfig["execution_mode"] = executionMode
	w.RunConfig = runConfig
}

func (w *Workflow) RunConfigUpdate() WorkflowRunConfigPatch {
	runConfig := w.RunConfig
	if runConfig == nil {
		runConfig = map[string]interface{}{}
	}
	return WorkflowRunConfigPatch{
		RunConfig:         runConfig,
		OverrideRunConfig: w.OverrideRunConfig,
	}
}

type CLIWorkflowLastExecution struct {
	Status    string   `json:"status"`
	LastRun   *string  `json:"last_run,omitempty"`
	Duration  *float64 `json:"duration,omitempty"`
	StartedAt *string  `json:"started_at,omitempty"`
	ID        *string  `json:"id,omitempty"`
}

type SimpleWorkflow struct {
	ID            string                    `json:"id"`
	Name          string                    `json:"name"`
	TestCount     int                       `json:"test_count"`
	LastExecution *CLIWorkflowLastExecution `json:"last_execution,omitempty"`
}

type CLIWorkflowListResponse struct {
	Workflows  []SimpleWorkflow `json:"data"`
	Count      int              `json:"count"`
	HasMore    *bool            `json:"has_more,omitempty"`
	Limit      *int             `json:"limit,omitempty"`
	Offset     *int             `json:"offset,omitempty"`
	TotalCount *int             `json:"total_count,omitempty"`
}

type WorkflowCatalogLimitError struct {
	MaxWorkflows int
	TotalCount   *int
}

func (e *WorkflowCatalogLimitError) Error() string {
	if e.TotalCount != nil {
		return fmt.Sprintf("workflow catalog contains %d workflows, exceeding the limit of %d", *e.TotalCount, e.MaxWorkflows)
	}
	return fmt.Sprintf("workflow catalog exceeds the limit of %d workflows", e.MaxWorkflows)
}

// ListWorkflows fetches one default-sized page of organization workflows.
func (c *Client) ListWorkflows(ctx context.Context) (*CLIWorkflowListResponse, error) {
	return c.ListWorkflowsPage(ctx, 200, 0)
}

func (c *Client) ListWorkflowsPage(ctx context.Context, limit, offset int) (*CLIWorkflowListResponse, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	path := fmt.Sprintf("/api/v1/workflows/get_with_last_status?limit=%d&offset=%d&history_limit=1", limit, offset)
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

func (c *Client) ListAllWorkflows(ctx context.Context, pageSize int) ([]SimpleWorkflow, error) {
	return c.listWorkflows(ctx, pageSize, 0)
}

func (c *Client) ListWorkflowsBounded(ctx context.Context, pageSize, maxWorkflows int) ([]SimpleWorkflow, error) {
	if maxWorkflows <= 0 {
		return nil, fmt.Errorf("maximum workflow count must be positive")
	}
	return c.listWorkflows(ctx, pageSize, maxWorkflows)
}

func (c *Client) listWorkflows(ctx context.Context, pageSize, maxWorkflows int) ([]SimpleWorkflow, error) {
	if pageSize <= 0 {
		pageSize = 200
	}
	if pageSize > 200 {
		pageSize = 200
	}
	if maxWorkflows > 0 && pageSize > maxWorkflows {
		pageSize = maxWorkflows
	}

	var all []SimpleWorkflow
	offset := 0
	const maxCatalogPages = 1000
	pages := 0

	for {
		if pages >= maxCatalogPages {
			return nil, fmt.Errorf("workflow catalog pagination exceeded %d pages", maxCatalogPages)
		}
		pages++
		resp, err := c.ListWorkflowsPage(ctx, pageSize, offset)
		if err != nil {
			return nil, err
		}
		if maxWorkflows > 0 && resp != nil && resp.TotalCount != nil && *resp.TotalCount > maxWorkflows {
			return nil, &WorkflowCatalogLimitError{MaxWorkflows: maxWorkflows, TotalCount: resp.TotalCount}
		}
		if resp == nil {
			break
		}
		if resp.TotalCount != nil && *resp.TotalCount < 0 {
			return nil, fmt.Errorf("workflow catalog returned an invalid negative total_count")
		}
		if resp.Offset != nil && *resp.Offset != offset {
			return nil, fmt.Errorf("workflow catalog returned an unexpected offset")
		}
		if len(resp.Workflows) == 0 {
			if (resp.HasMore != nil && *resp.HasMore) || (resp.TotalCount != nil && offset < *resp.TotalCount) {
				return nil, fmt.Errorf("workflow catalog pagination ended before all workflows were returned")
			}
			break
		}
		if maxWorkflows > 0 && len(all)+len(resp.Workflows) > maxWorkflows {
			return nil, &WorkflowCatalogLimitError{MaxWorkflows: maxWorkflows}
		}

		all = append(all, resp.Workflows...)
		offset += len(resp.Workflows)
		if resp.TotalCount != nil && offset > *resp.TotalCount {
			return nil, fmt.Errorf("workflow catalog returned more workflows than total_count")
		}

		if resp.HasMore != nil {
			if *resp.HasMore {
				if resp.TotalCount != nil && offset >= *resp.TotalCount {
					return nil, fmt.Errorf("workflow catalog pagination metadata is inconsistent")
				}
			} else {
				if resp.TotalCount != nil && offset < *resp.TotalCount {
					return nil, fmt.Errorf("workflow catalog pagination ended before total_count was reached")
				}
				break
			}
		} else if resp.TotalCount != nil {
			if offset >= *resp.TotalCount {
				break
			}
		} else if len(resp.Workflows) < pageSize {
			break
		}
		if maxWorkflows > 0 && offset >= maxWorkflows {
			return nil, &WorkflowCatalogLimitError{MaxWorkflows: maxWorkflows, TotalCount: resp.TotalCount}
		}
	}

	return all, nil
}

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

type WorkflowInfoTestItem struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Platform          string   `json:"platform"`
	LastStatus        *string  `json:"last_status,omitempty"`
	LastExecutionTime *string  `json:"last_execution_time,omitempty"`
	LastDuration      *float64 `json:"last_duration,omitempty"`
}

type WorkflowInfoResponse struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	TestInfo []WorkflowInfoTestItem `json:"test_info"`
}

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

func (c *Client) CreateHotReloadRelay(
	ctx context.Context,
	req HotReloadRelayCreateParams,
) (*HotReloadRelaySession, error) {
	resp, err := c.doRequestOnce(ctx, "POST", "/api/v1/hotreload/relays", req, traceHandoffHeadersFromContext(ctx))
	if err != nil {
		return nil, err
	}

	var result HotReloadRelaySession
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

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

func (c *Client) GetLatestBuildVersion(ctx context.Context, appID string) (*BuildVersion, error) {
	detail, err := c.ResolveBuildSelection(ctx, appID, BuildSelection{Mode: BuildSelectionModeLatest})
	if errors.Is(err, ErrBranchHasNoBuilds) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return detail.BuildVersion(), nil
}

type BuildVersionDetail struct {
	AutoGitBranchEnabled *bool                  `json:"auto_git_branch_enabled,omitempty"`
	BranchSource         BranchSource           `json:"branch_source,omitempty"`
	GitBranchName        string                 `json:"git_branch_name,omitempty"`
	BranchID             string                 `json:"branch_id,omitempty"`
	BranchName           string                 `json:"branch_name,omitempty"`
	ID                   string                 `json:"id"`
	AppID                string                 `json:"app_id,omitempty"`
	Version              string                 `json:"version"`
	DownloadURL          string                 `json:"download_url,omitempty"`
	PackageName          string                 `json:"package_name,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`
}

func (c *Client) GetBuildVersionDownloadURL(ctx context.Context, versionID string) (*BuildVersionDetail, error) {
	return c.GetBuildVersionDetail(ctx, versionID, true)
}

func (c *Client) GetBuildVersionDetail(ctx context.Context, versionID string, includeDownloadURL bool) (*BuildVersionDetail, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/builds/%s?include_download_url=%t", url.PathEscape(versionID), includeDownloadURL), nil)
	if err != nil {
		return nil, err
	}
	var result BuildVersionDetail
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if includeDownloadURL && result.DownloadURL == "" {
		return nil, fmt.Errorf("build version %s has no downloadable artifact", versionID)
	}
	return &result, nil
}

type StartDeviceRequest struct {
	GitBranchName  string             `json:"git_branch_name,omitempty"`
	BranchID       string             `json:"branch_id,omitempty"`
	BranchName     string             `json:"branch_name,omitempty"`
	BuildSelection BuildSelectionMode `json:"build_selection,omitempty"`
	PinnedBuildID  string             `json:"pinned_build_id,omitempty"`
	Platform       string             `json:"platform"`
	Web            *DeviceWebOptions  `json:"web,omitempty"`

	TestID string `json:"test_id,omitempty"`

	AppPackage string `json:"app_package,omitempty"`

	AppID string `json:"app_id,omitempty"`

	BuildID string `json:"build_id,omitempty"`

	AppURL string `json:"app_url,omitempty"`

	AppLink string `json:"app_link,omitempty"`

	LaunchEnvVarIds []string `json:"launch_env_var_ids,omitempty"`

	// EnvVars are inline launch environment variables (KEY=VALUE) applied to the
	// app's launch environment. Merged over LaunchEnvVarIds; inline takes precedence.
	EnvVars map[string]string `json:"env_vars,omitempty"`

	// LaunchArguments are non-secret inline iOS app arguments in exact token order.
	LaunchArguments []string `json:"launch_arguments,omitempty"`

	IsSimulation bool `json:"is_simulation,omitempty"`

	IdleTimeoutSeconds int `json:"idle_timeout_seconds,omitempty"`

	DeviceModel string `json:"device_model,omitempty"`

	OsVersion string `json:"os_version,omitempty"`

	DeviceRunnerID string `json:"device_runner_id,omitempty"`

	SessionID string `json:"session_id,omitempty"`

	ComputerSessionID string `json:"computer_session_id,omitempty"`

	// Inference "local" runs an iOS or Web session's steps inside a computer on
	// that computer's own model first.
	Inference string `json:"inference,omitempty"`

	RunConfig *DeviceRunConfig `json:"run_config,omitempty"`

	SessionProfile       *SessionProfileRequest       `json:"session_profile,omitempty"`
	SessionProfileSource *SessionProfileSource        `json:"session_profile_source,omitempty"`
	SessionProfileFiles  *SessionProfileCheckoutFiles `json:"session_profile_files,omitempty"`
}

type SessionProfileRequest struct {
	Instruction SessionProfileInstructionKind `json:"instruction"`
	Name        string                        `json:"name,omitempty"`
	Outputs     *AppliedSessionProfileOutputs `json:"outputs,omitempty"`
}

type AppliedSessionProfileOutputs struct {
	BrowserStorageState json.RawMessage `json:"browser_storage_state,omitempty"`
}

func (c *Client) GetBackendCapabilities(ctx context.Context) ([]string, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/backend-capabilities", nil)
	if err != nil {
		return nil, err
	}
	var result BackendCapabilities
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.Capabilities == nil {
		return nil, nil
	}
	capabilities := make([]string, 0, len(*result.Capabilities))
	for _, capability := range *result.Capabilities {
		capabilities = append(capabilities, string(capability))
	}
	return capabilities, nil
}

type DeviceRunConfig struct {
	MaxRetries int `json:"max_retries,omitempty"`

	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	ExecutionMode *DeviceExecutionModeConfig `json:"execution_mode,omitempty"`
}

type DeviceExecutionModeConfig struct {
	// SkipAppInstall prevents the backend boot path from installing the app.
	// Dev-loop flows use this so the CLI-owned install/relaunch step is the
	// single source of truth.
	SkipAppInstall bool `json:"skip_app_install,omitempty"`

	InitialOrientation string `json:"initial_orientation,omitempty"`

	InitialLocale string `json:"initial_locale,omitempty"`
}

// StartDevice starts a device session and returns the augmented StartDeviceResult.
func (c *Client) StartDevice(ctx context.Context, req *StartDeviceRequest) (*StartDeviceResult, error) {
	if req == nil {
		return nil, fmt.Errorf("start device request is nil")
	}
	if err := validateGitBranchName(req.GitBranchName); err != nil {
		return nil, err
	}
	if strings.EqualFold(req.Platform, "web") {
		canonicalRequest := *req
		canonicalRequest.Platform = "web"
		req = &canonicalRequest
	}
	if gitBranchNameFor(req.BuildSelection, req.GitBranchName) != req.GitBranchName {
		withoutGitBranch := *req
		withoutGitBranch.GitBranchName = ""
		req = &withoutGitBranch
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

	resp, err := c.doRequestOnce(ctx, "POST", "/api/v1/execution/start_device", req, headers)
	if err != nil {
		return nil, err
	}

	var result StartDeviceResult
	body, err := parseResponseWithRaw(resp, &result.StartDeviceResponse)
	if err != nil {
		return nil, err
	}
	billing := readBillingResponseFields(body)
	result.BillingURL = billing.BillingURL
	result.BillingAction = billing.BillingAction
	result.UpgradeBenefits = billing.UpgradeBenefits
	result.UsageNotice = billing.UsageNotice
	if result.Error != nil {
		message := withConcurrencyUpgradeHint(*result.Error, result.BillingURL)
		result.Error = &message
	}

	return &result, nil
}

// StartDeviceResult embeds StartDeviceResponse and shadows or augments selected billing fields.
type StartDeviceResult struct {
	StartDeviceResponse
	BillingURL      string           `json:"-"`
	BillingAction   BillingAction    `json:"-"`
	UpgradeBenefits *UpgradeBenefits `json:"-"`
	UsageNotice     *UsageNotice     `json:"-"`
}

func traceHandoffHeadersFromContext(ctx context.Context) map[string]string {
	handoff, ok := TraceHandoffFromContext(ctx)
	if !ok {
		return nil
	}
	return handoff.Headers()
}

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

type GroundElementRequest struct {
	Target string `json:"target"`

	ImageBase64 string `json:"image_base64"`

	Width int `json:"width"`

	Height int `json:"height"`

	Platform string `json:"platform,omitempty"`

	SessionID string `json:"session_id,omitempty"`
}

type GroundElementResponse struct {
	X int `json:"x"`

	Y int `json:"y"`

	Found bool `json:"found"`

	Error string `json:"error,omitempty"`
}

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

type DeviceSessionDetail struct {
	AppID           string                  `json:"app_id,omitempty"`
	BuildID         string                  `json:"build_id,omitempty"`
	BranchID        string                  `json:"branch_id,omitempty"`
	BranchName      string                  `json:"branch_name,omitempty"`
	BuildVersion    *string                 `json:"build_version,omitempty"`
	CommitSHA       *string                 `json:"commit_sha,omitempty"`
	ViewerKind      *string                 `json:"viewer_kind,omitempty"`
	ID              string                  `json:"id"`
	OrgID           string                  `json:"org_id"`
	Source          *string                 `json:"source"`
	SourceMetadata  map[string]interface{}  `json:"source_metadata,omitempty"`
	Platform        *string                 `json:"platform"`
	DeviceModel     *string                 `json:"device_model"`
	OsVersion       *string                 `json:"os_version"`
	Status          string                  `json:"status"`
	WhepURL         *string                 `json:"whep_url"`
	WorkflowRunID   *string                 `json:"workflow_run_id"`
	CreatedAt       *string                 `json:"created_at"`
	StartedAt       *string                 `json:"started_at"`
	EndedAt         *string                 `json:"ended_at"`
	ErrorMessage    *string                 `json:"error_message"`
	TraceID         *string                 `json:"trace_id"`
	ScreenWidth     *int                    `json:"screen_width"`
	ScreenHeight    *int                    `json:"screen_height"`
	ReportID        *string                 `json:"report_id"`
	HasVideo        bool                    `json:"has_video"`
	RecordingStatus *SessionRecordingStatus `json:"recording_status,omitempty"`
	StepCount       int                     `json:"step_count"`
	ActionCount     int                     `json:"action_count"`
	DurationSeconds *float64                `json:"duration_seconds"`
	TestID          *string                 `json:"test_id"`
	TestName        *string                 `json:"test_name"`
	UserEmail       *string                 `json:"user_email"`
	CanCancel       bool                    `json:"can_cancel"`
	SessionProfile  *SessionProfileReport   `json:"session_profile,omitempty"`

	ViewerLookupTimedOut bool `json:"viewer_lookup_timed_out,omitempty"`
}

var webAppFailurePattern = regexp.MustCompile(`; app_failure=([a-z_]{1,64}); (.+)$`)

// FailureReason extracts the trailing sentence after an "app_failure=<code>; " marker when present,
// otherwise returns the trimmed ErrorMessage (or "").
func (s DeviceSessionDetail) FailureReason() string {
	if s.ErrorMessage == nil {
		return ""
	}
	message := strings.TrimSpace(*s.ErrorMessage)
	if match := webAppFailurePattern.FindStringSubmatch(message); match != nil {
		return strings.TrimSpace(match[2])
	}
	return message
}

func (s DeviceSessionDetail) StopConfirmed() bool {
	switch strings.ToLower(s.Status) {
	case "completed", "cancelled", "failed", "timeout":
		releasedAt, ok := s.SourceMetadata["device_released_at"].(string)
		if !ok {
			return false
		}
		_, err := time.Parse(time.RFC3339Nano, releasedAt)
		return err == nil
	default:
		return false
	}
}

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

type CancelDeviceResponse struct {
	// Success acknowledges the request; settlement and release are separate facts.
	Success bool `json:"success"`

	Message string `json:"message,omitempty"`

	WorkflowRunID string `json:"workflow_run_id,omitempty"`

	DBUpdated        bool   `json:"db_updated,omitempty"`
	RequestAccepted  *bool  `json:"request_accepted,omitempty"`
	SessionSettled   *bool  `json:"session_settled,omitempty"`
	DeviceReleased   *bool  `json:"device_released,omitempty"`
	HatchetCancelled *bool  `json:"hatchet_cancelled,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
}

func (r CancelDeviceResponse) StopRequestAccepted() bool {
	return r.Success && (r.RequestAccepted == nil || *r.RequestAccepted)
}

func (r CancelDeviceResponse) StopConfirmed() bool {
	return r.SessionSettled != nil && *r.SessionSettled &&
		r.DeviceReleased != nil && *r.DeviceReleased
}

type DeviceSessionStopPendingError struct {
	Response CancelDeviceResponse
}

func (e *DeviceSessionStopPendingError) Error() string {
	return "device stop request accepted; session cleanup is not yet confirmed. Check 'revyl device list --json' or retry 'revyl device stop'"
}

func (c *Client) CancelDevice(ctx context.Context, workflowRunID string) (*CancelDeviceResponse, error) {
	return c.requestDeviceSessionStop(ctx, fmt.Sprintf("/api/v1/execution/device/status/cancel/%s", workflowRunID))
}

func (c *Client) StopDeviceSession(ctx context.Context, sessionID string) (*CancelDeviceResponse, error) {
	return c.requestDeviceSessionStop(ctx, fmt.Sprintf("/api/v1/execution/device/sessions/%s/stop", url.PathEscape(sessionID)))
}

const deviceStopRequestAttempts = 3

func (c *Client) requestDeviceSessionStop(ctx context.Context, path string) (*CancelDeviceResponse, error) {
	resp, err := c.doRequest(ctx, "POST", path, nil)
	attempts := 1
	for ; err != nil && attempts < deviceStopRequestAttempts && ctx.Err() == nil && isTransientTransportError(err); attempts++ {
		debugLogf("retrying device stop request (attempt %d/%d): %v", attempts+1, deviceStopRequestAttempts, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(calculateBackoff(attempts-1, c.retryBaseDelay, c.retryMaxDelay)):
		}
		resp, err = c.doRequest(ctx, "POST", path, nil)
	}
	if err != nil {
		if attempts > 1 {
			return nil, fmt.Errorf("device stop request failed after %d attempts: %w", attempts, err)
		}
		return nil, err
	}

	var result CancelDeviceResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if !result.StopRequestAccepted() {
		return &result, fmt.Errorf("device stop request was rejected: %s", result.Message)
	}
	if !result.StopConfirmed() {
		return &result, &DeviceSessionStopPendingError{Response: result}
	}
	return &result, nil
}

type DeleteTestResponse struct {
	ID string `json:"id"`

	Message string `json:"message"`
}

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

type CLIDeleteWorkflowResponse struct {
	ID string `json:"id"`

	Message string `json:"message"`
}

func (c *Client) DeleteWorkflow(ctx context.Context, workflowID string) (*CLIDeleteWorkflowResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/workflows/delete/%s", workflowID), nil)
	if err != nil {
		return nil, err
	}

	var fullResult DeleteWorkflowResponse
	if err := parseResponse(resp, &fullResult); err != nil {
		return nil, err
	}

	return &CLIDeleteWorkflowResponse{
		ID:      fullResult.Data.Id,
		Message: fullResult.Message,
	}, nil
}

type CLIDeleteAppResponse struct {
	Message string `json:"message"`

	DetachedTests int `json:"detached_tests,omitempty"`
}

func (c *Client) DeleteApp(ctx context.Context, appID string) (*CLIDeleteAppResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/apps/%s", appID), nil)
	if err != nil {
		return nil, err
	}

	var result CLIDeleteAppResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

type DeleteBuildVersionResponse struct {
	Message string `json:"message"`
}

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

type CLIModulesListResponse struct {
	Message string              `json:"message"`
	Result  []CLIModuleResponse `json:"result"`
}

type CLIModuleSingleResponse struct {
	Message string            `json:"message"`
	Result  CLIModuleResponse `json:"result"`
}

type CLICreateModuleRequest struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Blocks      []interface{} `json:"blocks"`
}

type CLIUpdateModuleRequest struct {
	Name            *string        `json:"name,omitempty"`
	Description     *string        `json:"description,omitempty"`
	Blocks          *[]interface{} `json:"blocks,omitempty"`
	ExpectedVersion *int           `json:"expected_version,omitempty"`
}

type CLIDeleteModuleResponse struct {
	Message string `json:"message"`
}

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

func (c *Client) RestoreModuleVersion(ctx context.Context, moduleID string, version int) error {
	body := map[string]int{"version": version}
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/api/v1/modules/%s/restore", moduleID), body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

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

type CLITagResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
	TestCount   int    `json:"test_count,omitempty"`
}

type CLITagListResponse struct {
	Tags []CLITagResponse `json:"tags"`
}

type CLICreateTagRequest struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type CLIUpdateTagRequest struct {
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CLISyncTagsRequest struct {
	TagNames []string `json:"tag_names"`
}

type CLISyncTagsResponse struct {
	TestID string             `json:"test_id"`
	Tags   []CLITagSyncResult `json:"tags"`
}

type CLITagSyncResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Color   string `json:"color,omitempty"`
	Created bool   `json:"created"`
}

type CLIBulkSyncTagsRequest struct {
	TestIDs      []string `json:"test_ids"`
	TagsToAdd    []string `json:"tags_to_add,omitempty"`
	TagsToRemove []string `json:"tags_to_remove,omitempty"`
}

type CLIBulkSyncTagsResponse struct {
	Results      []CLIBulkSyncResult `json:"results"`
	SuccessCount int                 `json:"success_count"`
	ErrorCount   int                 `json:"error_count"`
}

type CLIBulkSyncResult struct {
	TestID  string  `json:"test_id"`
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

type CLIDeleteTagResponse struct {
	Deleted bool   `json:"deleted"`
	TagID   string `json:"tag_id"`
}

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

func (c *Client) DeleteTag(ctx context.Context, tagID string) error {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/tests/tags/%s", tagID), nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

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

type CLIEnhancedTask struct {
	AppID                              string  `json:"app_id,omitempty"`
	BuildID                            string  `json:"build_id,omitempty"`
	ResolvedBuildID                    string  `json:"resolved_build_id,omitempty"`
	BuildVersion                       string  `json:"build_version,omitempty"`
	BranchID                           string  `json:"branch_id,omitempty"`
	BranchName                         string  `json:"branch_name,omitempty"`
	BranchIsInitialBranch              *bool   `json:"branch_is_initial_branch,omitempty"`
	ID                                 string  `json:"id"`
	TestID                             string  `json:"test_id"`
	Success                            *bool   `json:"success"`
	Progress                           float64 `json:"progress"`
	CurrentStep                        string  `json:"current_step,omitempty"`
	StepsCompleted                     int     `json:"steps_completed"`
	TotalSteps                         int     `json:"total_steps"`
	ErrorMessage                       string  `json:"error_message,omitempty"`
	Status                             string  `json:"status"`
	StartedAt                          string  `json:"started_at,omitempty"`
	CompletedAt                        string  `json:"completed_at,omitempty"`
	SessionID                          string  `json:"session_id,omitempty"`
	TestName                           string  `json:"test_name,omitempty"`
	GitBranchName                      string  `json:"git_branch_name,omitempty"`
	GitBranchIsRepositoryDefaultBranch *bool   `json:"git_branch_is_repository_default_branch,omitempty"`
	GitCommitSHA                       string  `json:"git_commit_sha,omitempty"`
	HasUncommittedDefinitionChanges    *bool   `json:"has_uncommitted_definition_changes,omitempty"`
	TestDefinitionSource               string  `json:"test_definition_source,omitempty"`
	WasQuarantined                     bool    `json:"was_quarantined,omitempty"`
	ExecutionTimeSeconds               float64 `json:"execution_time_seconds,omitempty"`
}

type CLIEnhancedHistoryItem struct {
	ID            string           `json:"id"`
	TestUID       string           `json:"test_uid"`
	ExecutionTime string           `json:"execution_time"`
	Status        string           `json:"status"`
	Duration      *float64         `json:"duration"`
	EnhancedTask  *CLIEnhancedTask `json:"enhanced_task"`
	HasReport     bool             `json:"has_report"`
}

type CLIEnhancedHistoryResponse struct {
	Items          []CLIEnhancedHistoryItem `json:"items"`
	TotalCount     int                      `json:"total_count"`
	RequestedCount int                      `json:"requested_count"`
	FoundCount     int                      `json:"found_count"`
}

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

func (c *Client) GetTestEnhancedHistory(ctx context.Context, testID string, limit, offset int) (*CLIEnhancedHistoryResponse, error) {
	return c.GetTestEnhancedHistoryWithOptions(ctx, testID, "", "", limit, offset)
}

func (c *Client) GetTestEnhancedHistoryWithOptions(ctx context.Context, testID, runBranchName, gitCommitSHA string, limit, offset int) (*CLIEnhancedHistoryResponse, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}

	query := url.Values{}
	if testID != "" {
		query.Set("test_id", testID)
	}
	if runBranchName != "" {
		query.Set("run_branch_name", runBranchName)
	}
	if gitCommitSHA != "" {
		query.Set("git_commit_sha", gitCommitSHA)
	}
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	path := "/api/v1/tests/get_test_enhanced_history?" + query.Encode()
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if err := requireHistoryFiltersCapability(resp, runBranchName != "" || gitCommitSHA != ""); err != nil {
		return nil, err
	}

	var result CLIEnhancedHistoryResponse
	if err := parseResponse(resp, &result); err != nil {
		if testID == "" && isMissingTestIDValidationError(err) {
			return nil, fmt.Errorf("%w: %w", ErrRunBranchAndGitCommitFiltersUnsupported, err)
		}
		return nil, err
	}
	return &result, nil
}

type CLITestRunQueuedInWorkflow struct {
	WorkflowExecutionID             string `json:"workflow_execution_id"`
	QueuedAt                        string `json:"queued_at"`
	GitBranchName                   string `json:"git_branch_name,omitempty"`
	GitCommitSHA                    string `json:"git_commit_sha,omitempty"`
	HasUncommittedDefinitionChanges bool   `json:"has_uncommitted_definition_changes"`
	TestDefinitionSource            string `json:"test_definition_source"`
}

type CLITestLatestRunResult struct {
	TestID                          string                      `json:"test_id"`
	TestName                        string                      `json:"test_name,omitempty"`
	RunResult                       TestRunResult               `json:"run_result"`
	LatestRun                       *CLIEnhancedHistoryItem     `json:"latest_run"`
	LatestRunQueuedInWorkflow       *CLITestRunQueuedInWorkflow `json:"latest_run_queued_in_workflow"`
	RunResultCountsSinceCreatedFrom *RunResultCounts            `json:"run_result_counts_since_created_from,omitempty"`
}

type CLITestLatestRunResultsResponse struct {
	Items                           []CLITestLatestRunResult `json:"items"`
	TotalCount                      int                      `json:"total_count"`
	LatestRunResultCounts           RunResultCounts          `json:"latest_run_result_counts"`
	QuarantinedFailedLatestRunCount int                      `json:"quarantined_failed_latest_run_count"`
	RunResultCountsSinceCreatedFrom *RunResultCounts         `json:"run_result_counts_since_created_from,omitempty"`
}

type LatestRunResultsFilter struct {
	TestID        string
	RunBranchName string
	GitCommitSHA  string
	CreatedFrom   *time.Time
}

var ErrLatestRunResultsUnsupported = errors.New("this Revyl server can't show test status by branch or commit yet")

func (c *Client) GetLatestRunResults(ctx context.Context, filter LatestRunResultsFilter, limit, offset int) (*CLITestLatestRunResultsResponse, error) {
	query := url.Values{}
	if filter.TestID != "" {
		query.Set("test_id", filter.TestID)
	}
	if filter.RunBranchName != "" {
		query.Set("run_branch_name", filter.RunBranchName)
	}
	if filter.GitCommitSHA != "" {
		query.Set("git_commit_sha", filter.GitCommitSHA)
	}
	if filter.CreatedFrom != nil {
		query.Set("created_from", filter.CreatedFrom.UTC().Format(time.RFC3339))
	}
	query.Set("limit", strconv.Itoa(limit))
	query.Set("offset", strconv.Itoa(offset))
	resp, err := c.doRequest(ctx, "GET", "/api/v1/tests/latest-run-results?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	var result CLITestLatestRunResultsResponse
	if err := parseResponse(resp, &result); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %w", ErrLatestRunResultsUnsupported, err)
		}
		return nil, err
	}
	return &result, nil
}

func isMissingTestIDValidationError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		return false
	}
	return slices.ContainsFunc(apiErr.ValidationIssues, func(issue APIValidationIssue) bool {
		return issue.Field == "query.test_id" && issue.Type == "missing"
	})
}

// CLIReportContextEnvelope pairs the raw response bytes with the typed OpenAPI-generated ReportContextResponse.
type CLIReportContextEnvelope struct {
	Raw    json.RawMessage
	Report *ReportContextResponse
}

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

type CLIShareableLinkRequest struct {
	TaskID          string `json:"task_id"`
	Origin          string `json:"origin,omitempty"`
	ExpirationHours *int   `json:"expiration_hours"`
}

type CLIShareableSessionLinkRequest struct {
	SessionID       string `json:"session_id"`
	Origin          string `json:"origin,omitempty"`
	ExpirationHours *int   `json:"expiration_hours"`
}

type CLIWorkflowShareableLinkRequest struct {
	WorkflowTaskID  string `json:"workflow_task_id"`
	Origin          string `json:"origin,omitempty"`
	ExpirationHours *int   `json:"expiration_hours"`
}

type CLIShareableLinkResponse struct {
	ShareableLink string `json:"shareable_link"`
}

type CLIWorkflowShareableLinkResponse struct {
	ShareableLink     string `json:"shareable_link"`
	ChildLinksCreated *int   `json:"child_links_created,omitempty"`
}

// GenerateShareableLink creates a public shareable link for a test execution
// report. origin is the front-end app URL (e.g. https://app.revyl.ai); the
// backend validates it against an allowlist and uses it as the link base.
// expirationHours is nil for a link that never expires.
func (c *Client) GenerateShareableLink(ctx context.Context, taskID, origin string, expirationHours *int) (*CLIShareableLinkResponse, error) {
	req := &CLIShareableLinkRequest{
		TaskID:          taskID,
		Origin:          origin,
		ExpirationHours: expirationHours,
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

// GenerateShareableSessionLink creates a public shareable link for a device
// session report. See GenerateShareableLink for the origin/expiration contract.
func (c *Client) GenerateShareableSessionLink(ctx context.Context, sessionID, origin string, expirationHours *int) (*CLIShareableLinkResponse, error) {
	req := &CLIShareableSessionLinkRequest{
		SessionID:       sessionID,
		Origin:          origin,
		ExpirationHours: expirationHours,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/report/async-run/generate_shareable_report_link_by_session", req)
	if err != nil {
		return nil, err
	}

	var result CLIShareableLinkResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GenerateWorkflowShareableLink creates a public shareable link for a workflow
// execution report. See GenerateShareableLink for the origin/expiration contract.
func (c *Client) GenerateWorkflowShareableLink(ctx context.Context, workflowTaskID, origin string, expirationHours *int) (*CLIWorkflowShareableLinkResponse, error) {
	req := &CLIWorkflowShareableLinkRequest{
		WorkflowTaskID:  workflowTaskID,
		Origin:          origin,
		ExpirationHours: expirationHours,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/workflows/share/generate_shareable_link", req)
	if err != nil {
		return nil, err
	}

	var result CLIWorkflowShareableLinkResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

type CLIWorkflowChildExecution struct {
	AutoGitBranchEnabled        *bool        `json:"auto_git_branch_enabled,omitempty"`
	ID                          string       `json:"id"`
	TestID                      string       `json:"test_id"`
	SessionID                   string       `json:"session_id,omitempty"`
	Status                      string       `json:"status,omitempty"`
	Success                     *bool        `json:"success,omitempty"`
	ErrorMessage                string       `json:"error_message,omitempty"`
	ExecutionTimeSeconds        *float64     `json:"execution_time_seconds,omitempty"`
	AppID                       string       `json:"app_id,omitempty"`
	AppName                     string       `json:"app_name,omitempty"`
	BuildID                     string       `json:"build_id,omitempty"`
	BuildVersion                string       `json:"build_version,omitempty"`
	BranchID                    string       `json:"branch_id,omitempty"`
	BranchName                  string       `json:"branch_name,omitempty"`
	BranchSource                BranchSource `json:"branch_source,omitempty"`
	GitBranchName               string       `json:"git_branch_name,omitempty"`
	TestDefinitionGitBranchName string       `json:"test_definition_git_branch_name,omitempty"`
}

type CLIWorkflowStatusResponse struct {
	ChildTasks          []CLIWorkflowChildExecution `json:"child_tasks,omitempty"`
	ExecutionID         string                      `json:"execution_id,omitempty"`
	WorkflowID          string                      `json:"workflow_id"`
	Status              string                      `json:"status"`
	Progress            float64                     `json:"progress"`
	CompletedTests      int                         `json:"completed_tests"`
	TotalTests          int                         `json:"total_tests"`
	PassedTests         int                         `json:"passed_tests"`
	FailedTests         int                         `json:"failed_tests"`
	StartedAt           string                      `json:"started_at,omitempty"`
	EstimatedCompletion string                      `json:"estimated_completion,omitempty"`
	Duration            string                      `json:"duration,omitempty"`
	ErrorMessage        string                      `json:"error_message,omitempty"`
}

type CLIWorkflowHistoryResponse struct {
	WorkflowID      string                      `json:"workflow_id"`
	Executions      []CLIWorkflowStatusResponse `json:"executions"`
	TotalCount      int                         `json:"total_count"`
	SuccessRate     float64                     `json:"success_rate"`
	AverageDuration *float64                    `json:"average_duration,omitempty"`
}

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

type CLIWorkflowDetailInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Tests       []string `json:"tests"`
}

type CLIWorkflowTestInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type CLIChildTaskReportInfo struct {
	AutoGitBranchEnabled        *bool        `json:"auto_git_branch_enabled,omitempty"`
	AppID                       string       `json:"app_id,omitempty"`
	AppName                     string       `json:"app_name,omitempty"`
	BranchSource                BranchSource `json:"branch_source,omitempty"`
	GitBranchName               string       `json:"git_branch_name,omitempty"`
	TestDefinitionGitBranchName string       `json:"test_definition_git_branch_name,omitempty"`
	BuildID                     string       `json:"build_id,omitempty"`
	BuildVersion                string       `json:"build_version,omitempty"`
	BranchID                    string       `json:"branch_id,omitempty"`
	BranchName                  string       `json:"branch_name,omitempty"`
	TaskID                      string       `json:"task_id"`
	TestID                      string       `json:"test_id,omitempty"`
	TestName                    string       `json:"test_name,omitempty"`
	Platform                    string       `json:"platform,omitempty"`
	Status                      string       `json:"status,omitempty"`
	Success                     *bool        `json:"success,omitempty"`
	StartedAt                   string       `json:"started_at,omitempty"`
	CompletedAt                 string       `json:"completed_at,omitempty"`
	Duration                    *float64     `json:"duration,omitempty"`
	ExecutionTimeSeconds        *float64     `json:"execution_time_seconds,omitempty"`
	StepsCompleted              *int         `json:"steps_completed,omitempty"`
	TotalSteps                  *int         `json:"total_steps,omitempty"`
	Progress                    *float64     `json:"progress,omitempty"`
	ErrorMessage                string       `json:"error_message,omitempty"`
}

type CLIUnifiedWorkflowReportResponse struct {
	WorkflowTask   CLIWorkflowTaskInfo      `json:"workflow_task"`
	WorkflowDetail *CLIWorkflowDetailInfo   `json:"workflow_detail,omitempty"`
	TestInfo       []CLIWorkflowTestInfo    `json:"test_info"`
	ChildTasks     []CLIChildTaskReportInfo `json:"child_tasks"`
}

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

func (c *Client) UpdateWorkflowRunConfig(ctx context.Context, workflowID string, request WorkflowRunConfigPatch) error {
	if request.RunConfig == nil && request.DefaultConcurrencyLimit == nil {
		return errors.New("update workflow run config: supply a run configuration or concurrency limit")
	}
	path := fmt.Sprintf("/api/v1/workflows/update_run_config/%s", workflowID)
	resp, err := c.doRequestWithRetry(ctx, "PUT", path, request)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

type CLIDeviceSessionHistoryResponse struct {
	Sessions []CLIDeviceSessionHistoryItem `json:"sessions"`
	Total    int                           `json:"total"`
	Limit    int                           `json:"limit"`
	Offset   int                           `json:"offset"`
}

type CLIDeviceSessionHistoryItem struct {
	ID                                 string                  `json:"id"`
	AppID                              *string                 `json:"app_id"`
	BuildID                            *string                 `json:"build_id"`
	AppName                            *string                 `json:"app_name"`
	BuildVersion                       *string                 `json:"build_version"`
	Source                             *string                 `json:"source"`
	ReportID                           *string                 `json:"report_id"`
	RecordingStatus                    *SessionRecordingStatus `json:"recording_status,omitempty"`
	TestExecutionID                    *string                 `json:"test_execution_id"`
	TestName                           *string                 `json:"test_name"`
	StartedAt                          *string                 `json:"started_at"`
	EndedAt                            *string                 `json:"ended_at"`
	StepCount                          int                     `json:"step_count"`
	Platform                           string                  `json:"platform"`
	Status                             string                  `json:"status"`
	CreatedAt                          string                  `json:"created_at"`
	Duration                           float64                 `json:"duration_seconds,omitempty"`
	BranchID                           string                  `json:"branch_id,omitempty"`
	BranchName                         string                  `json:"branch_name,omitempty"`
	BranchIsInitialBranch              *bool                   `json:"branch_is_initial_branch,omitempty"`
	GitBranchName                      string                  `json:"git_branch_name,omitempty"`
	GitBranchIsRepositoryDefaultBranch *bool                   `json:"git_branch_is_repository_default_branch,omitempty"`
	GitCommitSHA                       string                  `json:"git_commit_sha,omitempty"`
	HasUncommittedDefinitionChanges    *bool                   `json:"has_uncommitted_definition_changes,omitempty"`
}

func (c *Client) GetDeviceSessionHistory(ctx context.Context, limit, offset int) (*CLIDeviceSessionHistoryResponse, error) {
	return c.QueryDeviceSessionHistory(ctx, url.Values{"limit": {fmt.Sprint(limit)}, "offset": {fmt.Sprint(offset)}})
}

func (c *Client) GetDeviceSessionHistoryWithOptions(ctx context.Context, limit, offset int, appID, branchID, branchName string) (*CLIDeviceSessionHistoryResponse, error) {
	values := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	if appID != "" {
		values.Set("app_id", appID)
	}
	if branchID != "" {
		values.Set("branch_id", branchID)
	}
	if branchName != "" {
		values.Set("branch_name", branchName)
	}
	return c.QueryDeviceSessionHistory(ctx, values)
}

func (c *Client) QueryDeviceSessionHistory(ctx context.Context, filters url.Values) (*CLIDeviceSessionHistoryResponse, error) {
	if err := validateBranchSelector(filters.Get("branch_id"), filters.Get("branch_name")); err != nil {
		return nil, err
	}
	if (filters.Get("branch_id") != "" || filters.Get("branch_name") != "") && filters.Get("app_id") == "" {
		return nil, errors.New("a branch filter requires an app ID")
	}
	path := "/api/v1/execution/device-sessions/history?" + filters.Encode()
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if err := requireHistoryFiltersCapability(resp, filters.Get("run_branch_name") != "" || filters.Get("git_commit_sha") != ""); err != nil {
		return nil, err
	}
	var result CLIDeviceSessionHistoryResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ErrRunBranchAndGitCommitFiltersUnsupported reports that the server did not confirm support for
// run_branch_name and git_commit_sha history filters or refused a query without a test.
var ErrRunBranchAndGitCommitFiltersUnsupported = errors.New("this Revyl server can't filter history by Git branch or Git commit yet")

func requireHistoryFiltersCapability(resp *http.Response, requested bool) error {
	if !requested || resp.StatusCode >= http.StatusBadRequest {
		return nil
	}
	if historyFiltersSupported(resp, "run_branch_name", "git_commit_sha") {
		return nil
	}
	resp.Body.Close()
	return ErrRunBranchAndGitCommitFiltersUnsupported
}

func historyFiltersSupported(resp *http.Response, filterNames ...string) bool {
	supported := strings.Split(resp.Header.Get(historyFiltersCapabilityHeader), ",")
	for index, supportedName := range supported {
		supported[index] = strings.TrimSpace(supportedName)
	}
	for _, filterName := range filterNames {
		if !slices.Contains(supported, filterName) {
			return false
		}
	}
	return true
}

func (c *Client) DeleteBuildVersion(ctx context.Context, versionID string) (*DeleteBuildVersionResponse, error) {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/apps/builds/%s", versionID), nil)
	if err != nil {
		return nil, err
	}

	var result DeleteBuildVersionResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

type DeviceOpenURLTemplateRequest struct {
	URLTemplate string `json:"url_template"`
}

type DeviceOpenURLRequest struct {
	URL string `json:"url"`
}

// proxyNoRetryActions lists non-idempotent worker actions sent without automatic retries.
var proxyNoRetryActions = map[string]bool{
	"set_fold_state":         true,
	"start_screen_recording": true,
	"stop_screen_recording":  true,
	"input":                  true,
	"execute_step":           true,
	"install_async":          true,
	"push_notification":      true,
	"scroll":                 true,
	"open_url":               true,
}

var proxyLongRunningActions = map[string]bool{
	"install": true,
}

// proxyActionTimeouts gives single-attempt actions that outlast DefaultTimeout their own bound.
var proxyActionTimeouts = map[string]time.Duration{
	"launch":                LaunchTimeout,
	"stop_screen_recording": ScreenRecordingStopTimeout,
}

// ProxyWorkerRequest relays a device worker action through the backend proxy,
// selecting the HTTP method from action.
func (c *Client) ProxyWorkerRequest(ctx context.Context, workflowRunID, action string, body interface{}) ([]byte, int, error) {
	data, statusCode, _, err := c.ProxyWorkerRequestWithScreenToken(ctx, workflowRunID, action, body, false)
	return data, statusCode, err
}

type WorkerResolveTargetRequest struct {
	Target      string `json:"target"`
	SessionID   string `json:"session_id,omitempty"`
	ScreenToken string `json:"screen_token,omitempty"`
}

type WorkerResolveTargetResponse struct {
	X           *int   `json:"x"`
	Y           *int   `json:"y"`
	Found       bool   `json:"found"`
	Error       string `json:"error,omitempty"`
	ScreenToken string `json:"screen_token,omitempty"`
}

type RuntimeWorkerFailure struct {
	Error              string `json:"error"`
	ExecutionUncertain bool   `json:"execution_uncertain"`
	ActionApplied      *bool  `json:"action_applied"`
	Recorded           *bool  `json:"recorded"`
	FailureStage       string `json:"failure_stage,omitempty"`
	InputDispatched    *bool  `json:"input_dispatched,omitempty"`
}

type RuntimeLiveSummary struct {
	Sequence     int64   `json:"sequence"`
	Kind         string  `json:"kind"`
	Outcome      string  `json:"outcome"`
	URLOrigin    *string `json:"url_origin,omitempty"`
	RoutePattern *string `json:"route_pattern,omitempty"`
	StatusCode   *int    `json:"status_code,omitempty"`
}

type RuntimeSummaryPage struct {
	Entries             []RuntimeLiveSummary `json:"entries"`
	NextCursor          int64                `json:"next_cursor"`
	DroppedBeforeCursor bool                 `json:"dropped_before_cursor"`
}

func (c *Client) ProxyWorkerRequestWithScreenToken(ctx context.Context, workflowRunID, action string, body interface{}, noRetry bool) ([]byte, int, string, error) {
	resp, err := c.ProxyWorkerRequestDetailed(ctx, workflowRunID, action, body, noRetry)
	return resp.Body, resp.StatusCode, resp.ScreenToken, err
}

type ProxyWorkerResponse struct {
	Body        []byte
	StatusCode  int
	ScreenToken string
	RetryAfter  time.Duration
}

func (c *Client) ProxyWorkerRequestDetailed(ctx context.Context, workflowRunID, action string, body interface{}, noRetry bool) (ProxyWorkerResponse, error) {
	path := fmt.Sprintf("/api/v1/execution/device-proxy/%s/%s", workflowRunID, action)

	method := proxyWorkerMethodForAction(action)
	if method == http.MethodGet {
		body = nil
	}

	var resp *http.Response
	var err error
	if proxyLongRunningActions[action] {
		resp, err = c.doRequestOnceWithClient(ctx, method, path, body, c.uploadClient)
	} else if timeout, ok := proxyActionTimeouts[action]; ok {
		actionClient := *c.httpClient
		actionClient.Timeout = timeout
		resp, err = c.doRequestOnceWithClient(ctx, method, path, body, &actionClient)
	} else if noRetry || proxyNoRetryActions[action] {
		resp, err = c.doRequestOnce(ctx, method, path, body)
	} else {
		resp, err = c.doRequest(ctx, method, path, body)
	}
	if err != nil {
		return ProxyWorkerResponse{}, fmt.Errorf("proxy request failed for %s: %w", action, err)
	}
	defer resp.Body.Close()

	var responseReader io.Reader = resp.Body
	if noRetry {
		responseReader = io.LimitReader(resp.Body, 16*1024*1024+1)
	}
	data, err := io.ReadAll(responseReader)
	if err != nil {
		return ProxyWorkerResponse{StatusCode: resp.StatusCode}, fmt.Errorf("failed to read proxy response: %w", err)
	}
	if noRetry && len(data) > 16*1024*1024 {
		return ProxyWorkerResponse{StatusCode: resp.StatusCode}, fmt.Errorf("proxy response exceeded the size limit")
	}
	screenToken := ""
	if action == "screenshot" && resp.StatusCode == http.StatusOK {
		values := resp.Header.Values("X-Revyl-Screen-Token")
		if len(values) == 1 && len(values[0]) <= 8192 && !strings.ContainsAny(values[0], "\r\n") {
			screenToken = values[0]
		}
	}
	return ProxyWorkerResponse{
		Body:        data,
		StatusCode:  resp.StatusCode,
		ScreenToken: screenToken,
		RetryAfter:  ParseRetryAfter(resp.Header.Get("Retry-After")),
	}, nil
}

func ProxyWorkerActionIsRead(action string) bool {
	return proxyWorkerMethodForAction(action) == http.MethodGet
}

func proxyWorkerMethodForAction(action string) string {
	if idx := strings.Index(action, "?"); idx >= 0 {
		action = action[:idx]
	}
	switch action {
	case "device_state", "device_state/list":
		return http.MethodGet
	case "device_state/snapshot",
		"device_state/diff",
		"device_state/userdefaults",
		"device_state/sqlite/query":
		return http.MethodPost
	}
	base := action
	if idx := strings.Index(action, "/"); idx >= 0 {
		base = action[:idx]
	}
	switch base {
	case "screenshot", "health", "device_info", "step_status", "install_status", "hierarchy", "performance_metrics", "network_requests", "device_logs", "logs", "requests", "tabs":
		return http.MethodGet
	default:
		return http.MethodPost
	}
}

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

type CLIScriptInfo struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Code        string  `json:"code"`
	Runtime     string  `json:"runtime"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type CLIScriptListResponse struct {
	Scripts []CLIScriptInfo `json:"scripts"`
	Count   int             `json:"count"`
}

type CLICreateScriptRequest struct {
	Name        string  `json:"name"`
	Code        string  `json:"code"`
	Runtime     string  `json:"runtime"`
	Description *string `json:"description,omitempty"`
}

type CLIUpdateScriptRequest struct {
	Name        *string `json:"name,omitempty"`
	Code        *string `json:"code,omitempty"`
	Runtime     *string `json:"runtime,omitempty"`
	Description *string `json:"description,omitempty"`
}

type CLIScriptUsageResponse struct {
	Tests []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"tests"`
	Modules []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"modules"`
	Total int `json:"total"`
}

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

func (c *Client) ListAllScripts(ctx context.Context) ([]CLIScriptInfo, error) {
	const pageSize = 1000
	var scripts []CLIScriptInfo
	for {
		page, err := c.ListScripts(ctx, "", pageSize, len(scripts))
		if err != nil {
			return nil, fmt.Errorf("list saved scripts: %w", err)
		}
		scripts = append(scripts, page.Scripts...)
		if len(page.Scripts) < pageSize || len(scripts) >= page.Count {
			return scripts, nil
		}
	}
}

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

func (c *Client) DeleteScript(ctx context.Context, scriptID string) error {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/api/v1/tests/scripts/%s", scriptID), nil)
	if err != nil {
		return err
	}

	if resp.StatusCode == 204 {
		resp.Body.Close()
		return nil
	}

	return parseResponse(resp, nil)
}

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

// CustomVariable represents a test variable used in {{variable-name}} or
// {{variable_name}} syntax.
//
// Fields:
//   - ID: Unique identifier of the variable
//   - TestUID: Test UUID this variable belongs to
//   - VariableName: The variable name
//   - VariableValue: The variable value (may be empty for extraction-defined vars;
//     always empty for a secret, whose value never leaves the server)
//   - IsSecret: Whether the variable is a secret
type CustomVariable struct {
	ID            string `json:"id"`
	TestUID       string `json:"test_uid"`
	VariableName  string `json:"variable_name"`
	VariableValue string `json:"variable_value"`
	IsSecret      bool   `json:"is_secret"`
}

type CustomVariablesResponse struct {
	Message string           `json:"message"`
	Result  []CustomVariable `json:"result"`
}

type CustomVariableResponse struct {
	Message string         `json:"message"`
	Result  CustomVariable `json:"result"`
}

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

func (c *Client) RenameCustomVariable(ctx context.Context, testID, variableID, newName string) error {
	body := map[string]string{
		"test_uid":      testID,
		"variable_name": newName,
	}
	resp, err := c.doRequest(ctx, "PUT",
		fmt.Sprintf("/api/v1/variables/custom/update/%s", variableID), body)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

func (c *Client) DeleteCustomVariable(ctx context.Context, testID, name string) error {
	path := fmt.Sprintf("/api/v1/variables/custom/delete?test_uid=%s&variable_name=%s", url.QueryEscape(testID), url.QueryEscape(name))
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

func (c *Client) DeleteAllCustomVariables(ctx context.Context, testID string) error {
	path := fmt.Sprintf("/api/v1/variables/custom/delete_all?test_uid=%s", testID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

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

func (c *Client) RestoreTestVersion(ctx context.Context, testID string, version int) error {
	body := TestRestoreVersionRequest{Version: version}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/v1/tests/%s/restore", testID), body)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

type WorkflowTestWithPolicy struct {
	WorkflowTestID string `json:"id"`
	TestID         string `json:"test_id"`
	TestName       string `json:"test_name,omitempty"`
	Platform       string `json:"platform,omitempty"`
	FailurePolicy  string `json:"failure_policy,omitempty"`
}

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

func (c *Client) UpdateWorkflowTestFailurePolicy(ctx context.Context, workflowTestID string, policy string) error {
	body := map[string]string{"failure_policy": policy}
	resp, err := c.doRequest(ctx, "PATCH", fmt.Sprintf("/api/v1/workflows/workflow-tests/%s", workflowTestID), body)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

func (c *Client) UpdateWorkflowTests(ctx context.Context, workflowID string, testIDs []string) error {
	path := fmt.Sprintf("/api/v1/workflows/update_tests/%s", workflowID)
	resp, err := c.doRequest(ctx, "PUT", path, testIDs)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

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

type CLIOrgFileListResponse struct {
	Files []CLIOrgFile `json:"files"`
	Count int          `json:"count"`
}

type CLIOrgFileUploadRequest struct {
	Filename    string `json:"filename"`
	FileSize    int64  `json:"file_size"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type CLIOrgFileUploadResponse struct {
	File        CLIOrgFile `json:"file"`
	UploadURL   string     `json:"upload_url"`
	S3Key       string     `json:"s3_key,omitempty"`
	ExpiresIn   int        `json:"expires_in"`
	ContentType string     `json:"content_type"`
}

type CLIOrgFileDownloadResponse struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	ExpiresIn int    `json:"expires_in"`
}

type CLIOrgFileCompleteUploadRequest struct {
	S3Key       string `json:"s3_key,omitempty"`
	Filename    string `json:"filename,omitempty"`
	FileSize    int64  `json:"file_size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Description string `json:"description,omitempty"`
}

type CLIOrgFileUpdateRequest struct {
	Filename    *string `json:"filename,omitempty"`
	Description *string `json:"description,omitempty"`
}

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

func (c *Client) DownloadFileFromURL(ctx context.Context, fileURL, destPath string) error {
	return c.DownloadFileFromURLWithProgress(ctx, fileURL, destPath, nil)
}

func (c *Client) DownloadFileFromURLWithProgress(ctx context.Context, fileURL, destPath string, progress func(downloadedBytes, totalBytes int64)) error {
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

	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".revyl-download-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	copyWriter := io.Writer(tmp)
	if progress != nil {
		copyWriter = io.MultiWriter(tmp, &downloadProgressWriter{
			totalBytes: resp.ContentLength,
			report:     progress,
		})
		progress(0, resp.ContentLength)
	}
	copiedBytes, copyErr := io.Copy(copyWriter, resp.Body)
	if progress != nil {
		progress(copiedBytes, resp.ContentLength)
	}
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

type downloadProgressWriter struct {
	totalBytes int64
	report     func(downloadedBytes, totalBytes int64)
	downloaded int64
}

func (w *downloadProgressWriter) Write(p []byte) (int, error) {
	w.downloaded += int64(len(p))
	w.report(w.downloaded, w.totalBytes)
	return len(p), nil
}

type OrgLaunchVariable struct {
	ID                      string   `json:"id"`
	OrgID                   string   `json:"org_id"`
	Key                     string   `json:"key"`
	Kind                    string   `json:"kind"`
	Value                   string   `json:"value"`
	Arguments               []string `json:"arguments,omitempty"`
	IsSecret                bool     `json:"is_secret"`
	ReadableByAutomatedRuns *bool    `json:"readable_by_automated_runs,omitempty"`
	HasValue                *bool    `json:"has_value,omitempty"`
	Description             string   `json:"description,omitempty"`
	CreatedBy               string   `json:"created_by,omitempty"`
	CreatedAt               string   `json:"created_at,omitempty"`
	UpdatedAt               string   `json:"updated_at,omitempty"`
	AttachedTestCount       int      `json:"attached_test_count,omitempty"`
}

// UnmarshalJSON keeps launch variables from older API versions secret by
// default. Those responses predate is_secret and always represented encrypted
// values, so treating an omitted field as visible could expose them in CLI
// output or admit them to non-secret-only workflows.
func (v *OrgLaunchVariable) UnmarshalJSON(data []byte) error {
	type orgLaunchVariableWire OrgLaunchVariable
	decoded := orgLaunchVariableWire{IsSecret: true, Kind: "key_value"}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*v = OrgLaunchVariable(decoded)
	return nil
}

type OrgLaunchVariableWriteOptions struct {
	IsSecret                *bool
	ReadableByAutomatedRuns *bool
}

type OrgLaunchVariablesResponse struct {
	Message string              `json:"message"`
	Result  []OrgLaunchVariable `json:"result"`
}

type OrgLaunchVariableResponse struct {
	Message string            `json:"message"`
	Result  OrgLaunchVariable `json:"result"`
}

type OrgLaunchVariableDeleteResponse struct {
	Message           string            `json:"message"`
	Result            OrgLaunchVariable `json:"result"`
	DetachedTestCount int               `json:"detached_test_count"`
}

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

type GlobalVariableWriteOptions struct {
	IsSecret                *bool
	ReadableByAutomatedRuns *bool
}

// RevealGlobalVariable returns one global variable with its plaintext value,
// a secret's included. The backend audits every secret reveal.
func (c *Client) RevealGlobalVariable(ctx context.Context, variableID string) (*GlobalVariableResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/global/%s/plaintext", variableID)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result GlobalVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) AddGlobalVariable(ctx context.Context, name, value string, opts GlobalVariableWriteOptions) (*GlobalVariableResponse, error) {
	body := map[string]interface{}{
		"variable_name":  name,
		"variable_value": value,
	}
	if opts.IsSecret != nil {
		body["is_secret"] = *opts.IsSecret
	}
	if opts.ReadableByAutomatedRuns != nil {
		body["readable_by_automated_runs"] = *opts.ReadableByAutomatedRuns
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
	if opts.ReadableByAutomatedRuns != nil {
		body["readable_by_automated_runs"] = *opts.ReadableByAutomatedRuns
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

func (c *Client) DeleteGlobalVariable(ctx context.Context, variableID string) error {
	path := fmt.Sprintf("/api/v1/variables/global/%s", variableID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}

	return parseResponse(resp, nil)
}

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

func (c *Client) AddOrgLaunchVariable(ctx context.Context, key, value string, description *string, opts OrgLaunchVariableWriteOptions) (*OrgLaunchVariableResponse, error) {
	body := map[string]interface{}{
		"key":   key,
		"value": value,
	}
	if description != nil {
		body["description"] = *description
	}
	if opts.IsSecret != nil {
		body["is_secret"] = *opts.IsSecret
	}
	if opts.ReadableByAutomatedRuns != nil {
		body["readable_by_automated_runs"] = *opts.ReadableByAutomatedRuns
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

func (c *Client) UpdateOrgLaunchVariable(ctx context.Context, variableID string, key, value, description *string, opts OrgLaunchVariableWriteOptions) (*OrgLaunchVariableResponse, error) {
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
	if opts.IsSecret != nil {
		body["is_secret"] = *opts.IsSecret
	}
	if opts.ReadableByAutomatedRuns != nil {
		body["readable_by_automated_runs"] = *opts.ReadableByAutomatedRuns
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

// RevealOrgLaunchVariable returns one org launch variable with its plaintext
// value or arguments, a secret's included. The backend audits every secret reveal.
func (c *Client) RevealOrgLaunchVariable(ctx context.Context, variableID string) (*OrgLaunchVariableResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/org_launch_env/%s/plaintext", variableID)
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result OrgLaunchVariableResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

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

func (c *Client) GetRemoteBuildUploadURL(ctx context.Context, appID uuid.UUID, filename string, fileSize int64) (*RemoteBuildSourceUploadResponse, error) {
	size := int(fileSize)
	req := &RemoteBuildSourceUploadRequest{
		AppId:    appID,
		Filename: &filename,
		FileSize: &size,
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

// TriggerRemoteBuild dispatches a remote build job; when timeoutSeconds is non-nil it overrides
// req.TimeoutSeconds, while nil preserves any timeout already set on req.
func (c *Client) TriggerRemoteBuild(ctx context.Context, req *RemoteBuildRequest, timeoutSeconds *int) (*RemoteBuildTriggerResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("remote build request is nil")
	}
	if timeoutSeconds != nil {
		req.TimeoutSeconds = timeoutSeconds
	}
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

func (c *Client) GetRemoteBuildLogs(ctx context.Context, buildJobID string, afterID string) (*RemoteBuildLogsResponse, error) {
	path := fmt.Sprintf("/api/v1/apps/remote/%s/logs", buildJobID)
	values := url.Values{}
	if afterID != "" {
		values.Set("after_id", afterID)
	}
	values.Set("limit", "500")
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result RemoteBuildLogsResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) UploadFileToPresignedURL(ctx context.Context, uploadURL, contentType, filePath string, fileSize int64) error {
	return c.uploadFileWithRetry(ctx, uploadURL, contentType, filePath, fileSize)
}

func (c *Client) UploadFileToPresignedURLWithHeaders(ctx context.Context, uploadURL string, headers map[string]string, filePath string, fileSize int64) error {
	return c.uploadFileWithHeadersAndRetry(ctx, uploadURL, headers, filePath, fileSize)
}

// UploadFileToPresignedPost uploads filePath using the caller-supplied presigned S3 POST form policy and fields.
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

func (c *Client) CancelRemoteBuild(ctx context.Context, buildJobID string) error {
	path := fmt.Sprintf("/api/v1/apps/remote/%s", buildJobID)
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

type StoreKitConfigRefUpsertRequest struct {
	ScopeType string  `json:"scope_type"`
	ScopeID   string  `json:"scope_id"`
	Mode      string  `json:"mode"`
	FileID    *string `json:"file_id"`
}

type StoreKitConfigRef struct {
	ID        string  `json:"id"`
	ScopeType string  `json:"scope_type"`
	ScopeID   string  `json:"scope_id"`
	Mode      string  `json:"mode"`
	FileID    *string `json:"file_id"`
	Filename  *string `json:"filename"`
	FileSize  *int64  `json:"file_size"`
}

type StoreKitConfigRefResponse struct {
	Message string             `json:"message"`
	Result  *StoreKitConfigRef `json:"result,omitempty"`
}

func (c *Client) UpsertStoreKitConfigRef(ctx context.Context, req *StoreKitConfigRefUpsertRequest) (*StoreKitConfigRefResponse, error) {
	resp, err := c.doRequest(ctx, "PUT", "/api/v1/variables/ios_storekit/refs", req)
	if err != nil {
		return nil, err
	}
	var result StoreKitConfigRefResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetStoreKitConfigRef(ctx context.Context, scopeType, scopeID string) (*StoreKitConfigRefResponse, error) {
	path := fmt.Sprintf("/api/v1/variables/ios_storekit/refs?scope_type=%s&scope_id=%s",
		url.QueryEscape(scopeType), url.QueryEscape(scopeID))
	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var result StoreKitConfigRefResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteStoreKitConfigRef(ctx context.Context, scopeType, scopeID string) error {
	path := fmt.Sprintf("/api/v1/variables/ios_storekit/refs?scope_type=%s&scope_id=%s",
		url.QueryEscape(scopeType), url.QueryEscape(scopeID))
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == 204 {
		resp.Body.Close()
		return nil
	}
	return parseResponse(resp, nil)
}

func (c *Client) ListBuildCaches(ctx context.Context) (*BuildCacheListResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/apps/build-caches", nil)
	if err != nil {
		return nil, err
	}

	var result BuildCacheListResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteBuildCache evicts one remote-build cache key so the next build runs
// cold and re-uploads a fresh cache. Always safe: eviction can only cost
// build time, never correctness.
//
// Parameters:
//   - ctx: Context for cancellation
//   - key: Org-local cache key as written in .revyl config (e.g. "ios-derived-data")
//
// Returns:
//   - *BuildCacheDeleteResponse: The eviction result
//   - error: Any error that occurred (404 when no objects are stored for the key)
func (c *Client) DeleteBuildCache(ctx context.Context, key string) (*BuildCacheDeleteResponse, error) {
	path := fmt.Sprintf("/api/v1/apps/build-caches/%s", url.PathEscape(key))
	resp, err := c.doRequest(ctx, "DELETE", path, nil)
	if err != nil {
		return nil, err
	}

	var result BuildCacheDeleteResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
