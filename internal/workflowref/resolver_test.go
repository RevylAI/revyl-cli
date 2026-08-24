package workflowref

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

type mockWorkflowClient struct {
	byID      map[string]*api.Workflow
	workflows []api.SimpleWorkflow
	getCalls  int
	listCalls int
	listErr   error
	pageSize  int
	maxItems  int
}

func (m *mockWorkflowClient) GetWorkflow(_ context.Context, workflowID string) (*api.Workflow, error) {
	m.getCalls++
	if workflow, ok := m.byID[workflowID]; ok {
		return workflow, nil
	}
	return nil, &api.APIError{StatusCode: http.StatusNotFound, Message: "not found"}
}

func (m *mockWorkflowClient) ListAllWorkflows(_ context.Context, _ int) ([]api.SimpleWorkflow, error) {
	m.listCalls++
	return m.workflows, nil
}

func (m *mockWorkflowClient) ListWorkflowsBounded(_ context.Context, pageSize, maxWorkflows int) ([]api.SimpleWorkflow, error) {
	m.listCalls++
	m.pageSize = pageSize
	m.maxItems = maxWorkflows
	return m.workflows, m.listErr
}

func TestResolveValidWorkflowUUID(t *testing.T) {
	client := &mockWorkflowClient{
		byID: map[string]*api.Workflow{
			"027b91de-4a21-4bca-acfe-32db2a628f51": {
				ID:   "027b91de-4a21-4bca-acfe-32db2a628f51",
				Name: "nightly",
			},
		},
	}

	resolved, err := Resolve(context.Background(), client, "027B91DE-4A21-4BCA-ACFE-32DB2A628F51")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ID != "027b91de-4a21-4bca-acfe-32db2a628f51" {
		t.Fatalf("ID = %q", resolved.ID)
	}
	if resolved.Name != "nightly" {
		t.Fatalf("Name = %q", resolved.Name)
	}
	if client.listCalls != 0 {
		t.Fatalf("ListAllWorkflows called %d times, want 0", client.listCalls)
	}
}

func TestResolveInvalidUUIDShapedWorkflowName(t *testing.T) {
	name := "zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz"
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{{ID: "wf-1", Name: name}},
	}

	resolved, err := Resolve(context.Background(), client, name)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ID != "wf-1" {
		t.Fatalf("ID = %q, want wf-1", resolved.ID)
	}
	if client.getCalls != 0 {
		t.Fatalf("GetWorkflow called %d times, want 0", client.getCalls)
	}
}

func TestResolveValidUUIDShapedWorkflowNameWhenIDMissing(t *testing.T) {
	name := "11111111-1111-1111-1111-111111111111"
	client := &mockWorkflowClient{
		byID:      map[string]*api.Workflow{},
		workflows: []api.SimpleWorkflow{{ID: "wf-by-name", Name: name}},
	}

	resolved, err := Resolve(context.Background(), client, name)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.ID != "wf-by-name" {
		t.Fatalf("ID = %q, want wf-by-name", resolved.ID)
	}
	if client.getCalls != 1 || client.listCalls != 1 {
		t.Fatalf("calls get=%d list=%d, want get=1 list=1", client.getCalls, client.listCalls)
	}
}

func TestResolveDuplicateWorkflowNames(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{
			{ID: "wf-b", Name: "nightly"},
			{ID: "wf-a", Name: "nightly"},
		},
	}

	_, err := Resolve(context.Background(), client, "nightly")
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
	if !strings.Contains(err.Error(), "multiple workflows named") ||
		!strings.Contains(err.Error(), "wf-a") ||
		!strings.Contains(err.Error(), "wf-b") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveBadUUIDTypo(t *testing.T) {
	client := &mockWorkflowClient{}

	_, err := Resolve(context.Background(), client, "027b91de-4a21-4bca-acfe-32db2a628f5z")
	if err == nil {
		t.Fatal("expected invalid UUID-shaped error")
	}
	if !strings.Contains(err.Error(), "not a valid UUID") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveExactNamesEnumeratesCatalogOnce(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{
			{ID: "11111111-1111-4111-8111-111111111111", Name: "nightly"},
			{ID: "22222222-2222-4222-8222-222222222222", Name: "smoke"},
		},
	}

	resolved, err := ResolveExactNames(context.Background(), client, []string{"smoke", "nightly", "smoke"})
	if err != nil {
		t.Fatalf("ResolveExactNames() error = %v", err)
	}
	if client.listCalls != 1 {
		t.Fatalf("catalog calls = %d, want 1", client.listCalls)
	}
	if client.pageSize != exactNameCatalogPageSize || client.maxItems != exactNameCatalogMaxWorkflows {
		t.Fatalf("catalog bounds = (%d, %d), want (%d, %d)", client.pageSize, client.maxItems, exactNameCatalogPageSize, exactNameCatalogMaxWorkflows)
	}
	if got := resolved["smoke"].ID; got != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("smoke ID = %q", got)
	}
	if got := resolved["nightly"].ID; got != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("nightly ID = %q", got)
	}
}

func TestResolveExactNamesBestEffortPreservesValidSiblings(t *testing.T) {
	client := &mockWorkflowClient{workflows: []api.SimpleWorkflow{
		{ID: "11111111-1111-4111-8111-111111111111", Name: "smoke"},
	}}
	resolved, issues, err := ResolveExactNamesBestEffort(
		context.Background(),
		client,
		[]string{"smoke", "missing"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.listCalls != 1 || resolved["smoke"].ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("catalog calls = %d resolved = %#v", client.listCalls, resolved)
	}
	if issues["missing"] == nil || issues["missing"].Kind != ExactNameNotFound {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestResolveExactNamesIsCaseSensitive(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{{ID: "11111111-1111-4111-8111-111111111111", Name: "Smoke"}},
	}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameNotFound {
		t.Fatalf("error = %v (%T), want exact-name not-found error", err, err)
	}
}

func TestResolveExactNamesTrimsCatalogNames(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{{ID: "11111111-1111-4111-8111-111111111111", Name: "  smoke  "}},
	}

	resolved, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	if err != nil {
		t.Fatalf("ResolveExactNames() error = %v", err)
	}
	if got := resolved["smoke"]; got.ID != "11111111-1111-4111-8111-111111111111" || got.Name != "  smoke  " {
		t.Fatalf("resolution = %#v", got)
	}
}

func TestResolveExactNamesDetectsDuplicatesAfterTrimmingCatalogNames(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{
			{ID: "22222222-2222-4222-8222-222222222222", Name: " smoke "},
			{ID: "11111111-1111-4111-8111-111111111111", Name: "smoke"},
		},
	}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameAmbiguous {
		t.Fatalf("error = %v (%T), want ambiguous exact-name error", err, err)
	}
	if got := strings.Join(resolutionErr.WorkflowIDs, ","); got != "11111111-1111-4111-8111-111111111111,22222222-2222-4222-8222-222222222222" {
		t.Fatalf("sorted workflow IDs = %q", got)
	}
}

func TestResolveExactNamesDetectsDuplicatesAcrossCatalog(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{
			{ID: "22222222-2222-4222-8222-222222222222", Name: "smoke"},
			{ID: "11111111-1111-4111-8111-111111111111", Name: "smoke"},
		},
	}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameAmbiguous {
		t.Fatalf("error = %v (%T), want ambiguous exact-name error", err, err)
	}
	if got := strings.Join(resolutionErr.WorkflowIDs, ","); got != "11111111-1111-4111-8111-111111111111,22222222-2222-4222-8222-222222222222" {
		t.Fatalf("sorted workflow IDs = %q", got)
	}
}

func TestResolveExactNamesRejectsInvalidServerID(t *testing.T) {
	client := &mockWorkflowClient{
		workflows: []api.SimpleWorkflow{{ID: "not-a-uuid", Name: "smoke"}},
	}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameInvalidWorkflowID {
		t.Fatalf("error = %v (%T), want invalid-workflow-ID error", err, err)
	}
}

func TestResolveExactNamesPropagatesCatalogFailure(t *testing.T) {
	client := &mockWorkflowClient{listErr: context.Canceled}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameCatalogUnavailable {
		t.Fatalf("error = %v (%T), want catalog-unavailable error", err, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want wrapped context cancellation", err)
	}
}

func TestResolveExactNamesClassifiesCatalogLimit(t *testing.T) {
	client := &mockWorkflowClient{
		listErr: &api.WorkflowCatalogLimitError{MaxWorkflows: exactNameCatalogMaxWorkflows},
	}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke"})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameCatalogLimit {
		t.Fatalf("error = %v (%T), want catalog-limit error", err, err)
	}
}

func TestResolveExactNamesRejectsBlankWithoutCatalogCall(t *testing.T) {
	client := &mockWorkflowClient{}

	_, err := ResolveExactNames(context.Background(), client, []string{"smoke", " "})
	var resolutionErr *ExactNameResolutionError
	if !errors.As(err, &resolutionErr) || resolutionErr.Kind != ExactNameInvalidInput {
		t.Fatalf("error = %v (%T), want invalid-input error", err, err)
	}
	if client.listCalls != 0 {
		t.Fatalf("catalog calls = %d, want 0", client.listCalls)
	}
}
