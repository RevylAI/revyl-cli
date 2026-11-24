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
var ErrBranchHasNoCompatibleBuild = errors.New("selected branch has no usable build; upload one to it or choose another branch")
var ErrBranchNotFound = errors.New("build branch not found")

// BranchError is ErrBranchNotFound, ErrBranchHasNoBuilds or
// ErrBranchHasNoCompatibleBuild worded with the branch and app it failed on;
// errors.Is still matches the sentinel.
type BranchError struct {
	Err        error
	BranchName string
	// AppName is empty when the caller knows only the app's ID.
	AppName string
	// FromGit marks a branch named after the checked-out Git branch.
	FromGit bool
	// GitBranchName is the detected Git branch that was tried before the
	// app's default branch. It is set only when the server reports that the
	// app's default branch replaced it, including when that branch also fails.
	GitBranchName string
}

func (e *BranchError) Unwrap() error { return e.Err }

func (e *BranchError) Error() string {
	if e.GitBranchName != "" && e.BranchName != "" && e.GitBranchName != e.BranchName {
		fallback := (&BranchError{Err: e.Err, BranchName: e.BranchName, AppName: e.AppName}).Error()
		return fmt.Sprintf("Git branch %q was unavailable; app default %s", e.GitBranchName, fallback)
	}
	if e.BranchName == "" {
		if e.AppName != "" && errors.Is(e.Err, ErrBranchHasNoCompatibleBuild) {
			return fmt.Sprintf("no branch on %q has a usable build; upload one first", e.AppName)
		}
		return e.Err.Error()
	}
	branch := fmt.Sprintf("branch %q", e.BranchName)
	if e.FromGit {
		branch += " from Git"
	}
	onApp := ""
	if e.AppName != "" {
		onApp = fmt.Sprintf(" on %q", e.AppName)
	}
	switch {
	case errors.Is(e.Err, ErrBranchNotFound):
		return fmt.Sprintf("%s doesn't exist%s", branch, onApp)
	case errors.Is(e.Err, ErrBranchHasNoBuilds):
		return fmt.Sprintf("%s%s has no builds; upload a build to it first", branch, onApp)
	case errors.Is(e.Err, ErrBranchHasNoCompatibleBuild):
		return fmt.Sprintf("%s%s has no usable build; upload one to it or choose another branch", branch, onApp)
	default:
		return e.Err.Error()
	}
}

const MaxGitBranchNameLength = 4096

const maxBranchNameLength = 255

// ValidateBranchName applies the API's branch name rule (BranchName in
// cognisim_schemas) so a bad name fails before any request.
func ValidateBranchName(name string) error {
	switch {
	case name == "":
		return errors.New("branch name can't be empty")
	case utf8.RuneCountInString(name) > maxBranchNameLength:
		return fmt.Errorf("branch name must be at most %d characters", maxBranchNameLength)
	case strings.TrimSpace(name) != name:
		return fmt.Errorf("branch name %q can't start or end with whitespace", name)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return fmt.Errorf("branch name %q can't contain control characters", name)
	}
	return nil
}

type BuildSelection struct {
	GitBranchName string
	BranchID      string
	BranchName    string
	Mode          BuildSelectionMode
	BuildID       string
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
		return ValidateBranchName(branchName)
	}
	return nil
}

// gitBranchNameFor returns the detected Git branch a request may carry.
// Latest across all branches ignores it, so such a request never sends one.
func gitBranchNameFor(mode BuildSelectionMode, gitBranchName string) string {
	if mode == BuildSelectionModeLatestAllBranches {
		return ""
	}
	return gitBranchName
}

func validateGitBranchName(name string) error {
	if utf8.RuneCountInString(name) > MaxGitBranchNameLength {
		return errors.New("Git branch name exceeds 4096 characters; select a Revyl branch with --branch")
	}
	return nil
}

func (s BuildSelection) Validate() error {
	if err := validateGitBranchName(s.GitBranchName); err != nil {
		return err
	}
	if err := validateBranchSelector(s.BranchID, s.BranchName); err != nil {
		return err
	}
	switch s.Mode {
	case "", BuildSelectionModeInherit, BuildSelectionModeLatest, BuildSelectionModePinned:
	case BuildSelectionModeLatestAllBranches:
		if s.BranchID != "" || s.BranchName != "" {
			return errors.New("All branches · Latest build can't be combined with a branch")
		}
	default:
		return errors.New("build selection must be inherit, latest, latest_all_branches, or pinned")
	}
	if s.BuildID != "" {
		if _, err := uuid.Parse(s.BuildID); err != nil {
			return errors.New("build ID must be a UUID")
		}
		if s.Mode != "" && s.Mode != BuildSelectionModePinned {
			return errors.New("an exact build can't be combined with Automatic, Latest build, or All branches · Latest build")
		}
	} else if s.Mode == BuildSelectionModePinned {
		return errors.New("an exact build (pinned) requires a build ID")
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
		return errors.New("a saved exact build (pinned) requires a build ID; other build choices can't take one")
	}
	if buildID != "" && branchID == "" {
		return errors.New("saved exact build requires its branch")
	}
	return (BuildSelection{BranchID: branchID, Mode: s.BuildSelection, BuildID: buildID}).Validate()
}

type BuildBranch struct {
	AutoGitBranchEnabled *bool   `json:"auto_git_branch_enabled,omitempty"`
	ID                   string  `json:"id"`
	AppID                string  `json:"app_id"`
	Name                 string  `json:"name"`
	PinnedBuildID        *string `json:"pinned_build_id"`
	PinnedBuildVersion   *string `json:"pinned_build_version"`
	PinnedBuildAvailable *bool   `json:"pinned_build_available"`
	IsDefault            *bool   `json:"is_default,omitempty"`
	CreatedAt            string  `json:"created_at,omitempty"`
	// BranchSource and GitBranchName are set when a Git branch resolution
	// chose this branch.
	BranchSource  BranchSource `json:"branch_source,omitempty"`
	GitBranchName string       `json:"git_branch_name,omitempty"`
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
	return c.ResolveBranchWithGitBranch(ctx, appID, branchID, branchName, "")
}

func (c *Client) ResolveBranchWithGitBranch(ctx context.Context, appID, branchID, branchName, gitBranchName string, modes ...BuildSelectionMode) (*BuildBranch, error) {
	mode := BuildSelectionModeInherit
	if len(modes) > 0 {
		mode = modes[0]
	}
	gitBranchName = gitBranchNameFor(mode, gitBranchName)
	if branchID == "" && branchName == "" && gitBranchName != "" {
		if err := validateGitBranchName(gitBranchName); err != nil {
			return nil, err
		}
		resp, err := c.doRequest(ctx, "POST", "/api/v1/apps/resolve", buildResolutionRequest{
			AppID: appID, GitBranchName: gitBranchName, BuildSelection: mode,
		})
		if err != nil {
			return nil, err
		}
		var result buildResolutionResult
		if err := parseResponse(resp, &result); err != nil {
			return nil, err
		}
		if result.BranchID == "" {
			return nil, errors.New("app has no default branch; update the app branch settings")
		}
		branch, err := c.ResolveBranch(ctx, appID, result.BranchID, "")
		if err != nil {
			return nil, err
		}
		branch.BranchSource, branch.GitBranchName = result.BranchSource, result.GitBranchName
		branch.AutoGitBranchEnabled = result.AutoGitBranchEnabled
		return branch, nil
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
			return nil, &BranchError{Err: ErrBranchNotFound, BranchName: branchName}
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
	if err := ValidateBranchName(name); err != nil {
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

// FindUploadBranch returns the existing branch an upload to this destination
// lands on: the branch by ID or name, the branch named after gitBranchName, or
// the app's default branch when no destination is given. It returns nil, with
// no error, when the named branch doesn't exist yet. Such an upload sends the
// name instead of creating the branch first, so the backend creates it and can
// make a new app's first upload its default branch.
func (c *Client) FindUploadBranch(ctx context.Context, appID, branchID, branchName, gitBranchName string) (*BuildBranch, error) {
	var app *App
	if branchID == "" && branchName == "" && gitBranchName != "" {
		if err := validateGitBranchName(gitBranchName); err != nil {
			return nil, err
		}
		var err error
		app, err = c.getApp(ctx, appID, false)
		if err != nil {
			return nil, err
		}
		if app.AutomaticGitBranchEnabled() {
			if err := validateBranchSelector("", gitBranchName); err != nil {
				return nil, fmt.Errorf("detected Git branch cannot be used as a Revyl branch; select one with --branch: %w", err)
			}
			branchName = gitBranchName
		} else {
			branchID = app.DefaultBranchID
		}
	}
	branch, err := c.ResolveBranch(ctx, appID, branchID, branchName)
	if errors.Is(err, ErrBranchNotFound) && branchName != "" {
		return nil, nil
	}
	if branch != nil && app != nil {
		branch.AutoGitBranchEnabled = app.AutoGitBranchEnabled
		if app.AutomaticGitBranchEnabled() {
			branch.BranchSource, branch.GitBranchName = BranchSourceGitBranch, gitBranchName
		} else {
			branch.BranchSource, branch.GitBranchName = BranchSourceDefaultBranch, ""
		}
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
	GitBranchName  string             `json:"git_branch_name,omitempty"`
	AppID          string             `json:"build_var_id"`
	BranchID       string             `json:"branch_id,omitempty"`
	BranchName     string             `json:"branch_name,omitempty"`
	BuildSelection BuildSelectionMode `json:"build_selection,omitempty"`
	PinnedBuildID  string             `json:"pinned_build_id,omitempty"`
}
type buildResolutionResult struct {
	AutoGitBranchEnabled *bool        `json:"auto_git_branch_enabled,omitempty"`
	BranchSource         BranchSource `json:"branch_source"`
	GitBranchName        string       `json:"git_branch_name"`
	AppID                string       `json:"build_var_id"`
	AppName              string       `json:"app_name"`
	BuildID              string       `json:"version_id"`
	BranchID             string       `json:"branch_id"`
	BranchName           string       `json:"branch_name"`
	ResolutionStatus     string       `json:"resolution_status"`
	DownloadURL          string       `json:"download_url"`
}

// FellBackFromGitBranch reports whether the app's default branch replaced
// gitBranchName, the Git branch a request sent or a result reports.
func (s BranchSource) FellBackFromGitBranch(gitBranchName string) bool {
	return s == BranchSourceDefaultBranch && gitBranchName != ""
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
		GitBranchName:  gitBranchNameFor(mode, selection.GitBranchName),
		AppID:          appID,
		BranchID:       selection.BranchID,
		BranchName:     selection.BranchName,
		BuildSelection: mode,
		PinnedBuildID:  selection.BuildID,
	})
	if err != nil {
		return nil, err
	}
	var result buildResolutionResult
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.ResolutionStatus == "branch_has_no_builds" {
		return nil, buildResolutionBranchError(selection, result, ErrBranchHasNoBuilds)
	}
	if result.BuildID == "" {
		return nil, buildResolutionBranchError(selection, result, ErrBranchHasNoCompatibleBuild)
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
	detail.BranchSource, detail.GitBranchName = result.BranchSource, result.GitBranchName
	detail.AutoGitBranchEnabled = result.AutoGitBranchEnabled
	return detail, nil
}

func buildResolutionBranchError(selection BuildSelection, result buildResolutionResult, err error) *BranchError {
	branchError := &BranchError{Err: err, BranchName: result.BranchName, AppName: result.AppName}
	if result.BranchSource.FellBackFromGitBranch(result.GitBranchName) {
		branchError.GitBranchName = result.GitBranchName
	}
	return branchError
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
	if req.GitBranchName != "" {
		payload["git_branch_name"] = req.GitBranchName
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
