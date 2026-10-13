package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revyl/cli/internal/api"
)

func TestBranchDefaultPolicyDisplay(t *testing.T) {
	pinnedID, pinnedVersion := "33333333-3333-4333-8333-333333333333", "v1.2"
	for _, test := range []struct {
		name                   string
		pin, version           *string
		wantBranch, wantAppRow string
	}{
		{"latest", nil, nil, "Latest build", "Latest build"},
		{"pinned", &pinnedID, &pinnedVersion, "Pinned build (v1.2)", "Pinned build"},
		{"pinned without a version", &pinnedID, nil, "Pinned build (" + pinnedID + ")", "Pinned build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			branch := api.BuildBranch{ID: pickerBranchID, Name: "default", PinnedBuildID: test.pin, PinnedBuildVersion: test.version}
			picker := newBuildPicker(nil, pickerAppID, "Build")
			picker.loading, picker.branches = false, []api.BuildBranch{branch}
			hub := newHubModel("dev", false)
			hub.appBuildScope.BranchID = branch.ID
			hub.appBuildBranch = &branch
			app := api.App{DefaultBranchName: "default"}
			if test.pin != nil {
				app.DefaultBranchPinnedBuildID = *test.pin
			}
			for _, display := range []struct{ label, want string }{
				{picker.options()[1], "default · " + test.wantBranch},
				{hub.renderAppDetail(), "default · " + test.wantBranch},
				{appBranchLabel(app), "default (default) · " + test.wantAppRow},
			} {
				if !strings.Contains(display.label, display.want) || strings.Contains(display.label, "Current build") {
					t.Fatalf("branch default display = %q, want %q", display.label, display.want)
				}
			}
		})
	}
}

func TestBranchPolicyInheritAndLatestRemainDistinct(t *testing.T) {
	pinnedID := "33333333-3333-4333-8333-333333333333"
	for index, mode := range []api.BuildSelectionMode{api.BuildSelectionModeInherit, api.BuildSelectionModeLatest} {
		picker := newBuildPicker(nil, pickerAppID, "Build")
		picker.loading, picker.cursor = false, 1
		picker.branches = []api.BuildBranch{{ID: pickerBranchID, Name: "default", PinnedBuildID: &pinnedID}}
		picker, _ = pickerKey(picker, tea.KeyEnter)
		if options := picker.options(); len(options) != 3 || strings.Contains(strings.Join(options, " "), "Current") {
			t.Fatalf("unexpected run selection choices: %v", options)
		}
		picker.cursor = index
		picker, _ = pickerKey(picker, tea.KeyEnter)
		if !picker.done || picker.selection.Mode != mode || picker.selection.BranchID != pickerBranchID {
			t.Fatalf("selection changed: %#v", picker.selection)
		}
	}
}

func TestBuildBrowserScopeLabelNeverClaimsAnEmptyBranchItCannotSee(t *testing.T) {
	branch := api.BuildBranch{ID: pickerBranchID, Name: "main"}
	hub := newHubModel("dev", false)
	hub.appBuildScope.BranchID = branch.ID
	hub.appBuildBranch = &branch
	if detail := hub.renderAppDetail(); !strings.Contains(detail, "main · Latest build") || strings.Contains(detail, "no builds") {
		t.Fatalf("scope label for a branch read by ID = %q", detail)
	}

	picker := newBuildPicker(nil, pickerAppID, "Build")
	picker.loading, picker.branches = false, []api.BuildBranch{branch}
	if option := picker.options()[1]; !strings.Contains(option, "main · Latest build (no builds)") {
		t.Fatalf("listed branch with no latest build = %q", option)
	}
}
