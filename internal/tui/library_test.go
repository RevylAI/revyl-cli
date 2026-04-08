package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

// newLibraryHubModel returns a hub model parked on the Library view at the
// Modules tab in list mode, with the four list slices pre-populated so the
// tests can exercise navigation without an API client.
func newLibraryHubModel() hubModel {
	m := newHubModel("dev", false)
	m.width = 100
	m.height = 30
	m.currentView = viewLibrary
	m.libraryTab = libTabModules
	m.libraryMode = libModeList
	m.moduleItems = []ModuleItem{
		{ID: "m1", Name: "login-flow", BlockCount: 3},
		{ID: "m2", Name: "checkout", BlockCount: 5},
	}
	m.varItems = []VariableItem{
		{ID: "v1", Name: "API_TOKEN", Value: "abc"},
	}
	m.scriptItems = []ScriptItem{
		{ID: "s1", Name: "parse_otp", Runtime: "python", Description: "decode SMS"},
	}
	m.fileItems = []FileItem{
		{ID: "f1", Filename: "cert.pem", FileSize: 2048, ContentType: "application/x-pem-file"},
	}
	return m
}

func newLibraryHubModelWithClient() hubModel {
	m := newLibraryHubModel()
	m.client = &api.Client{}
	return m
}

func sendKey(t *testing.T, m hubModel, key string) hubModel {
	t.Helper()
	res, _ := handleLibraryKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	hm, ok := res.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", res)
	}
	return hm
}

func sendSpecialKey(t *testing.T, m hubModel, kt tea.KeyType) hubModel {
	t.Helper()
	res, _ := handleLibraryKey(m, tea.KeyMsg{Type: kt})
	hm, ok := res.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", res)
	}
	return hm
}

func TestLibraryTabCycleForward(t *testing.T) {
	m := newLibraryHubModel()
	want := []libraryTab{libTabVariables, libTabScripts, libTabFiles, libTabModules}
	for i, expected := range want {
		m = sendSpecialKey(t, m, tea.KeyTab)
		if m.libraryTab != expected {
			t.Fatalf("step %d: expected tab %d, got %d", i, expected, m.libraryTab)
		}
		if m.libraryMode != libModeList {
			t.Fatalf("step %d: tab switch should reset to list mode, got %v", i, m.libraryMode)
		}
	}
}

func TestLibraryTabCycleBackward(t *testing.T) {
	m := newLibraryHubModel()
	m = sendSpecialKey(t, m, tea.KeyShiftTab)
	if m.libraryTab != libTabFiles {
		t.Fatalf("shift+tab from modules should land on files, got %v", m.libraryTab)
	}
}

func TestLibraryTabDirectJump(t *testing.T) {
	m := newLibraryHubModel()
	cases := map[string]libraryTab{
		"2": libTabVariables,
		"3": libTabScripts,
		"4": libTabFiles,
		"1": libTabModules,
	}
	for key, expected := range cases {
		m2 := sendKey(t, m, key)
		if m2.libraryTab != expected {
			t.Fatalf("key %q expected tab %d, got %d", key, expected, m2.libraryTab)
		}
	}
}

func TestLibraryTabSwitchSetsLoadingFlagForActiveTab(t *testing.T) {
	tests := []struct {
		name           string
		startTab       libraryTab
		key            tea.KeyMsg
		wantTab        libraryTab
		moduleLoading  bool
		varsLoading    bool
		scriptsLoading bool
		filesLoading   bool
	}{
		{
			name:        "tab to variables",
			startTab:    libTabModules,
			key:         tea.KeyMsg{Type: tea.KeyTab},
			wantTab:     libTabVariables,
			varsLoading: true,
		},
		{
			name:           "tab to scripts",
			startTab:       libTabVariables,
			key:            tea.KeyMsg{Type: tea.KeyTab},
			wantTab:        libTabScripts,
			scriptsLoading: true,
		},
		{
			name:         "shift tab to files",
			startTab:     libTabModules,
			key:          tea.KeyMsg{Type: tea.KeyShiftTab},
			wantTab:      libTabFiles,
			filesLoading: true,
		},
		{
			name:          "direct jump to modules",
			startTab:      libTabFiles,
			key:           tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")},
			wantTab:       libTabModules,
			moduleLoading: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLibraryHubModelWithClient()
			m.libraryTab = tt.startTab

			res, cmd := handleLibraryKey(m, tt.key)
			got, ok := res.(hubModel)
			if !ok {
				t.Fatalf("expected hubModel, got %T", res)
			}
			if cmd == nil {
				t.Fatal("expected fetch command on tab switch")
			}
			if got.libraryTab != tt.wantTab {
				t.Fatalf("expected tab %v, got %v", tt.wantTab, got.libraryTab)
			}
			if got.libraryMode != libModeList {
				t.Fatalf("expected list mode after tab switch, got %v", got.libraryMode)
			}
			if got.moduleLoading != tt.moduleLoading {
				t.Fatalf("moduleLoading = %v, want %v", got.moduleLoading, tt.moduleLoading)
			}
			if got.varsLoading != tt.varsLoading {
				t.Fatalf("varsLoading = %v, want %v", got.varsLoading, tt.varsLoading)
			}
			if got.scriptsLoading != tt.scriptsLoading {
				t.Fatalf("scriptsLoading = %v, want %v", got.scriptsLoading, tt.scriptsLoading)
			}
			if got.filesLoading != tt.filesLoading {
				t.Fatalf("filesLoading = %v, want %v", got.filesLoading, tt.filesLoading)
			}
		})
	}
}

func TestLibraryRenderShowsAllTabsAndActiveHighlight(t *testing.T) {
	m := newLibraryHubModel()
	out := renderLibrary(m)
	for _, label := range []string{"Modules", "Variables", "Scripts", "Files"} {
		if !strings.Contains(out, label) {
			t.Errorf("library output missing tab label %q", label)
		}
	}
	if !strings.Contains(out, "[1 Modules]") {
		t.Errorf("active tab should be bracketed, got:\n%s", out)
	}
}

func TestLibraryRenderShowsCLIHintPerTab(t *testing.T) {
	hints := map[libraryTab]string{
		libTabModules:   "revyl module create",
		libTabVariables: "revyl global var set",
		libTabScripts:   "revyl script create",
		libTabFiles:     "revyl file upload",
	}
	for tab, want := range hints {
		m := newLibraryHubModel()
		m.libraryTab = tab
		out := renderLibrary(m)
		if !strings.Contains(out, want) {
			t.Errorf("tab %d: expected CLI hint %q in render, got:\n%s", tab, want, out)
		}
	}
}

func TestLibraryVariablesCreateWizardFlow(t *testing.T) {
	m := newLibraryHubModel()
	m.libraryTab = libTabVariables

	// Press 'n' to open the create wizard.
	m = sendKey(t, m, "n")
	if m.libraryMode != libModeEditing {
		t.Fatalf("expected editing mode after 'n', got %v", m.libraryMode)
	}
	if !m.varIsCreating {
		t.Fatal("expected varIsCreating to be true for new wizard")
	}
	if m.varNameInput.Value() != "" || m.varValueInput.Value() != "" {
		t.Fatal("create wizard should start with empty inputs")
	}

	// Type a name into the focused field.
	for _, r := range "API_KEY" {
		res, _ := handleLibraryKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = res.(hubModel)
	}
	if m.varNameInput.Value() != "API_KEY" {
		t.Fatalf("expected name 'API_KEY', got %q", m.varNameInput.Value())
	}

	// Tab to value field.
	m = sendSpecialKey(t, m, tea.KeyTab)
	if m.varEditField != 1 {
		t.Fatalf("expected varEditField=1 after tab, got %d", m.varEditField)
	}

	// Esc cancels back to list mode.
	m = sendSpecialKey(t, m, tea.KeyEsc)
	if m.libraryMode != libModeList {
		t.Fatalf("esc should return to list mode, got %v", m.libraryMode)
	}
}

func TestLibraryVariablesEditPrefillsExistingValues(t *testing.T) {
	m := newLibraryHubModel()
	m.libraryTab = libTabVariables
	m = sendKey(t, m, "e")
	if m.libraryMode != libModeEditing {
		t.Fatalf("expected editing mode, got %v", m.libraryMode)
	}
	if m.varIsCreating {
		t.Fatal("editing existing variable should not set varIsCreating")
	}
	if m.varNameInput.Value() != "API_TOKEN" {
		t.Fatalf("expected name prefilled to 'API_TOKEN', got %q", m.varNameInput.Value())
	}
	if m.varValueInput.Value() != "abc" {
		t.Fatalf("expected value prefilled to 'abc', got %q", m.varValueInput.Value())
	}
}

func TestLibraryDeleteConfirmRequiresExplicitYes(t *testing.T) {
	// Variables tab: 'd' opens confirm, 'n' cancels.
	m := newLibraryHubModel()
	m.libraryTab = libTabVariables
	m = sendKey(t, m, "d")
	if m.libraryMode != libModeConfirmDelete {
		t.Fatalf("expected confirm mode after 'd', got %v", m.libraryMode)
	}
	m = sendKey(t, m, "n")
	if m.libraryMode != libModeList {
		t.Fatalf("'n' should cancel back to list, got %v", m.libraryMode)
	}
}

func TestLibraryVariableDeletedMsgReturnsToListMode(t *testing.T) {
	m := newLibraryHubModelWithClient()
	m.libraryTab = libTabVariables
	m.libraryMode = libModeEditing
	m.selectedVar = &m.varItems[0]

	res, cmd := m.Update(VariableDeletedMsg{})
	got, ok := res.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", res)
	}
	if cmd == nil {
		t.Fatal("expected refetch command after variable delete")
	}
	if got.libraryMode != libModeList {
		t.Fatalf("expected list mode after VariableDeletedMsg, got %v", got.libraryMode)
	}
	if got.selectedVar != nil {
		t.Fatalf("expected selectedVar cleared after VariableDeletedMsg, got %+v", got.selectedVar)
	}
}

func TestLibraryScriptsEditOpensNameField(t *testing.T) {
	m := newLibraryHubModel()
	m.libraryTab = libTabScripts
	m = sendKey(t, m, "e")
	if m.libraryMode != libModeEditing {
		t.Fatalf("expected editing mode after 'e', got %v", m.libraryMode)
	}
	if m.selectedScript == nil || m.selectedScript.ID != "s1" {
		t.Fatalf("expected selectedScript=s1, got %+v", m.selectedScript)
	}
	if m.scriptEditNameInput.Value() != "parse_otp" {
		t.Fatalf("expected name prefilled, got %q", m.scriptEditNameInput.Value())
	}
	if m.scriptEditDescInput.Value() != "decode SMS" {
		t.Fatalf("expected description prefilled, got %q", m.scriptEditDescInput.Value())
	}
}

func TestLibraryFilesEditOpensDescriptionField(t *testing.T) {
	m := newLibraryHubModel()
	m.libraryTab = libTabFiles
	m.fileItems[0].Description = "TLS cert"
	m = sendKey(t, m, "e")
	if m.libraryMode != libModeEditing {
		t.Fatalf("expected editing mode, got %v", m.libraryMode)
	}
	if m.fileEditDescInput.Value() != "TLS cert" {
		t.Fatalf("expected description prefilled, got %q", m.fileEditDescInput.Value())
	}
}

func TestLibraryEscapeFromListReturnsToDashboard(t *testing.T) {
	m := newLibraryHubModel()
	m = sendSpecialKey(t, m, tea.KeyEsc)
	if m.currentView != viewDashboard {
		t.Fatalf("esc from library list should return to dashboard, got %v", m.currentView)
	}
}

func TestLibraryFormatFileSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2.0 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, c := range cases {
		got := formatLibraryFileSize(c.in)
		if got != c.want {
			t.Errorf("formatLibraryFileSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLibraryMessageHandlersPopulateSlices(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewLibrary

	// Scripts
	res, _ := m.Update(ScriptListMsg{Scripts: []ScriptItem{{ID: "s1", Name: "x"}}})
	m = res.(hubModel)
	if len(m.scriptItems) != 1 || m.scriptItems[0].Name != "x" {
		t.Fatalf("ScriptListMsg should populate scriptItems, got %+v", m.scriptItems)
	}

	// Files
	res, _ = m.Update(FileListMsg{Files: []FileItem{{ID: "f1", Filename: "a"}}})
	m = res.(hubModel)
	if len(m.fileItems) != 1 || m.fileItems[0].Filename != "a" {
		t.Fatalf("FileListMsg should populate fileItems, got %+v", m.fileItems)
	}

	// Variables
	res, _ = m.Update(VariableListMsg{Variables: []VariableItem{{ID: "v1", Name: "K", Value: "V"}}})
	m = res.(hubModel)
	if len(m.varItems) != 1 || m.varItems[0].Name != "K" {
		t.Fatalf("VariableListMsg should populate varItems, got %+v", m.varItems)
	}
}

func TestLibraryModuleDetailMsgRoutesToLibraryMode(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewLibrary
	m.libraryTab = libTabModules
	m.libraryMode = libModeList
	res, _ := m.Update(ModuleDetailMsg{Module: &ModuleItem{ID: "m1", Name: "x"}})
	m = res.(hubModel)
	if m.libraryMode != libModeDetail {
		t.Fatalf("ModuleDetailMsg in library should set libModeDetail, got %v", m.libraryMode)
	}
	if m.currentView != viewLibrary {
		t.Fatalf("currentView should remain viewLibrary, got %v", m.currentView)
	}
}
