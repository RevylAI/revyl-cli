package api

import (
	"context"
	"fmt"
	"net/url"
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
	Persona    string `json:"persona"`
	SecretName string `json:"secret_name"`
}

type DeviceWebOptions struct {
	Viewport *DeviceWebViewport `json:"viewport,omitempty"`
	SignIn   *DeviceWebSignIn   `json:"sign_in,omitempty"`
}

type ExternalURLBuildRequest struct {
	BranchID   string                 `json:"branch_id,omitempty"`
	BranchName string                 `json:"branch_name,omitempty"`
	URL        string                 `json:"url"`
	CommitSHA  string                 `json:"commit_sha,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

type WebBuild struct {
	ID           string `json:"id"`
	AppID        string `json:"app_id"`
	ArtifactKind string `json:"artifact_kind"`
	Version      string `json:"version,omitempty"`
	WasReused    bool   `json:"was_reused,omitempty"`
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

// GetLatestWebBuild returns the newest URL or bundle build on the selected
// branch, or across every branch when neither branchID nor branchName is set.
func (c *Client) GetLatestWebBuild(ctx context.Context, appID, branchID, branchName string) (*BuildVersion, error) {
	query := url.Values{"include_jobs": {"false"}, "artifact_kinds": {"web_external", "web_bundle"}, "page": {"1"}, "page_size": {"1"}}
	if branchID != "" {
		query.Set("branch_id", branchID)
	}
	if branchName != "" {
		query.Set("branch_name", branchName)
	}
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/%s/builds?%s", url.PathEscape(appID), query.Encode()), nil)
	if err != nil {
		return nil, err
	}
	var result BuildVersionsPage
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		return nil, nil
	}
	if len(result.Items) != 1 || !IsWebBuildArtifactKind(result.Items[0].ArtifactKind) || result.Items[0].ID == "" {
		return nil, fmt.Errorf("latest Web build response did not contain a URL or bundle build")
	}
	return &result.Items[0], nil
}

func IsWebBuildArtifactKind(kind string) bool {
	return kind == "web_external" || kind == "web_bundle"
}

func (c *Client) GetLatestExternalURLBuild(ctx context.Context, appID string) (*BuildVersion, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/%s/builds?artifact_kind=web_external&page=1&page_size=1", url.PathEscape(appID)), nil)
	if err != nil {
		return nil, err
	}
	var result BuildVersionsPage
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if len(result.Items) == 0 {
		return nil, nil
	}
	if len(result.Items) != 1 || result.Items[0].ArtifactKind != "web_external" || result.Items[0].ID == "" {
		return nil, fmt.Errorf("latest URL build response did not contain a URL build")
	}
	return &result.Items[0], nil
}
