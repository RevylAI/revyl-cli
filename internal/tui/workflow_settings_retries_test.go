package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestWorkflowSettingsDoesNotOfferWholeTestRetries(t *testing.T) {
	m := hubModel{wfSettingsWorkflow: &api.Workflow{RunConfig: map[string]interface{}{
		"execution_mode": map[string]interface{}{"retries": float64(3)},
	}}}
	if strings.Contains(renderWorkflowSettings(m), "Retries:") {
		t.Fatal("whole-test retries must not be offered in workflow settings")
	}
	if wfSettingsSectionCount != 3 {
		t.Fatal("whole-test retry settings must not be navigable")
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
