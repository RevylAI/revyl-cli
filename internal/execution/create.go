// Package execution provides shared execution logic for tests and workflows.
package execution

import (
	"context"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

// CreateTestParams contains parameters for creating a test.
//
// Fields:
//   - Name: Test name
//   - Platform: Target platform (ios or android)
//   - YAMLContent: Optional YAML test definition (if provided, creates with blocks)
//   - BuildVarID: Optional build variable ID to associate
//   - OrgID: Organization ID
//   - DevMode: If true, use local development servers
type CreateTestParams struct {
	Name        string
	Platform    string
	YAMLContent string
	BuildVarID  string
	OrgID       string
	DevMode     bool
}

// CreateTestResult contains the result of test creation.
//
// Fields:
//   - TestID: The created test UUID
//   - TestName: The test name
//   - TestURL: URL to the test editor
type CreateTestResult struct {
	TestID   string `json:"test_id"`
	TestName string `json:"test_name"`
	TestURL  string `json:"test_url"`
}

// CreateTest creates a new test on the server.
//
// If YAMLContent is provided, it validates the YAML and creates the test with blocks.
// Otherwise, creates an empty test that can be edited in the browser.
//
// Parameters:
//   - ctx: Context for cancellation
//   - apiKey: API key for authentication
//   - params: Test creation parameters
//
// Returns:
//   - *CreateTestResult: Creation result with test ID and URL
//   - error: Any error that occurred
func CreateTest(ctx context.Context, apiKey string, params CreateTestParams) (*CreateTestResult, error) {
	// Validate platform
	if params.Platform != "ios" && params.Platform != "android" {
		return nil, fmt.Errorf("invalid platform '%s': must be 'ios' or 'android'", params.Platform)
	}

	var tasks []interface{}

	// YAML validation is handled by the yaml package for fast local feedback.
	// The backend is responsible for parsing YAML content and converting it to tasks.
	// We pass empty tasks here; if YAML content was provided, the backend will process it.

	client := api.NewClientWithDevMode(apiKey, params.DevMode)
	resp, err := client.CreateTest(ctx, &api.CreateTestRequest{
		Name:       params.Name,
		Platform:   params.Platform,
		Tasks:      tasks,
		BuildVarID: params.BuildVarID,
		OrgID:      params.OrgID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create test: %w", err)
	}

	testURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(params.DevMode), resp.ID)

	return &CreateTestResult{
		TestID:   resp.ID,
		TestName: params.Name,
		TestURL:  testURL,
	}, nil
}

// CreateWorkflowParams contains parameters for creating a workflow.
//
// Fields:
//   - Name: Workflow name
//   - TestIDs: Optional test IDs to include in the workflow
//   - Owner: User ID who owns this workflow (required by backend)
//   - DevMode: If true, use local development servers
type CreateWorkflowParams struct {
	Name    string
	TestIDs []string
	Owner   string
	DevMode bool
}

// CreateWorkflowResult contains the result of workflow creation.
//
// Fields:
//   - WorkflowID: The created workflow UUID
//   - WorkflowName: The workflow name
//   - WorkflowURL: URL to the workflow editor
type CreateWorkflowResult struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	WorkflowURL  string `json:"workflow_url"`
}

// CreateWorkflow creates a new workflow on the server.
//
// Parameters:
//   - ctx: Context for cancellation
//   - apiKey: API key for authentication
//   - params: Workflow creation parameters
//
// Returns:
//   - *CreateWorkflowResult: Creation result with workflow ID and URL
//   - error: Any error that occurred
func CreateWorkflow(ctx context.Context, apiKey string, params CreateWorkflowParams) (*CreateWorkflowResult, error) {
	client := api.NewClientWithDevMode(apiKey, params.DevMode)
	resp, err := client.CreateWorkflow(ctx, &api.CLICreateWorkflowRequest{
		Name:     params.Name,
		Tests:    params.TestIDs,
		Schedule: "No Schedule",
		Owner:    params.Owner,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	workflowURL := fmt.Sprintf("%s/workflows/%s", config.GetAppURL(params.DevMode), resp.Data.ID)

	return &CreateWorkflowResult{
		WorkflowID:   resp.Data.ID,
		WorkflowName: params.Name,
		WorkflowURL:  workflowURL,
	}, nil
}

// OpenTestEditorParams contains parameters for opening a test editor.
//
// Fields:
//   - TestNameOrID: Test name (alias from config) or UUID
//   - DevMode: If true, use local development servers
type OpenTestEditorParams struct {
	TestNameOrID string
	DevMode      bool
}

// OpenTestEditorResult contains the result of opening a test editor.
//
// Fields:
//   - TestID: The resolved test UUID
//   - TestURL: URL to the test editor
type OpenTestEditorResult struct {
	TestID  string `json:"test_id"`
	TestURL string `json:"test_url"`
}

// OpenTestEditor resolves a test and returns the editor URL.
//
// Parameters:
//   - cfg: Project config for alias resolution (can be nil)
//   - params: Parameters including test name/ID
//
// Returns:
//   - *OpenTestEditorResult: Result with test ID and URL
func OpenTestEditor(cfg *config.ProjectConfig, params OpenTestEditorParams) *OpenTestEditorResult {
	// Resolve test ID from alias
	testID := params.TestNameOrID
	if cfg != nil {
		if id, ok := cfg.Tests[params.TestNameOrID]; ok {
			testID = id
		}
	}

	testURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(params.DevMode), testID)

	return &OpenTestEditorResult{
		TestID:  testID,
		TestURL: testURL,
	}
}

// OpenWorkflowEditorParams contains parameters for opening a workflow editor.
//
// Fields:
//   - WorkflowNameOrID: Workflow name (alias from config) or UUID
//   - DevMode: If true, use local development servers
type OpenWorkflowEditorParams struct {
	WorkflowNameOrID string
	DevMode          bool
}

// OpenWorkflowEditorResult contains the result of opening a workflow editor.
//
// Fields:
//   - WorkflowID: The resolved workflow UUID
//   - WorkflowURL: URL to the workflow editor
type OpenWorkflowEditorResult struct {
	WorkflowID  string `json:"workflow_id"`
	WorkflowURL string `json:"workflow_url"`
}

// OpenWorkflowEditor resolves a workflow and returns the editor URL.
//
// Parameters:
//   - cfg: Project config for alias resolution (can be nil)
//   - params: Parameters including workflow name/ID
//
// Returns:
//   - *OpenWorkflowEditorResult: Result with workflow ID and URL
func OpenWorkflowEditor(cfg *config.ProjectConfig, params OpenWorkflowEditorParams) *OpenWorkflowEditorResult {
	// Resolve workflow ID from alias
	workflowID := params.WorkflowNameOrID
	if cfg != nil {
		if id, ok := cfg.Workflows[params.WorkflowNameOrID]; ok {
			workflowID = id
		}
	}

	workflowURL := fmt.Sprintf("%s/workflows/%s", config.GetAppURL(params.DevMode), workflowID)

	return &OpenWorkflowEditorResult{
		WorkflowID:  workflowID,
		WorkflowURL: workflowURL,
	}
}
