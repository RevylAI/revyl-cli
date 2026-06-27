package tui

import (
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

func connectedReposState(enabled bool) *api.GithubRepositoriesResponse {
	return &api.GithubRepositoriesResponse{
		Repositories: []api.GithubOrgRepository{
			{Owner: "revyl", Repo: "app", InstallationID: 1},
		},
		Installation:             &api.GithubOrgInstallation{InstallationID: 1, Status: "active"},
		HasAccess:                true,
		GithubIntegrationEnabled: enabled,
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
			name:    "connected no automation",
			mutate:  func(m *hubModel) { m.integrationsRepos = connectedReposState(false) },
			wantSub: "connected",
		},
		{
			name:    "connected with automation",
			mutate:  func(m *hubModel) { m.integrationsRepos = connectedReposState(true) },
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

	model, cmd := m.startGithubConnect(false)
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
	m.integrationsRepos = connectedReposState(true)

	model, _ := m.startGithubConnect(false)
	m = asHub(t, model)
	if m.integrationsConnecting {
		t.Fatalf("already-connected connect should not start connecting")
	}
	if !strings.Contains(m.integrationsStatus, "already connected") {
		t.Fatalf("status = %q, want 'already connected'", m.integrationsStatus)
	}
}

func TestStartGithubPushSetsBusy(t *testing.T) {
	m := newHubModel("dev", false)
	m.client = api.NewClientWithBaseURL("test-key", "http://127.0.0.1:0")

	model, cmd := m.startGithubPush()
	m = asHub(t, model)
	if !m.integrationsBusy {
		t.Fatalf("startGithubPush should set integrationsBusy = true")
	}
	if cmd == nil {
		t.Fatalf("startGithubPush should return a command")
	}
}

func TestUpdateIntegrationsConnectCheckStaleSeqIgnored(t *testing.T) {
	m := newHubModel("dev", false)
	m.integrationsConnecting = true
	m.integrationsPollSeq = 5

	model, _ := updateIntegrationsConnectCheck(m, IntegrationsConnectCheckMsg{
		Repos: connectedReposState(true),
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
		Repos: connectedReposState(true),
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

func TestUpdateIntegrationsPushDoneNotConnected(t *testing.T) {
	m := newHubModel("dev", false)
	m.integrationsBusy = true

	model, _ := updateIntegrationsPushDone(m, IntegrationsPushDoneMsg{
		Err: &api.APIError{StatusCode: http.StatusForbidden},
	})
	m = asHub(t, model)
	if m.integrationsBusy {
		t.Fatalf("push done should clear busy")
	}
	if !m.integrationsStatusErr {
		t.Fatalf("push failure should set statusErr")
	}
	if !strings.Contains(m.integrationsStatus, "connect") {
		t.Fatalf("not-connected push should hint at connecting, got %q", m.integrationsStatus)
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
