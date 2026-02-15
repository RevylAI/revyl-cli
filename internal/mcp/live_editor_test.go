package mcp

import (
	"context"
	"testing"

	"github.com/revyl/cli/internal/config"
)

// newTestServerNoTools creates a minimal Server for handler testing
// without calling registerTools() (which requires the full MCP SDK).
func newTestServerNoTools(cfg *config.ProjectConfig) *Server {
	return &Server{
		config:  cfg,
		workDir: "/tmp/test-project",
		version: "test",
	}
}

func TestOpenTestEditorNoHotReload(t *testing.T) {
	cfg := &config.ProjectConfig{
		Tests: map[string]string{
			"login-flow": "test-uuid-123",
		},
	}

	s := newTestServerNoTools(cfg)
	ctx := context.Background()

	_, output, err := s.handleOpenTestEditor(ctx, nil, OpenTestEditorInput{
		TestNameOrID: "login-flow",
		NoOpen:       true,
	})
	if err != nil {
		t.Fatalf("handleOpenTestEditor: unexpected error: %v", err)
	}
	if !output.Success {
		t.Fatalf("handleOpenTestEditor: expected success, got error: %s", output.Error)
	}
	if output.TestID != "test-uuid-123" {
		t.Errorf("expected test ID 'test-uuid-123', got %q", output.TestID)
	}
	if output.HotReload {
		t.Error("expected hot_reload=false when no hotreload config")
	}
	if output.EditorURL == "" {
		t.Error("expected non-empty editor URL")
	}
	if output.TunnelURL != "" {
		t.Error("expected empty tunnel URL without hot reload")
	}
}

func TestOpenTestEditorMissingTestName(t *testing.T) {
	s := newTestServerNoTools(nil)
	ctx := context.Background()

	_, output, err := s.handleOpenTestEditor(ctx, nil, OpenTestEditorInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Success {
		t.Error("expected failure when test_name_or_id is empty")
	}
	if output.Error == "" {
		t.Error("expected error message when test_name_or_id is empty")
	}
}

func TestOpenTestEditorResolvesUUID(t *testing.T) {
	s := newTestServerNoTools(nil)
	ctx := context.Background()

	_, output, err := s.handleOpenTestEditor(ctx, nil, OpenTestEditorInput{
		TestNameOrID: "raw-uuid-456",
		NoOpen:       true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected success, got error: %s", output.Error)
	}
	if output.TestID != "raw-uuid-456" {
		t.Errorf("expected test ID 'raw-uuid-456', got %q", output.TestID)
	}
}

func TestOpenTestEditorIdempotent(t *testing.T) {
	cfg := &config.ProjectConfig{
		Tests: map[string]string{
			"login-flow": "test-uuid-123",
		},
	}

	s := newTestServerNoTools(cfg)
	ctx := context.Background()

	// First call — no hot reload, should succeed
	_, output1, _ := s.handleOpenTestEditor(ctx, nil, OpenTestEditorInput{
		TestNameOrID: "login-flow",
		NoOpen:       true,
	})
	if !output1.Success {
		t.Fatalf("first call: expected success, got error: %s", output1.Error)
	}

	// Second call for same test — should also succeed (idempotent)
	_, output2, _ := s.handleOpenTestEditor(ctx, nil, OpenTestEditorInput{
		TestNameOrID: "login-flow",
		NoOpen:       true,
	})
	if !output2.Success {
		t.Fatalf("second call: expected success, got error: %s", output2.Error)
	}
	if output2.TestID != output1.TestID {
		t.Errorf("expected same test ID across calls, got %q and %q", output1.TestID, output2.TestID)
	}
}

func TestStopHotReloadNoSession(t *testing.T) {
	s := newTestServerNoTools(nil)
	ctx := context.Background()

	_, output, err := s.handleStopHotReload(ctx, nil, StopHotReloadInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !output.Success {
		t.Fatalf("expected success, got error: %s", output.Error)
	}
	if output.Message != "No active hot reload session" {
		t.Errorf("expected 'No active hot reload session', got %q", output.Message)
	}
}

func TestHotReloadStatusNoSession(t *testing.T) {
	s := newTestServerNoTools(nil)
	ctx := context.Background()

	_, output, err := s.handleHotReloadStatus(ctx, nil, HotReloadStatusInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Active {
		t.Error("expected inactive status when no session running")
	}
	if output.TestID != "" {
		t.Errorf("expected empty test ID, got %q", output.TestID)
	}
}

func TestShutdownNoSession(t *testing.T) {
	s := newTestServerNoTools(nil)
	// Should not panic when no session is active
	s.Shutdown()
}
