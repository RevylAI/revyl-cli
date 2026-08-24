// Package projectpublication resolves and publishes the complete canonical
// project configuration without owning any command- or TUI-specific output.
package projectpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/gitremote"
)

// Client is the narrow API surface required for an observed-state publication.
type Client interface {
	ReadProjectConfiguration(context.Context, string, api.ProjectConfigurationReadRequest) (*api.ProjectConfigurationReadResponse, error)
	ReplaceProjectConfiguration(context.Context, string, api.ProjectConfigurationReplaceRequest) (*api.ProjectConfigurationReplaceResponse, error)
}

// Candidate is one complete canonical project configuration, its verified
// repository binding, and the caller's explicit publication intent.
type Candidate struct {
	ProjectID                 string
	Locator                   api.ProjectConfigurationRepositoryLocator
	Configuration             api.AuthoredRevylConfig
	AllowGitAuthorityOverride bool
}

type Error struct {
	Code  string
	cause error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.cause }

// ResolveCandidate resolves the nearest canonical config within the active Git
// worktree and binds it to that worktree's GitHub repository.
func ResolveCandidate(cwd string) (*Candidate, error) {
	local, err := config.ResolveProjectContext(cwd, "")
	if err != nil {
		return nil, err
	}
	namespace, repositoryName, err := gitremote.ResolveSlug(local.WorktreeRoot, "")
	if err != nil {
		return nil, err
	}
	authored, err := authoredConfigForAPI(*local.Authored)
	if err != nil {
		return nil, err
	}
	return &Candidate{
		ProjectID: local.Authored.Project.ID,
		Locator: api.ProjectConfigurationRepositoryLocator{
			Provider:                      "github",
			Namespace:                     namespace,
			RepositoryName:                repositoryName,
			RepositoryRelativeProjectRoot: local.RepositoryRelativeProjectRoot,
		},
		Configuration: authored,
	}, nil
}

// Publish performs one observed-state, whole-aggregate replacement. Validation
// and compilation remain part of the backend's atomic replacement boundary.
func Publish(
	ctx context.Context,
	client Client,
	candidate Candidate,
) (*api.ProjectConfigurationReplaceResponse, error) {
	current, err := client.ReadProjectConfiguration(
		ctx,
		candidate.ProjectID,
		api.ProjectConfigurationReadRequest{Locator: candidate.Locator},
	)
	if err != nil {
		return nil, actionableReadError(err)
	}
	if current.State == api.ProjectConfigurationReadResponseStatePresent {
		if current.Resource == nil {
			return nil, fmt.Errorf("server returned present configuration without a resource")
		}
		if current.Resource.Authority == api.ConfigurationAuthorityGitDefaultBranch &&
			!candidate.AllowGitAuthorityOverride {
			return nil, gitAuthorityError()
		}
	}
	precondition, err := projectConfigurationPrecondition(current)
	if err != nil {
		return nil, err
	}
	request := api.ProjectConfigurationReplaceRequest{
		Locator:       candidate.Locator,
		Configuration: candidate.Configuration,
		Precondition:  precondition,
	}
	if candidate.AllowGitAuthorityOverride {
		force := true
		request.Force = &force
	}
	result, err := client.ReplaceProjectConfiguration(
		ctx,
		candidate.ProjectID,
		request,
	)
	if err != nil {
		return nil, actionableWriteError(err)
	}
	return result, nil
}

func authoredConfigForAPI(authored config.AuthoredConfig) (api.AuthoredRevylConfig, error) {
	payload, err := json.Marshal(authored)
	if err != nil {
		return api.AuthoredRevylConfig{}, fmt.Errorf("encode project configuration: %w", err)
	}
	var converted api.AuthoredRevylConfig
	if err := json.Unmarshal(payload, &converted); err != nil {
		return api.AuthoredRevylConfig{}, fmt.Errorf("encode project configuration: %w", err)
	}
	return converted, nil
}

func projectConfigurationPrecondition(
	current *api.ProjectConfigurationReadResponse,
) (api.ProjectConfigurationReplaceRequest_Precondition, error) {
	var precondition api.ProjectConfigurationReplaceRequest_Precondition
	if current == nil {
		return precondition, fmt.Errorf("server returned no project configuration state")
	}
	if current.State == api.ProjectConfigurationReadResponseStateAbsent {
		err := precondition.FromProjectConfigurationAbsentPrecondition(
			api.ProjectConfigurationAbsentPrecondition{State: "absent"},
		)
		return precondition, err
	}
	if current.State != api.ProjectConfigurationReadResponseStatePresent || current.Resource == nil {
		return precondition, fmt.Errorf("server returned invalid project configuration state")
	}
	err := precondition.FromProjectConfigurationPresentPrecondition(
		api.ProjectConfigurationPresentPrecondition{
			State:                    "present",
			ProjectConfigurationHash: current.Resource.ProjectConfigurationHash,
		},
	)
	return precondition, err
}

func actionableReadError(err error) error {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case "project_configuration_inaccessible":
		return &Error{Code: "project_configuration_inaccessible", cause: err}
	case "project_removed":
		return &Error{Code: "project_removed", cause: err}
	case "repository_provider_unavailable":
		return &Error{Code: "repository_provider_unavailable", cause: err}
	default:
		return err
	}
}

func actionableWriteError(err error) error {
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.Code {
	case "project_removed":
		return &Error{Code: "project_removed", cause: err}
	case "git_authority_rejects_manual_write":
		return gitAuthorityError()
	case "observed_configuration_changed", "observed_configuration_now_present", "observed_configuration_no_longer_present":
		return &Error{Code: "observed_configuration_changed", cause: err}
	default:
		return err
	}
}

func gitAuthorityError() error {
	return &Error{Code: "git_authority_rejects_manual_write"}
}
