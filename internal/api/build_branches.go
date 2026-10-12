package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

var ErrBranchHasNoBuilds = errors.New("selected branch has no builds; upload a build to this branch first")
var ErrBranchHasNoCompatibleBuild = errors.New("selected branch has no compatible build; upload a compatible build or choose another branch")
var ErrBranchNotFound = errors.New("build branch not found")

const MaxContextBranchNameLength = 4096

type BuildSelection struct {
	ContextBranchName string
	BranchID          string
	BranchName        string
	Mode              BuildSelectionMode
	BuildID           string
}

func validateBranchSelector(branchID, branchName string) error {
	if branchID != "" && branchName != "" {
		return errors.New("provide either branch ID or branch name, not both")
	}
	if branchID != "" {
		if _, err := uuid.Parse(branchID); err != nil {
			return errors.New("branch ID must be a UUID")
		}
	}
	if branchName != "" {
		if strings.TrimSpace(branchName) != branchName || utf8.RuneCountInString(branchName) > 255 {
			return errors.New("branch name must be 1–255 characters without surrounding whitespace")
		}
		for _, r := range branchName {
			if unicode.IsControl(r) {
				return errors.New("branch name cannot contain control characters")
			}
		}
	}
	return nil
}

func validateContextBranchName(name string) error {
	if utf8.RuneCountInString(name) > MaxContextBranchNameLength {
		return errors.New("Git branch context exceeds 4096 characters; select a Revyl branch with --branch")
	}
	return nil
}

func (s BuildSelection) Validate() error {
	if err := validateContextBranchName(s.ContextBranchName); err != nil {
		return err
	}
	if err := validateBranchSelector(s.BranchID, s.BranchName); err != nil {
		return err
	}
	switch s.Mode {
	case "", BuildSelectionModeInherit, BuildSelectionModeLatest, BuildSelectionModePinned:
	default:
		return errors.New("build selection must be inherit, latest, or pinned")
	}
	if s.BuildID != "" {
		if _, err := uuid.Parse(s.BuildID); err != nil {
			return errors.New("build ID must be a UUID")
		}
		if s.Mode != "" && s.Mode != BuildSelectionModePinned {
			return errors.New("an exact build cannot be combined with inherit or latest selection")
		}
	} else if s.Mode == BuildSelectionModePinned {
		return errors.New("pinned selection requires a build ID")
	}
	return nil
}

type SavedBuildSelection struct {
	BranchID       *string            `json:"branch_id"`
	BuildSelection BuildSelectionMode `json:"build_selection"`
	PinnedBuildID  *string            `json:"pinned_build_id"`
}

func NewSavedBuildSelection(branchID, mode, pinnedID string) (*SavedBuildSelection, error) {
	s := &SavedBuildSelection{BuildSelection: BuildSelectionMode(mode)}
	if s.BuildSelection == "" {
		s.BuildSelection = BuildSelectionModeInherit
	}
	if branchID != "" {
		s.BranchID = &branchID
	}
	if pinnedID != "" {
		s.PinnedBuildID = &pinnedID
	}
	return s, s.Validate()
}

func (s *SavedBuildSelection) Validate() error {
	if s == nil {
		return nil
	}
	var branchID, buildID string
	if s.BranchID != nil {
		branchID = *s.BranchID
		if branchID == "" {
			return errors.New("saved branch ID cannot be empty")
		}
	}
	if s.PinnedBuildID != nil {
		buildID = *s.PinnedBuildID
		if buildID == "" {
			return errors.New("saved build ID cannot be empty")
		}
	}
	if s.BuildSelection == "" {
		return errors.New("saved build selection requires a mode")
	}
	if (s.BuildSelection == BuildSelectionModePinned) != (s.PinnedBuildID != nil) {
		return errors.New("saved pinned selection requires an exact build; other modes cannot pin a build")
	}
	if buildID != "" && branchID == "" {
		return errors.New("saved exact build requires its branch")
	}
	return (BuildSelection{BranchID: branchID, Mode: s.BuildSelection, BuildID: buildID}).Validate()
}

type BuildBranch struct {
	ID                   string  `json:"id"`
	AppID                string  `json:"app_id"`
	Name                 string  `json:"name"`
	PinnedBuildID        *string `json:"pinned_build_id"`
	PinnedBuildVersion   *string `json:"pinned_build_version"`
	PinnedBuildAvailable *bool   `json:"pinned_build_available"`
	IsDefault            *bool   `json:"is_default,omitempty"`
	CreatedAt            string  `json:"created_at,omitempty"`
	// LatestBuild is set by branch lists; nil there means the branch has no builds.
	LatestBuild *BuildBranchLatestBuild `json:"latest_build,omitempty"`
}

type BuildBranchesPage struct {
	Branches   []BuildBranch `json:"branches"`
	Total      int           `json:"total"`
	Page       int           `json:"page"`
	PageSize   int           `json:"page_size"`
	TotalPages int           `json:"total_pages"`
}

type BranchListOptions struct {
	Name, Search   string
	Page, PageSize int
}
type BuildListOptions struct {
	Search               string
	BranchID, BranchName string
	ExactVersion         string
	Page, PageSize       int
}

func (c *Client) ListBuildBranches(ctx context.Context, appID string, opts BranchListOptions) (*BuildBranchesPage, error) {
	values := url.Values{}
	if opts.Name != "" {
		values.Set("name", opts.Name)
	}
	if opts.Search != "" {
		values.Set("search", opts.Search)
	}
	if opts.Page > 0 {
		values.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PageSize > 0 {
		values.Set("page_size", strconv.Itoa(opts.PageSize))
	}
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/%s/branches?%s", url.PathEscape(appID), values.Encode()), nil)
	if err != nil {
		return nil, err
	}
	var result BuildBranchesPage
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.Branches == nil {
		result.Branches = []BuildBranch{}
	}
	return &result, nil
}

func (c *Client) ResolveBuildBranch(ctx context.Context, appID, branchName string) (*BuildBranch, error) {
	return c.ResolveBranch(ctx, appID, "", branchName)
}

func (c *Client) ResolveBranch(ctx context.Context, appID, branchID, branchName string) (*BuildBranch, error) {
	return c.ResolveBranchWithContext(ctx, appID, branchID, branchName, "")
}

func (c *Client) ResolveBranchWithContext(ctx context.Context, appID, branchID, branchName, contextBranchName string) (*BuildBranch, error) {
	usesContext := branchID == "" && branchName == "" && contextBranchName != ""
	if usesContext {
		if err := validateContextBranchName(contextBranchName); err != nil {
			return nil, err
		}
		if err := validateBranchSelector("", contextBranchName); err != nil {
			return nil, fmt.Errorf("detected Git branch cannot be used as a Revyl branch; select one with --branch: %w", err)
		}
		branchName = contextBranchName
	}
	if err := validateBranchSelector(branchID, branchName); err != nil {
		return nil, err
	}
	if branchName != "" {
		branches, err := c.ListBuildBranches(ctx, appID, BranchListOptions{Name: branchName, PageSize: 1})
		if err != nil {
			return nil, err
		}
		if len(branches.Branches) == 0 {
			if usesContext {
				return nil, fmt.Errorf("%w: detected Git branch has no matching Revyl branch; upload a build or select another branch with --branch", ErrBranchNotFound)
			}
			return nil, ErrBranchNotFound
		}
		return &branches.Branches[0], nil
	}
	if branchID == "" {
		app, err := c.getApp(ctx, appID, false)
		if err != nil {
			return nil, err
		}
		branchID = app.DefaultBranchID
		if branchID == "" {
			return nil, errors.New("app has no default branch; update the app branch settings")
		}
	}
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/%s/branches/%s", url.PathEscape(appID), url.PathEscape(branchID)), nil)
	if err != nil {
		return nil, err
	}
	var result BuildBranch
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CreateBuildBranch(ctx context.Context, appID, name string) (*BuildBranch, error) {
	if name == "" {
		return nil, errors.New("branch name is required")
	}
	if err := validateBranchSelector("", name); err != nil {
		return nil, err
	}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/v1/apps/%s/branches", url.PathEscape(appID)), map[string]string{"name": name})
	if err != nil {
		return nil, err
	}
	var result BuildBranch
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ResolveUploadBranch(ctx context.Context, appID, branchID, branchName string) (*BuildBranch, error) {
	return c.ResolveUploadBranchWithContext(ctx, appID, branchID, branchName, "")
}

func (c *Client) ResolveUploadBranchWithContext(ctx context.Context, appID, branchID, branchName, contextBranchName string) (*BuildBranch, error) {
	branch, err := c.ResolveBranchWithContext(ctx, appID, branchID, branchName, contextBranchName)
	if branchID == "" && branchName == "" {
		branchName = contextBranchName
	}
	if errors.Is(err, ErrBranchNotFound) && branchName != "" {
		return c.CreateBuildBranch(ctx, appID, branchName)
	}
	return branch, err
}

type UpdateBuildBranchRequest struct {
	Name          *string  `json:"name,omitempty"`
	PinnedBuildID **string `json:"pinned_build_id,omitempty"`
}

func (c *Client) UpdateBuildBranch(ctx context.Context, appID, branchID string, req *UpdateBuildBranchRequest) (*BuildBranch, error) {
	resp, err := c.doRequest(ctx, "PATCH", fmt.Sprintf("/api/v1/apps/%s/branches/%s", url.PathEscape(appID), url.PathEscape(branchID)), req)
	if err != nil {
		return nil, err
	}
	var result BuildBranch
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) SetDefaultBuildBranch(ctx context.Context, appID, branchID string) error {
	resp, err := c.doRequest(ctx, "PATCH", fmt.Sprintf("/api/v1/apps/%s", url.PathEscape(appID)), map[string]string{"default_branch_id": branchID})
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

type buildResolutionRequest struct {
	ContextBranchName string             `json:"context_branch_name,omitempty"`
	AppID             string             `json:"build_var_id"`
	BranchID          string             `json:"branch_id,omitempty"`
	BranchName        string             `json:"branch_name,omitempty"`
	BuildSelection    BuildSelectionMode `json:"build_selection,omitempty"`
	PinnedBuildID     string             `json:"pinned_build_id,omitempty"`
}
type buildResolutionResult struct {
	AppID            string `json:"build_var_id"`
	BuildID          string `json:"version_id"`
	BranchID         string `json:"branch_id"`
	BranchName       string `json:"branch_name"`
	ResolutionStatus string `json:"resolution_status"`
	DownloadURL      string `json:"download_url"`
}

func (c *Client) ResolveBuildSelection(ctx context.Context, appID string, selection BuildSelection) (*BuildVersionDetail, error) {
	return c.resolveBuildSelection(ctx, appID, selection, true)
}

// ResolveBuildSelectionMetadata applies the runnable build policy without signing an artifact URL.
func (c *Client) ResolveBuildSelectionMetadata(ctx context.Context, appID string, selection BuildSelection) (*BuildVersionDetail, error) {
	return c.resolveBuildSelection(ctx, appID, selection, false)
}

func (c *Client) resolveBuildSelection(ctx context.Context, appID string, selection BuildSelection, includeDownloadURL bool) (*BuildVersionDetail, error) {
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	mode := selection.Mode
	if selection.BuildID != "" {
		mode = BuildSelectionModePinned
	}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/v1/apps/resolve?include_download_url=%t", includeDownloadURL), buildResolutionRequest{
		ContextBranchName: selection.ContextBranchName,
		AppID:             appID,
		BranchID:          selection.BranchID,
		BranchName:        selection.BranchName,
		BuildSelection:    mode,
		PinnedBuildID:     selection.BuildID,
	})
	if err != nil {
		return nil, err
	}
	var result buildResolutionResult
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.ResolutionStatus == "branch_has_no_builds" {
		return nil, ErrBranchHasNoBuilds
	}
	if result.BuildID == "" {
		return nil, ErrBranchHasNoCompatibleBuild
	}
	detail, err := c.GetBuildVersionDetail(ctx, result.BuildID, false)
	if err != nil {
		return nil, err
	}
	detail.DownloadURL = result.DownloadURL
	if includeDownloadURL && detail.DownloadURL == "" {
		return nil, errors.New("resolved build has no downloadable artifact; upload a new build or select another build")
	}
	detail.BranchID = result.BranchID
	detail.BranchName = result.BranchName
	return detail, nil
}

func (d *BuildVersionDetail) BuildVersion() *BuildVersion {
	return &BuildVersion{ID: d.ID, BranchID: d.BranchID, BranchName: d.BranchName, Version: d.Version, PackageName: d.PackageName, Metadata: d.Metadata}
}

func (c *Client) ListBuildVersionsWithOptions(ctx context.Context, appID string, opts BuildListOptions) (*BuildVersionsPage, error) {
	if err := validateBranchSelector(opts.BranchID, opts.BranchName); err != nil {
		return nil, err
	}
	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PageSize <= 0 {
		opts.PageSize = defaultBuildVersionsPageSize
	}
	if opts.PageSize > maxBuildVersionsPageSize {
		opts.PageSize = maxBuildVersionsPageSize
	}
	values := url.Values{"page": {strconv.Itoa(opts.Page)}, "page_size": {strconv.Itoa(opts.PageSize)}}
	if opts.BranchID != "" {
		values.Set("branch_id", opts.BranchID)
	}
	if opts.BranchName != "" {
		values.Set("branch_name", opts.BranchName)
	}
	if opts.ExactVersion != "" {
		values.Set("exact_version", opts.ExactVersion)
	}
	if opts.Search != "" {
		values.Set("search", opts.Search)
	}
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/v1/apps/%s/builds?%s", url.PathEscape(appID), values.Encode()), nil)
	if err != nil {
		return nil, err
	}
	var result BuildVersionsPage
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.Items == nil {
		result.Items = []BuildVersion{}
	}
	return &result, nil
}

func uploadDestinationPayload(req *UploadBuildRequest, payload map[string]interface{}) map[string]interface{} {
	if req.ContextBranchName != "" {
		payload["context_branch_name"] = req.ContextBranchName
	}
	if req.BranchID != "" {
		payload["branch_id"] = req.BranchID
	}
	if req.BranchName != "" {
		payload["branch_name"] = req.BranchName
	}
	return payload
}

func (c *Client) SetWorkflowBuildOverrideEnabled(ctx context.Context, workflowID string, enabled bool) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/v1/workflows/update_build_config/%s", url.PathEscape(workflowID)), map[string]bool{"override_build_config": enabled})
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}
