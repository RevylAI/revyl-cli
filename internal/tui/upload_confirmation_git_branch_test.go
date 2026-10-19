package tui

import (
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAppBuildUploadKeepsExplicitDestinationAfterGitChanges(t *testing.T) {
	root := selectGitBranchWorktree(t, "feature/one")
	fixture := newUploadConfirmationFixture(t)
	upload := openUploadConfirmation(t, fixture.client, appBuildQuery{BranchID: pickerOtherBranchID})
	next, cmd := upload.Update(keyRune('b'))
	upload = next.(uploadBuildModel)
	next, _ = upload.Update(cmd())
	upload = next.(uploadBuildModel)
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyDown})
	upload = next.(uploadBuildModel)
	next, _ = upload.Update(tea.KeyMsg{Type: tea.KeyEnter})
	upload = next.(uploadBuildModel)
	if !strings.Contains(upload.View(), "feature/one") || len(fixture.branchNames) != 0 {
		t.Fatalf("explicit upload destination inferred Git: lookups=%v, view=%s", fixture.branchNames, upload.View())
	}
	if output, err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/feature/two").CombinedOutput(); err != nil {
		t.Fatalf("change fixture branch: %v: %s", err, output)
	}
	confirmUploadDestination(t, upload, fixture, pickerBranchID)
	if len(fixture.branchNames) != 0 {
		t.Fatalf("explicit confirmation detected Git: %v", fixture.branchNames)
	}
}

func TestAppBuildUploadAutomaticUsesGitBranchShownAtConfirmation(t *testing.T) {
	root := selectGitBranchWorktree(t, "feature/one")
	fixture := newUploadConfirmationFixture(t)
	upload := openUploadConfirmation(t, fixture.client, appBuildQuery{BranchID: pickerBranchID})
	upload.gitBranch = "feature/one"
	if !strings.Contains(upload.View(), "Git branch (feature/one)") || len(fixture.branchNames) != 0 {
		t.Fatalf("All browsing resolved Automatic before upload: lookups=%v, view=%s", fixture.branchNames, upload.View())
	}
	if output, err := exec.Command("git", "-C", root, "symbolic-ref", "HEAD", "refs/heads/feature/two").CombinedOutput(); err != nil {
		t.Fatalf("change fixture branch: %v: %s", err, output)
	}
	confirmUploadDestination(t, upload, fixture, pickerBranchID)
	if len(fixture.branchNames) != 1 || fixture.branchNames[0] != "feature/one" {
		t.Fatalf("Automatic did not preserve the reviewed Git branch: %v", fixture.branchNames)
	}
}

func TestAppBuildUploadDoesNotInheritReadFallback(t *testing.T) {
	selectGitBranchWorktree(t, "feature/one")
	fixture := newUploadConfirmationFixture(t)
	fixture.defaultBranchID = pickerOtherBranchID
	fixture.gitBranchFellBack = true
	upload := openUploadConfirmation(t, fixture.client, appBuildQuery{BranchID: pickerOtherBranchID})
	upload.gitBranch = "feature/one"
	if upload.branchSelection.BranchID != "" || len(fixture.branchNames) != 0 {
		t.Fatalf("All browsing resolved an upload fallback: selection=%+v, lookups=%v", upload.branchSelection, fixture.branchNames)
	}
	confirmUploadDestination(t, upload, fixture, pickerBranchID)
	if len(fixture.branchNames) != 1 || fixture.branchNames[0] != "feature/one" {
		t.Fatalf("upload did not resolve its own inferred destination: %v", fixture.branchNames)
	}
}
