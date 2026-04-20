// Package mcp provides the device session management for MCP server.
//
// DeviceSessionManager handles the lifecycle of cloud-hosted device sessions,
// including provisioning, idle timeout, worker HTTP proxying, and grounding
// model integration.
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	startdevice "github.com/revyl/cli/internal/device"
	"github.com/revyl/cli/internal/ui"

	openapi_types "github.com/oapi-codegen/runtime/types"
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

	// WhepURL is the raw WebRTC playback URL for the active device stream.
	WhepURL *string `json:"whep_url,omitempty"`

	// Platform is "ios" or "android".
	Platform string `json:"platform"`

	// ScreenWidth is the device screen width in pixels (0 when unknown).
	ScreenWidth int `json:"screen_width,omitempty"`

	// ScreenHeight is the device screen height in pixels (0 when unknown).
	ScreenHeight int `json:"screen_height,omitempty"`

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

	// devMode mirrors the --dev CLI flag for URL construction.
	devMode bool
}

// SetDevMode configures whether the manager generates localhost URLs (true)
// or production URLs (false) for viewer and report links.
func (m *DeviceSessionManager) SetDevMode(devMode bool) {
	m.devMode = devMode
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

// ResolveBuildVersionURL resolves a build version ID to its download URL and
// package name via the backend API.
//
// Parameters:
//   - ctx: Context for the API call.
//   - buildVersionID: The build version UUID to resolve.
//
// Returns:
//   - appURL: The resolved download URL.
//   - packageName: The detected package/bundle ID (may be empty).
//   - error: If the build version cannot be resolved.
func (m *DeviceSessionManager) ResolveBuildVersionURL(ctx context.Context, buildVersionID string) (appURL string, packageName string, err error) {
	detail, err := m.apiClient.GetBuildVersionDownloadURL(ctx, buildVersionID)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve build version %s: %w", buildVersionID, err)
	}
	if detail == nil || strings.TrimSpace(detail.DownloadURL) == "" {
		return "", "", fmt.Errorf("build version %s has no download URL", buildVersionID)
	}
	return strings.TrimSpace(detail.DownloadURL), strings.TrimSpace(detail.PackageName), nil
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

	// DeviceModel overrides the target device model (e.g. "iPhone 16").
	DeviceModel string
	// OsVersion overrides the target OS runtime (e.g. "iOS 18.5").
	OsVersion string
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

	resolvedArtifact, err := startdevice.ResolveStartArtifact(ctx, m.apiClient, startdevice.StartArtifactOptions{
		AppID:          opts.AppID,
		BuildVersionID: opts.BuildVersionID,
		AppURL:         opts.AppURL,
		AppPackage:     opts.AppPackage,
	})
	if err != nil {
		return -1, nil, err
	}

	// Build the start device request
	req := &api.StartDeviceRequest{
		Platform:     platform,
		IsSimulation: strings.TrimSpace(opts.TestID) == "",
	}
	if idleTimeout > 0 {
		req.IdleTimeoutSeconds = int(idleTimeout.Seconds())
	}
	if strings.TrimSpace(opts.TestID) != "" {
		req.TestID = strings.TrimSpace(opts.TestID)
	}
	if resolvedArtifact.AppURL != "" {
		req.AppURL = resolvedArtifact.AppURL
	}
	if strings.TrimSpace(opts.AppLink) != "" {
		req.AppLink = strings.TrimSpace(opts.AppLink)
	}
	if resolvedArtifact.AppPackage != "" {
		req.AppPackage = resolvedArtifact.AppPackage
	}
	_ = opts.SandboxID // Reserved for backend support.
	if opts.DeviceModel != "" {
		req.DeviceModel = opts.DeviceModel
	}
	if opts.OsVersion != "" {
		req.OsVersion = opts.OsVersion
	}

	// Start the device via backend API
	resp, err := m.apiClient.StartDevice(ctx, req)
	if err != nil {
		return -1, nil, fmt.Errorf("failed to start device: %w", err)
	}

	if resp.WorkflowRunId == nil || *resp.WorkflowRunId == (openapi_types.UUID{}) {
		errMsg := "no workflow run ID returned"
		if resp.Error != nil {
			errMsg = *resp.Error
		}
		return -1, nil, fmt.Errorf("failed to start device: %s", errMsg)
	}

	workflowRunID := resp.WorkflowRunId.String()

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
	tmpSession := &DeviceSession{WorkerBaseURL: workerBaseURL, WorkflowRunID: workflowRunID}
	deviceReady := false
	var lastHealth workerHealthResponse
	for i := 0; i < 15; i++ { // 15 * 2s = 30s max
		if health, err := m.healthCheckSession(tmpSession); err == nil {
			lastHealth = health
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

	sessionID := m.backendSessionIDByWorkflowRunLocked(ctx, workflowRunID)
	appURL := config.GetAppURL(m.devMode)
	viewerURL := ""
	if sessionID != "" {
		viewerURL = fmt.Sprintf("%s/sessions/%s", appURL, url.PathEscape(sessionID))
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

	if deviceReady {
		session.ScreenWidth = lastHealth.ScreenWidth
		session.ScreenHeight = lastHealth.ScreenHeight
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
	m.nextIndex = 0
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

// StopIdleTimer cancels the idle timer for a session without stopping the
// session itself. Used by `revyl dev` to delegate idle timeout enforcement
// to the worker process, which has accurate cross-source activity tracking.
//
// Parameters:
//   - index: The session index whose timer should be cancelled.
func (m *DeviceSessionManager) StopIdleTimer(index int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if timer, ok := m.idleTimers[index]; ok {
		timer.Stop()
		delete(m.idleTimers, index)
	}
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
	if err := os.WriteFile(tmpPath, imageBytes, 0o600); err != nil {
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

	_ = os.WriteFile(filepath.Join(dir, "device-sessions.json"), data, 0o600)
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

// CheckSessionAlive queries the backend to determine whether a device session
// is still running. Used by the dev loop poll to detect worker-side idle
// timeout, cancellation, or failure without maintaining a local timer.
//
// Parameters:
//   - ctx: Context for the backend request (should have a short timeout).
//   - session: The session to check.
//
// Returns:
//   - alive: true if the session is still running or status is indeterminate.
//   - reason: Human-readable explanation when alive is false.
func (m *DeviceSessionManager) CheckSessionAlive(ctx context.Context, session *DeviceSession) (alive bool, reason string) {
	if session == nil || m.apiClient == nil {
		return true, ""
	}
	resp, err := m.apiClient.GetWorkerWSURL(ctx, session.WorkflowRunID)
	if err != nil {
		return true, "" // network error -- assume alive, don't kill
	}
	switch resp.Status {
	case api.WorkerConnectionResponseStatusStopped:
		return false, "session idle timeout"
	case api.WorkerConnectionResponseStatusCancelled:
		return false, "session was cancelled"
	case api.WorkerConnectionResponseStatusFailed:
		return false, "session failed on the worker"
	default:
		return true, ""
	}
}

// workerHealthResponse represents the JSON body returned by the worker /health endpoint.
type workerHealthResponse struct {
	Status          string `json:"status"`
	DeviceConnected bool   `json:"device_connected"`
	ScreenWidth     int    `json:"screen_width"`
	ScreenHeight    int    `json:"screen_height"`
}

// healthCheckSession pings the worker /health endpoint to verify the session is live
// and the device is connected.
//
// Parameters:
//   - session: The session to health check.
//
// Returns:
//   - workerHealthResponse with parsed fields (dimensions, status) on success.
//   - error describing the failure (unreachable, device not connected, etc.).
func (m *DeviceSessionManager) healthCheckSession(session *DeviceSession) (workerHealthResponse, error) {
	if session == nil {
		return workerHealthResponse{}, fmt.Errorf("no session")
	}
	if m.apiClient != nil && strings.TrimSpace(session.WorkflowRunID) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		body, err := m.proxyWorkerRequestForSession(ctx, session, "/health", nil)
		if err != nil {
			var workerErr *WorkerHTTPError
			if errors.As(err, &workerErr) {
				return workerHealthResponse{}, workerErr
			}
			if reason := m.checkSessionStatusOnFailure(session); reason != "" {
				return workerHealthResponse{}, fmt.Errorf("%s", reason)
			}
			return workerHealthResponse{}, err
		}
		return parseWorkerHealth(body)
	}

	url := session.WorkerBaseURL + "/health"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return workerHealthResponse{}, err
	}
	client := m.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if reason := m.checkSessionStatusOnFailure(session); reason != "" {
			return workerHealthResponse{}, fmt.Errorf("%s", reason)
		}
		return workerHealthResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return workerHealthResponse{}, fmt.Errorf("worker returned %d", resp.StatusCode)
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return workerHealthResponse{}, nil
	}
	return parseWorkerHealth(body)
}

// parseWorkerHealth unmarshals the /health JSON body and validates device connectivity.
//
// Returns:
//   - workerHealthResponse with all parsed fields (including ScreenWidth/ScreenHeight).
//   - error if the device is not connected; nil otherwise.
func parseWorkerHealth(body []byte) (workerHealthResponse, error) {
	var health workerHealthResponse
	if jsonErr := json.Unmarshal(body, &health); jsonErr != nil {
		return workerHealthResponse{}, nil
	}
	if !health.DeviceConnected {
		return health, fmt.Errorf("worker healthy but device not connected")
	}
	return health, nil
}

// applyBackendScreenDimensions copies screen dimensions from an
// ActiveDeviceSessionItem into a local DeviceSession when they are available.
func applyBackendScreenDimensions(session *DeviceSession, bs api.ActiveDeviceSessionItem) {
	if bs.ScreenWidth != nil && *bs.ScreenWidth > 0 {
		session.ScreenWidth = *bs.ScreenWidth
	}
	if bs.ScreenHeight != nil && *bs.ScreenHeight > 0 {
		session.ScreenHeight = *bs.ScreenHeight
	}
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

// WorkerActionResponse captures the standard worker action envelope used by
// device actions such as download_file.
type WorkerActionResponse struct {
	Success    bool    `json:"success"`
	Action     string  `json:"action"`
	LatencyMs  float64 `json:"latency_ms"`
	Error      string  `json:"error,omitempty"`
	BundleID   string  `json:"bundle_id,omitempty"`
	DevicePath string  `json:"device_path,omitempty"`
}

// DeviceDownloadFileRequest is the canonical request body for download_file.
type DeviceDownloadFileRequest struct {
	URL      string `json:"url"`
	Filename string `json:"filename,omitempty"`
}

// LiveStepRequest is the canonical worker request body for execute_step.
type LiveStepRequest struct {
	StepType        string         `json:"step_type"`
	StepDescription string         `json:"step_description"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	StepID          string         `json:"step_id,omitempty"`
	NodeID          string         `json:"node_id,omitempty"`
	TestID          string         `json:"test_id,omitempty"`
	RunID           string         `json:"run_id,omitempty"`
	SessionID       string         `json:"session_id,omitempty"`
	OrgID           string         `json:"org_id,omitempty"`
	AccessToken     string         `json:"access_token,omitempty"`
	WorkflowRunID   string         `json:"workflow_run_id,omitempty"`
	ExecutionID     string         `json:"execution_id,omitempty"`
	ReportID        string         `json:"report_id,omitempty"`
	StepDBID        string         `json:"step_db_id,omitempty"`
	ParentStepDBID  string         `json:"parent_step_db_id,omitempty"`
	SourceModuleID  string         `json:"source_module_id,omitempty"`
}

// LiveStepResponse is the canonical worker response body for execute_step.
type LiveStepResponse struct {
	Success       bool            `json:"success"`
	StepType      string          `json:"step_type"`
	StepID        string          `json:"step_id"`
	WorkflowRunID string          `json:"workflow_run_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	ExecutionID   string          `json:"execution_id,omitempty"`
	ReportID      string          `json:"report_id,omitempty"`
	StepOutput    json.RawMessage `json:"step_output"`
}

// maxErrorBodyLen caps the response body surfaced in error messages to avoid
// leaking internal stack traces, tokens, or verbose HTML error pages.
const maxErrorBodyLen = 512

func (e *WorkerHTTPError) Error() string {
	if e == nil {
		return "worker request failed"
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("worker returned %d on %s", e.StatusCode, e.Path)
	}
	if len(body) > maxErrorBodyLen {
		body = body[:maxErrorBodyLen] + "... (truncated)"
	}
	return fmt.Sprintf("worker returned %d on %s: %s", e.StatusCode, e.Path, body)
}

// DownloadFileForSession executes the worker download_file action and returns
// the structured worker response, including the resolved on-device path.
func (m *DeviceSessionManager) DownloadFileForSession(
	ctx context.Context,
	index int,
	req DeviceDownloadFileRequest,
) (*WorkerActionResponse, error) {
	respBody, err := m.WorkerRequestForSession(ctx, index, "/download_file", req)
	if err != nil {
		return nil, err
	}

	var result WorkerActionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse download_file response: %w", err)
	}
	return &result, nil
}

// stepAcceptedResponse is the 202 body returned when the worker accepts a
// step for async execution. The caller should poll step_status/{step_id}.
type stepAcceptedResponse struct {
	StepID string `json:"step_id"`
	Status string `json:"status"` // "accepted"
}

// stepStatusResponse is the body returned by GET /step_status/{step_id}.
type stepStatusResponse struct {
	StepID string          `json:"step_id"`
	Status string          `json:"status"` // "running", "completed", "failed"
	Result json.RawMessage `json:"result"` // LiveStepResponse JSON when terminal
}

const (
	stepPollBaseDelay = 500 * time.Millisecond
	stepPollMaxDelay  = 5 * time.Second
	stepPollTimeout   = 10 * time.Minute
)

// ExecuteLiveStepForSession executes one high-level step through the worker's
// canonical execute_step route and returns the shared JSON contract used by
// CLI, MCP, and SDK consumers.
//
// The worker returns HTTP 202 and runs the step asynchronously. This method
// polls GET /step_status/{step_id} until a terminal status is reached, then
// returns the final LiveStepResponse. For backward compatibility with older
// workers that return 200 synchronously, the response body is inspected.
func (m *DeviceSessionManager) ExecuteLiveStepForSession(
	ctx context.Context,
	index int,
	req LiveStepRequest,
) (*LiveStepResponse, error) {
	session, err := m.ResolveSession(index)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = session.SessionID
	}
	if strings.TrimSpace(req.WorkflowRunID) == "" {
		req.WorkflowRunID = session.WorkflowRunID
	}

	respBody, err := m.workerRequestForSession(ctx, session, "/execute_step", req)
	if err != nil {
		return nil, err
	}

	// Detect async (202) vs sync (200) response by checking for the
	// "accepted" status field that only stepAcceptedResponse carries.
	var accepted stepAcceptedResponse
	if err := json.Unmarshal(respBody, &accepted); err == nil && accepted.Status == "accepted" && accepted.StepID != "" {
		return m.pollStepUntilDone(ctx, session, accepted.StepID)
	}

	// Sync fallback for older workers returning 200 with full result.
	var result LiveStepResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse execute_step response: %w", err)
	}
	return &result, nil
}

// pollStepUntilDone polls GET /step_status/{step_id} with exponential backoff
// until the step reaches a terminal status or the timeout is exceeded.
func (m *DeviceSessionManager) pollStepUntilDone(
	ctx context.Context,
	session *DeviceSession,
	stepID string,
) (*LiveStepResponse, error) {
	deadline := time.Now().Add(stepPollTimeout)
	delay := stepPollBaseDelay
	path := "/step_status/" + stepID

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("step %s did not complete within %v", stepID, stepPollTimeout)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		respBody, err := m.workerRequestForSession(ctx, session, path, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to poll step status for %s: %w", stepID, err)
		}

		var status stepStatusResponse
		if err := json.Unmarshal(respBody, &status); err != nil {
			return nil, fmt.Errorf("failed to parse step_status response: %w", err)
		}

		if status.Status == "running" {
			// Exponential backoff capped at stepPollMaxDelay.
			delay = delay * 2
			if delay > stepPollMaxDelay {
				delay = stepPollMaxDelay
			}
			continue
		}

		// Terminal status -- parse the embedded result.
		if status.Result == nil {
			return nil, fmt.Errorf("step %s finished with status %q but no result", stepID, status.Status)
		}
		var result LiveStepResponse
		if err := json.Unmarshal(status.Result, &result); err != nil {
			return nil, fmt.Errorf("failed to parse step result for %s: %w", stepID, err)
		}
		return &result, nil
	}
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

// workerProxyActionFromPath converts worker path formats like "/tap",
// "/resolve_target?x=1", or "/step_status/{id}" into proxy action strings.
//
// Single-segment paths return just the action (e.g. "tap").
// Multi-segment paths like "/step_status/{uuid}" are allowed when the base
// segment is in compoundPathActions; the full trimmed path is returned.
func workerProxyActionFromPath(path string) (string, error) {
	action := strings.TrimSpace(path)
	if action == "" {
		return "", fmt.Errorf("invalid worker path %q for proxy fallback", path)
	}
	if idx := strings.Index(action, "?"); idx >= 0 {
		action = action[:idx]
	}
	action = strings.Trim(action, "/")
	if action == "" {
		return "", fmt.Errorf("invalid worker path %q for proxy fallback", path)
	}
	// Allow compound paths (e.g. "step_status/{uuid}") for actions that
	// carry a sub-resource identifier.
	if strings.Contains(action, "/") {
		base := action[:strings.Index(action, "/")]
		if compoundPathActions[base] {
			return action, nil
		}
		return "", fmt.Errorf("invalid worker path %q for proxy fallback", path)
	}
	return action, nil
}

// compoundPathActions lists base action names whose proxy paths include
// a sub-resource segment (e.g. step_status/{step_id}).
var compoundPathActions = map[string]bool{
	"step_status": true,
}

// proxyWorkerRequestForSession forwards a worker action through the backend
// device-proxy endpoint. CLI/MCP device control uses this as the canonical
// transport so sandboxed environments never depend on direct worker DNS.
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

// WorkerRequest sends a worker action request to the active session.
// For backward compatibility, uses the active session. Use
// WorkerRequestForSession to target a specific session by index.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - path: Worker action path (e.g. "/tap", "/health").
//   - body: Request body to send as JSON (nil for read-only actions).
//
// Returns:
//   - []byte: Response body bytes.
//   - error: Any error during the request.
func (m *DeviceSessionManager) WorkerRequest(ctx context.Context, path string, body interface{}) ([]byte, error) {
	session, err := m.ResolveSession(-1)
	if err != nil {
		return nil, err
	}
	return m.workerRequestForSession(ctx, session, path, body)
}

// WorkerRequestForSession sends a worker action request to a specific session.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: The session index to target.
//   - path: Worker action path (e.g. "/tap", "/health").
//   - body: Request body to send as JSON (nil for read-only actions).
//
// Returns:
//   - []byte: Response body bytes.
//   - error: Any error during the request.
func (m *DeviceSessionManager) WorkerRequestForSession(ctx context.Context, index int, path string, body interface{}) ([]byte, error) {
	session, err := m.ResolveSession(index)
	if err != nil {
		return nil, err
	}
	return m.workerRequestForSession(ctx, session, path, body)
}

// nonIdempotentPaths lists worker paths whose side-effects make retry unsafe.
// Retrying these creates duplicate work (e.g. duplicate agent steps).
var nonIdempotentPaths = map[string]bool{
	"/execute_step": true,
}

// workerRequestForSession is the internal implementation that sends a worker
// action request to a given session using the backend relay.
func (m *DeviceSessionManager) workerRequestForSession(ctx context.Context, session *DeviceSession, path string, body interface{}) ([]byte, error) {
	if session != nil {
		m.ResetIdleTimer(session.Index)
	}

	respBody, err := m.proxyWorkerRequestForSession(ctx, session, path, body)
	if err == nil {
		return respBody, nil
	}

	// Non-idempotent actions must not be retried -- return errors directly.
	if nonIdempotentPaths[path] {
		var workerErr *WorkerHTTPError
		if errors.As(err, &workerErr) {
			return nil, workerErr
		}
		return nil, fmt.Errorf("backend device control request failed: %w", err)
	}

	var workerErr *WorkerHTTPError
	if errors.As(err, &workerErr) {
		if workerErr.StatusCode == http.StatusServiceUnavailable {
			time.Sleep(2 * time.Second)

			retryRespBody, retryErr := m.proxyWorkerRequestForSession(ctx, session, path, body)
			if retryErr == nil {
				return retryRespBody, nil
			}

			var retryWorkerErr *WorkerHTTPError
			if errors.As(retryErr, &retryWorkerErr) {
				return nil, fmt.Errorf(
					"%w. "+
						"The device may not be fully connected yet -- wait a few seconds and retry, or call device_doctor() to diagnose",
					retryWorkerErr,
				)
			}
			return nil, retryErr
		}
		if workerErr.StatusCode >= 500 {
			return nil, fmt.Errorf("%w. Call device_doctor() to check worker health", workerErr)
		}
		return nil, workerErr
	}

	if reason := m.checkSessionStatusOnFailure(session); reason != "" {
		return nil, fmt.Errorf("%s. Start a new session with start_device_session()", reason)
	}

	return nil, fmt.Errorf("backend device control request failed: %w", err)
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
	return m.WorkerRequest(ctx, "/screenshot", nil)
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
	return m.WorkerRequestForSession(ctx, index, "/screenshot", nil)
}

// ---------------------------------------------------------------------------
// Device Logs and Performance Metrics Polling
// ---------------------------------------------------------------------------

// DeviceLogsPollResponse is the JSON contract returned by the worker
// /device_logs endpoint.
type DeviceLogsPollResponse struct {
	Success        bool     `json:"success"`
	Platform       string   `json:"platform"`
	SessionID      string   `json:"session_id,omitempty"`
	NextCursor     string   `json:"next_cursor"`
	CaptureRunning bool     `json:"capture_running"`
	Items          []string `json:"items"`
}

// PerfSampleItem is a single performance sample from the worker.
type PerfSampleItem struct {
	Timestamp    float64                `json:"timestamp"`
	CPU          map[string]interface{} `json:"cpu,omitempty"`
	MemoryApp    map[string]interface{} `json:"memory_app,omitempty"`
	MemorySystem map[string]interface{} `json:"memory_system,omitempty"`
	FPS          *float64               `json:"fps,omitempty"`
}

// PerfPollSummary is the aggregate summary for a perf window.
type PerfPollSummary struct {
	SampleCount   int      `json:"sample_count"`
	WindowStart   *float64 `json:"window_start,omitempty"`
	WindowEnd     *float64 `json:"window_end,omitempty"`
	AvgCPUPercent *float64 `json:"avg_cpu_percent,omitempty"`
	PeakRSSMB     *float64 `json:"peak_rss_mb,omitempty"`
	AvgRSSMB      *float64 `json:"avg_rss_mb,omitempty"`
	AvgFPS        *float64 `json:"avg_fps,omitempty"`
}

// PerfPollResponse is the JSON contract returned by the worker
// /performance_metrics endpoint.
type PerfPollResponse struct {
	Success        bool             `json:"success"`
	Platform       string           `json:"platform"`
	SessionID      string           `json:"session_id,omitempty"`
	NextCursor     string           `json:"next_cursor"`
	CaptureRunning bool             `json:"capture_running"`
	Items          []PerfSampleItem `json:"items"`
	Summary        *PerfPollSummary `json:"summary,omitempty"`
}

// PollDeviceLogsForSession fetches incremental device logs from a session.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: Session index to target.
//   - cursor: Opaque cursor from a previous response ("0" for start).
//   - limit: Maximum number of log lines to return.
//
// Returns:
//   - *DeviceLogsPollResponse: Parsed response with new lines and next_cursor.
//   - error: Any transport or parse error.
func (m *DeviceSessionManager) PollDeviceLogsForSession(ctx context.Context, index int, cursor string, limit int) (*DeviceLogsPollResponse, error) {
	path := fmt.Sprintf("/device_logs?cursor=%s&limit=%d", url.QueryEscape(cursor), limit)
	respBody, err := m.WorkerRequestForSession(ctx, index, path, nil)
	if err != nil {
		return nil, err
	}
	var result DeviceLogsPollResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse device_logs response: %w", err)
	}
	return &result, nil
}

// PollPerformanceMetricsForSession fetches incremental perf samples from a session.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - index: Session index to target.
//   - cursor: Opaque cursor from a previous response ("0" for start).
//   - limit: Maximum number of samples to return.
//
// Returns:
//   - *PerfPollResponse: Parsed response with new samples, summary, and next_cursor.
//   - error: Any transport or parse error.
func (m *DeviceSessionManager) PollPerformanceMetricsForSession(ctx context.Context, index int, cursor string, limit int) (*PerfPollResponse, error) {
	path := fmt.Sprintf("/performance_metrics?cursor=%s&limit=%d", url.QueryEscape(cursor), limit)
	respBody, err := m.WorkerRequestForSession(ctx, index, path, nil)
	if err != nil {
		return nil, err
	}
	var result PerfPollResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse performance_metrics response: %w", err)
	}
	return &result, nil
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
	respBody, err := m.workerRequestForSession(ctx, session, "/resolve_target", body)
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
	screenshotBytes, err := m.workerRequestForSession(ctx, session, "/screenshot", nil)
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
		ci := time.Time{}
		cj := time.Time{}
		if backendSessions[i].CreatedAt != nil {
			ci = *backendSessions[i].CreatedAt
		}
		if backendSessions[j].CreatedAt != nil {
			cj = *backendSessions[j].CreatedAt
		}
		return ci.Before(cj)
	})

	// Step 4: Build backend lookup maps for pruning/reconciliation.
	// Use ALL backend sessions (not just email-filtered) for pruning, so
	// explicitly attached sessions from other users don't get removed.
	allBackendIDs := make(map[string]bool)
	for _, s := range activeResp.Sessions {
		allBackendIDs[s.Id] = true
	}

	backendIDsByWorkflow := make(map[string]string)
	backendSessionByID := make(map[string]api.ActiveDeviceSessionItem)
	backendSessionByWorkflow := make(map[string]api.ActiveDeviceSessionItem)
	for _, bs := range backendSessions {
		backendSessionByID[bs.Id] = bs
		if bs.WorkflowRunId != nil && *bs.WorkflowRunId != "" {
			backendIDsByWorkflow[*bs.WorkflowRunId] = bs.Id
			backendSessionByWorkflow[*bs.WorkflowRunId] = bs
		}
	}

	// Step 5: Reconcile then prune local sessions not in backend.
	// Reconcile by workflow run ID to avoid churn when SessionID was seeded
	// with workflowRunID during StartSession before backend session ID was known.
	reconcileSessionIDsByWorkflow(m.sessions, backendIDsByWorkflow)
	for _, ls := range m.sessions {
		if bs, ok := backendSessionByID[ls.SessionID]; ok {
			ls.WhepURL = bs.WhepUrl
			applyBackendScreenDimensions(ls, bs)
			continue
		}
		if bs, ok := backendSessionByWorkflow[ls.WorkflowRunID]; ok {
			ls.SessionID = bs.Id
			ls.WhepURL = bs.WhepUrl
			applyBackendScreenDimensions(ls, bs)
		}
	}
	for idx, ls := range m.sessions {
		if allBackendIDs[ls.SessionID] {
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

		workflowRunID := ""
		if bs.WorkflowRunId != nil {
			workflowRunID = *bs.WorkflowRunId
		}

		// Validate worker is actually reachable before adding.
		// DNS entries are cleaned up before backend DB status is updated,
		// so a non-empty URL doesn't guarantee the worker is alive.
		tmpSession := &DeviceSession{WorkerBaseURL: workerBaseURL, WorkflowRunID: workflowRunID}
		if _, hErr := m.healthCheckSession(tmpSession); hErr != nil {
			ui.PrintDebug("skipping session %s: worker unreachable (%v)", shortPrefix(bs.Id, 8), hErr)
			continue
		}

		// Build viewer URL
		appURL := config.GetAppURL(m.devMode)
		viewerURL := fmt.Sprintf("%s/sessions/%s", appURL, url.PathEscape(bs.Id))

		startedAt := time.Now()
		if bs.StartedAt != nil {
			startedAt = *bs.StartedAt
		}

		idx := m.nextIndex
		m.nextIndex++

		session := &DeviceSession{
			Index:         idx,
			SessionID:     bs.Id,
			WorkflowRunID: workflowRunID,
			WorkerBaseURL: workerBaseURL,
			ViewerURL:     viewerURL,
			WhepURL:       bs.WhepUrl,
			Platform:      bs.Platform,
			StartedAt:     startedAt,
			LastActivity:  time.Now(),
			IdleTimeout:   5 * time.Minute,
		}
		applyBackendScreenDimensions(session, bs)

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

	// Step 8: Persist
	m.persistSessions()
	return nil
}

// AttachBySessionID connects to an existing session by its ID, bypassing the
// email-based sync filter. This enables attaching to any session in the same org
// regardless of who started it.
//
// If the session is already in the local map, it is activated directly.
// Otherwise it is fetched from the backend, the worker URL is resolved,
// and it is added to the local session list and set as active.
//
// Parameters:
//   - ctx: Context for cancellation.
//   - sessionID: The device session UUID.
//
// Returns:
//   - index: The local session index.
//   - session: The attached DeviceSession.
//   - error: Any error during attachment.
func (m *DeviceSessionManager) AttachBySessionID(ctx context.Context, sessionID string) (int, *DeviceSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.apiClient == nil {
		return -1, nil, fmt.Errorf("no API client configured")
	}

	// Check if already known locally.
	for idx, s := range m.sessions {
		if s.SessionID == sessionID {
			m.activeIndex = idx
			m.persistSessions()
			return idx, s, nil
		}
	}

	// Ensure org info for viewer URL construction.
	if err := m.ensureOrgInfoLocked(ctx); err != nil {
		return -1, nil, fmt.Errorf("failed to resolve org info: %w", err)
	}

	// Fetch the session directly by ID.
	detail, err := m.apiClient.GetDeviceSessionByID(ctx, sessionID)
	if err != nil {
		return -1, nil, fmt.Errorf("session not found or not accessible: %w", err)
	}

	// Verify it's in a usable state.
	status := strings.ToLower(detail.Status)
	if status == "completed" || status == "failed" || status == "cancelled" || status == "timeout" {
		return -1, nil, fmt.Errorf("session %s is in terminal state: %s", sessionID[:min(8, len(sessionID))], detail.Status)
	}

	// Resolve the worker URL via workflow run ID.
	if detail.WorkflowRunID == nil || *detail.WorkflowRunID == "" {
		return -1, nil, fmt.Errorf("session %s has no workflow run ID (may still be queued)", sessionID[:min(8, len(sessionID))])
	}

	wsResp, wsErr := m.apiClient.GetWorkerWSURL(ctx, *detail.WorkflowRunID)
	if wsErr != nil {
		return -1, nil, fmt.Errorf("failed to resolve worker URL: %w", wsErr)
	}

	workerBaseURL := ""
	if wsResp.WorkerWsUrl != nil && *wsResp.WorkerWsUrl != "" {
		workerBaseURL = wsURLToHTTP(*wsResp.WorkerWsUrl)
	}
	if workerBaseURL == "" {
		return -1, nil, fmt.Errorf("worker URL not available for session %s (device may still be starting)", sessionID[:min(8, len(sessionID))])
	}

	// Health-check the worker.
	tmpSession := &DeviceSession{WorkerBaseURL: workerBaseURL, WorkflowRunID: *detail.WorkflowRunID}
	if _, hErr := m.healthCheckSession(tmpSession); hErr != nil {
		return -1, nil, fmt.Errorf("worker unreachable for session %s: %w", sessionID[:min(8, len(sessionID))], hErr)
	}

	// Build the session.
	platform := "unknown"
	if detail.Platform != nil {
		platform = *detail.Platform
	}

	startedAt := time.Now()
	if detail.StartedAt != nil {
		if t, parseErr := time.Parse(time.RFC3339, *detail.StartedAt); parseErr == nil {
			startedAt = t
		}
	}

	appURL := config.GetAppURL(m.devMode)
	viewerURL := fmt.Sprintf("%s/sessions/%s", appURL, url.PathEscape(sessionID))

	idx := m.nextIndex
	m.nextIndex++

	session := &DeviceSession{
		Index:         idx,
		SessionID:     sessionID,
		WorkflowRunID: *detail.WorkflowRunID,
		WorkerBaseURL: workerBaseURL,
		ViewerURL:     viewerURL,
		WhepURL:       detail.WhepURL,
		Platform:      platform,
		StartedAt:     startedAt,
		LastActivity:  time.Now(),
		IdleTimeout:   5 * time.Minute,
	}

	m.sessions[idx] = session
	m.activeIndex = idx
	m.resetIdleTimerForSessionLocked(idx, ctx)
	m.persistSessions()

	return idx, session, nil
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
