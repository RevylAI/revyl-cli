package api

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

type DeviceWebViewport struct {
	Width             *int     `json:"width,omitempty"`
	Height            *int     `json:"height,omitempty"`
	DeviceScaleFactor *float64 `json:"device_scale_factor,omitempty"`
}

// DeviceWebSignIn names the org secret launch variable that signs the
// session's sign-in code. It never carries the secret value.
type DeviceWebSignIn struct {
	Path       string `json:"path"`
	SecretName string `json:"secret_name"`
}

type DeviceWebOptions struct {
	SecretEnvVars map[string]string  `json:"secret_env_vars,omitempty"`
	Viewport      *DeviceWebViewport `json:"viewport,omitempty"`
	SignIn        *DeviceWebSignIn   `json:"sign_in,omitempty"`
}

type ExternalURLBuildRequest struct {
	BranchID   string                 `json:"branch_id,omitempty"`
	BranchName string                 `json:"branch_name,omitempty"`
	URL        string                 `json:"url"`
	CommitSHA  string                 `json:"commit_sha,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type WebBuild struct {
	AutoGitBranchEnabled *bool        `json:"auto_git_branch_enabled,omitempty"`
	ID                   string       `json:"id"`
	AppID                string       `json:"app_id"`
	ArtifactKind         string       `json:"artifact_kind"`
	Version              string       `json:"version,omitempty"`
	WasReused            bool         `json:"was_reused,omitempty"`
	BranchID             string       `json:"branch_id,omitempty"`
	BranchName           string       `json:"branch_name,omitempty"`
	BranchSource         BranchSource `json:"branch_source,omitempty"`
	GitBranchName        string       `json:"git_branch_name,omitempty"`
}

type ExternalURLBuild = WebBuild

func (c *Client) CreateExternalURLBuild(ctx context.Context, appID string, request *ExternalURLBuildRequest) (*ExternalURLBuild, error) {
	resp, err := c.doRequestOnce(ctx, "POST", fmt.Sprintf("/api/v1/apps/%s/builds/external", url.PathEscape(appID)), request)
	if err != nil {
		return nil, err
	}
	var result ExternalURLBuild
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetExternalURLBuild(ctx context.Context, buildID string) (*ExternalURLBuild, error) {
	return c.GetWebBuild(ctx, buildID)
}

func (c *Client) GetWebBuild(ctx context.Context, buildID string) (*WebBuild, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/builds/%s", url.PathEscape(buildID)), nil)
	if err != nil {
		return nil, err
	}
	var result WebBuild
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetLatestWebBuild resolves the newest URL or bundle build using branch policy.
func (c *Client) GetLatestWebBuild(ctx context.Context, appID string, selection BuildSelection) (*WebBuild, error) {
	if selection.Mode == "" {
		selection.Mode = BuildSelectionModeLatest
	}
	if selection.Mode != BuildSelectionModeLatest && selection.Mode != BuildSelectionModeLatestAllBranches || selection.BuildID != "" {
		return nil, fmt.Errorf("latest Web build requires latest or latest_all_branches selection")
	}
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/apps/resolve?include_download_url=false", buildResolutionRequest{
		AppID: appID, BranchID: selection.BranchID, BranchName: selection.BranchName,
		GitBranchName: gitBranchNameFor(selection.Mode, selection.GitBranchName), BuildSelection: selection.Mode,
	})
	if err != nil {
		return nil, err
	}
	var result BuildResolutionResponse
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.ResolutionStatus == nil {
		return nil, fmt.Errorf("latest Web build response omitted a resolution status")
	}
	switch *result.ResolutionStatus {
	case BuildResolutionResponseResolutionStatusBranchHasNoBuilds, BuildResolutionResponseResolutionStatusNoCompatibleBuild:
		return nil, nil
	case BuildResolutionResponseResolutionStatusResolved:
	default:
		return nil, fmt.Errorf("latest Web build response contained an unknown resolution status")
	}
	if result.BuildVarId.String() != appID || !strings.EqualFold(result.Platform, "web") || result.VersionId == nil || *result.VersionId == uuid.Nil || result.ArtifactKind == nil || !IsWebBuildArtifactKind(*result.ArtifactKind) {
		return nil, fmt.Errorf("latest Web build response did not contain a URL or bundle build on the selected app")
	}
	build := &WebBuild{ID: result.VersionId.String(), AppID: appID, ArtifactKind: *result.ArtifactKind}
	build.AutoGitBranchEnabled = result.AutoGitBranchEnabled
	if result.BranchId != nil {
		build.BranchID = result.BranchId.String()
	}
	if result.BranchName != nil {
		build.BranchName = *result.BranchName
	}
	if result.Version != nil {
		build.Version = *result.Version
	}
	if result.BranchSource != nil {
		build.BranchSource = *result.BranchSource
	}
	if result.GitBranchName != nil {
		build.GitBranchName = *result.GitBranchName
	}
	return build, nil
}

func IsWebBuildArtifactKind(kind string) bool {
	return kind == "web_external" || kind == "web_bundle"
}

func (c *Client) GetLatestExternalURLBuild(ctx context.Context, appID string) (*WebBuild, error) {
	build, err := c.GetLatestWebBuild(ctx, appID, BuildSelection{Mode: BuildSelectionModeLatest})
	if err != nil || build == nil {
		return nil, err
	}
	if build.ArtifactKind != "web_external" {
		return nil, fmt.Errorf("latest URL build response did not contain a URL build")
	}
	return build, nil
}
