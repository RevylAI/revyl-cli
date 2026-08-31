package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/projectpublication"
)

func connectedReposState() *api.GithubRepositoriesResponse {
	return &api.GithubRepositoriesResponse{
		Repositories: []api.GithubOrgRepository{
			{Owner: "revyl", Repo: "app", InstallationID: 1},
		},
		Installation: &api.GithubOrgInstallation{InstallationID: 1, Status: "active"},
		HasAccess:    true,
	}
}

func asHub(t *testing.T, model tea.Model) hubModel {
	t.Helper()
	hm, ok := model.(hubModel)
	if !ok {
		t.Fatalf("expected hubModel, got %T", model)
	}
	return hm
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestRenderGithubStatusBadge(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*hubModel)
		wantSub string
	}{
		{
			name:    "loading",
			mutate:  func(m *hubModel) { m.integrationsLoading = true },
			wantSub: "checking",
		},
		{
			name:    "not connected",
			mutate:  func(m *hubModel) {},
			wantSub: "not connected",
		},
		{
			name:    "connected with automation",
			mutate:  func(m *hubModel) { m.integrationsRepos = connectedReposState() },
			wantSub: "PR automation available",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newHubModel("dev", false)
			tc.mutate(&m)
			got := renderGithubStatusBadge(m)
			if !strings.Contains(got, tc.wantSub) {
				t.Fatalf("renderGithubStatusBadge() = %q, want substring %q", got, tc.wantSub)
			}
		})
	}
}

func TestHandleIntegrationsKeyCursorNav(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewIntegrations

	model, _ := handleIntegrationsKey(m, keyMsg("down"))
	m = asHub(t, model)
	if m.integrationsCursor != 1 {
		t.Fatalf("after down, cursor = %d, want 1", m.integrationsCursor)
	}

	model, _ = handleIntegrationsKey(m, keyMsg("up"))
	m = asHub(t, model)
	if m.integrationsCursor != 0 {
		t.Fatalf("after up, cursor = %d, want 0", m.integrationsCursor)
	}

	// Up at the top stays at 0.
	model, _ = handleIntegrationsKey(m, keyMsg("up"))
	m = asHub(t, model)
	if m.integrationsCursor != 0 {
		t.Fatalf("up at top, cursor = %d, want 0", m.integrationsCursor)
	}
}

func TestRenderIntegrationsIncludesCanonicalPublishAction(t *testing.T) {
	m := newHubModel("dev", false)
	view := renderIntegrations(m)
	if !strings.Contains(view, "Publish config") || !strings.Contains(view, "complete project configuration") {
		t.Fatalf("renderIntegrations() missing canonical publish action: %q", view)
	}
	if integrationActionHotkey("publish") != "p" {
		t.Fatalf("publish hotkey = %q, want p", integrationActionHotkey("publish"))
	}
}

func TestStartProjectConfigurationPublishSetsBusyState(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("test-key", "http://127.0.0.1:0")

	model, cmd := m.startProjectConfigurationPublish()
	m = asHub(t, model)
	if !m.integrationsPublishing {
		t.Fatal("startProjectConfigurationPublish should set integrationsPublishing")
	}
	if cmd == nil {
		t.Fatal("startProjectConfigurationPublish should return a command")
	}
	if !strings.Contains(m.integrationsStatus, "complete project configuration") {
		t.Fatalf("status = %q", m.integrationsStatus)
	}
}

func TestUpdateIntegrationsPublishDone(t *testing.T) {
	for _, test := range []struct {
		name    string
		message IntegrationsPublishDoneMsg
		want    string
		wantErr bool
	}{
		{
			name: "applied",
			message: IntegrationsPublishDoneMsg{
				Outcome: api.ProjectConfigurationReplaceResponseOutcomeApplied,
			},
			want: "Published the complete project configuration.",
		},
		{
			name: "unchanged",
			message: IntegrationsPublishDoneMsg{
				Outcome: api.ProjectConfigurationReplaceResponseOutcomeUnchanged,
			},
			want: "Revyl already has this project configuration.",
		},
		{
			name:    "error",
			message: IntegrationsPublishDoneMsg{Err: errStub("conflict")},
			want:    "Publish failed: conflict",
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newHubModel("dev", false)
			m.integrationsPublishing = true
			model, _ := updateIntegrationsPublishDone(m, test.message)
			m = asHub(t, model)
			if m.integrationsPublishing {
				t.Fatal("publication result should clear integrationsPublishing")
			}
			if m.integrationsStatus != test.want || m.integrationsStatusErr != test.wantErr {
				t.Fatalf("status = %q, error = %t", m.integrationsStatus, m.integrationsStatusErr)
			}
		})
	}
}

func TestUpdateIntegrationsPublishDoneExplainsRemovedProject(t *testing.T) {
	m := newHubModel("dev", false)
	m.integrationsPublishing = true
	model, _ := updateIntegrationsPublishDone(m, IntegrationsPublishDoneMsg{
		Err: &projectpublication.Error{Code: "project_removed"},
	})
	m = asHub(t, model)
	for _, want := range []string{
		"was deleted",
		"local-only commands",
		"GitHub settings",
		"revyl -C <replacement-root> config pull",
	} {
		if !strings.Contains(m.integrationsStatus, want) {
			t.Fatalf("status = %q, want %q", m.integrationsStatus, want)
		}
	}
	if !m.integrationsStatusErr || m.integrationsPublishing {
		t.Fatalf("error = %t, publishing = %t", m.integrationsStatusErr, m.integrationsPublishing)
	}
}

func TestHandleIntegrationsKeyEscReturnsToDashboard(t *testing.T) {
	m := newHubModel("dev", false)
	m.currentView = viewIntegrations
	m.integrationsConnecting = true
	startSeq := m.integrationsPollSeq

	model, _ := handleIntegrationsKey(m, keyMsg("esc"))
	m = asHub(t, model)
	if m.currentView != viewDashboard {
		t.Fatalf("esc currentView = %v, want viewDashboard", m.currentView)
	}
	if m.integrationsConnecting {
		t.Fatalf("esc should stop connecting")
	}
	if m.integrationsPollSeq == startSeq {
		t.Fatalf("esc should invalidate the poll loop (bump pollSeq)")
	}
}

func TestStartGithubConnectSetsConnecting(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("test-key", "http://127.0.0.1:0")

	model, cmd := m.startGithubConnect()
	m = asHub(t, model)
	if !m.integrationsConnecting {
		t.Fatalf("startGithubConnect should set integrationsConnecting = true")
	}
	if cmd == nil {
		t.Fatalf("startGithubConnect should return a command")
	}
}

func TestStartGithubConnectAlreadyConnectedNoOp(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("test-key", "http://127.0.0.1:0")
	m.integrationsRepos = connectedReposState()

	model, _ := m.startGithubConnect()
	m = asHub(t, model)
	if m.integrationsConnecting {
		t.Fatalf("already-connected connect should not start connecting")
	}
	if !strings.Contains(m.integrationsStatus, "already connected") {
		t.Fatalf("status = %q, want 'already connected'", m.integrationsStatus)
	}
}

func TestUpdateIntegrationsConnectCheckStaleSeqIgnored(t *testing.T) {
	m := newHubModel("dev", false)
	m.integrationsConnecting = true
	m.integrationsPollSeq = 5

	model, _ := updateIntegrationsConnectCheck(m, IntegrationsConnectCheckMsg{
		Repos: connectedReposState(),
		Seq:   4, // stale
	})
	m = asHub(t, model)
	if !m.integrationsConnecting {
		t.Fatalf("stale connect check should be ignored (still connecting)")
	}
}

func TestUpdateIntegrationsConnectCheckBecomesActive(t *testing.T) {
	m := newHubModel("dev", false)
	m.integrationsConnecting = true
	m.integrationsPollSeq = 2

	model, _ := updateIntegrationsConnectCheck(m, IntegrationsConnectCheckMsg{
		Repos: connectedReposState(),
		Seq:   2,
	})
	m = asHub(t, model)
	if m.integrationsConnecting {
		t.Fatalf("active install should clear connecting")
	}
	if !m.integrationsRepos.IsConnected() {
		t.Fatalf("repos should be marked connected")
	}
	if m.integrationsStatusErr {
		t.Fatalf("connected status should not be an error")
	}
}

func TestUpdateIntegrationsStatusError(t *testing.T) {
	m := newHubModel("dev", false)
	m.integrationsLoading = true

	model, _ := updateIntegrationsStatus(m, IntegrationsStatusMsg{Err: errStub("boom")})
	m = asHub(t, model)
	if m.integrationsLoading {
		t.Fatalf("status result should clear loading")
	}
	if !m.integrationsStatusErr {
		t.Fatalf("status error should set statusErr")
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }
