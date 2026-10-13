package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

type appBuildQuery struct {
	Search   string
	BranchID string
}
type testBuildSavedMsg struct{ err error }

func (m hubModel) beginBuildPicker(appID, title, target, currentBranchID string) (tea.Model, tea.Cmd) {
	if m.client == nil || appID == "" {
		m.err = fmt.Errorf("select an app before choosing its branch")
		return m, nil
	}
	picker := newBuildPicker(m.client, appID, title)
	picker.currentBranchID = currentBranchID
	m.err = nil
	if target == "app-filter" {
		picker.branchOnly, picker.allowAll = true, true
	} else {
		picker.offerAllBranches = true
	}
	m.buildPicker, m.buildPickerTarget = &picker, target
	return m, picker.Init()
}

func (m hubModel) updateHubBuildPicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	picker, cmd := m.buildPicker.Update(msg)
	m.buildPicker = &picker
	if !picker.done {
		return m, cmd
	}
	m.buildPicker = nil
	if picker.cancelled {
		if m.buildPickerTarget == "device" {
			m.deviceStartStep = int(deviceStartStepApp)
		}
		return m, nil
	}
	switch m.buildPickerTarget {
	case "dev-loop":
		return m, runDevLoopProcessCmd(m.devMode, picker.selection.BranchName)
	case "device":
		m.deviceBuildSelection, m.deviceBuildLabel = picker.selection, picker.label
		m.deviceStartStep = int(deviceStartStepDevice)
	case "app-filter":
		m.appBuildScope.BranchID = picker.selection.BranchID
		m.appBuildCursor, m.appBuilds, m.appsLoading = 0, nil, true
		m.appBuildPage, m.appBuildHasNext, m.appBuildBranch = 0, false, nil
		cmd := m.startAppBuildFetch(1, nil)
		return m, cmd
	case "test-save":
		if m.selectedTestDetail != nil {
			return m, saveTestBuildCmd(m.client, m.selectedTestDetail.ID, m.selectedTestDetail.Version, picker.selection)
		}
	case "test-run":
		if m.selectedTestDetail != nil {
			em := newExecutionModel(m.selectedTestDetail.ID, m.selectedTestDetail.Name, m.apiKey, m.devMode, m.width, m.height)
			em.buildSelection = picker.selection
			em.overrideAppID = m.selectedTestDetail.AppID
			m.executionModel, m.currentView = &em, viewExecution
			return m, em.Init()
		}
	case "workflow-ios", "workflow-android":
		if m.wfSettingsWorkflow == nil {
			return m, nil
		}
		key := "ios_build"
		if m.buildPickerTarget == "workflow-android" {
			key = "android_build"
		}
		row, ok := m.wfSettingsWorkflow.BuildConfig[key].(map[string]interface{})
		if !ok {
			return m, nil
		}
		saved, err := api.NewSavedBuildSelection(picker.selection.BranchID, string(picker.selection.Mode), picker.selection.BuildID)
		if err != nil {
			m.err = err
			return m, nil
		}
		row["branch_id"], row["pinned_build_id"] = nil, nil
		if saved.BranchID != nil {
			row["branch_id"] = *saved.BranchID
		}
		if saved.PinnedBuildID != nil {
			row["pinned_build_id"] = *saved.PinnedBuildID
		}
		row["build_selection"] = string(saved.BuildSelection)
		if m.wfSettingsBranchLabels == nil {
			m.wfSettingsBranchLabels = map[string]string{}
		}
		m.wfSettingsBranchLabels[key] = picker.label
		delete(row, "pinned_version")
		m.wfSettingsDirty = true
	case "workflow-run":
		if m.selectedWfDetail == nil {
			return m, nil
		}
		m.currentView, m.wfExecTaskID, m.wfExecStatus = viewWorkflowExecution, "", nil
		m.wfExecDone, m.wfExecStartTime, m.wfExecReturnView = false, time.Now(), viewWorkflowDetail
		return m, executeWorkflowCmd(m.client, m.selectedWfDetail.ID, picker.selection)
	}
	return m, nil
}

func saveTestBuildCmd(client *api.Client, testID string, version int, selection api.BuildSelection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		saved, err := api.NewSavedBuildSelection(selection.BranchID, string(selection.Mode), selection.BuildID)
		if err != nil {
			return testBuildSavedMsg{err: err}
		}
		_, err = client.UpdateTest(ctx, &api.UpdateTestRequest{TestID: testID, ExpectedVersion: version, SavedBuildSelection: saved})
		return testBuildSavedMsg{err: err}
	}
}

func workflowBuildSelectionLabel(row map[string]interface{}) string {
	selection := api.BuildSelection{}
	selection.BranchID, _ = row["branch_id"].(string)
	selection.BuildID, _ = row["pinned_build_id"].(string)
	mode, _ := row["build_selection"].(string)
	selection.Mode = api.BuildSelectionMode(mode)
	if legacy, ok := row["pinned_version"].(string); ok && legacy != "" {
		return "Exact build " + legacy
	}
	return buildSelectionLabel(selection)
}

func workflowSettingsBuildLabel(m hubModel, key string, row map[string]interface{}) string {
	if label := m.wfSettingsBranchLabels[key]; label != "" {
		return label
	}
	return workflowBuildSelectionLabel(row)
}
