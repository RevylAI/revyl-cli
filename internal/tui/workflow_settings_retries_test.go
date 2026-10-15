package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/revyl/cli/internal/api"
)

func TestApplySettingsEditRetries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		input       string
		wantRetries int
		wantDirty   bool
		wantErr     bool
	}{
		{name: "within range", input: "3", wantRetries: 3, wantDirty: true},
		{name: "above maximum", input: "20", wantRetries: 1, wantErr: true},
		{name: "zero", input: "0", wantRetries: 1, wantErr: true},
		{name: "not a number", input: "two", wantRetries: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := hubModel{
				wfSettingsWorkflow: &api.Workflow{RunConfig: map[string]interface{}{
					"execution_mode": map[string]interface{}{"retries": float64(1)},
				}},
				wfSettingsEditField: "retries",
				wfSettingsInput:     textinput.New(),
			}
			m.wfSettingsInput.SetValue(tc.input)

			applySettingsEdit(&m)

			if retries, _ := m.wfSettingsWorkflow.RunConfigRetries(); retries != tc.wantRetries {
				t.Fatalf("retries = %d, want %d", retries, tc.wantRetries)
			}
			if m.wfSettingsDirty != tc.wantDirty {
				t.Fatalf("dirty = %v, want %v", m.wfSettingsDirty, tc.wantDirty)
			}
			if (m.wfSettingsEditErr != "") != tc.wantErr {
				t.Fatalf("edit error = %q, want error %v", m.wfSettingsEditErr, tc.wantErr)
			}
		})
	}
}

func TestWorkflowSettingsFailedSaveKeepsEditsAndShowsError(t *testing.T) {
	m := hubModel{currentView: viewWorkflowSettings, wfSettingsDirty: true, wfSettingsLoading: true}
	updated, cmd := m.Update(WorkflowSettingsSavedMsg{Err: errors.New("permission denied")})
	got := updated.(hubModel)
	if cmd != nil || got.currentView != viewWorkflowSettings || !got.wfSettingsDirty || got.wfSettingsLoading {
		t.Fatal("failed save must retain edits and stay on settings")
	}
	if got.err == nil || got.err.Error() != "settings save failed: permission denied; your edits are kept; retry saving" {
		t.Fatalf("failed save error = %v", got.err)
	}
	if got.wfSettingsEditErr != "" || strings.Count(renderWorkflowSettings(got), "permission denied") != 1 {
		t.Fatalf("failed save must render exactly one error line:\n%s", renderWorkflowSettings(got))
	}
}
