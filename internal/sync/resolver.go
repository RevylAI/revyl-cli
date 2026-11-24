// Package sync reconciles local test files with saved tests.
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

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/orgguard"
	"github.com/revyl/cli/internal/util"
	"gopkg.in/yaml.v3"
)

type SyncStatus int

const (
	// StatusSynced means no local checksum change or newer remote version was detected.
	StatusSynced SyncStatus = iota
	StatusModified
	StatusOutdated
	StatusConflict
	StatusLocalOnly
	StatusRemoteOnly
	// StatusOrphaned means the local/config remote link is stale or inaccessible.
	StatusOrphaned
)

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

type RemoteLinkIssue string

const (
	RemoteLinkIssueNone RemoteLinkIssue = ""
	// RemoteLinkIssueMissing means the linked lookup returned HTTP 404.
	RemoteLinkIssueMissing RemoteLinkIssue = "missing"
	// RemoteLinkIssueInvalidID indicates the configured remote test ID is invalid (400).
	RemoteLinkIssueInvalidID RemoteLinkIssue = "invalid-id"
	// RemoteLinkIssueUnauthorized means the linked lookup returned HTTP 401.
	RemoteLinkIssueUnauthorized RemoteLinkIssue = "unauthorized"
	// RemoteLinkIssueForbidden indicates access to the linked test is denied (403).
	RemoteLinkIssueForbidden RemoteLinkIssue = "forbidden"
)

type TestSyncStatus struct {
	Name          string
	Status        SyncStatus
	LocalVersion  int
	RemoteVersion int
	LastSync      string
	RemoteID      string
	// LinkIssue indicates a non-fatal remote link resolution issue.
	LinkIssue        RemoteLinkIssue
	LinkIssueMessage string
	ErrorMessage     string
}

type SyncResult struct {
	Name       string
	NewVersion int
	Conflict   bool
	Error      error
	// TagSyncError is a non-fatal error from tag synchronization.
	TagSyncError error
}

type Resolver struct {
	client     *api.Client
	localTests map[string]*config.LocalTest
}

func NewResolver(client *api.Client, _ *config.ProjectConfig, localTests map[string]*config.LocalTest) *Resolver {
	if localTests == nil {
		localTests = make(map[string]*config.LocalTest)
	}

	return &Resolver{
		client:     client,
		localTests: localTests,
	}
}

const remoteStatusBatchSize = 100

// GetAllStatuses batches canonical UUID links and looks up other links individually.
func (r *Resolver) GetAllStatuses(ctx context.Context) ([]TestSyncStatus, error) {
	if len(r.localTests) == 0 {
		return nil, nil
	}

	var batchedNames, individualNames []string
	for name, localTest := range r.localTests {
		remoteID := localTest.Meta.RemoteID
		if parsed, err := uuid.Parse(remoteID); err == nil && parsed.String() == strings.ToLower(remoteID) {
			batchedNames = append(batchedNames, name)
		} else {
			individualNames = append(individualNames, name)
		}
	}
	sort.Strings(batchedNames)

	statuses := r.getIndividualStatuses(ctx, individualNames)
	for start := 0; start < len(batchedNames); start += remoteStatusBatchSize {
		end := min(start+remoteStatusBatchSize, len(batchedNames))
		statuses = append(statuses, r.getBatchedStatuses(ctx, batchedNames[start:end])...)
	}

	return statuses, nil
}

// getBatchedStatuses falls back to individual lookups for HTTP 400 or 404.
func (r *Resolver) getBatchedStatuses(ctx context.Context, names []string) []TestSyncStatus {
	remoteIDs := make([]string, 0, len(names))
	for _, name := range names {
		remoteIDs = append(remoteIDs, r.localTests[name].Meta.RemoteID)
	}

	remoteTests, err := r.client.GetExistingTestsByIDs(ctx, remoteIDs)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 400 || apiErr.StatusCode == 404) {
			return r.getIndividualStatuses(ctx, names)
		}
		statuses := make([]TestSyncStatus, 0, len(names))
		for _, name := range names {
			status := r.newLinkedStatus(name)
			if apiErr == nil || !applyRemoteLookupError(status, apiErr) {
				status = &TestSyncStatus{Name: name, Status: StatusLocalOnly, ErrorMessage: err.Error()}
			}
			statuses = append(statuses, *status)
		}
		return statuses
	}

	remoteVersionByID := make(map[string]int, len(remoteTests))
	for _, remoteTest := range remoteTests {
		if remoteTest.Id == nil {
			continue
		}
		remoteVersion := 0
		if remoteTest.Version != nil {
			remoteVersion = *remoteTest.Version
		}
		remoteVersionByID[remoteTest.Id.String()] = remoteVersion
	}

	statuses := make([]TestSyncStatus, 0, len(names))
	for _, name := range names {
		status := r.newLinkedStatus(name)
		if remoteVersion, ok := remoteVersionByID[strings.ToLower(status.RemoteID)]; ok {
			r.applyRemoteVersion(status, remoteVersion)
		} else {
			status.Status = StatusOrphaned
			status.LinkIssue = RemoteLinkIssueMissing
			status.LinkIssueMessage = "remote test not found"
		}
		statuses = append(statuses, *status)
	}
	return statuses
}

// getIndividualStatuses resolves statuses with one lookup per test and
// bounded concurrency.
func (r *Resolver) getIndividualStatuses(ctx context.Context, names []string) []TestSyncStatus {
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
	return statuses
}

func (r *Resolver) newLinkedStatus(name string) *TestSyncStatus {
	status := &TestSyncStatus{Name: name}

	localTest, hasLocal := r.localTests[name]
	if !hasLocal || localTest.Meta.RemoteID == "" {
		status.Status = StatusLocalOnly
		return status
	}

	status.RemoteID = localTest.Meta.RemoteID
	status.LocalVersion = localTest.Meta.LocalVersion
	if localTest.Meta.LastSyncedAt != "" {
		status.LastSync = formatTimeAgo(localTest.Meta.LastSyncedAt)
	} else {
		status.LastSync = "never"
	}
	return status
}

func (r *Resolver) getTestStatus(ctx context.Context, name string) (*TestSyncStatus, error) {
	status := r.newLinkedStatus(name)
	if status.RemoteID == "" {
		return status, nil
	}

	remoteTest, err := r.client.GetTest(ctx, status.RemoteID)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && applyRemoteLookupError(status, apiErr) {
			return status, nil
		}
		return nil, err
	}

	r.applyRemoteVersion(status, remoteTest.Version)
	return status, nil
}

func applyRemoteLookupError(status *TestSyncStatus, apiErr *api.APIError) bool {
	switch apiErr.StatusCode {
	case 404:
		status.LinkIssue = RemoteLinkIssueMissing
		status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "remote test not found")
	case 400:
		status.LinkIssue = RemoteLinkIssueInvalidID
		status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "invalid remote test ID")
	case 401:
		status.LinkIssue = RemoteLinkIssueUnauthorized
		status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "not authorized to access this test")
	case 403:
		status.LinkIssue = RemoteLinkIssueForbidden
		status.LinkIssueMessage = bestLinkIssueMessage(apiErr, "access denied for this test")
	default:
		return false
	}
	status.Status = StatusOrphaned
	return true
}

func (r *Resolver) applyRemoteVersion(status *TestSyncStatus, remoteVersion int) {
	status.RemoteVersion = remoteVersion

	localTest, hasLocal := r.localTests[status.Name]
	hasLocalChanges := hasLocal && localTest.HasLocalChanges()

	if !hasLocal {
		status.Status = StatusRemoteOnly
	} else if hasLocalChanges && remoteVersion > localTest.Meta.RemoteVersion {
		status.Status = StatusConflict
	} else if hasLocalChanges {
		status.Status = StatusModified
	} else if remoteVersion > localTest.Meta.RemoteVersion {
		status.Status = StatusOutdated
	} else {
		status.Status = StatusSynced
	}
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

	pushScriptNames, pushModuleNames := r.FetchNameMappings(ctx)

	for name, localTest := range testsToSync {
		result := SyncResult{Name: name}

		var resolvedAppID string
		if localTest.Test.Build.Name != "" {
			cacheKey := localTest.Test.Build.Name + "\x00" + localTest.Test.Metadata.Platform
			if cached, ok := appIDCache[cacheKey]; ok {
				resolvedAppID = cached
			} else {
				appID, err := r.resolveBuildNameToAppID(ctx, localTest.Test.Build.Name, localTest.Test.Metadata.Platform)
				if err != nil {
					result.Error = err
					results = append(results, result)
					continue
				}
				resolvedAppID = appID
				appIDCache[cacheKey] = resolvedAppID
			}
		}

		saved, err := execution.SavedBuildSelectionFromConfig(localTest.Test.Build)
		if err != nil {
			result.Error = err
			results = append(results, result)
			continue
		}
		remoteID := localTest.Meta.RemoteID

		if remoteID == "" {
			if !createOrgIDResolved {
				createOrgID, createOrgIDErr = orgguard.ResolveCreateOrgID(ctx, r.client, nil)
				createOrgIDResolved = true
			}
			if createOrgIDErr != nil {
				result.Error = createOrgIDErr
				results = append(results, result)
				continue
			}

			resolvedBlocks := deepCopyBlocks(localTest.Test.Blocks)
			if err := resolveBlockNamesForPush(resolvedBlocks, pushScriptNames, pushModuleNames); err != nil {
				result.Error = err
				results = append(results, result)
				continue
			}
			runConfig, err := r.savedRunConfigWithFileLocation(ctx, "", localTest.Test.Location)
			if err != nil {
				result.Error = err
				results = append(results, result)
				continue
			}

			resp, err := r.client.CreateTest(ctx, &api.CreateTestRequest{
				SavedBuildSelection: saved,
				PinnedVersionID:     localTest.Test.Build.PinnedVersion,
				Name:                localTest.Test.Metadata.Name,
				Platform:            localTest.Test.Metadata.Platform,
				Tasks:               resolvedBlocks,
				AppID:               resolvedAppID,
				OrgID:               createOrgID,
				RunConfig:           runConfig,
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
					result.Error = fmt.Errorf("saved v%d, but its settings failed to save: %w", resp.Version, cfgErr)
				}

				sanitized := util.SanitizeForFilename(name)
				path := filepath.Join(testsDir, sanitized+".yaml")
				if saveErr := config.SaveLocalTest(path, localTest); saveErr != nil {
					result.Error = errors.Join(result.Error, fmt.Errorf("synced but failed to save local file: %w", saveErr))
				}
			}
		} else {
			expectedVersion := 0
			if !force {
				expectedVersion = localTest.Meta.RemoteVersion
			}

			updateBlocks := deepCopyBlocks(localTest.Test.Blocks)
			if err := resolveBlockNamesForPush(updateBlocks, pushScriptNames, pushModuleNames); err != nil {
				result.Error = err
				results = append(results, result)
				continue
			}
			runConfig, err := r.savedRunConfigWithFileLocation(ctx, remoteID, localTest.Test.Location)
			if err != nil {
				result.Error = err
				results = append(results, result)
				continue
			}

			resp, err := r.client.UpdateTest(ctx, &api.UpdateTestRequest{
				SavedBuildSelection: saved,
				TestID:              remoteID,
				Name:                localTest.Test.Metadata.Name,
				Description:         localTest.Test.Metadata.Description,
				Platform:            localTest.Test.Metadata.Platform,
				Tasks:               updateBlocks,
				AppID:               resolvedAppID,
				PinnedVersionID:     localTest.Test.Build.PinnedVersion,
				ExpectedVersion:     expectedVersion,
				Force:               force,
				RunConfig:           runConfig,
			})
			if err != nil {
				if apiErr, ok := err.(*api.APIError); ok && apiErr.IsTestVersionConflict() {
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
					result.Error = fmt.Errorf("saved v%d, but its settings failed to save: %w", resp.Version, cfgErr)
				}

				sanitized := util.SanitizeForFilename(name)
				path := filepath.Join(testsDir, sanitized+".yaml")
				if saveErr := config.SaveLocalTest(path, localTest); saveErr != nil {
					result.Error = errors.Join(result.Error, fmt.Errorf("synced but failed to save local file: %w", saveErr))
				}
			}
		}

		results = append(results, result)
	}

	return results, nil
}

// resolveBuildNameToAppID looks up the app ID for a given build name and platform,
// after resolving {{global.<name>}} placeholders the way test create does.
// Paginates through all apps if needed. Returns empty string if no app matches
// (non-fatal); a placeholder that can't be resolved is an error.
func (r *Resolver) resolveBuildNameToAppID(ctx context.Context, buildName, platform string) (string, error) {
	buildName, err := execution.ResolveGlobalBuildNamePlaceholders(ctx, r.client, strings.TrimSpace(buildName))
	if err != nil {
		return "", err
	}
	buildName = strings.TrimSpace(buildName)
	if buildName == "" {
		return "", nil
	}

	page := 1
	for {
		appsResp, err := r.client.ListApps(ctx, platform, page, 100)
		if err != nil {
			return "", nil
		}

		for _, app := range appsResp.Items {
			if strings.EqualFold(app.Name, buildName) {
				return app.ID, nil
			}
		}

		if !appsResp.HasNext {
			break
		}
		page++
	}

	return "", nil
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

// syncTestConfig leaves omitted variable and launch attachment sections unchanged;
// explicit empty sections clear non-secret variables or attachments. Saved secret
// variables are never overwritten by file values. Target fields are updated only
// when at least one is set. Earlier errors can prevent replacing launch attachments;
// the joined error can describe a partial sync.
func (r *Resolver) syncTestConfig(ctx context.Context, testID string, localTest *config.LocalTest) error {
	var errs []error

	if localTest.Test.Variables != nil {
		savedVars, err := r.client.ListCustomVariables(ctx, testID)
		if err != nil {
			errs = append(errs, fmt.Errorf("list custom vars: %w", err))
		} else {
			secretNames := make(map[string]bool)
			for _, v := range savedVars.Result {
				if v.IsSecret {
					secretNames[v.VariableName] = true
					continue
				}
				if err := r.client.DeleteCustomVariable(ctx, testID, v.VariableName); err != nil {
					errs = append(errs, fmt.Errorf("delete var %s: %w", v.VariableName, err))
				}
			}
			for name, value := range localTest.Test.Variables {
				if secretNames[name] {
					continue
				}
				if _, err := r.client.AddCustomVariable(ctx, testID, name, value); err != nil {
					errs = append(errs, fmt.Errorf("add var %s: %w", name, err))
				}
			}
		}
	}

	if localTest.Test.EnvVars != nil {
		orgVarsResp, err := r.client.ListOrgLaunchVariables(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("list org launch vars: %w", err))
		} else {
			idsByKey := make(map[string]string, len(orgVarsResp.Result))
			for _, v := range orgVarsResp.Result {
				idsByKey[v.Key] = v.ID
			}

			envVarIDs := make([]string, 0, len(localTest.Test.EnvVars))
			for _, key := range localTest.Test.EnvVars {
				id, ok := idsByKey[key]
				if !ok {
					errs = append(errs, fmt.Errorf("unknown launch var key %s", key))
					continue
				}
				envVarIDs = append(envVarIDs, id)
			}

			if len(errs) == 0 {
				if _, err := r.client.ReplaceTestLaunchEnvVarAttachments(ctx, testID, envVarIDs); err != nil {
					errs = append(errs, fmt.Errorf("replace launch var attachments: %w", err))
				}
			}
		}
	}

	if localTest.Test.Device != nil {
		if localTest.Test.Device.Model != "" || localTest.Test.Device.OSVersion != "" || localTest.Test.Device.Orientation != "" {
			if err := r.client.UpdateDeviceTarget(ctx, testID, localTest.Test.Device.Model, localTest.Test.Device.OSVersion, localTest.Test.Device.Orientation); err != nil {
				errs = append(errs, fmt.Errorf("update device target: %w", err))
			}
		}
	}

	return errors.Join(errs...)
}

// savedRunConfigWithFileLocation returns the run config to save with a test
// whose file sets a location: the test's saved run config, every other
// setting kept, with the file's location. It returns nil when the file sets
// no location, so the saved run config stays as it is.
func (r *Resolver) savedRunConfigWithFileLocation(ctx context.Context, testID string, location *config.TestLocation) (map[string]interface{}, error) {
	if location == nil {
		return nil, nil
	}
	runConfig := map[string]interface{}{}
	if testID != "" {
		savedTest, err := r.client.GetTest(ctx, testID)
		if err != nil {
			return nil, fmt.Errorf("read the saved run config: %w", err)
		}
		if savedTest.RunConfig != nil {
			runConfig = savedTest.RunConfig
		}
	}
	executionMode, ok := runConfig["execution_mode"].(map[string]interface{})
	if !ok {
		executionMode = map[string]interface{}{}
		runConfig["execution_mode"] = executionMode
	}
	executionMode["initial_location"] = map[string]interface{}{
		"latitude":  location.Latitude,
		"longitude": location.Longitude,
	}
	return runConfig, nil
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

	remoteVersion, definition, err := r.remoteTestDefinition(ctx, remoteID)
	if err != nil {
		result.Error = err
		return result
	}

	localTest := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      remoteID,
			RemoteVersion: remoteVersion,
			LocalVersion:  remoteVersion,
			LastSyncedAt:  time.Now().Format(time.RFC3339),
		},
		Test: definition,
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

	result.NewVersion = remoteVersion
	r.localTests[sanitized] = localTest
	if sanitized != name {
		delete(r.localTests, name)
	}

	return result
}

func (r *Resolver) remoteTestDefinition(ctx context.Context, remoteID string) (int, config.TestDefinition, error) {
	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		return 0, config.TestDefinition{}, err
	}

	blocks, err := authoredBlocksFromTasks(remoteTest.Tasks)
	if err != nil {
		return 0, config.TestDefinition{}, err
	}

	scriptNames, moduleNames := r.FetchNameMappings(ctx)
	resolveBlockNames(blocks, scriptNames, moduleNames)

	definition := config.TestDefinition{
		Metadata: config.TestMetadata{
			Name:        remoteTest.Name,
			Platform:    strings.ToLower(remoteTest.Platform),
			Description: remoteTest.Description,
		},
		Blocks: blocks,
	}

	if remoteTest.AppID != "" {
		app, err := r.client.GetApp(ctx, remoteTest.AppID)
		if err == nil && app != nil {
			definition.Build = config.TestBuildConfig{
				Name:         app.Name,
				SystemPrompt: app.SystemPrompt,
			}
		}
	}
	// With branch reads disabled, the API projects saved pins as inherit plus pinned_version.
	legacyOnlyPin := remoteTest.PinnedVersion != "" && remoteTest.BranchID == "" && remoteTest.PinnedBuildID == "" &&
		(remoteTest.BuildSelection == "" || remoteTest.BuildSelection == api.BuildSelectionModeInherit)
	if legacyOnlyPin {
		definition.Build.PinnedVersion = remoteTest.PinnedVersion
	} else {
		definition.Build.BranchID = remoteTest.BranchID
		if remoteTest.BuildSelection != api.BuildSelectionModeLatestAllBranches {
			definition.Build.BuildSelection = string(remoteTest.BuildSelection)
		}
		definition.Build.PinnedBuildID = remoteTest.PinnedBuildID
	}

	tags, err := r.client.GetTestTags(ctx, remoteID)
	if err != nil {
		return 0, config.TestDefinition{}, fmt.Errorf("failed to read the test's tags: %w", err)
	}
	if len(tags) > 0 {
		tagNames := make([]string, len(tags))
		for i, t := range tags {
			tagNames[i] = t.Name
		}
		definition.Metadata.Tags = tagNames
	}

	// Secrets stay in Revyl, since the API never returns their values.
	varsResp, err := r.client.ListCustomVariables(ctx, remoteID)
	if err != nil {
		return 0, config.TestDefinition{}, fmt.Errorf("failed to read the test's variables: %w", err)
	}
	vars := make(map[string]string, len(varsResp.Result))
	for _, v := range varsResp.Result {
		if !v.IsSecret {
			vars[v.VariableName] = v.VariableValue
		}
	}
	if len(vars) > 0 {
		definition.Variables = vars
	}

	envResp, err := r.client.ListTestLaunchEnvVarAttachments(ctx, remoteID)
	if err != nil {
		return 0, config.TestDefinition{}, fmt.Errorf("failed to read the test's launch variables: %w", err)
	}
	if len(envResp.Result) > 0 {
		envVars := make([]string, 0, len(envResp.Result))
		for _, ev := range envResp.Result {
			envVars = append(envVars, ev.Key)
		}
		sort.Strings(envVars)
		definition.EnvVars = envVars
	}

	if len(remoteTest.MobileTargets) > 0 {
		mt := remoteTest.MobileTargets[0]
		if mt.DeviceModel != "" && mt.DeviceModel != "AUTO" {
			definition.Device = &config.TestDeviceConfig{
				Model:       mt.DeviceModel,
				OSVersion:   mt.OSVersion,
				Orientation: remoteTest.Orientation,
			}
		}
	} else if remoteTest.Orientation != "" && remoteTest.Orientation != "portrait" {
		definition.Device = &config.TestDeviceConfig{
			Orientation: remoteTest.Orientation,
		}
	}

	if executionMode, ok := remoteTest.RunConfig["execution_mode"].(map[string]interface{}); ok {
		if location, ok := executionMode["initial_location"].(map[string]interface{}); ok {
			latitude, hasLatitude := location["latitude"].(float64)
			longitude, hasLongitude := location["longitude"].(float64)
			if hasLatitude && hasLongitude {
				definition.Location = &config.TestLocation{Latitude: latitude, Longitude: longitude}
			}
		}
	}

	return remoteTest.Version, definition, nil
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

// GetDiff compares the file (-) with the saved test's pull projection (+),
// including test settings but excluding _meta.
func (r *Resolver) GetDiff(ctx context.Context, testName string) (string, error) {
	localTest, hasLocal := r.localTests[testName]
	if !hasLocal {
		return "", fmt.Errorf("local test not found: %s", testName)
	}

	remoteID := localTest.Meta.RemoteID
	if remoteID == "" {
		return "", fmt.Errorf("no remote ID for test: %s", testName)
	}

	_, remoteDefinition, err := r.remoteTestDefinition(ctx, remoteID)
	if err != nil {
		return "", err
	}

	var sides [2]string
	for i, definition := range []config.TestDefinition{localTest.Test, remoteDefinition} {
		data, err := yaml.Marshal(struct {
			Test config.TestDefinition `yaml:"test"`
		}{definition})
		if err != nil {
			return "", fmt.Errorf("failed to marshal test definition: %w", err)
		}
		sides[i] = string(data)
	}

	return generateSimpleDiff(sides[0], sides[1]), nil
}

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

func generateSimpleDiff(local, remote string) string {
	if local == remote {
		return ""
	}

	localLines := strings.Split(local, "\n")
	remoteLines := strings.Split(remote, "\n")

	var diff strings.Builder
	diff.WriteString("--- local\n")
	diff.WriteString("+++ remote\n")

	maxLen := len(localLines)
	if len(remoteLines) > maxLen {
		maxLen = len(remoteLines)
	}

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
			if currentHunk != nil {
				currentHunk.lines = append(currentHunk.lines, " "+localLines[i])
				currentHunk.localCount++
				currentHunk.remoteCount++
			}
			i++
			j++
		} else {
			if currentHunk == nil {
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

				for k := startLocal; k < i; k++ {
					currentHunk.lines = append(currentHunk.lines, " "+localLines[k])
					currentHunk.localCount++
					currentHunk.remoteCount++
				}
			}

			if i < len(localLines) && (j >= len(remoteLines) || !containsLine(remoteLines[j:], localLines[i])) {
				currentHunk.lines = append(currentHunk.lines, "-"+localLines[i])
				currentHunk.localCount++
				i++
			} else if j < len(remoteLines) {
				currentHunk.lines = append(currentHunk.lines, "+"+remoteLines[j])
				currentHunk.remoteCount++
				j++
			}
		}

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

	if currentHunk != nil {
		hunks = append(hunks, *currentHunk)
	}

	for _, h := range hunks {
		diff.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", h.localStart, h.localCount, h.remoteStart, h.remoteCount))
		for _, line := range h.lines {
			diff.WriteString(line + "\n")
		}
	}

	return diff.String()
}

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

// FetchNameMappings returns script and module ID-to-name maps, in that order.
// Each failed lookup leaves only its own map empty.
func (r *Resolver) FetchNameMappings(ctx context.Context) (map[string]string, map[string]string) {
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

// RemoteTestBlocks projects the saved blocks for an authored file, resolving
// script and module names where mappings are available.
func (r *Resolver) RemoteTestBlocks(ctx context.Context, remoteID string, scriptNames, moduleNames map[string]string) ([]config.TestBlock, error) {
	remoteTest, err := r.client.GetTest(ctx, remoteID)
	if err != nil {
		return nil, err
	}
	blocks, err := authoredBlocksFromTasks(remoteTest.Tasks)
	if err != nil {
		return nil, err
	}
	resolveBlockNames(blocks, scriptNames, moduleNames)
	return blocks, nil
}

func authoredBlocksFromTasks(tasks interface{}) ([]config.TestBlock, error) {
	blocks, err := convertTasksToBlocks(tasks)
	if err != nil {
		return nil, fmt.Errorf("parse remote test blocks: %w", err)
	}
	return stripBlockIDs(blocks), nil
}

// resolveBlockNames also handles legacy script IDs in step_description when the
// ID has a known name; unresolved references may remain.
func resolveBlockNames(blocks []config.TestBlock, scriptNames, moduleNames map[string]string) {
	for i := range blocks {
		b := &blocks[i]

		if b.Type == "code_execution" && b.StepDescription != "" {
			if name, ok := scriptNames[b.StepDescription]; ok {
				b.Script = name
				b.ScriptID = b.StepDescription
				b.StepDescription = ""
			}
		}
		if b.Type == "code_execution" && b.Script == "" && b.ScriptID != "" {
			if name, ok := scriptNames[b.ScriptID]; ok {
				b.Script = name
			}
		}
		if b.Type == "code_execution" && b.Script != "" {
			b.ScriptID = ""
		}

		if b.Type == "module_import" && b.ModuleID != "" {
			if name, ok := moduleNames[b.ModuleID]; ok {
				b.Module = name
			}
		}
		if b.Type == "module_import" && b.Module != "" {
			b.ModuleID = ""
		}

		if isDefaultStepType(b.Type, b.StepType) {
			b.StepType = ""
		}
		if (b.Type == "if" || b.Type == "while") && b.Condition != "" && b.StepDescription == b.Condition {
			b.StepDescription = ""
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

func isDefaultStepType(blockType, stepType string) bool {
	switch blockType {
	case "instructions":
		return stepType == "instruction"
	case "validation":
		return stepType == "validation"
	case "extraction":
		return stepType == "extract"
	case "code_execution":
		return stepType == "code_execution"
	case "module_import":
		return stepType == "module_import"
	case "if":
		return stepType == "decision"
	case "while":
		return stepType == "loop"
	default:
		return false
	}
}

// resolveBlockNamesForPush resolves human-readable module and script names to
// explicit UUID fields before blocks are sent to the API. Operates on the
// provided slice in-place; callers should pass a deep copy if the originals
// must stay unmodified.
//
// Parameters:
//   - blocks: The blocks to resolve (mutated in-place)
//   - scriptNames: Map of script UUID -> name (from FetchNameMappings)
//   - moduleNames: Map of module UUID -> name (from FetchNameMappings)
func resolveBlockNamesForPush(blocks []config.TestBlock, scriptNames, moduleNames map[string]string) error {
	scriptByName := make(map[string]string, len(scriptNames))
	for id, name := range scriptNames {
		scriptByName[name] = id
	}
	moduleByName := make(map[string]string, len(moduleNames))
	for id, name := range moduleNames {
		moduleByName[name] = id
	}

	var errors []string
	resolveBlockNamesForPushWalk(blocks, scriptByName, moduleByName, &errors)
	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return nil
}

func resolveBlockNamesForPushWalk(blocks []config.TestBlock, scriptByName, moduleByName map[string]string, errors *[]string) {
	for i := range blocks {
		b := &blocks[i]

		if b.Type == "module_import" && b.ModuleID == "" && b.Module != "" {
			if id, ok := moduleByName[b.Module]; ok {
				b.ModuleID = id
			} else {
				*errors = append(*errors, fmt.Sprintf("module %q not found; use an exact module name", b.Module))
			}
		}

		if b.Type == "code_execution" && b.ScriptID == "" && b.Script != "" {
			if id, ok := scriptByName[b.Script]; ok {
				b.ScriptID = id
			} else {
				*errors = append(*errors, fmt.Sprintf("script %q not found; use an exact script name", b.Script))
			}
		}

		if len(b.Then) > 0 {
			resolveBlockNamesForPushWalk(b.Then, scriptByName, moduleByName, errors)
		}
		if len(b.Else) > 0 {
			resolveBlockNamesForPushWalk(b.Else, scriptByName, moduleByName, errors)
		}
		if len(b.Body) > 0 {
			resolveBlockNamesForPushWalk(b.Body, scriptByName, moduleByName, errors)
		}
	}
}

func deepCopyBlocks(blocks []config.TestBlock) []config.TestBlock {
	out := make([]config.TestBlock, len(blocks))
	copy(out, blocks)
	for i := range out {
		if len(out[i].Then) > 0 {
			out[i].Then = deepCopyBlocks(out[i].Then)
		}
		if len(out[i].Else) > 0 {
			out[i].Else = deepCopyBlocks(out[i].Else)
		}
		if len(out[i].Body) > 0 {
			out[i].Body = deepCopyBlocks(out[i].Body)
		}
	}
	return out
}

func convertTasksToBlocks(tasks interface{}) ([]config.TestBlock, error) {
	if tasks == nil {
		return nil, nil
	}

	data, err := json.Marshal(tasks)
	if err != nil {
		return nil, fmt.Errorf("marshal tasks: %w", err)
	}
	var savedBlocks interface{}
	if err := json.Unmarshal(data, &savedBlocks); err != nil {
		return nil, fmt.Errorf("unmarshal tasks: %w", err)
	}
	nameGlobalVariables(savedBlocks)
	if data, err = json.Marshal(savedBlocks); err != nil {
		return nil, fmt.Errorf("marshal tasks: %w", err)
	}

	var blocks []config.TestBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, fmt.Errorf("unmarshal blocks: %w", err)
	}

	return blocks, nil
}

func nameGlobalVariables(savedBlocks interface{}) {
	switch value := savedBlocks.(type) {
	case map[string]interface{}:
		if name, ok := value["variable_name"].(string); ok && name != "" && value["variable_scope"] == "global" && !strings.HasPrefix(name, "global.") {
			value["variable_name"] = "global." + name
		}
		for _, child := range value {
			nameGlobalVariables(child)
		}
	case []interface{}:
		for _, child := range value {
			nameGlobalVariables(child)
		}
	}
}

// AuthoredBlocks drops server block IDs and replaces mapped module and script
// references with names.
func AuthoredBlocks(savedBlocks interface{}, scriptNames, moduleNames map[string]string) ([]config.TestBlock, error) {
	blocks, err := convertTasksToBlocks(savedBlocks)
	if err != nil {
		return nil, err
	}
	blocks = stripBlockIDs(blocks)
	resolveBlockNames(blocks, scriptNames, moduleNames)
	return blocks, nil
}

func stripBlockIDs(blocks []config.TestBlock) []config.TestBlock {
	result := make([]config.TestBlock, len(blocks))
	for i, block := range blocks {
		result[i] = block
		result[i].ID = ""

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

func ensureDir(path string) error {
	return os.MkdirAll(path, 0755)
}
