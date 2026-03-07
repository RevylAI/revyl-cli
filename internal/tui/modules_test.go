package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRenderModuleDetail_ReadableSummaries(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 100
	m.height = 24
	m.currentView = viewModuleDetail
	m.selectedModule = &ModuleItem{
		ID:          "module-1",
		Name:        "login-flow",
		Description: "Reusable login flow for smoke tests",
		BlockCount:  6,
		Blocks: []interface{}{
			map[string]interface{}{
				"id":               "block-1",
				"type":             "instructions",
				"step_description": "Enter email in the email field",
			},
			map[string]interface{}{
				"type":             "extraction",
				"step_description": "Extract the OTP code",
				"variable_name":    "otp-code",
			},
			map[string]interface{}{
				"type":             "manual",
				"step_type":        "navigate",
				"step_description": "myapp://login",
			},
			map[string]interface{}{
				"type":             "code_execution",
				"step_description": "script-uuid-here",
				"variable_name":    "auth-token",
			},
			map[string]interface{}{
				"type":             "module_import",
				"step_description": "Shared setup",
				"module_id":        "module-123",
			},
			map[string]interface{}{
				"type":      "if",
				"condition": "{{logged-in}} == true",
				"thenChildren": []interface{}{
					map[string]interface{}{
						"type":             "validation",
						"step_description": "Dashboard is visible",
					},
				},
				"else_children": []interface{}{
					map[string]interface{}{
						"type":             "instructions",
						"step_description": "Tap Sign In",
					},
				},
			},
		},
	}

	out := renderModuleDetail(m)

	for _, want := range []string{
		"Instructions  Enter email in the email field",
		"Extraction  Extract the OTP code -> {{otp-code}}",
		"Manual  Navigate to myapp://login",
		"Code execution  script-uuid-here -> {{auth-token}}",
		"Module import  Shared setup",
		"If  {{logged-in}} == true",
		"THEN",
		"ELSE",
		"Validation  Dashboard is visible",
		"Instructions  Tap Sign In",
		"scroll",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected module detail to contain %q, got:\n%s", want, out)
		}
	}

	if strings.Contains(out, "map[") {
		t.Fatalf("expected module detail to avoid raw map rendering, got:\n%s", out)
	}
}

func TestRenderModuleDetail_WrapsAndShowsScrollIndicators(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 60
	m.height = 13
	m.currentView = viewModuleDetail
	m.selectedModule = &ModuleItem{
		ID:         "module-2",
		Name:       "checkout-flow",
		BlockCount: 2,
		Blocks: []interface{}{
			map[string]interface{}{
				"type":             "instructions",
				"step_description": "This is a deliberately long instruction that should wrap across multiple lines without falling back to unreadable raw output in the module detail view.",
			},
			map[string]interface{}{
				"type":      "while",
				"condition": "{{cart-ready}} == false",
				"body": []interface{}{
					map[string]interface{}{
						"type":             "instructions",
						"step_description": "Scroll until the cart section is visible on screen.",
					},
				},
			},
		},
	}

	out := renderModuleDetail(m)
	if !strings.Contains(out, "↓ more") {
		t.Fatalf("expected initial render to show a down-scroll indicator, got:\n%s", out)
	}
	if strings.Contains(out, "…") {
		t.Fatalf("expected wrapped content instead of truncation, got:\n%s", out)
	}

	foundBody := false
	for scroll := 1; scroll <= moduleDetailMaxScroll(m); scroll++ {
		m.moduleDetailScroll = scroll
		out = renderModuleDetail(m)
		if strings.Contains(out, "↑ more") && strings.Contains(out, "BODY") {
			foundBody = true
			break
		}
	}
	if !foundBody {
		t.Fatalf("expected a scrolled render to show both an up-scroll indicator and the while body label, got:\n%s", out)
	}
}

func TestHandleModuleDetailKey_ScrollsWithinBounds(t *testing.T) {
	m := newHubModel("dev", false)
	m.width = 60
	m.height = 13
	m.currentView = viewModuleDetail
	m.selectedModule = &ModuleItem{
		ID:         "module-3",
		Name:       "long-flow",
		BlockCount: 1,
		Blocks: []interface{}{
			map[string]interface{}{
				"type":             "instructions",
				"step_description": strings.Repeat("Very long block text for scrolling coverage ", 8),
			},
		},
	}

	nextModel, cmd := handleModuleDetailKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("expected nil cmd when scrolling down, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.moduleDetailScroll != 1 {
		t.Fatalf("expected scroll offset to increment to 1, got %d", next.moduleDetailScroll)
	}

	next.moduleDetailScroll = moduleDetailMaxScroll(next)
	nextModel, cmd = handleModuleDetailKey(next, tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("expected nil cmd when scrolling at lower bound, got %v", cmd)
	}
	next = nextModel.(hubModel)
	if next.moduleDetailScroll != moduleDetailMaxScroll(next) {
		t.Fatalf("expected scroll offset to remain clamped at max, got %d", next.moduleDetailScroll)
	}

	nextModel, cmd = handleModuleDetailKey(next, tea.KeyMsg{Type: tea.KeyUp})
	if cmd != nil {
		t.Fatalf("expected nil cmd when scrolling up, got %v", cmd)
	}
	next = nextModel.(hubModel)
	if next.moduleDetailScroll != moduleDetailMaxScroll(next)-1 {
		t.Fatalf("expected scroll offset to decrement by 1, got %d", next.moduleDetailScroll)
	}
}

func TestHandleModuleDetailKey_DeleteConfirmBlocksScroll(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewModuleDetail
	m.moduleDetailScroll = 2
	m.moduleConfirmDelete = true
	m.selectedModule = &ModuleItem{
		ID:         "module-4",
		Name:       "login-flow",
		BlockCount: 1,
		Blocks: []interface{}{
			map[string]interface{}{
				"type":             "instructions",
				"step_description": "Tap Sign In",
			},
		},
	}

	nextModel, cmd := handleModuleDetailKey(m, tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("expected nil cmd while delete confirmation is active, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.moduleDetailScroll != 2 {
		t.Fatalf("expected scroll offset to remain unchanged during delete confirmation, got %d", next.moduleDetailScroll)
	}
	if !next.moduleConfirmDelete {
		t.Fatalf("expected delete confirmation to remain active")
	}
}

func TestUpdate_ModuleDetailMsgResetsScroll(t *testing.T) {
	m := newHubModel("dev", false)
	m.moduleDetailScroll = 7

	nextModel, cmd := m.Update(ModuleDetailMsg{
		Module: &ModuleItem{
			ID:         "module-5",
			Name:       "profile-flow",
			BlockCount: 1,
			Blocks: []interface{}{
				map[string]interface{}{
					"type":             "instructions",
					"step_description": "Open the profile tab",
				},
			},
		},
	})
	if cmd != nil {
		t.Fatalf("expected nil cmd for module detail update without follow-up work, got %v", cmd)
	}

	next := nextModel.(hubModel)
	if next.currentView != viewModuleDetail {
		t.Fatalf("expected update to switch to module detail, got %v", next.currentView)
	}
	if next.moduleDetailScroll != 0 {
		t.Fatalf("expected module detail scroll to reset, got %d", next.moduleDetailScroll)
	}
}
