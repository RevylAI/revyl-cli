package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
)

func TestDevLoopRestartTeardownStopsPreviousSession(t *testing.T) {
	now := time.Now()
	mgr := NewDeviceSessionManager(nil, t.TempDir())
	mgr.sessions[0] = &DeviceSession{
		Index:         0,
		SessionID:     "sess-1",
		WorkflowRunID: "wf-1",
		Platform:      "ios",
		StartedAt:     now,
		LastActivity:  now,
		IdleTimeout:   5 * time.Minute,
	}
	mgr.activeIndex = 0

	srv := &Server{
		sessionMgr:          mgr,
		devLoopActive:       true,
		devLoopSessionIndex: 0,
	}

	manager, index, shouldStopSession := srv.clearDevLoopState()
	if manager != nil {
		t.Fatalf("expected nil hot reload manager in test setup")
	}
	if index != 0 {
		t.Fatalf("expected prior session index 0, got %d", index)
	}
	if !shouldStopSession {
		t.Fatal("expected prior dev-loop session to be stopped")
	}

	if err := srv.stopDetachedDevLoop(context.Background(), manager, index, shouldStopSession); err != nil {
		t.Fatalf("stopDetachedDevLoop returned error: %v", err)
	}
	if got := mgr.GetSession(0); got != nil {
		t.Fatalf("expected previous session to be removed, got %+v", got)
	}
}

func TestDevLoopRestartTeardownIgnoresMissingPriorSession(t *testing.T) {
	srv := &Server{
		sessionMgr:          NewDeviceSessionManager(nil, t.TempDir()),
		devLoopActive:       true,
		devLoopSessionIndex: 42,
	}

	manager, index, shouldStopSession := srv.clearDevLoopState()
	if manager != nil {
		t.Fatalf("expected nil hot reload manager in test setup")
	}
	if index != 42 {
		t.Fatalf("expected prior session index 42, got %d", index)
	}
	if !shouldStopSession {
		t.Fatal("expected prior dev-loop session to be stopped")
	}

	if err := srv.stopDetachedDevLoop(context.Background(), manager, index, shouldStopSession); err != nil {
		t.Fatalf("expected missing prior session to be ignored, got %v", err)
	}
}

func TestDevLoopRestartTeardownPropagatesStopErrors(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/execution/device/status/cancel/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"cancel failed"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer apiServer.Close()

	now := time.Now()
	apiClient := api.NewClientWithBaseURL("test-api-key", apiServer.URL)
	mgr := NewDeviceSessionManager(apiClient, t.TempDir())
	mgr.sessions[1] = &DeviceSession{
		Index:         1,
		SessionID:     "sess-2",
		WorkflowRunID: "wf-2",
		Platform:      "android",
		StartedAt:     now,
		LastActivity:  now,
		IdleTimeout:   5 * time.Minute,
	}
	mgr.activeIndex = 1

	srv := &Server{
		sessionMgr:          mgr,
		devLoopActive:       true,
		devLoopSessionIndex: 1,
	}

	manager, index, shouldStopSession := srv.clearDevLoopState()
	err := srv.stopDetachedDevLoop(context.Background(), manager, index, shouldStopSession)
	if err == nil {
		t.Fatal("expected teardown stop error, got nil")
	}
	if !strings.Contains(err.Error(), "backend cancel failed") {
		t.Fatalf("expected backend cancel failure, got %v", err)
	}
}
