package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/auth"
)

type SetupProjectState string

type SetupEnvironment string

const (
	setupEnvironmentLocal SetupEnvironment = "local"
	setupEnvironmentCloud SetupEnvironment = "cloud"

	projectStateInitialized    SetupProjectState = "initialized"
	projectStateNotInitialized SetupProjectState = "not_initialized"
	projectStateAmbiguous      SetupProjectState = "ambiguous"
	projectStateInvalid        SetupProjectState = "invalid"
	projectStateLegacy         SetupProjectState = "legacy_config"
	projectStateOutsideGit     SetupProjectState = "outside_git"
	projectStateNested         SetupProjectState = "nested"
)

type SetupStatusInput struct{}

// SetupEnvironmentSignals reports the secret-free markers behind the environment
// classification, so a Cloud run can be diagnosed from one tool call without
// asking the operator to echo variables that may hold a credential.
type SetupEnvironmentSignals struct {
	CloudContextPresent bool `json:"cloud_context_present"`
	// CloudContextInvalid reports that a Cloud context file could not be loaded or validated.
	CloudContextInvalid bool                        `json:"cloud_context_invalid"`
	APIKeyEnvironment   auth.APIKeyEnvironmentState `json:"api_key_environment"`
}

type SetupStatusOutput struct {
	Ready            bool                    `json:"ready"`
	AuthState        SetupAuthState          `json:"auth_state"`
	ProjectState     SetupProjectState       `json:"project_state"`
	Environment      SetupEnvironment        `json:"environment"`
	Signals          SetupEnvironmentSignals `json:"signals"`
	ProjectDirectory string                  `json:"project_directory,omitempty"`
	Remediation      *Remediation            `json:"remediation,omitempty"`
}

func (s *Server) registerSetupStatusTool() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "setup_status",
		Description: "Report Revyl authentication, project setup, and exact remediation commands when needed.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Check Revyl Setup",
			ReadOnlyHint: true,
		},
	}, s.handleSetupStatus)
}

func (s *Server) handleSetupStatus(
	_ context.Context,
	_ *mcp.CallToolRequest,
	_ SetupStatusInput,
) (*mcp.CallToolResult, SetupStatusOutput, error) {
	authentication := s.resolveAndApplyDevAuthentication()
	project := resolveSetupProjectStateForMode(s.workDir, s.devMode)
	output := SetupStatusOutput{
		Ready:            authentication.State == authenticationStateAuthenticated && projectStateHasConfig(project.State),
		AuthState:        authentication.State,
		ProjectState:     project.State,
		Environment:      setupEnvironment(authentication.HeadlessCloud),
		Signals:          authentication.Signals,
		ProjectDirectory: project.ProjectDirectory,
	}

	if authentication.State != authenticationStateAuthenticated {
		output.Remediation = authenticationRemediation(authentication.State)
		return nil, output, nil
	}
	output.Remediation = project.Remediation
	return nil, output, nil
}

func setupEnvironment(cloud bool) SetupEnvironment {
	if cloud {
		return setupEnvironmentCloud
	}
	return setupEnvironmentLocal
}
