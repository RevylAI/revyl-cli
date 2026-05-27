package workflowref

import (
	"context"
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
