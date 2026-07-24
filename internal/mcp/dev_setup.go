package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SetupProjectState identifies the current Revyl project setup state.
type SetupProjectState string

// SetupEnvironment identifies whether setup runs locally or in a headless Cloud runtime.
type SetupEnvironment string

const (
	setupEnvironmentLocal SetupEnvironment = "local"
	setupEnvironmentCloud SetupEnvironment = "cloud"

	projectStateInitialized    SetupProjectState = "initialized"
	projectStateNotInitialized SetupProjectState = "not_initialized"
	projectStateAmbiguous      SetupProjectState = "ambiguous"
	projectStateInvalid        SetupProjectState = "invalid"
)

// SetupStatusInput is the empty input contract for setup_status.
type SetupStatusInput struct{}

// SetupStatusOutput reports whether authentication and project setup are ready.
type SetupStatusOutput struct {
	Ready            bool              `json:"ready"`
	AuthState        SetupAuthState    `json:"auth_state"`
	ProjectState     SetupProjectState `json:"project_state"`
	Environment      SetupEnvironment  `json:"environment"`
	ProjectDirectory string            `json:"project_directory,omitempty"`
	Remediation      *Remediation      `json:"remediation,omitempty"`
}

// registerSetupStatusTool registers the read-only deferred setup inspection tool.
func (s *Server) registerSetupStatusTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "setup_status",
		Description: "Report Revyl authentication, project setup, and one exact remediation action when needed.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Check Revyl Setup",
			ReadOnlyHint: true,
		},
	}, s.handleSetupStatus)
}

// handleSetupStatus re-resolves current credentials and inspects project initialization.
//
// Parameters:
//   - ctx: MCP request context.
//   - req: MCP request metadata.
//   - input: Empty setup-status input.
//
// Returns:
//   - *mcp.CallToolResult: Optional MCP result metadata.
//   - SetupStatusOutput: Typed setup state and optional remediation.
//   - error: Transport-level handler failure.
func (s *Server) handleSetupStatus(
	_ context.Context,
	_ *mcp.CallToolRequest,
	_ SetupStatusInput,
) (*mcp.CallToolResult, SetupStatusOutput, error) {
	authentication := s.resolveAndApplyDevAuthentication()
	project := resolveSetupProjectState(s.workDir)
	output := SetupStatusOutput{
		Ready:            authentication.State == authenticationStateAuthenticated && project.State == projectStateInitialized,
		AuthState:        authentication.State,
		ProjectState:     project.State,
		Environment:      setupEnvironment(authentication.HeadlessCloud),
		ProjectDirectory: project.ProjectDirectory,
	}

	if authentication.State != authenticationStateAuthenticated {
		output.Remediation = authenticationRemediation(authentication.State)
		return nil, output, nil
	}
	output.Remediation = project.Remediation
	return nil, output, nil
}

// setupEnvironment returns the stable setup-status environment name.
//
// Parameters:
//   - cloud: Whether the bootstrap-established headless Cloud context is present.
//
// Returns:
//   - SetupEnvironment: "cloud" or "local".
func setupEnvironment(cloud bool) SetupEnvironment {
	if cloud {
		return setupEnvironmentCloud
	}
	return setupEnvironmentLocal
}
