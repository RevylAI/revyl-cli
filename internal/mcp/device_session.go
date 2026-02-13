// Package mcp provides the device session management for MCP server.
//
// DeviceSessionManager handles the lifecycle of cloud-hosted device sessions,
// including provisioning, idle timeout, worker HTTP proxying, and grounding
// model integration.
package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
)

// pngDimensions extracts width and height from a PNG file's IHDR chunk.
// Returns (width, height, ok). Falls back to (0, 0, false) if the data
// is not a valid PNG or too short.
func pngDimensions(data []byte) (int, int, bool) {
	// PNG signature (8 bytes) + IHDR length (4) + "IHDR" (4) + width (4) + height (4) = 24 bytes minimum
	if len(data) < 24 {
		return 0, 0, false
	}
	// Verify PNG signature
	pngSig := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	for i, b := range pngSig {
		if data[i] != b {
			return 0, 0, false
		}
	}
	// Width at offset 16, height at offset 20 (big-endian uint32)
	width := int(binary.BigEndian.Uint32(data[16:20]))
	height := int(binary.BigEndian.Uint32(data[20:24]))
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

// DeviceSession represents an active device session with its connection info.
type DeviceSession struct {
	// SessionID is the unique identifier for this session.
	SessionID string `json:"session_id"`

	// WorkflowRunID is the Hatchet workflow run powering this session.
	WorkflowRunID string `json:"workflow_run_id"`

	// WorkerBaseURL is the HTTP base URL for the device worker
	// (e.g. "https://worker-xxx.revyl.ai").
	WorkerBaseURL string `json:"worker_base_url"`

	// ViewerURL is a browser URL where the device screen can be watched live.
	ViewerURL string `json:"viewer_url"`

	// Platform is "ios" or "android".
	Platform string `json:"platform"`

	// StartedAt is when the session was created.
	StartedAt time.Time `json:"started_at"`

	// LastActivity is the timestamp of the most recent tool call.
	LastActivity time.Time `json:"last_activity"`

	// IdleTimeout is how long the session can be idle before auto-stop.
	IdleTimeout time.Duration `json:"idle_timeout"`
}

// DeviceSessionManager manages the active device session singleton.
//
// Only one session can be active at a time. All device tools auto-inject
// the active session's worker URL. The session auto-terminates after
// the idle timeout elapses.
type DeviceSessionManager struct {
	session   *DeviceSession
	mu        sync.RWMutex
	apiClient *api.Client
	idleTimer *time.Timer
	workDir   string

	// groundingURL is the base URL for the grounding API.
	// Defaults to the cognisim_action grounding endpoint.
	groundingURL string

	// httpClient is used for worker and grounding HTTP requests.
	// Has a 30-second timeout to prevent hanging on unresponsive services.
	httpClient *http.Client
}

// NewDeviceSessionManager creates a new session manager.
//
// Parameters:
//   - apiClient: The API client for backend communication.
//   - workDir: The working directory for persisting session state.
//
// Returns:
//   - *DeviceSessionManager: A new session manager instance.
func NewDeviceSessionManager(apiClient *api.Client, workDir string) *DeviceSessionManager {
	groundingURL := os.Getenv("REVYL_GROUNDING_URL")
	if groundingURL == "" {
		groundingURL = "https://action.revyl.ai"
	}

	return &DeviceSessionManager{
		apiClient:    apiClient,
		workDir:      workDir,
		groundingURL: groundingURL,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// StartSession provisions a new cloud device and sets it as the active session.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - platform: "ios" or "android".
//   - appID: Optional app ID to pre-install.
//   - buildVersionID: Optional build version ID.
//   - testID: Optional test ID to link the session to.
//   - sandboxID: Optional sandbox ID for dedicated device.
//   - idleTimeout: How long the session can be idle (default 5 min).
//
// Returns:
//   - *DeviceSession: The newly created session.
//   - error: Any error during provisioning.
func (m *DeviceSessionManager) StartSession(
	ctx context.Context,
	platform string,
	appID string,
	buildVersionID string,
	testID string,
	sandboxID string,
	idleTimeout time.Duration,
) (*DeviceSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing session if any
	if m.session != nil {
		m.stopSessionLocked(ctx)
	}

	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute
	}

	// Build the start device request
	req := &api.StartDeviceRequest{
		Platform:     platform,
		IsSimulation: testID == "",
	}
	if testID != "" {
		req.TestID = testID
	}

	// Start the device via backend API
	resp, err := m.apiClient.StartDevice(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to start device: %w", err)
	}

	if resp.WorkflowRunId == nil || *resp.WorkflowRunId == "" {
		errMsg := "no workflow run ID returned"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return nil, fmt.Errorf("failed to start device: %s", errMsg)
	}

	workflowRunID := *resp.WorkflowRunId

	// Poll for worker URL (up to 120 seconds)
	workerBaseURL, err := m.waitForWorkerURL(ctx, workflowRunID, 120*time.Second)
	if err != nil {
		// Cancel the device if we can't get the worker URL
		_, _ = m.apiClient.CancelDevice(ctx, workflowRunID)
		return nil, fmt.Errorf("device started but worker not ready: %w. Try again or call device_doctor() to diagnose", err)
	}

	// Build viewer URL
	baseURL := "https://app.revyl.ai"
	if os.Getenv("LOCAL") == "true" || os.Getenv("LOCAL") == "True" {
		baseURL = "http://localhost:3000"
	}
	viewerURL := fmt.Sprintf("%s/tests/execute?workflowRunId=%s", baseURL, workflowRunID)

	now := time.Now()
	session := &DeviceSession{
		SessionID:     workflowRunID,
		WorkflowRunID: workflowRunID,
		WorkerBaseURL: workerBaseURL,
		ViewerURL:     viewerURL,
		Platform:      platform,
		StartedAt:     now,
		LastActivity:  now,
		IdleTimeout:   idleTimeout,
	}

	m.session = session
	// Use context.Background() for the idle timer so it's not tied to the
	// caller's request context, which may be cancelled before the timer fires.
	m.resetIdleTimerLocked(context.Background())
	m.persistSession()

	return session, nil
}

// StopSession stops the active session and releases the device.
//
// Parameters:
//   - ctx: Context for cancellation.
//
// Returns:
//   - error: Any error during teardown.
func (m *DeviceSessionManager) StopSession(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil {
		return fmt.Errorf("no active device session")
	}

	m.stopSessionLocked(ctx)
	return nil
}

// GetActive returns the active session, or nil if none exists.
//
// Returns:
//   - *DeviceSession: The current active session, or nil.
func (m *DeviceSessionManager) GetActive() *DeviceSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.session
}

// ResetIdleTimer resets the idle timeout. Called on every tool invocation.
func (m *DeviceSessionManager) ResetIdleTimer() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil {
		return
	}

	m.session.LastActivity = time.Now()
	m.resetIdleTimerLocked(context.Background())
}

// stopSessionLocked stops the session without acquiring the lock. Caller must hold m.mu.
func (m *DeviceSessionManager) stopSessionLocked(ctx context.Context) {
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}

	if m.session != nil {
		if m.apiClient != nil {
			_, _ = m.apiClient.CancelDevice(ctx, m.session.WorkflowRunID)
		}
		m.session = nil
		m.clearPersistedSession()
	}
}

// resetIdleTimerLocked resets the idle timer. Caller must hold m.mu.
func (m *DeviceSessionManager) resetIdleTimerLocked(ctx context.Context) {
	if m.idleTimer != nil {
		m.idleTimer.Stop()
	}

	if m.session == nil {
		return
	}

	timeout := m.session.IdleTimeout
	m.idleTimer = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.session != nil {
			m.stopSessionLocked(ctx)
		}
	})
}

// waitForWorkerURL polls the backend until the worker URL is available.
func (m *DeviceSessionManager) waitForWorkerURL(ctx context.Context, workflowRunID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := m.apiClient.GetWorkerWSURL(ctx, workflowRunID)
		if err == nil && resp.WorkerWsUrl != nil && *resp.WorkerWsUrl != "" {
			// Convert WS URL to HTTP base URL
			return wsURLToHTTP(*resp.WorkerWsUrl), nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return "", fmt.Errorf("timed out waiting for worker URL after %v", timeout)
}

// wsURLToHTTP converts a WebSocket URL to its HTTP equivalent.
// e.g. "wss://worker-xxx.revyl.ai/ws/stream?token=abc" -> "https://worker-xxx.revyl.ai"
func wsURLToHTTP(wsURL string) string {
	httpURL := strings.Replace(wsURL, "wss://", "https://", 1)
	httpURL = strings.Replace(httpURL, "ws://", "http://", 1)

	// Strip the path component
	if idx := strings.Index(httpURL, "/ws/"); idx != -1 {
		httpURL = httpURL[:idx]
	}

	return httpURL
}

// persistSession saves the session state to disk for CLI mode.
func (m *DeviceSessionManager) persistSession() {
	if m.session == nil || m.workDir == "" {
		return
	}

	dir := filepath.Join(m.workDir, ".revyl")
	_ = os.MkdirAll(dir, 0o755)

	data, err := json.MarshalIndent(m.session, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(filepath.Join(dir, "device-session.json"), data, 0o644)
}

// clearPersistedSession removes the persisted session file.
func (m *DeviceSessionManager) clearPersistedSession() {
	if m.workDir == "" {
		return
	}
	_ = os.Remove(filepath.Join(m.workDir, ".revyl", "device-session.json"))
}

// LoadPersistedSession attempts to load a previously persisted session from disk.
// Used by CLI commands to resume an existing session.
//
// Returns:
//   - *DeviceSession: The loaded session, or nil if none exists.
func (m *DeviceSessionManager) LoadPersistedSession() *DeviceSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session != nil {
		return m.session
	}

	path := filepath.Join(m.workDir, ".revyl", "device-session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var session DeviceSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil
	}

	m.session = &session
	// Validate the persisted session is still alive by pinging the worker
	if err := m.healthCheckLocked(); err != nil {
		m.session = nil
		m.clearPersistedSession()
		return nil
	}
	// Restart idle timer for the restored session so it auto-terminates
	// if no tool calls come in within the timeout window.
	m.resetIdleTimerLocked(context.Background())
	return &session
}

// healthCheckLocked pings the worker /health endpoint to verify the session is live.
// Caller must hold m.mu.
func (m *DeviceSessionManager) healthCheckLocked() error {
	if m.session == nil {
		return fmt.Errorf("no session")
	}
	url := m.session.WorkerBaseURL + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("worker returned %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Worker HTTP Client - proxies requests to the device worker
// ---------------------------------------------------------------------------

// WorkerHTTPRequest represents a request to be sent to the worker.
type WorkerHTTPRequest struct {
	Method string
	Path   string
	Body   interface{}
}

// WorkerHTTPResponse represents a response from the worker.
type WorkerHTTPResponse struct {
	StatusCode int
	Body       []byte
}

// WorkerRequest sends an HTTP request to the active session's worker.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - method: HTTP method (GET, POST, etc.).
//   - path: URL path on the worker (e.g. "/tap").
//   - body: Request body to send as JSON (nil for GET requests).
//
// Returns:
//   - []byte: Response body bytes.
//   - error: Any error during the request.
func (m *DeviceSessionManager) WorkerRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	m.mu.RLock()
	session := m.session
	m.mu.RUnlock()

	if session == nil {
		return nil, fmt.Errorf("no active device session. Call start_device_session(platform='android') first")
	}

	url := session.WorkerBaseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("worker request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read worker response: %w", err)
	}

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("worker returned %d: %s. Call device_doctor() to check worker health", resp.StatusCode, string(respBody))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("worker returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// Screenshot captures the current device screen.
//
// Parameters:
//   - ctx: Context for cancellation.
//
// Returns:
//   - []byte: PNG image bytes.
//   - error: Any error during capture.
func (m *DeviceSessionManager) Screenshot(ctx context.Context) ([]byte, error) {
	return m.WorkerRequest(ctx, "GET", "/screenshot", nil)
}

// ---------------------------------------------------------------------------
// Grounding Client - resolves target descriptions to coordinates
// ---------------------------------------------------------------------------

// GroundingResponse represents the response from the grounding API.
type GroundingResponse struct {
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Confidence float64 `json:"confidence"`
	LatencyMs  float64 `json:"latency_ms"`
	Found      bool    `json:"found"`
	Error      string  `json:"error,omitempty"`
}

// ResolvedTarget holds the result of resolving a target string to coordinates.
type ResolvedTarget struct {
	X          int
	Y          int
	Confidence float64
}

// ResolveTarget takes a natural language target description, captures a screenshot,
// sends it to the grounding model, and returns pixel coordinates.
//
// This is the core method used by all dual-param device tools when the agent
// provides a target string instead of x, y coordinates.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - target: Natural language element description (e.g. "Sign In button").
//
// Returns:
//   - *ResolvedTarget: The resolved coordinates with confidence.
//   - error: If grounding fails or element is not found.
func (m *DeviceSessionManager) ResolveTarget(ctx context.Context, target string) (*ResolvedTarget, error) {
	// Step 1: Capture screenshot from worker
	screenshotBytes, err := m.Screenshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to capture screenshot for grounding: %w", err)
	}

	// Step 2: Base64-encode the screenshot
	imageBase64 := base64.StdEncoding.EncodeToString(screenshotBytes)

	// Step 3: Get image dimensions from PNG header; fall back to standard mobile
	width, height, ok := pngDimensions(screenshotBytes)
	if !ok {
		width = 1080
		height = 1920
	}

	// Step 4: Call grounding API
	groundReq := map[string]interface{}{
		"target":       target,
		"image_base64": imageBase64,
		"width":        width,
		"height":       height,
	}

	reqBody, err := json.Marshal(groundReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal grounding request: %w", err)
	}

	groundURL := m.groundingURL + "/api/v1/ground"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", groundURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create grounding request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("grounding request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read grounding response: %w", err)
	}

	var groundResp GroundingResponse
	if err := json.Unmarshal(respBody, &groundResp); err != nil {
		return nil, fmt.Errorf("failed to parse grounding response: %w", err)
	}

	if !groundResp.Found {
		errMsg := fmt.Sprintf("could not locate '%s' in the screenshot", target)
		if groundResp.Error != "" {
			errMsg = groundResp.Error
		}
		return nil, fmt.Errorf("%s. Try screenshot() to see the current screen and adjust the target description", errMsg)
	}

	return &ResolvedTarget{
		X:          groundResp.X,
		Y:          groundResp.Y,
		Confidence: groundResp.Confidence,
	}, nil
}

// GroundingURL returns the configured grounding API base URL.
func (m *DeviceSessionManager) GroundingURL() string {
	return m.groundingURL
}

// WorkDir returns the working directory used for session persistence.
func (m *DeviceSessionManager) WorkDir() string {
	return m.workDir
}
