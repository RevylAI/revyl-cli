package tui

import (
	"strings"
	"testing"
)

func TestQuickActionsDoesNotIncludeTestFlight(t *testing.T) {
	for _, action := range quickActions {
		if action.Key == "publish_testflight" {
			t.Fatalf("expected publish_testflight quick action to be removed")
		}
	}
}

func TestHandleAppDetailKey_PDoesNotOpenPublishFlow(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewAppDetail

	nextModel, cmd := m.handleAppDetailKey(keyRune('p'))
	if cmd != nil {
		t.Fatalf("expected no command for removed publish shortcut, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewAppDetail {
		t.Fatalf("expected to remain on app detail view, got %v", next.currentView)
	}
	if next.publishTFModel != nil {
		t.Fatalf("expected publish flow model to remain nil")
	}
}

func TestRenderAppDetailDoesNotShowPublishHint(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 100
	m.height = 24
	m.currentView = viewAppDetail
	m.selectedAppName = "Example App"

	out := m.renderAppDetail()
	if strings.Contains(strings.ToLower(out), "publish") {
		t.Fatalf("expected app detail help to omit publish hint, got: %s", out)
	}
}

func TestDeriveSetupStepsDoesNotIncludeASCStep(t *testing.T) {
	steps := deriveSetupSteps([]HealthCheck{
		{Name: "Authentication", Status: "ok"},
		{Name: "API Connection", Status: "ok"},
		{Name: "Project Config", Status: "ok"},
		{Name: "App Linked", Status: "ok"},
		{Name: "Build Uploaded", Status: "warning"},
		{Name: "ASC Credentials", Status: "warning"},
		{Name: "Tests Configured", Status: "warning"},
	}, nil)

	for _, step := range steps {
		if step.Label == "Configure App Store Connect" {
			t.Fatalf("expected ASC setup step to be removed")
		}
	}
	if len(steps) != 6 {
		t.Fatalf("expected 6 setup steps after removing ASC setup, got %d", len(steps))
	}
}
