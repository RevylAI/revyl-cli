// Package sync provides version conflict detection and resolution for test synchronization.
//
// This package handles detecting conflicts between local and remote test versions,
// and provides strategies for resolving them.
package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/orgguard"
	"github.com/revyl/cli/internal/util"
)

// SyncStatus represents the sync status of a test.
type SyncStatus int

const (
	// StatusSynced means local and remote are in sync.
	StatusSynced SyncStatus = iota
	// StatusModified means local has changes not pushed.
	StatusModified
	// StatusOutdated means remote has changes not pulled.
	StatusOutdated
	// StatusConflict means both local and remote have changes.
	StatusConflict
	// StatusLocalOnly means test exists only locally.
	StatusLocalOnly
	// StatusRemoteOnly means test exists only on remote.
	StatusRemoteOnly
	// StatusOrphaned means the local/config remote link is stale or inaccessible.
	StatusOrphaned
)

// String returns the string representation of a sync status.
func (s SyncStatus) String() string {
	switch s {
	case StatusSynced:
		return "synced"
	case StatusModified:
		return "modified"
	case StatusOutdated:
		return "outdated"
	case StatusConflict:
		return "conflict"
	case StatusLocalOnly:
		return "local-only"
	case StatusRemoteOnly:
		return "remote-only"
	case StatusOrphaned:
		return "orphaned"
	default:
		return "unknown"
	}
}

// RemoteLinkIssue describes why a linked remote test could not be resolved.
type RemoteLinkIssue string

const (
	// RemoteLinkIssueNone indicates there is no remote link issue.
	RemoteLinkIssueNone RemoteLinkIssue = ""
	// RemoteLinkIssueMissing indicates the remote test no longer exists (404).
	RemoteLinkIssueMissing RemoteLinkIssue = "missing"
	// RemoteLinkIssueInvalidID indicates the configured remote test ID is invalid (400).
	RemoteLinkIssueInvalidID RemoteLinkIssue = "invalid-id"
	// RemoteLinkIssueUnauthorized indicates authentication is missing/expired (401).
	RemoteLinkIssueUnauthorized RemoteLinkIssue = "unauthorized"
	// RemoteLinkIssueForbidden indicates access to the linked test is denied (403).
	RemoteLinkIssueForbidden RemoteLinkIssue = "forbidden"
)

// TestSyncStatus contains sync status information for a test.
type TestSyncStatus struct {
	// Name is the test name/alias.
	Name string
	// Status is the sync status.
	Status SyncStatus
	// LocalVersion is the local version number.
	LocalVersion int
	// RemoteVersion is the remote version number.
	RemoteVersion int
	// LastSync is a human-readable last sync time.
	LastSync string
	// RemoteID is the test ID on the server.
	RemoteID string
	// LinkIssue indicates a non-fatal remote link resolution issue.
	LinkIssue RemoteLinkIssue
	// LinkIssueMessage is a user-facing detail about LinkIssue.
	LinkIssueMessage string
	// ErrorMessage contains any error that occurred while determining status.
	ErrorMessage string
}

// SyncResult contains the result of a sync operation.
type SyncResult struct {
	// Name is the test name.
	Name string
	// NewVersion is the new version after sync.
	NewVersion int
	// Conflict indicates if there was a conflict.
	Conflict bool
	// Error is any error that occurred.
	Error error
	// TagSyncError is a non-fatal error from tag synchronization.
	TagSyncError error
	// ConfigSyncError is a non-fatal error from variable/env/device config sync.
	ConfigSyncError error
}

// Resolver handles test name resolution and sync operations.
type Resolver struct {
	client     *api.Client
	config     *config.ProjectConfig
	localTests map[string]*config.LocalTest
}

// NewResolver creates a new sync resolver.
//
// Parameters:
//   - client: The API client
//   - cfg: The project configuration
//   - localTests: Map of local test definitions
//
// Returns:
//   - *Resolver: A new resolver instance
func NewResolver(client *api.Client, cfg *config.ProjectConfig, localTests map[string]*config.LocalTest) *Resolver {
	if cfg == nil {
		cfg = &config.ProjectConfig{}
	}
	if cfg.Build.Platforms == nil {
		cfg.Build.Platforms = make(map[string]config.BuildPlatform)
	}
	config.ApplyDefaults(cfg)
	if localTests == nil {
		localTests = make(map[string]*config.LocalTest)
	}

	return &Resolver{
		client:     client,
		config:     cfg,
		localTests: localTests,
	}
}

// GetAllStatuses returns sync status for all known tests.
// Fetches are parallelised with bounded concurrency to avoid sequential
// round-trips that dominated TUI startup time.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - []TestSyncStatus: List of test sync statuses
//   - error: Any error that occurred
func (r *Resolver) GetAllStatuses(ctx context.Context) ([]TestSyncStatus, error) {
	names := make([]string, 0, len(r.localTests))
	for name := range r.localTests {
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, nil
	}

	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)
	results := make(chan TestSyncStatus, len(names))

	for _, name := range names {
		sem <- struct{}{}
		go func(n string) {
			defer func() { <-sem }()
			status, err := r.getTestStatus(ctx, n)
			if err != nil {
				results <- TestSyncStatus{
					Name:         n,
					Status:       StatusLocalOnly,
					ErrorMessage: err.Error(),
				}
				return
			}
			results <- *status
		}(name)
	}

	statuses := make([]TestSyncStatus, 0, len(names))
	for range names {
		statuses = append(statuses, <-results)
	}

	return statuses, nil
}

// getTestStatus gets the sync status for a single test.
func (r *Resolver) getTestStatus(ctx context.Context, name string) (*TestSyncStatus, error) {
	status := &TestSyncStatus{Name: name}

	// Check if we have a local test
	localTest, hasLocal := r.localTests[name]

	// Determine remote ID from local YAML _meta.remote_id
	var remoteID string
	if hasLocal && localTest.Meta.RemoteID != "" {
		remoteID = localTest.Meta.RemoteID
	}

	if remoteID == "" {
		status.RemoteID = ""
		status.Status = StatusLocalOnly
		return status, nil
	}

	status.RemoteID = remoteID

	if hasLocal {
		status.LocalVersion = localTest.Meta.LocalVersion
		if localTest.Meta.LastSyncedAt != "" {
			status.LastSync = formatTimeAgo(localTest.Meta.LastSyncedAt)
		} else {
			status.LastSync = "never"
		}
	}

	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case 404:
				status.Status = StatusOrphaned
				status.LinkIssue = RemoteLinkIssueMissing
				status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "remote test not found")
				return status, nil
			case 400:
				status.Status = StatusOrphaned
				status.LinkIssue = RemoteLinkIssueInvalidID
				status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "invalid remote test ID")
				return status, nil
			case 401:
				status.Status = StatusOrphaned
				status.LinkIssue = RemoteLinkIssueUnauthorized
				status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "not authorized to access this test")
				return status, nil
			case 403:
				status.Status = StatusOrphaned
				status.LinkIssue = RemoteLinkIssueForbidden
				status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "access denied for this test")
				return status, nil
			}
		}
		return nil, err
	}

	status.RemoteVersion = remoteTest.Version

	// Check for local modifications using checksum-based detection
	hasLocalChanges := hasLocal && localTest.HasLocalChanges()

	// Determine sync status
	if !hasLocal {
		status.Status = StatusRemoteOnly
	} else if hasLocalChanges && remoteTest.Version > localTest.Meta.RemoteVersion {
		// Both local content changed AND remote has newer version = conflict
		status.Status = StatusConflict
	} else if hasLocalChanges {
		// Local content changed (detected via checksum mismatch)
		status.Status = StatusModified
	} else if remoteTest.Version > localTest.Meta.RemoteVersion {
		// Remote has newer version, no local changes
		status.Status = StatusOutdated
	} else {
		// Checksums match and versions are in sync
		status.Status = StatusSynced
	}

	return status, nil
}

func bestLinkIssueMessage(apiErr *api.APIError, fallback string) string {
	if apiErr == nil {
		return fallback
	}
	if msg := strings.TrimSpace(apiErr.Detail); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(apiErr.Message); msg != "" {
		return msg
	}
	return fallback
}

// SyncToRemote pushes local changes to remote.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testName: Specific test name (empty for all)
//   - testsDir: Directory where tests are stored (for saving updated metadata)
//   - force: Force overwrite remote
//
// Returns:
//   - []SyncResult: Results for each synced test
//   - error: Any error that occurred
func (r *Resolver) SyncToRemote(ctx context.Context, testName, testsDir string, force bool) ([]SyncResult, error) {
	var results []SyncResult

	testsToSync := make(map[string]*config.LocalTest)
	if testName != "" {
		if test, ok := r.localTests[testName]; ok {
			testsToSync[testName] = test
		} else {
			return nil, fmt.Errorf("test not found: %s", testName)
		}
	} else {
		testsToSync = r.localTests
	}

	// Cache resolved build name → app ID to avoid redundant ListApps calls
	// when multiple tests share the same build.name + platform.
	appIDCache := make(map[string]string) // key: "buildName\x00platform"
	createOrgID := ""
	createOrgIDResolved := false
	var createOrgIDErr error

	for name, localTest := range testsToSync {
		result := SyncResult{Name: name}

		// Resolve build.name → app ID for push (with caching)
		var resolvedAppID string
		if localTest.Test.Build.Name != "" {
			cacheKey := localTest.Test.Build.Name + "\x00" + localTest.Test.Metadata.Platform
			if cached, ok := appIDCache[cacheKey]; ok {
				resolvedAppID = cached
			} else {
				resolvedAppID = r.resolveBuildNameToAppID(ctx, localTest.Test.Build.Name, localTest.Test.Metadata.Platform)
				appIDCache[cacheKey] = resolvedAppID
			}
		}

		remoteID := localTest.Meta.RemoteID

		if remoteID == "" {
			if !createOrgIDResolved {
				createOrgID, createOrgIDErr = orgguard.ResolveCreateOrgID(ctx, r.client, r.config)
				createOrgIDResolved = true
			}
			if createOrgIDErr != nil {
				result.Error = createOrgIDErr
				results = append(results, result)
				continue
			}

			// Create new test on remote
			resp, err := r.client.CreateTest(ctx, &api.CreateTestRequest{
				Name:     localTest.Test.Metadata.Name,
				Platform: localTest.Test.Metadata.Platform,
				Tasks:    localTest.Test.Blocks,
				AppID:    resolvedAppID,
				OrgID:    createOrgID,
			})
			if err != nil {
				result.Error = err
			} else {
				result.NewVersion = resp.Version
				localTest.Meta.RemoteID = resp.ID
				localTest.Meta.RemoteVersion = resp.Version
				localTest.Meta.LocalVersion = resp.Version
				localTest.Meta.LastSyncedAt = time.Now().Format(time.RFC3339)

				if tagErr := r.syncTagsForTest(ctx, resp.ID, localTest.Test.Metadata.Tags); tagErr != nil {
					result.TagSyncError = tagErr
				}

				if cfgErr := r.syncTestConfig(ctx, resp.ID, localTest); cfgErr != nil {
					result.ConfigSyncError = cfgErr
				}

				sanitized := util.SanitizeForFilename(name)
				path := filepath.Join(testsDir, sanitized+".yaml")
				if saveErr := config.SaveLocalTest(path, localTest); saveErr != nil {
					result.Error = fmt.Errorf("synced but failed to save local file: %w", saveErr)
				}
			}
		} else {
			// Update existing test
			expectedVersion := 0
			if !force {
				expectedVersion = localTest.Meta.RemoteVersion
			}

			resp, err := r.client.UpdateTest(ctx, &api.UpdateTestRequest{
				TestID:          remoteID,
				Name:            localTest.Test.Metadata.Name,
				Description:     localTest.Test.Metadata.Description,
				Tasks:           localTest.Test.Blocks,
				AppID:           resolvedAppID,
				PinnedVersionID: localTest.Test.Build.PinnedVersion,
				ExpectedVersion: expectedVersion,
				Force:           force,
			})
			if err != nil {
				// Check if it's a version conflict
				if apiErr, ok := err.(*api.APIError); ok && apiErr.StatusCode == 409 {
					result.Conflict = true
				} else {
					result.Error = err
				}
			} else {
				result.NewVersion = resp.Version
				localTest.Meta.RemoteVersion = resp.Version
				localTest.Meta.LocalVersion = resp.Version
				localTest.Meta.LastSyncedAt = time.Now().Format(time.RFC3339)

				if tagErr := r.syncTagsForTest(ctx, remoteID, localTest.Test.Metadata.Tags); tagErr != nil {
					result.TagSyncError = tagErr
				}

				if cfgErr := r.syncTestConfig(ctx, remoteID, localTest); cfgErr != nil {
					result.ConfigSyncError = cfgErr
				}

				// Save updated local test file
				sanitized := util.SanitizeForFilename(name)
				path := filepath.Join(testsDir, sanitized+".yaml")
				if saveErr := config.SaveLocalTest(path, localTest); saveErr != nil {
					result.Error = fmt.Errorf("synced but failed to save local file: %w", saveErr)
				}
			}
		}

		results = append(results, result)
	}

	return results, nil
}

// resolveBuildNameToAppID looks up the app ID for a given build name and platform.
// Paginates through all apps if needed. Returns empty string if not found (non-fatal).
func (r *Resolver) resolveBuildNameToAppID(ctx context.Context, buildName, platform string) string {
	if buildName == "" {
		return ""
	}

	page := 1
	for {
		appsResp, err := r.client.ListApps(ctx, platform, page, 100)
		if err != nil {
			return ""
		}

		for _, app := range appsResp.Items {
			if strings.EqualFold(app.Name, buildName) {
				return app.ID
			}
		}

		if !appsResp.HasNext {
			break
		}
		page++
	}

	return ""
}

// syncTagsForTest syncs tags for a test if tags are present.
// Returns an error if the sync fails so callers can surface it.
func (r *Resolver) syncTagsForTest(ctx context.Context, testID string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	_, err := r.client.SyncTestTags(ctx, testID, &api.CLISyncTagsRequest{
		TagNames: tags,
	})
	return err
}

// syncTestConfig pushes variables, env vars, and device targets from local YAML
// to the remote test. Always deletes then re-adds so removing a section from
// YAML correctly clears the remote values.
//
// Returns a joined error of all individual failures so callers can surface
// partial-sync warnings without aborting the overall operation.
func (r *Resolver) syncTestConfig(ctx context.Context, testID string, localTest *config.LocalTest) error {
	var errs []error

	if err := r.client.DeleteAllCustomVariables(ctx, testID); err != nil {
		errs = append(errs, fmt.Errorf("delete custom vars: %w", err))
	}
	for name, value := range localTest.Test.Variables {
		if _, err := r.client.AddCustomVariable(ctx, testID, name, value); err != nil {
			errs = append(errs, fmt.Errorf("add var %s: %w", name, err))
		}
	}

	if err := r.client.DeleteAllEnvVars(ctx, testID); err != nil {
		errs = append(errs, fmt.Errorf("delete env vars: %w", err))
	}
	for key, value := range localTest.Test.EnvVars {
		if _, err := r.client.AddEnvVar(ctx, testID, key, value); err != nil {
			errs = append(errs, fmt.Errorf("add env var %s: %w", key, err))
		}
	}

	if localTest.Test.Device != nil && localTest.Test.Device.Model != "" {
		if err := r.client.UpdateDeviceTarget(ctx, testID, localTest.Test.Device.Model, localTest.Test.Device.OSVersion); err != nil {
			errs = append(errs, fmt.Errorf("update device target: %w", err))
		}
	}

	return errors.Join(errs...)
}

// PullFromRemote pulls remote changes to local.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testName: Specific test name (empty for all)
//   - testsDir: Directory to save tests
//   - force: Force overwrite local
//
// Returns:
//   - []SyncResult: Results for each pulled test
//   - error: Any error that occurred
func (r *Resolver) PullFromRemote(ctx context.Context, testName, testsDir string, force bool) ([]SyncResult, error) {
	var results []SyncResult

	// Ensure tests directory exists
	if err := ensureDir(testsDir); err != nil {
		return nil, fmt.Errorf("failed to create tests directory: %w", err)
	}

	testsToPull := make(map[string]string) // name -> remoteID
	if testName != "" {
		if local, ok := r.localTests[testName]; ok && local.Meta.RemoteID != "" {
			testsToPull[testName] = local.Meta.RemoteID
		} else {
			return nil, fmt.Errorf("test not found or has no remote ID: %s", testName)
		}
	} else {
		for name, local := range r.localTests {
			if local.Meta.RemoteID != "" {
				testsToPull[name] = local.Meta.RemoteID
			}
		}
	}

	for name, remoteID := range testsToPull {
		results = append(results, r.pullRemoteTest(ctx, name, remoteID, testsDir, force))
	}

	return results, nil
}

// ImportRemoteTest pulls a remote test into local/config state even when there
// is no existing alias or local file for it yet.
func (r *Resolver) ImportRemoteTest(ctx context.Context, remoteID, preferredName, testsDir string, force bool) ([]SyncResult, error) {
	if err := ensureDir(testsDir); err != nil {
		return nil, fmt.Errorf("failed to create tests directory: %w", err)
	}

	remoteID = strings.TrimSpace(remoteID)
	if remoteID == "" {
		return nil, fmt.Errorf("remote test ID is required")
	}

	if alias := findAliasByRemoteID(r.localTests, remoteID); alias != "" {
		return []SyncResult{r.pullRemoteTest(ctx, alias, remoteID, testsDir, force)}, nil
	}

	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		return nil, err
	}

	baseName := strings.TrimSpace(preferredName)
	if baseName == "" {
		baseName = strings.TrimSpace(remoteTest.Name)
	}
	alias := buildImportAlias(baseName, r.localTests, remoteID)
	return []SyncResult{r.pullRemoteTest(ctx, alias, remoteID, testsDir, force)}, nil
}

func (r *Resolver) pullRemoteTest(ctx context.Context, name, remoteID, testsDir string, force bool) SyncResult {
	result := SyncResult{Name: name}

	if local, ok := r.localTests[name]; ok && !force {
		if local.HasLocalChanges() {
			result.Conflict = true
			return result
		}
	}

	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		result.Error = err
		return result
	}

	blocks, err := convertTasksToBlocks(remoteTest.Tasks)
	if err != nil {
		result.Error = fmt.Errorf("parse remote test blocks: %w", err)
		return result
	}
	blocks = stripBlockIDs(blocks)

	scriptNames, moduleNames := r.fetchNameMappings(ctx)
	resolveBlockNames(blocks, scriptNames, moduleNames)

	localTest := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      remoteID,
			RemoteVersion: remoteTest.Version,
			LocalVersion:  remoteTest.Version,
			LastSyncedAt:  time.Now().Format(time.RFC3339),
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{
				Name:        remoteTest.Name,
				Platform:    strings.ToLower(remoteTest.Platform),
				Description: remoteTest.Description,
			},
			Blocks: blocks,
		},
	}

	if remoteTest.AppID != "" {
		app, err := r.client.GetApp(ctx, remoteTest.AppID)
		if err == nil && app != nil {
			localTest.Test.Build = config.TestBuildConfig{
				Name:         app.Name,
				SystemPrompt: app.SystemPrompt,
			}
		}
	}
	if remoteTest.PinnedVersion != "" {
		localTest.Test.Build.PinnedVersion = remoteTest.PinnedVersion
	}

	tags, err := r.client.GetTestTags(ctx, remoteID)
	if err == nil && len(tags) > 0 {
		tagNames := make([]string, len(tags))
		for i, t := range tags {
			tagNames[i] = t.Name
		}
		localTest.Test.Metadata.Tags = tagNames
	}

	// Fetch custom variables
	if varsResp, err := r.client.ListCustomVariables(ctx, remoteID); err == nil && len(varsResp.Result) > 0 {
		vars := make(map[string]string, len(varsResp.Result))
		for _, v := range varsResp.Result {
			vars[v.VariableName] = v.VariableValue
		}
		localTest.Test.Variables = vars
	}

	// Fetch app launch env vars
	if envResp, err := r.client.ListEnvVars(ctx, remoteID); err == nil && len(envResp.Result) > 0 {
		envVars := make(map[string]string, len(envResp.Result))
		for _, ev := range envResp.Result {
			envVars[ev.Key] = ev.Value
		}
		localTest.Test.EnvVars = envVars
	}

	// Parse device targets from GetTest response (first target wins)
	if len(remoteTest.MobileTargets) > 0 {
		mt := remoteTest.MobileTargets[0]
		if mt.DeviceModel != "" && mt.DeviceModel != "AUTO" {
			localTest.Test.Device = &config.TestDeviceConfig{
				Model:     mt.DeviceModel,
				OSVersion: mt.OSVersion,
			}
		}
	}

	sanitized := util.SanitizeForFilename(name)
	if sanitized == "" {
		sanitized = fallbackTestAlias(remoteID)
	}
	path := filepath.Join(testsDir, sanitized+".yaml")
	if err := config.SaveLocalTest(path, localTest); err != nil {
		result.Error = err
		return result
	}

	result.NewVersion = remoteTest.Version
	r.localTests[sanitized] = localTest
	if sanitized != name {
		delete(r.localTests, name)
	}

	return result
}

func findAliasByRemoteID(localTests map[string]*config.LocalTest, remoteID string) string {
	if strings.TrimSpace(remoteID) == "" {
		return ""
	}

	aliases := make([]string, 0, len(localTests))
	for alias := range localTests {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		local := localTests[alias]
		if local != nil && strings.TrimSpace(local.Meta.RemoteID) == remoteID {
			return alias
		}
	}

	return ""
}

func buildImportAlias(baseName string, localTests map[string]*config.LocalTest, remoteID string) string {
	alias := util.SanitizeForFilename(baseName)
	if alias == "" {
		alias = fallbackTestAlias(remoteID)
	}
	return ensureUniqueTestAlias(alias, localTests, remoteID)
}

func ensureUniqueTestAlias(base string, localTests map[string]*config.LocalTest, remoteID string) string {
	if !testAliasTakenByOther(base, localTests, remoteID) {
		return base
	}

	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !testAliasTakenByOther(candidate, localTests, remoteID) {
			return candidate
		}
	}
}

func testAliasTakenByOther(alias string, localTests map[string]*config.LocalTest, remoteID string) bool {
	if local, ok := localTests[alias]; ok {
		if local != nil && strings.TrimSpace(local.Meta.RemoteID) == remoteID {
			return false
		}
		return true
	}

	return false
}

func fallbackTestAlias(remoteID string) string {
	trimmed := util.SanitizeForFilename(strings.TrimSpace(remoteID))
	if len(trimmed) > 8 {
		trimmed = trimmed[:8]
	}
	if trimmed == "" {
		trimmed = "import"
	}
	return "test-" + trimmed
}

// GetDiff returns a diff between local and remote versions.
//
// Parameters:
//   - ctx: Context for cancellation
//   - testName: The test name
//
// Returns:
//   - string: The diff output
//   - error: Any error that occurred
func (r *Resolver) GetDiff(ctx context.Context, testName string) (string, error) {
	localTest, hasLocal := r.localTests[testName]
	if !hasLocal {
		return "", fmt.Errorf("local test not found: %s", testName)
	}

	remoteID := localTest.Meta.RemoteID
	if remoteID == "" {
		return "", fmt.Errorf("no remote ID for test: %s", testName)
	}

	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		return "", err
	}

	// Generate diff by comparing local blocks with remote tasks.
	// Both are marshaled to JSON first to get a canonical representation,
	// since localTest.Test is a structured TestDefinition while
	// remoteTest.Tasks is a raw interface{} from the API.
	localJSON, err := json.MarshalIndent(localTest.Test.Blocks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal local test blocks: %w", err)
	}
	remoteJSON, err := json.MarshalIndent(remoteTest.Tasks, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal remote test tasks: %w", err)
	}

	return generateSimpleDiff(string(localJSON), string(remoteJSON)), nil
}

// formatTimeAgo formats a timestamp as a human-readable "time ago" string.
func formatTimeAgo(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return timestamp
	}

	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		mins := int(duration.Minutes())
		return fmt.Sprintf("%dm ago", mins)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		return fmt.Sprintf("%dh ago", hours)
	default:
		days := int(duration.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// generateSimpleDiff generates a unified diff between local and remote content.
//
// Parameters:
//   - local: The local content
//   - remote: The remote content
//
// Returns:
//   - string: A unified diff format string showing the differences
func generateSimpleDiff(local, remote string) string {
	if local == remote {
		return ""
	}

	localLines := strings.Split(local, "\n")
	remoteLines := strings.Split(remote, "\n")

	// Build the diff output
	var diff strings.Builder
	diff.WriteString("--- local\n")
	diff.WriteString("+++ remote\n")

	// Use a simple line-by-line comparison with context
	// This is a simplified diff that shows changed, added, and removed lines
	maxLen := len(localLines)
	if len(remoteLines) > maxLen {
		maxLen = len(remoteLines)
	}

	// Track hunks of changes
	type hunk struct {
		localStart  int
		localCount  int
		remoteStart int
		remoteCount int
		lines       []string
	}

	var hunks []hunk
	var currentHunk *hunk
	contextLines := 3

	i, j := 0, 0
	for i < len(localLines) || j < len(remoteLines) {
		if i < len(localLines) && j < len(remoteLines) && localLines[i] == remoteLines[j] {
			// Lines match - context line
			if currentHunk != nil {
				currentHunk.lines = append(currentHunk.lines, " "+localLines[i])
				currentHunk.localCount++
				currentHunk.remoteCount++
			}
			i++
			j++
		} else {
			// Lines differ - start or continue a hunk
			if currentHunk == nil {
				// Start new hunk with context
				startLocal := i - contextLines
				if startLocal < 0 {
					startLocal = 0
				}
				startRemote := j - contextLines
				if startRemote < 0 {
					startRemote = 0
				}

				currentHunk = &hunk{
					localStart:  startLocal + 1, // 1-indexed
					remoteStart: startRemote + 1,
				}

				// Add leading context
				for k := startLocal; k < i; k++ {
					currentHunk.lines = append(currentHunk.lines, " "+localLines[k])
					currentHunk.localCount++
					currentHunk.remoteCount++
				}
			}

			// Find the next matching line or end
			if i < len(localLines) && (j >= len(remoteLines) || !containsLine(remoteLines[j:], localLines[i])) {
				// Line removed from local
				currentHunk.lines = append(currentHunk.lines, "-"+localLines[i])
				currentHunk.localCount++
				i++
			} else if j < len(remoteLines) {
				// Line added in remote
				currentHunk.lines = append(currentHunk.lines, "+"+remoteLines[j])
				currentHunk.remoteCount++
				j++
			}
		}

		// Check if we should close the hunk (after enough matching lines)
		if currentHunk != nil && i < len(localLines) && j < len(remoteLines) {
			matchCount := 0
			for k := 0; k < contextLines*2 && i+k < len(localLines) && j+k < len(remoteLines); k++ {
				if localLines[i+k] == remoteLines[j+k] {
					matchCount++
				} else {
					break
				}
			}
			if matchCount >= contextLines*2 {
				// Add trailing context and close hunk
				for k := 0; k < contextLines && i < len(localLines) && j < len(remoteLines); k++ {
					if localLines[i] == remoteLines[j] {
						currentHunk.lines = append(currentHunk.lines, " "+localLines[i])
						currentHunk.localCount++
						currentHunk.remoteCount++
						i++
						j++
					}
				}
				hunks = append(hunks, *currentHunk)
				currentHunk = nil
			}
		}
	}

	// Close any remaining hunk
	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	// Format hunks
	for _, h := range hunks {
		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", h.localStart, h.localCount, h.remoteStart, h.remoteCount))
		for _, line := range h.lines {
			diff.WriteString(line + "\n")
		}
	}

	return diff.String()
}

// containsLine checks if a line exists in the remaining lines.
func containsLine(lines []string, target string) bool {
	// Only look ahead a limited distance to avoid O(n^2) behavior
	lookAhead := 10
	if len(lines) < lookAhead {
		lookAhead = len(lines)
	}
	for i := 0; i < lookAhead; i++ {
		if lines[i] == target {
			return true
		}
	}
	return false
}

// fetchNameMappings fetches script and module name mappings for the org.
// Returns two maps: scriptID->name and moduleID->name.
// Failures are non-fatal; empty maps are returned on error.
func (r *Resolver) fetchNameMappings(ctx context.Context) (map[string]string, map[string]string) {
	scriptNames := make(map[string]string)
	moduleNames := make(map[string]string)

	if scripts, err := r.client.ListScripts(ctx, "", 500, 0); err == nil {
		for _, s := range scripts.Scripts {
			scriptNames[s.ID] = s.Name
		}
	}

	if modules, err := r.client.ListModules(ctx); err == nil {
		for _, m := range modules.Result {
			moduleNames[m.ID] = m.Name
		}
	}

	return scriptNames, moduleNames
}

// resolveBlockNames replaces script/module UUIDs with human-readable names
// in pulled blocks so the local YAML is readable.
//
// For code_execution blocks whose StepDescription is a UUID matching a known
// script, the UUID is moved to Script and StepDescription is cleared.
//
// For module_import blocks whose ModuleID matches a known module, the UUID
// is moved to Module and ModuleID is cleared.
func resolveBlockNames(blocks []config.TestBlock, scriptNames, moduleNames map[string]string) {
	for i := range blocks {
		b := &blocks[i]

		if b.Type == "code_execution" && b.StepDescription != "" {
			if name, ok := scriptNames[b.StepDescription]; ok {
				b.Script = name
				b.StepDescription = ""
			}
		}

		if b.Type == "module_import" && b.ModuleID != "" {
			if name, ok := moduleNames[b.ModuleID]; ok {
				b.Module = name
				b.ModuleID = ""
			}
		}

		if len(b.Then) > 0 {
			resolveBlockNames(b.Then, scriptNames, moduleNames)
		}
		if len(b.Else) > 0 {
			resolveBlockNames(b.Else, scriptNames, moduleNames)
		}
		if len(b.Body) > 0 {
			resolveBlockNames(b.Body, scriptNames, moduleNames)
		}
	}
}

// convertTasksToBlocks converts the API tasks (interface{}) to []config.TestBlock.
//
// Parameters:
//   - tasks: The tasks from the API response (can be []interface{}, []map[string]interface{}, etc.)
//
// Returns:
//   - []config.TestBlock: The converted blocks
//   - error: Any parse error from converting tasks to blocks
func convertTasksToBlocks(tasks interface{}) ([]config.TestBlock, error) {
	if tasks == nil {
		return nil, nil
	}

	// Marshal to JSON then unmarshal to []TestBlock
	// This handles the type conversion from interface{} to the concrete struct
	data, err := json.Marshal(tasks)
	if err != nil {
		return nil, fmt.Errorf("marshal tasks: %w", err)
	}

	var blocks []config.TestBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, fmt.Errorf("unmarshal blocks: %w", err)
	}

	return blocks, nil
}

// stripBlockIDs removes server-generated IDs from blocks recursively.
//
// Block IDs are computed server-side from the test_id and the block's
// semantic path (position in the hierarchy). They should not be stored
// in local YAML files because:
//   - IDs are noise for users editing tests
//   - They cause merge conflicts across branches
//   - They pollute diffs with irrelevant changes
//
// Parameters:
//   - blocks: The blocks to strip IDs from
//
// Returns:
//   - []config.TestBlock: Blocks with IDs cleared
func stripBlockIDs(blocks []config.TestBlock) []config.TestBlock {
	result := make([]config.TestBlock, len(blocks))
	for i, block := range blocks {
		result[i] = block
		result[i].ID = ""       // Server-generated; noise in YAML
		result[i].StepType = "" // Always inferrable from type; omit for cleaner YAML

		if len(block.Then) > 0 {
			result[i].Then = stripBlockIDs(block.Then)
		}
		if len(block.Else) > 0 {
			result[i].Else = stripBlockIDs(block.Else)
		}
		if len(block.Body) > 0 {
			result[i].Body = stripBlockIDs(block.Body)
		}
	}
	return result
}

// ensureDir creates a directory if it doesn't exist.
//
// Parameters:
//   - path: The directory path to create
//
// Returns:
//   - error: Any error that occurred during creation
func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}
