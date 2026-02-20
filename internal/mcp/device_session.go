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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
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
	// Index is the local session index (tmux-style numbering, 0-based).
	Index int `json:"index"`

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

// persistedState is the on-disk format for device-sessions.json.
type persistedState struct {
	Active    int              `json:"active"`
	NextIdx   int              `json:"next_index"`
	OrgID     string           `json:"org_id"`
	UserEmail string           `json:"user_email"`
	Sessions  []*DeviceSession `json:"sessions"`
}

type screenAnchorState struct {
	Token       string
	CapturedAt  time.Time
	ActionsUsed int
	ImageBytes  []byte
	ImagePath   string
}

// DeviceSessionManager manages multiple concurrent device sessions.
//
// Sessions are identified by integer indices (tmux-style). One session
// is marked as "active" and is used by default when no explicit index
// is provided. The manager syncs with the backend to discover sessions
// started from other clients (browser, MCP, CLI in another directory).
type DeviceSessionManager struct {
	sessions      map[int]*DeviceSession
	activeIndex   int
	nextIndex     int
	mu            sync.RWMutex
	apiClient     *api.Client
	idleTimers    map[int]*time.Timer
	screenAnchors map[int]*screenAnchorState
	workDir       string
	orgID         string
	userEmail     string

	// httpClient is used for worker HTTP requests.
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
	return &DeviceSessionManager{
		apiClient:     apiClient,
		workDir:       workDir,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		sessions:      make(map[int]*DeviceSession),
		idleTimers:    make(map[int]*time.Timer),
		screenAnchors: make(map[int]*screenAnchorState),
		activeIndex:   -1,
	}
}

// shortPrefix returns a stable short identifier for logs.
func shortPrefix(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// reconcileSessionIDsByWorkflow updates local SessionID values from backend
// IDs using WorkflowRunID matches.
func reconcileSessionIDsByWorkflow(sessions map[int]*DeviceSession, backendIDsByWorkflow map[string]string) {
	for _, s := range sessions {
		if s == nil || s.WorkflowRunID == "" {
			continue
		}
		if backendID, ok := backendIDsByWorkflow[s.WorkflowRunID]; ok {
			s.SessionID = backendID
		}
	}
}

// ensureOrgInfoLocked populates cached org/user info.
// Caller must hold m.mu.
func (m *DeviceSessionManager) ensureOrgInfoLocked(ctx context.Context) error {
	if m.apiClient == nil {
		if m.orgID != "" {
			return nil
		}
		return fmt.Errorf("no API client configured")
	}
	validateResp, err := m.apiClient.ValidateAPIKey(ctx)
	if err != nil {
		// Keep operating with cached org/user only when validation is unavailable.
		// This preserves offline/cache-first behavior while preventing stale cache
		// from overriding a valid API key when validation succeeds.
		if m.orgID != "" {
			ui.PrintDebug("failed to validate API key, using cached org info: %v", err)
			return nil
		}
		return fmt.Errorf("failed to validate API key: %w", err)
	}
	m.orgID = validateResp.OrgID
	m.userEmail = validateResp.Email
	return nil
}

// backendSessionIDByWorkflowRunLocked resolves backend device session ID from workflow run ID.
// Returns empty string when not resolvable.
// Caller must hold m.mu.
func (m *DeviceSessionManager) backendSessionIDByWorkflowRunLocked(ctx context.Context, workflowRunID string) string {
	if workflowRunID == "" || m.apiClient == nil {
		return ""
	}
	if err := m.ensureOrgInfoLocked(ctx); err != nil {
		ui.PrintDebug("failed to load org info for workflow/session mapping: %v", err)
		return ""
	}

	activeResp, err := m.apiClient.GetActiveDeviceSessions(ctx, m.orgID)
	if err != nil {
		ui.PrintDebug("failed to fetch active sessions for workflow/session mapping: %v", err)
		return ""
	}
	for _, s := range activeResp.Sessions {
		if m.userEmail != "" && s.UserEmail != nil && *s.UserEmail != m.userEmail {
			continue
		}
		if s.WorkflowRunId != nil && *s.WorkflowRunId == workflowRunID {
			return s.Id
		}
	}
	return ""
}

// StartSession provisions a new cloud device and adds it to the session map.
// The new session is auto-set as active if it is the first session.
//
// StartSessionOptions configures device provisioning behavior.
type StartSessionOptions struct {
	Platform string

	// Optional app selection inputs.
	// Priority for app installation is:
	//   1. AppURL
	//   2. BuildVersionID (resolved to download URL)
	//   3. AppID (latest build resolved to download URL)
	AppID          string
	BuildVersionID string
	AppURL         string

	// Optional app launch link and package hints.
	AppLink    string
	AppPackage string

	// Optional test/session metadata.
	TestID    string
	SandboxID string

	// Optional idle timeout (defaults to 5 minutes).
	IdleTimeout time.Duration
}

// StartSession provisions a new cloud device and adds it to the session map.
// The new session is auto-set as active if it is the first session.
//
// Returns:
//   - int: The assigned session index.
//   - *DeviceSession: The newly created session.
//   - error: Any error during provisioning.
func (m *DeviceSessionManager) StartSession(
	ctx context.Context,
	opts StartSessionOptions,
) (int, *DeviceSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	platform := strings.ToLower(strings.TrimSpace(opts.Platform))
	if platform != "ios" && platform != "android" {
		return -1, nil, fmt.Errorf("platform must be 'ios' or 'android'")
	}

	idleTimeout := opts.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute
	}

	appPackage := strings.TrimSpace(opts.AppPackage)
	appURL := strings.TrimSpace(opts.AppURL)
	if appURL == "" && strings.TrimSpace(opts.BuildVersionID) != "" {
		detail, err := m.apiClient.GetBuildVersionDownloadURL(ctx, strings.TrimSpace(opts.BuildVersionID))
		if err != nil {
			return -1, nil, fmt.Errorf("failed to resolve build version %s: %w", opts.BuildVersionID, err)
		}
		appURL = strings.TrimSpace(detail.DownloadURL)
		if appPackage == "" {
			appPackage = strings.TrimSpace(detail.PackageName)
		}
	}
	if appURL == "" && strings.TrimSpace(opts.AppID) != "" {
		latest, err := m.apiClient.GetLatestBuildVersion(ctx, strings.TrimSpace(opts.AppID))
		if err != nil {
			return -1, nil, fmt.Errorf("failed to resolve latest build for app %s: %w", opts.AppID, err)
		}
		if latest != nil {
			detail, err := m.apiClient.GetBuildVersionDownloadURL(ctx, latest.ID)
			if err != nil {
				return -1, nil, fmt.Errorf("failed to resolve latest build artifact for app %s: %w", opts.AppID, err)
			}
			appURL = strings.TrimSpace(detail.DownloadURL)
			if appPackage == "" {
				appPackage = strings.TrimSpace(detail.PackageName)
			}
		}
	}

	// Build the start device request
	req := &api.StartDeviceRequest{
		Platform:     platform,
		IsSimulation: strings.TrimSpace(opts.TestID) == "",
	}
	if strings.TrimSpace(opts.TestID) != "" {
		req.TestID = strings.TrimSpace(opts.TestID)
	}
	if appURL != "" {
		req.AppURL = appURL
	}
	if strings.TrimSpace(opts.AppLink) != "" {
		req.AppLink = strings.TrimSpace(opts.AppLink)
	}
	if appPackage != "" {
		req.AppPackage = appPackage
	}
	_ = opts.SandboxID // Reserved for backend support.

	// Start the device via backend API
	resp, err := m.apiClient.StartDevice(ctx, req)
	if err != nil {
		return -1, nil, fmt.Errorf("failed to start device: %w", err)
	}

	if resp.WorkflowRunId == nil || *resp.WorkflowRunId == "" {
		errMsg := "no workflow run ID returned"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return -1, nil, fmt.Errorf("failed to start device: %s", errMsg)
	}

	workflowRunID := *resp.WorkflowRunId

	// Poll for worker URL (up to 120 seconds)
	workerBaseURL, err := m.waitForWorkerURL(ctx, workflowRunID, 120*time.Second)
	if err != nil {
		// Cancel the device if we can't get the worker URL
		_, _ = m.apiClient.CancelDevice(context.Background(), workflowRunID)
		return -1, nil, fmt.Errorf("device started but worker not ready: %w. Try again or call device_doctor() to diagnose", err)
	}

	// Wait for the device to actually be connected (up to 30 seconds).
	// The worker URL can exist before the device is fully provisioned,
	// so we poll /health until device_connected is true.
	tmpSession := &DeviceSession{WorkerBaseURL: workerBaseURL}
	deviceReady := false
	for i := 0; i < 15; i++ { // 15 * 2s = 30s max
		if err := m.healthCheckSession(tmpSession); err == nil {
			deviceReady = true
			break
		}
		select {
		case <-ctx.Done():
			_, _ = m.apiClient.CancelDevice(context.Background(), workflowRunID)
			return -1, nil, fmt.Errorf("cancelled while waiting for device to connect: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	if !deviceReady {
		// Device didn't connect in time but the worker exists — still return
		// the session so the agent can retry or diagnose, but log a warning.
		// The session is usable; the device may connect shortly after.
	}

	// Build viewer URL
	baseURL := "https://app.revyl.ai"
	if os.Getenv("LOCAL") == "true" || os.Getenv("LOCAL") == "True" {
		baseURL = "http://localhost:3000"
	}
	viewerURL := fmt.Sprintf("%s/tests/execute?workflowRunId=%s&platform=%s", baseURL, workflowRunID, platform)
	sessionID := m.backendSessionIDByWorkflowRunLocked(ctx, workflowRunID)
	if sessionID == "" {
		// Fallback for eventual-consistency windows; SyncSessions will reconcile
		// SessionID from WorkflowRunID on the next sync.
		sessionID = workflowRunID
	}

	idx := m.nextIndex
	m.nextIndex++

	now := time.Now()
	session := &DeviceSession{
		Index:         idx,
		SessionID:     sessionID,
		WorkflowRunID: workflowRunID,
		WorkerBaseURL: workerBaseURL,
		ViewerURL:     viewerURL,
		Platform:      platform,
		StartedAt:     now,
		LastActivity:  now,
		IdleTimeout:   idleTimeout,
	}

	m.sessions[idx] = session

	// Auto-set as active if this is the first session
	if m.activeIndex < 0 || len(m.sessions) == 1 {
		m.activeIndex = idx
	}

	// Use context.Background() for the idle timer so it's not tied to the
	// caller's request context, which may be cancelled before the timer fires.
	m.resetIdleTimerForSessionLocked(idx, context.Background())
	m.persistSessions()

	return idx, session, nil
}

// StopSession stops a specific session by index and releases the device.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: The session index to stop.
//
// Returns:
//   - error: Any error during teardown.
func (m *DeviceSessionManager) StopSession(ctx context.Context, index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[index]
	if !ok {
		return fmt.Errorf("no session at index %d", index)
	}

	cancelErr := m.stopSessionAtIndexLocked(ctx, index, session)
	m.recompactIndicesLocked()
	m.persistSessions()
	return cancelErr
}

// StopAllSessions stops all active sessions.
//
// Parameters:
//   - ctx: Context for cancellation.
//
// Returns:
//   - error: The first error encountered, if any.
func (m *DeviceSessionManager) StopAllSessions(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for idx, session := range m.sessions {
		if err := m.stopSessionAtIndexLocked(ctx, idx, session); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.recompactIndicesLocked()
	m.persistSessions()
	return firstErr
}

// GetActive returns the active session, or nil if none exists.
//
// Returns:
//   - *DeviceSession: The current active session, or nil.
func (m *DeviceSessionManager) GetActive() *DeviceSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.activeIndex < 0 {
		return nil
	}
	return m.sessions[m.activeIndex]
}

// GetSession returns the session at the given index, or nil if not found.
//
// Parameters:
//   - index: The session index.
//
// Returns:
//   - *DeviceSession: The session, or nil.
func (m *DeviceSessionManager) GetSession(index int) *DeviceSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[index]
}

// SetActive switches the active session pointer.
//
// Parameters:
//   - index: The session index to set as active.
//
// Returns:
//   - error: If the index does not exist.
func (m *DeviceSessionManager) SetActive(index int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[index]; !ok {
		return fmt.Errorf("no session at index %d", index)
	}
	m.activeIndex = index
	m.persistSessions()
	return nil
}

// ListSessions returns all active sessions sorted by index.
//
// Returns:
//   - []*DeviceSession: All live sessions.
func (m *DeviceSessionManager) ListSessions() []*DeviceSession {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*DeviceSession, 0, len(m.sessions))
	// Collect and sort by index
	indices := make([]int, 0, len(m.sessions))
	for idx := range m.sessions {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		result = append(result, m.sessions[idx])
	}
	return result
}

// ActiveIndex returns the current active session index (-1 if none).
func (m *DeviceSessionManager) ActiveIndex() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeIndex
}

// SessionCount returns the number of active sessions.
func (m *DeviceSessionManager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// ResolveSession resolves a session by index with fallback logic.
// Pass -1 to use the active session (with single-session fallback).
//
// Resolution priority:
//  1. Explicit index (>= 0) -> use that session, error if not found
//  2. Active index -> use active session
//  3. Single session -> use it implicitly
//  4. Error with guidance
//
// Parameters:
//   - index: The session index, or -1 for active/auto.
//
// Returns:
//   - *DeviceSession: The resolved session.
//   - error: If resolution fails.
func (m *DeviceSessionManager) ResolveSession(index int) (*DeviceSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if index >= 0 {
		s, ok := m.sessions[index]
		if !ok {
			return nil, fmt.Errorf("no session at index %d. Call list_device_sessions() to see active sessions", index)
		}
		return s, nil
	}

	// Try active
	if m.activeIndex >= 0 {
		if s, ok := m.sessions[m.activeIndex]; ok {
			return s, nil
		}
	}

	// Single-session fallback
	if len(m.sessions) == 1 {
		for _, s := range m.sessions {
			return s, nil
		}
	}

	if len(m.sessions) == 0 {
		return nil, fmt.Errorf("no active device sessions. Start one with start_device_session(platform='ios') or start_device_session(platform='android')")
	}

	return nil, fmt.Errorf("multiple sessions active. Specify session_index or call list_device_sessions() to see them")
}

// ResetIdleTimer resets the idle timeout for a specific session.
// Called on every tool invocation.
//
// Parameters:
//   - index: The session index to reset the timer for.
func (m *DeviceSessionManager) ResetIdleTimer(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[index]
	if !ok {
		return
	}

	session.LastActivity = time.Now()
	m.resetIdleTimerForSessionLocked(index, context.Background())
}

// MarkScreenshotAnchor records that a fresh screenshot was captured for a
// session and returns a token representing that anchor point.
func (m *DeviceSessionManager) MarkScreenshotAnchor(index int) string {
	return m.MarkScreenshotAnchorWithImage(index, nil)
}

// MarkScreenshotAnchorWithImage records a screenshot anchor and optionally
// stores the exact screenshot bytes for later analyzer replay.
func (m *DeviceSessionManager) MarkScreenshotAnchorWithImage(index int, imageBytes []byte) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[index]; !ok {
		return ""
	}
	if m.screenAnchors == nil {
		m.screenAnchors = make(map[int]*screenAnchorState)
	}

	token := fmt.Sprintf("screen-%d-%d", index, time.Now().UnixNano())
	imageCopy := make([]byte, len(imageBytes))
	copy(imageCopy, imageBytes)
	m.screenAnchors[index] = &screenAnchorState{
		Token:       token,
		CapturedAt:  time.Now(),
		ActionsUsed: 0,
		ImageBytes:  imageCopy,
	}
	return token
}

// PersistAnchorImage writes the anchored screenshot to disk and associates it
// with the given screen token.
func (m *DeviceSessionManager) PersistAnchorImage(index int, screenToken string, imageBytes []byte) (string, error) {
	if len(imageBytes) == 0 {
		return "", fmt.Errorf("cannot persist empty anchor image")
	}
	token := strings.TrimSpace(screenToken)
	if token == "" {
		return "", fmt.Errorf("screen_token is required to persist anchor image")
	}

	m.mu.Lock()
	anchor, ok := m.screenAnchors[index]
	if !ok || anchor == nil || anchor.Token == "" {
		m.mu.Unlock()
		return "", fmt.Errorf("no screenshot anchor found for session")
	}
	if token != anchor.Token {
		m.mu.Unlock()
		return "", fmt.Errorf("screen_token does not match the latest screenshot for this session")
	}
	m.mu.Unlock()

	path, err := m.writePNGArtifact(filepath.Join("screenshots", fmt.Sprintf("session-%d", index)), token+".png", imageBytes)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	if current, ok := m.screenAnchors[index]; ok && current != nil && current.Token == token {
		current.ImagePath = path
	}
	m.mu.Unlock()
	return path, nil
}

// LoadAnchorImage returns the anchored screenshot bytes and persisted path.
// It first uses in-memory bytes, then falls back to disk when available.
func (m *DeviceSessionManager) LoadAnchorImage(index int, screenToken string) ([]byte, string, error) {
	token := strings.TrimSpace(screenToken)
	if token == "" {
		return nil, "", fmt.Errorf("screen_token is required")
	}

	m.mu.RLock()
	anchor, ok := m.screenAnchors[index]
	if !ok || anchor == nil || anchor.Token == "" {
		m.mu.RUnlock()
		return nil, "", fmt.Errorf("no screenshot anchor found for session")
	}
	if token != anchor.Token {
		m.mu.RUnlock()
		return nil, "", fmt.Errorf("screen_token does not match the latest screenshot for this session")
	}
	mem := make([]byte, len(anchor.ImageBytes))
	copy(mem, anchor.ImageBytes)
	path := anchor.ImagePath
	m.mu.RUnlock()

	if len(mem) > 0 {
		return mem, path, nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("no image data available for anchor")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, err
	}
	return data, path, nil
}

// writePNGArtifact writes bytes to a deterministic location under .revyl/mcp.
func (m *DeviceSessionManager) writePNGArtifact(relDir, fileName string, imageBytes []byte) (string, error) {
	root := m.workDir
	if strings.TrimSpace(root) == "" {
		root = os.TempDir()
	}
	dir := filepath.Join(root, ".revyl", "mcp", relDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	finalPath := filepath.Join(dir, fileName)
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, imageBytes, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return finalPath, nil
}

// stopSessionAtIndexLocked stops a specific session without acquiring the lock.
// Caller must hold m.mu.
func (m *DeviceSessionManager) stopSessionAtIndexLocked(ctx context.Context, index int, session *DeviceSession) error {
	// Stop idle timer
	if timer, ok := m.idleTimers[index]; ok {
		timer.Stop()
		delete(m.idleTimers, index)
	}

	// Cancel on backend
	var cancelErr error
	if m.apiClient != nil && session != nil {
		resp, err := m.apiClient.CancelDevice(ctx, session.WorkflowRunID)
		if err != nil {
			ui.PrintDebug("CancelDevice failed for %s: %v", session.WorkflowRunID, err)
			cancelErr = fmt.Errorf("backend cancel failed: %w", err)
		} else {
			ui.PrintDebug("CancelDevice succeeded for %s: %s", session.WorkflowRunID, resp.Message)
		}
	}

	// Remove from map
	delete(m.sessions, index)
	delete(m.screenAnchors, index)

	// Adjust active index if needed
	if m.activeIndex == index {
		m.activeIndex = -1
		// Auto-switch to lowest remaining
		lowest := -1
		for idx := range m.sessions {
			if lowest < 0 || idx < lowest {
				lowest = idx
			}
		}
		m.activeIndex = lowest
	}

	return cancelErr
}

// resetIdleTimerForSessionLocked resets the idle timer for a specific session.
// Caller must hold m.mu.
func (m *DeviceSessionManager) resetIdleTimerForSessionLocked(index int, ctx context.Context) {
	if timer, ok := m.idleTimers[index]; ok {
		timer.Stop()
	}

	session, ok := m.sessions[index]
	if !ok {
		return
	}

	timeout := session.IdleTimeout
	m.idleTimers[index] = time.AfterFunc(timeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if s, ok := m.sessions[index]; ok {
			_ = m.stopSessionAtIndexLocked(ctx, index, s)
			m.persistSessions()
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

// persistSessions saves the multi-session state to disk.
func (m *DeviceSessionManager) persistSessions() {
	if m.workDir == "" {
		return
	}

	dir := filepath.Join(m.workDir, ".revyl")
	_ = os.MkdirAll(dir, 0o755)

	sessions := make([]*DeviceSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}

	state := persistedState{
		Active:    m.activeIndex,
		NextIdx:   m.nextIndex,
		OrgID:     m.orgID,
		UserEmail: m.userEmail,
		Sessions:  sessions,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile(filepath.Join(dir, "device-sessions.json"), data, 0o644)
}

// recompactIndicesLocked reassigns all session indices to be 0-based and contiguous,
// preserving relative order by current index. This prevents indices from growing
// unboundedly as sessions are started and stopped over time.
//
// Caller must hold m.mu. This method also recreates idle timer closures so they
// capture the correct new index values (idle timer callbacks close over the index).
func (m *DeviceSessionManager) recompactIndicesLocked() {
	if len(m.sessions) == 0 {
		m.nextIndex = 0
		m.activeIndex = -1
		clear(m.screenAnchors)
		return
	}

	// Collect current indices and sort to preserve relative order.
	oldIndices := make([]int, 0, len(m.sessions))
	for idx := range m.sessions {
		oldIndices = append(oldIndices, idx)
	}
	sort.Ints(oldIndices)

	// Check if already compact (0..n-1 contiguous). Skip work if so.
	alreadyCompact := true
	for i, oldIdx := range oldIndices {
		if oldIdx != i {
			alreadyCompact = false
			break
		}
	}
	if alreadyCompact {
		for idx := range m.screenAnchors {
			if _, ok := m.sessions[idx]; !ok {
				delete(m.screenAnchors, idx)
			}
		}
		m.nextIndex = len(m.sessions)
		return
	}

	// Build old-to-new mapping and rebuild the sessions map.
	oldToNew := make(map[int]int, len(oldIndices))
	newSessions := make(map[int]*DeviceSession, len(oldIndices))
	for newIdx, oldIdx := range oldIndices {
		oldToNew[oldIdx] = newIdx
		session := m.sessions[oldIdx]
		session.Index = newIdx
		newSessions[newIdx] = session
	}
	m.sessions = newSessions

	newAnchors := make(map[int]*screenAnchorState, len(m.screenAnchors))
	for oldIdx, anchor := range m.screenAnchors {
		newIdx, ok := oldToNew[oldIdx]
		if !ok {
			continue
		}
		newAnchors[newIdx] = anchor
	}
	m.screenAnchors = newAnchors

	// Remap active index.
	if m.activeIndex >= 0 {
		if newIdx, ok := oldToNew[m.activeIndex]; ok {
			m.activeIndex = newIdx
		} else {
			m.activeIndex = -1
		}
	}

	// Recreate idle timers with fresh closures capturing new indices.
	// Old timers must be stopped first to prevent stale-index callbacks.
	newTimers := make(map[int]*time.Timer, len(m.idleTimers))
	now := time.Now()
	for oldIdx, timer := range m.idleTimers {
		timer.Stop()
		newIdx, ok := oldToNew[oldIdx]
		if !ok {
			continue
		}
		session, exists := m.sessions[newIdx]
		if !exists {
			continue
		}
		timeout := session.IdleTimeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}

		remaining := timeout
		if !session.LastActivity.IsZero() {
			remaining = session.LastActivity.Add(timeout).Sub(now)
		}
		if remaining <= 0 {
			remaining = time.Millisecond
		}

		capturedIdx := newIdx // explicit capture for closure
		newTimers[capturedIdx] = time.AfterFunc(remaining, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if s, ok := m.sessions[capturedIdx]; ok {
				_ = m.stopSessionAtIndexLocked(context.Background(), capturedIdx, s)
				m.persistSessions()
			}
		})
	}
	m.idleTimers = newTimers

	m.nextIndex = len(m.sessions)
}

// loadLocalCache reads device-sessions.json from disk into memory.
// Also handles migration from old device-session.json (singular) format.
// Does NOT validate sessions against the backend.
func (m *DeviceSessionManager) loadLocalCache() {
	if m.workDir == "" {
		return
	}

	// Try new format first
	path := filepath.Join(m.workDir, ".revyl", "device-sessions.json")
	data, err := os.ReadFile(path)
	if err == nil {
		var state persistedState
		if json.Unmarshal(data, &state) == nil {
			m.activeIndex = state.Active
			m.nextIndex = state.NextIdx
			if state.OrgID != "" {
				m.orgID = state.OrgID
			}
			if state.UserEmail != "" {
				m.userEmail = state.UserEmail
			}
			for _, s := range state.Sessions {
				m.sessions[s.Index] = s
			}
			// Recompact indices to fill gaps from stale persisted state.
			m.recompactIndicesLocked()
			return
		}
	}

	// Migration: try old singular device-session.json
	oldPath := filepath.Join(m.workDir, ".revyl", "device-session.json")
	oldData, oldErr := os.ReadFile(oldPath)
	if oldErr != nil {
		return
	}

	var oldSession DeviceSession
	if json.Unmarshal(oldData, &oldSession) != nil {
		return
	}

	// Migrate to new format
	oldSession.Index = 0
	m.sessions[0] = &oldSession
	m.activeIndex = 0
	m.nextIndex = 1
	m.persistSessions()

	// Clean up old file
	_ = os.Remove(oldPath)

	// Recompact indices to fill gaps from stale persisted state.
	m.recompactIndicesLocked()
}

// LoadPersistedSession loads sessions from the local cache file.
// This is used by CLI commands that use cache-first strategy.
// Deprecated: use loadLocalCache() directly within the manager.
//
// Returns:
//   - *DeviceSession: The active session, or nil if none exists.
func (m *DeviceSessionManager) LoadPersistedSession() *DeviceSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) > 0 && m.activeIndex >= 0 {
		if s, ok := m.sessions[m.activeIndex]; ok {
			return s
		}
	}

	m.loadLocalCache()

	if m.activeIndex >= 0 {
		return m.sessions[m.activeIndex]
	}
	return nil
}

// checkSessionStatusOnFailure queries the backend for the session's actual status
// when the worker is unreachable. This turns vague network errors into clear messages
// like "session was stopped externally".
//
// Parameters:
//   - session: The session to check status for.
//
// Returns a human-readable reason string, or "" if the status can't be determined.
func (m *DeviceSessionManager) checkSessionStatusOnFailure(session *DeviceSession) string {
	if session == nil || m.apiClient == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := m.apiClient.GetWorkerWSURL(ctx, session.WorkflowRunID)
	if err != nil {
		return "" // can't reach backend either
	}
	switch resp.Status {
	case api.WorkerConnectionResponseStatusStopped, api.WorkerConnectionResponseStatusCancelled:
		return "session was stopped externally (from browser or another client)"
	case api.WorkerConnectionResponseStatusFailed:
		return "session failed on the worker"
	default:
		return ""
	}
}

// workerHealthResponse represents the JSON body returned by the worker /health endpoint.
type workerHealthResponse struct {
	Status          string `json:"status"`
	DeviceConnected bool   `json:"device_connected"`
}

// healthCheckSession pings the worker /health endpoint to verify the session is live
// and the device is connected.
//
// Parameters:
//   - session: The session to health check.
//
// Returns:
//   - nil if the worker is reachable AND the device is connected.
//   - error describing the failure (unreachable, device not connected, etc.).
func (m *DeviceSessionManager) healthCheckSession(session *DeviceSession) error {
	if session == nil {
		return fmt.Errorf("no session")
	}
	url := session.WorkerBaseURL + "/health"
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
		// Worker unreachable -- check backend for the real reason
		// (e.g. session was stopped from the browser).
		if reason := m.checkSessionStatusOnFailure(session); reason != "" {
			return fmt.Errorf("%s", reason)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("worker returned %d", resp.StatusCode)
	}

	// Parse the response body to check device_connected field.
	// The worker /health endpoint always returns 200, but reports
	// device_connected=false when the device reference is nil.
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		// Worker is reachable but we can't read the body — treat as healthy
		// to avoid false negatives on transient read errors.
		return nil
	}
	var health workerHealthResponse
	if jsonErr := json.Unmarshal(body, &health); jsonErr != nil {
		return nil
	}
	if !health.DeviceConnected {
		return fmt.Errorf("worker healthy but device not connected")
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

// WorkerHTTPError captures a non-success HTTP response returned by the worker.
// Callers can inspect StatusCode and Path with errors.As for compatibility
// fallback handling.
type WorkerHTTPError struct {
	StatusCode int
	Path       string
	Body       string
}

func (e *WorkerHTTPError) Error() string {
	if e == nil {
		return "worker request failed"
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("worker returned %d on %s", e.StatusCode, e.Path)
	}
	return fmt.Sprintf("worker returned %d on %s: %s", e.StatusCode, e.Path, body)
}

// isWorkerConnectivityError reports whether the direct worker request failed due to
// network reachability rather than application-level behavior.
func isWorkerConnectivityError(err error) bool {
	if err == nil {
		return false
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	lower := strings.ToLower(err.Error())
	for _, needle := range []string{
		"no such host",
		"i/o timeout",
		"connection refused",
		"network is unreachable",
		"no route to host",
		"temporary failure in name resolution",
		"proxyconnect",
		"tls handshake timeout",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// workerProxyActionFromPath converts worker path formats like "/tap" or
// "/resolve_target?x=1" into proxy action names (e.g. "tap", "resolve_target").
func workerProxyActionFromPath(path string) (string, error) {
	action := strings.TrimSpace(path)
	if action == "" {
		return "", fmt.Errorf("invalid worker path %q for proxy fallback", path)
	}
	if idx := strings.Index(action, "?"); idx >= 0 {
		action = action[:idx]
	}
	action = strings.Trim(action, "/")
	if action == "" || strings.Contains(action, "/") {
		return "", fmt.Errorf("invalid worker path %q for proxy fallback", path)
	}
	return action, nil
}

// proxyWorkerRequestForSession forwards a worker action through the backend
// device-proxy endpoint. This is a fallback path for sandbox environments
// where direct worker DNS resolution may fail.
func (m *DeviceSessionManager) proxyWorkerRequestForSession(
	ctx context.Context,
	session *DeviceSession,
	path string,
	body interface{},
) ([]byte, error) {
	if m.apiClient == nil {
		return nil, fmt.Errorf("backend proxy unavailable: no API client configured")
	}
	if session == nil || strings.TrimSpace(session.WorkflowRunID) == "" {
		return nil, fmt.Errorf("backend proxy unavailable: missing workflow run ID")
	}
	action, err := workerProxyActionFromPath(path)
	if err != nil {
		return nil, err
	}

	respBody, statusCode, err := m.apiClient.ProxyWorkerRequest(ctx, session.WorkflowRunID, action, body)
	if err != nil {
		return nil, err
	}
	if statusCode >= 400 {
		return nil, &WorkerHTTPError{
			StatusCode: statusCode,
			Path:       path,
			Body:       string(respBody),
		}
	}
	return respBody, nil
}

// WorkerRequest sends an HTTP request to the active session's worker.
// For backward compatibility, uses the active session. Use WorkerRequestForSession
// to target a specific session by index.
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
	session, err := m.ResolveSession(-1)
	if err != nil {
		return nil, err
	}
	return m.workerRequestForSession(ctx, session, method, path, body)
}

// WorkerRequestForSession sends an HTTP request to a specific session's worker.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: The session index to target.
//   - method: HTTP method (GET, POST, etc.).
//   - path: URL path on the worker (e.g. "/tap").
//   - body: Request body to send as JSON (nil for GET requests).
//
// Returns:
//   - []byte: Response body bytes.
//   - error: Any error during the request.
func (m *DeviceSessionManager) WorkerRequestForSession(ctx context.Context, index int, method, path string, body interface{}) ([]byte, error) {
	session, err := m.ResolveSession(index)
	if err != nil {
		return nil, err
	}
	return m.workerRequestForSession(ctx, session, method, path, body)
}

// workerRequestForSession is the internal implementation that sends an HTTP request
// to a given session's worker.
func (m *DeviceSessionManager) workerRequestForSession(ctx context.Context, session *DeviceSession, method, path string, body interface{}) ([]byte, error) {
	// Any direct device action should count as activity and extend the
	// session's idle timeout window.
	if session != nil {
		m.ResetIdleTimer(session.Index)
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
		if isWorkerConnectivityError(err) {
			proxiedResp, proxyErr := m.proxyWorkerRequestForSession(ctx, session, path, body)
			if proxyErr == nil {
				ui.PrintDebug("worker request proxied via backend (session=%d, path=%s)", session.Index, path)
				return proxiedResp, nil
			}
			var proxyWorkerErr *WorkerHTTPError
			if errors.As(proxyErr, &proxyWorkerErr) {
				return nil, proxyWorkerErr
			}
			ui.PrintDebug("worker direct request failed and proxy fallback failed: %v", proxyErr)
		}

		// Worker unreachable -- check backend for the real reason
		// (e.g. session was stopped from the browser).
		if reason := m.checkSessionStatusOnFailure(session); reason != "" {
			return nil, fmt.Errorf("%s. Start a new session with start_device_session()", reason)
		}

		// Provide actionable error messages for common network failures.
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) || strings.Contains(strings.ToLower(err.Error()), "no such host") {
			return nil, fmt.Errorf(
				"worker DNS lookup failed for %s: the device session has likely been terminated. "+
					"Call list_device_sessions() to check status or start_device_session() for a new session",
				session.WorkerBaseURL,
			)
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, fmt.Errorf(
				"worker request timed out for %s on %s. "+
					"Call device_doctor() to diagnose or stop_device_session(session_index=%d) to clean up",
				path, session.WorkerBaseURL, session.Index,
			)
		}

		return nil, fmt.Errorf("worker request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read worker response: %w", err)
	}

	if resp.StatusCode >= 500 {
		// Retry once for 503 (temporarily unavailable) -- the device may still
		// be finishing setup or recovering from a transient issue.
		if resp.StatusCode == 503 {
			resp.Body.Close()
			time.Sleep(2 * time.Second)

			// Rebuild the request (body may have been consumed).
			var retryBody io.Reader
			if body != nil {
				data, marshalErr := json.Marshal(body)
				if marshalErr != nil {
					return nil, fmt.Errorf("failed to marshal retry request body: %w", marshalErr)
				}
				retryBody = bytes.NewReader(data)
			}
			retryReq, retryReqErr := http.NewRequestWithContext(ctx, method, url, retryBody)
			if retryReqErr != nil {
				return nil, fmt.Errorf("failed to create retry request: %w", retryReqErr)
			}
			if body != nil {
				retryReq.Header.Set("Content-Type", "application/json")
			}

			retryResp, retryErr := client.Do(retryReq)
			if retryErr == nil {
				defer retryResp.Body.Close()
				retryRespBody, readErr := io.ReadAll(retryResp.Body)
				if readErr != nil {
					return nil, fmt.Errorf("failed to read worker retry response: %w", readErr)
				}
				if retryResp.StatusCode < 400 {
					return retryRespBody, nil
				}
				// Retry also failed -- fall through with retry response details.
				workerErr := &WorkerHTTPError{
					StatusCode: retryResp.StatusCode,
					Path:       path,
					Body:       string(retryRespBody),
				}
				return nil, fmt.Errorf(
					"%w. "+
						"The device may not be fully connected yet -- wait a few seconds and retry, or call device_doctor() to diagnose",
					workerErr,
				)
			}
			// Retry request itself failed -- fall through with original error.
		}

		workerErr := &WorkerHTTPError{
			StatusCode: resp.StatusCode,
			Path:       path,
			Body:       string(respBody),
		}
		return nil, fmt.Errorf(
			"%w. Call device_doctor() to check worker health",
			workerErr,
		)
	}
	if resp.StatusCode >= 400 {
		return nil, &WorkerHTTPError{
			StatusCode: resp.StatusCode,
			Path:       path,
			Body:       string(respBody),
		}
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

// ScreenshotForSession captures a specific session's device screen.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: The session index to screenshot.
//
// Returns:
//   - []byte: PNG image bytes.
//   - error: Any error during capture.
func (m *DeviceSessionManager) ScreenshotForSession(ctx context.Context, index int) ([]byte, error) {
	return m.WorkerRequestForSession(ctx, index, "GET", "/screenshot", nil)
}

// ---------------------------------------------------------------------------
// Grounding Client - resolves target descriptions to coordinates
// ---------------------------------------------------------------------------

// ResolvedTarget holds the result of resolving a target string to coordinates.
type ResolvedTarget struct {
	X int
	Y int
}

type workerResolveTargetRequest struct {
	Target       string `json:"target"`
	SessionID    string `json:"session_id,omitempty"`
	GrounderType string `json:"grounder_type,omitempty"`
}

type workerResolveTargetResponse struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Found bool   `json:"found"`
	Error string `json:"error,omitempty"`
}

// ResolveTarget takes a natural language target description, captures a screenshot,
// resolves it via the worker grounding endpoint, and returns device-space pixel
// coordinates. For older workers that do not implement /resolve_target, this
// method falls back to backend grounding.
//
// This is the core method used by all dual-param device tools when the agent
// provides a target string instead of x, y coordinates.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - target: Natural language element description (e.g. "Sign In button").
//
// Returns:
//   - *ResolvedTarget: The resolved coordinates.
//   - error: If grounding fails or element is not found.
func (m *DeviceSessionManager) ResolveTarget(ctx context.Context, target string) (*ResolvedTarget, error) {
	session, err := m.ResolveSession(-1)
	if err != nil {
		return nil, err
	}
	return m.resolveTargetForSession(ctx, session, target)
}

// ResolveTargetForSession resolves a target description to coordinates using
// a specific session's device screen and platform.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: The session index to use for grounding.
//   - target: Natural language element description (e.g. "Sign In button").
//
// Returns:
//   - *ResolvedTarget: The resolved coordinates.
//   - error: If grounding fails or element is not found.
func (m *DeviceSessionManager) ResolveTargetForSession(ctx context.Context, index int, target string) (*ResolvedTarget, error) {
	session, err := m.ResolveSession(index)
	if err != nil {
		return nil, err
	}
	return m.resolveTargetForSession(ctx, session, target)
}

// resolveTargetForSession is the internal implementation of target resolution.
func (m *DeviceSessionManager) resolveTargetForSession(ctx context.Context, session *DeviceSession, target string) (*ResolvedTarget, error) {
	// Prefer worker-native grounding (single hop + device-space coordinates).
	resolved, workerErr := m.resolveTargetViaWorkerForSession(ctx, session, target)
	if workerErr == nil {
		return resolved, nil
	}

	if !shouldFallbackToBackendGrounding(workerErr) {
		return nil, workerErr
	}

	ui.PrintDebug("worker resolve_target unavailable, falling back to backend grounding: %v", workerErr)
	return m.resolveTargetViaBackendForSession(ctx, session, target)
}

// shouldFallbackToBackendGrounding reports whether worker grounding errors
// should trigger backend fallback.
func shouldFallbackToBackendGrounding(err error) bool {
	if err == nil {
		return false
	}

	var workerErr *WorkerHTTPError
	if errors.As(err, &workerErr) {
		if workerErr.StatusCode >= 500 {
			return true
		}
		switch workerErr.StatusCode {
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
			return true
		default:
			return false
		}
	}

	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "worker resolve_target returned invalid json")
}

// resolveTargetViaWorkerForSession resolves coordinates through the worker's
// /resolve_target endpoint, which returns device-space coordinates.
func (m *DeviceSessionManager) resolveTargetViaWorkerForSession(ctx context.Context, session *DeviceSession, target string) (*ResolvedTarget, error) {
	body := workerResolveTargetRequest{
		Target:    target,
		SessionID: session.SessionID,
	}
	respBody, err := m.workerRequestForSession(ctx, session, http.MethodPost, "/resolve_target", body)
	if err != nil {
		return nil, err
	}

	var resolvedResp workerResolveTargetResponse
	if err := json.Unmarshal(respBody, &resolvedResp); err != nil {
		return nil, fmt.Errorf("worker resolve_target returned invalid JSON: %w", err)
	}

	if !resolvedResp.Found {
		errMsg := fmt.Sprintf("could not locate '%s' in the screenshot", target)
		if resolvedResp.Error != "" {
			errMsg = resolvedResp.Error
		}
		return nil, fmt.Errorf("%s. Try screenshot() to see the current screen and adjust the target description", errMsg)
	}

	return &ResolvedTarget{
		X: resolvedResp.X,
		Y: resolvedResp.Y,
	}, nil
}

// resolveTargetViaBackendForSession resolves coordinates through the backend
// grounding proxy, used as compatibility fallback for older workers.
func (m *DeviceSessionManager) resolveTargetViaBackendForSession(ctx context.Context, session *DeviceSession, target string) (*ResolvedTarget, error) {
	// Step 1: Capture screenshot from worker
	screenshotBytes, err := m.workerRequestForSession(ctx, session, "GET", "/screenshot", nil)
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

	// Step 4: Call backend grounding endpoint (routes through Hatchet)
	sessionID := session.SessionID
	platform := session.Platform

	groundResp, err := m.apiClient.GroundElement(ctx, &api.GroundElementRequest{
		Target:      target,
		ImageBase64: imageBase64,
		Width:       width,
		Height:      height,
		Platform:    platform,
		SessionID:   sessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("grounding request failed: %w", err)
	}

	if !groundResp.Found {
		errMsg := fmt.Sprintf("could not locate '%s' in the screenshot", target)
		if groundResp.Error != "" {
			errMsg = groundResp.Error
		}
		return nil, fmt.Errorf("%s. Try screenshot() to see the current screen and adjust the target description", errMsg)
	}

	return &ResolvedTarget{
		X: groundResp.X,
		Y: groundResp.Y,
	}, nil
}

// SyncSessions synchronizes local session state with the backend.
// Queries the backend for all active sessions belonging to the authenticated user,
// resolves worker URLs for newly discovered sessions, and prunes sessions that
// no longer exist on the backend.
//
// Parameters:
//   - ctx: Context for cancellation.
//
// Returns:
//   - error: Any error during synchronization. Non-fatal errors (e.g. backend
//     unreachable) are logged but may still return error to the caller.
func (m *DeviceSessionManager) SyncSessions(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Load local cache first for orgID/userEmail if not yet populated
	if len(m.sessions) == 0 {
		m.loadLocalCache()
	}

	// Step 1: Resolve orgID if not cached
	if err := m.ensureOrgInfoLocked(ctx); err != nil {
		return err
	}

	// Step 2: Fetch active sessions from backend
	activeResp, err := m.apiClient.GetActiveDeviceSessions(ctx, m.orgID)
	if err != nil {
		return fmt.Errorf("failed to fetch active sessions: %w", err)
	}

	// Step 3: Filter by user email (only your sessions)
	backendSessions := make([]api.ActiveDeviceSessionItem, 0)
	for _, s := range activeResp.Sessions {
		if m.userEmail != "" && s.UserEmail != nil && *s.UserEmail != m.userEmail {
			continue
		}
		backendSessions = append(backendSessions, s)
	}

	// Sort by created_at ASC for deterministic index assignment
	sort.Slice(backendSessions, func(i, j int) bool {
		ci, cj := "", ""
		if backendSessions[i].CreatedAt != nil {
			ci = *backendSessions[i].CreatedAt
		}
		if backendSessions[j].CreatedAt != nil {
			cj = *backendSessions[j].CreatedAt
		}
		return ci < cj
	})

	// Step 4: Build backend lookup maps for pruning/reconciliation.
	backendIDs := make(map[string]bool)
	backendIDsByWorkflow := make(map[string]string)
	for _, bs := range backendSessions {
		backendIDs[bs.Id] = true
		if bs.WorkflowRunId != nil && *bs.WorkflowRunId != "" {
			backendIDsByWorkflow[*bs.WorkflowRunId] = bs.Id
		}
	}

	// Step 5: Reconcile then prune local sessions not in backend.
	// Reconcile by workflow run ID to avoid churn when SessionID was seeded
	// with workflowRunID during StartSession before backend session ID was known.
	reconcileSessionIDsByWorkflow(m.sessions, backendIDsByWorkflow)
	for idx, ls := range m.sessions {
		if backendIDs[ls.SessionID] {
			continue
		}
		// Session no longer exists on backend; clean up locally.
		if timer, ok := m.idleTimers[idx]; ok {
			timer.Stop()
			delete(m.idleTimers, idx)
		}
		delete(m.sessions, idx)
	}

	// Step 6: Add backend sessions not in local map
	// Build reverse lookup: sessionID -> local index
	localByID := make(map[string]int)
	for idx, ls := range m.sessions {
		if ls.SessionID != "" {
			localByID[ls.SessionID] = idx
		}
	}

	for _, bs := range backendSessions {
		if _, exists := localByID[bs.Id]; exists {
			continue // already known locally
		}

		// Need to resolve worker URL
		workerBaseURL := ""
		if bs.WorkflowRunId != nil && *bs.WorkflowRunId != "" {
			wsResp, wsErr := m.apiClient.GetWorkerWSURL(ctx, *bs.WorkflowRunId)
			if wsErr == nil && wsResp.WorkerWsUrl != nil && *wsResp.WorkerWsUrl != "" {
				workerBaseURL = wsURLToHTTP(*wsResp.WorkerWsUrl)
			}
		}

		if workerBaseURL == "" {
			// Can't resolve worker URL; skip this session
			continue
		}

		// Validate worker is actually reachable before adding.
		// DNS entries are cleaned up before backend DB status is updated,
		// so a non-empty URL doesn't guarantee the worker is alive.
		tmpSession := &DeviceSession{WorkerBaseURL: workerBaseURL}
		if hErr := m.healthCheckSession(tmpSession); hErr != nil {
			ui.PrintDebug("skipping session %s: worker unreachable (%v)", shortPrefix(bs.Id, 8), hErr)
			continue
		}

		// Build viewer URL
		baseURL := "https://app.revyl.ai"
		if os.Getenv("LOCAL") == "true" || os.Getenv("LOCAL") == "True" {
			baseURL = "http://localhost:3000"
		}
		workflowRunID := ""
		if bs.WorkflowRunId != nil {
			workflowRunID = *bs.WorkflowRunId
		}
		viewerURL := fmt.Sprintf("%s/tests/execute?workflowRunId=%s&platform=%s", baseURL, workflowRunID, bs.Platform)

		startedAt := time.Now()
		if bs.StartedAt != nil {
			if t, parseErr := time.Parse(time.RFC3339, *bs.StartedAt); parseErr == nil {
				startedAt = t
			}
		}

		idx := m.nextIndex
		m.nextIndex++

		session := &DeviceSession{
			Index:         idx,
			SessionID:     bs.Id,
			WorkflowRunID: workflowRunID,
			WorkerBaseURL: workerBaseURL,
			ViewerURL:     viewerURL,
			Platform:      bs.Platform,
			StartedAt:     startedAt,
			LastActivity:  time.Now(),
			IdleTimeout:   5 * time.Minute,
		}

		m.sessions[idx] = session
		m.resetIdleTimerForSessionLocked(idx, context.Background())
	}

	// Step 7: Fix active index if needed
	if m.activeIndex >= 0 {
		if _, ok := m.sessions[m.activeIndex]; !ok {
			m.activeIndex = -1
		}
	}
	if m.activeIndex < 0 && len(m.sessions) > 0 {
		// Auto-select lowest index
		lowest := -1
		for idx := range m.sessions {
			if lowest < 0 || idx < lowest {
				lowest = idx
			}
		}
		m.activeIndex = lowest
	}

	// Step 8: Recompact indices so they are 0-based contiguous.
	m.recompactIndicesLocked()

	// Step 9: Persist
	m.persistSessions()
	return nil
}

// WorkDir returns the working directory used for session persistence.
func (m *DeviceSessionManager) WorkDir() string {
	return m.workDir
}

// APIClient returns the underlying API client for direct backend queries.
// May be nil if the manager was constructed without one.
func (m *DeviceSessionManager) APIClient() *api.Client {
	return m.apiClient
}

// SetOrgInfo caches the org ID and user email to avoid re-fetching
// on subsequent SyncSessions calls.
//
// Parameters:
//   - orgID: The organization ID.
//   - userEmail: The user's email address.
func (m *DeviceSessionManager) SetOrgInfo(orgID, userEmail string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgID = orgID
	m.userEmail = userEmail
}
