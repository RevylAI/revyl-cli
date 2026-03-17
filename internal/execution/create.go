// Package execution provides shared execution logic for tests and workflows.
package execution

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	yamlPkg "gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/orgguard"
	yamlvalidate "github.com/revyl/cli/internal/yaml"
)

// CreateTestParams contains parameters for creating a test.
//
// Fields:
//   - Name: Test name
//   - Platform: Target platform (ios or android)
//   - YAMLContent: Optional YAML test definition (if provided, creates with blocks)
//   - AppID: Optional app ID to associate
//   - OrgID: Organization ID
//   - DevMode: If true, use local development servers
type CreateTestParams struct {
	Name             string
	Platform         string
	YAMLContent      string
	AppID            string
	OrgID            string
	ModuleNamesOrIDs []string
	Tags             []string
	Config           *config.ProjectConfig
	AllowEmpty       bool
	DevMode          bool
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

type createTestYAMLDefinition struct {
	Test struct {
		Metadata struct {
			Name     string   `yaml:"name"`
			Platform string   `yaml:"platform"`
			Tags     []string `yaml:"tags,omitempty"`
		} `yaml:"metadata"`
		Build struct {
			Name string `yaml:"name"`
		} `yaml:"build"`
		Blocks []interface{} `yaml:"blocks"`
	} `yaml:"test"`
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
	client := api.NewClientWithDevMode(apiKey, params.DevMode)
	return CreateTestWithClient(ctx, client, params)
}

// CreateTestWithClient creates a new test using an existing API client.
//
// Parameters:
//   - ctx: Context for cancellation
//   - client: The authenticated API client
//   - params: Test creation parameters
//
// Returns:
//   - *CreateTestResult: Creation result with test ID and URL
//   - error: Any error that occurred
func CreateTestWithClient(ctx context.Context, client *api.Client, params CreateTestParams) (*CreateTestResult, error) {
	if client == nil {
		return nil, fmt.Errorf("failed to create test: authenticated client is required")
	}

	req, err := buildCreateTestRequest(ctx, client, params)
	if err != nil {
		return nil, fmt.Errorf("failed to create test: %w", err)
	}

	resp, err := client.CreateTest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create test: %w", err)
	}

	// Sync tags from YAML metadata or explicit Tags param after creation
	tags := params.Tags
	if len(tags) == 0 && params.YAMLContent != "" {
		if testDef, parseErr := parseCreateTestYAML(params.YAMLContent); parseErr == nil {
			tags = testDef.Test.Metadata.Tags
		}
	}
	if len(tags) > 0 {
		_, _ = client.SyncTestTags(ctx, resp.ID, &api.CLISyncTagsRequest{
			TagNames: tags,
		})
	}

	testURL := fmt.Sprintf(
		"%s/tests/execute?testUid=%s",
		config.GetAppURL(params.DevMode),
		url.QueryEscape(resp.ID),
	)

	return &CreateTestResult{
		TestID:   resp.ID,
		TestName: req.Name,
		TestURL:  testURL,
	}, nil
}

func buildCreateTestRequest(ctx context.Context, client *api.Client, params CreateTestParams) (*api.CreateTestRequest, error) {
	platform := normalizeCreatePlatform(params.Platform)
	// Validate platform
	if platform != "ios" && platform != "android" {
		return nil, fmt.Errorf("invalid platform '%s': must be 'ios' or 'android'", params.Platform)
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	testDef, err := parseCreateTestYAML(params.YAMLContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	moduleBlocks, err := resolveCreateTestModules(ctx, client, params.ModuleNamesOrIDs)
	if err != nil {
		return nil, err
	}
	tasks := append(moduleBlocks, testDef.Test.Blocks...)
	if len(tasks) == 0 && !params.AllowEmpty {
		return nil, fmt.Errorf("test content is required: provide yaml_content or module_names_or_ids, or use open_test_editor for manual authoring")
	}

	appInfo, err := resolveCreateTestApp(ctx, client, params.Config, platform, params.AppID, testDef.Test.Build.Name)
	if err != nil {
		return nil, err
	}
	if len(tasks) > 0 {
		if err := validateCreateTestContent(name, platform, appInfo.Name, tasks); err != nil {
			return nil, err
		}
	}

	orgID := strings.TrimSpace(params.OrgID)
	if orgID == "" {
		resolvedOrgID, err := orgguard.ResolveCreateOrgID(ctx, client, params.Config)
		if err != nil {
			return nil, err
		}
		orgID = resolvedOrgID
	}

	if len(tasks) == 0 {
		tasks = []interface{}{}
	}

	return &api.CreateTestRequest{
		Name:     name,
		Platform: platform,
		Tasks:    tasks,
		AppID:    appInfo.ID,
		OrgID:    orgID,
	}, nil
}

// ResolveConfiguredAppID returns the configured default app ID for a runtime platform.
func ResolveConfiguredAppID(cfg *config.ProjectConfig, runtimePlatform string) string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return ""
	}

	runtimePlatform = normalizeCreatePlatform(runtimePlatform)
	if runtimePlatform == "" {
		return ""
	}

	if expoCfg := cfg.HotReload.GetProviderConfig("expo"); expoCfg != nil {
		if mappedKey := strings.TrimSpace(expoCfg.PlatformKeys[runtimePlatform]); mappedKey != "" {
			if mapped, ok := cfg.Build.Platforms[mappedKey]; ok && strings.TrimSpace(mapped.AppID) != "" {
				return strings.TrimSpace(mapped.AppID)
			}
		}
	}

	if bestKey := pickBestBuildPlatformKey(cfg, runtimePlatform); bestKey != "" {
		if platformCfg, ok := cfg.Build.Platforms[bestKey]; ok && strings.TrimSpace(platformCfg.AppID) != "" {
			return strings.TrimSpace(platformCfg.AppID)
		}
	}

	if platformCfg, ok := cfg.Build.Platforms[runtimePlatform]; ok {
		return strings.TrimSpace(platformCfg.AppID)
	}

	return ""
}

func normalizeCreatePlatform(platform string) string {
	return strings.ToLower(strings.TrimSpace(platform))
}

func parseCreateTestYAML(content string) (*createTestYAMLDefinition, error) {
	def := &createTestYAMLDefinition{}
	if strings.TrimSpace(content) == "" {
		return def, nil
	}
	if err := yamlPkg.Unmarshal([]byte(content), def); err != nil {
		return nil, err
	}
	if def.Test.Blocks == nil {
		def.Test.Blocks = []interface{}{}
	}
	return def, nil
}

func resolveCreateTestModules(ctx context.Context, client *api.Client, refs []string) ([]interface{}, error) {
	blocks := make([]interface{}, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		moduleID, moduleName, err := resolveCreateTestModule(ctx, client, ref)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, map[string]interface{}{
			"type":             "module_import",
			"step_description": moduleName,
			"module_id":        moduleID,
		})
	}
	return blocks, nil
}

func resolveCreateTestModule(ctx context.Context, client *api.Client, ref string) (string, string, error) {
	if looksLikeUUID(ref) {
		resp, err := client.GetModule(ctx, ref)
		if err == nil {
			return resp.Result.ID, resp.Result.Name, nil
		}
	}

	listResp, err := client.ListModules(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to list modules: %w", err)
	}

	for _, module := range listResp.Result {
		if strings.EqualFold(strings.TrimSpace(module.Name), ref) {
			return module.ID, module.Name, nil
		}
	}

	return "", "", fmt.Errorf("module %q not found", ref)
}

func resolveCreateTestApp(ctx context.Context, client *api.Client, cfg *config.ProjectConfig, platform, explicitAppID, yamlBuildName string) (*api.App, error) {
	explicitAppID = strings.TrimSpace(explicitAppID)
	yamlBuildName = strings.TrimSpace(yamlBuildName)

	if explicitAppID != "" {
		appInfo, err := resolveAppByID(ctx, client, explicitAppID, platform, "app_id")
		if err != nil {
			return nil, err
		}
		if yamlBuildName != "" {
			buildApp, buildErr := resolveAppByName(ctx, client, platform, yamlBuildName)
			if buildErr == nil && buildApp.ID != appInfo.ID {
				return nil, fmt.Errorf("app_id %q conflicts with yaml build.name %q (resolved to %s)", explicitAppID, yamlBuildName, buildApp.ID)
			}
		}
		return appInfo, nil
	}

	if yamlBuildName != "" {
		appInfo, err := resolveAppByName(ctx, client, platform, yamlBuildName)
		if err != nil {
			return nil, err
		}
		return appInfo, nil
	}

	if configuredAppID := ResolveConfiguredAppID(cfg, platform); configuredAppID != "" {
		appInfo, err := resolveAppByID(ctx, client, configuredAppID, platform, "configured app")
		if err != nil {
			return nil, err
		}
		return appInfo, nil
	}

	return nil, fmt.Errorf("could not resolve an app for platform %s: provide app_id, include yaml_content with test.build.name, or configure a default app in .revyl/config.yaml", platform)
}

func resolveAppByID(ctx context.Context, client *api.Client, appID, platform, source string) (*api.App, error) {
	appInfo, err := client.GetApp(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("%s %q could not be resolved: %w", source, appID, err)
	}
	if normalized := normalizeCreatePlatform(appInfo.Platform); normalized != "" && normalized != platform {
		return nil, fmt.Errorf("%s %q targets platform %s, not %s", source, appID, normalized, platform)
	}
	if !appHasBuilds(appInfo) {
		return nil, fmt.Errorf("app %q has no uploaded builds. Upload a build or choose another app", appInfo.Name)
	}
	return appInfo, nil
}

func resolveAppByName(ctx context.Context, client *api.Client, platform, name string) (*api.App, error) {
	target := canonicalAppName(name)
	if target == "" {
		return nil, fmt.Errorf("yaml build.name is empty")
	}

	matches := make([]api.App, 0, 2)
	page := 1
	for {
		resp, err := client.ListApps(ctx, platform, page, 100)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s apps: %w", platform, err)
		}
		for _, appInfo := range resp.Items {
			if canonicalAppName(appInfo.Name) == target {
				matches = append(matches, appInfo)
			}
		}
		if !resp.HasNext {
			break
		}
		page++
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("yaml build.name %q did not match any %s app. Provide app_id or upload/link a build first", name, platform)
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].ID < matches[j].ID
		})
		lines := make([]string, 0, len(matches))
		for _, match := range matches {
			lines = append(lines, fmt.Sprintf("  - %s (%s)", match.ID, match.Name))
		}
		return nil, fmt.Errorf("multiple %s apps match yaml build.name %q. Use app_id instead:\n%s", platform, name, strings.Join(lines, "\n"))
	}

	if !appHasBuilds(&matches[0]) {
		return nil, fmt.Errorf("app %q has no uploaded builds. Upload a build or choose another app", matches[0].Name)
	}
	return &matches[0], nil
}

func validateCreateTestContent(name, platform, appName string, tasks []interface{}) error {
	doc := map[string]interface{}{
		"test": map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":     name,
				"platform": platform,
			},
			"build": map[string]interface{}{
				"name": appName,
			},
			"blocks": tasks,
		},
	}
	content, err := yamlPkg.Marshal(doc)
	if err != nil {
		return fmt.Errorf("failed to marshal composed test content: %w", err)
	}
	result := yamlvalidate.ValidateYAML(string(content))
	if !result.Valid {
		return fmt.Errorf("YAML validation failed: %v", result.Errors)
	}
	return nil
}

func appHasBuilds(appInfo *api.App) bool {
	if appInfo == nil {
		return false
	}
	return appInfo.VersionsCount > 0 ||
		strings.TrimSpace(appInfo.CurrentVersion) != "" ||
		strings.TrimSpace(appInfo.LatestVersion) != ""
}

func canonicalAppName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return ""
	}

	normalized := strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(lower)

	var b strings.Builder
	lastWasSpace := false
	for _, r := range normalized {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastWasSpace = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasSpace = false
		case r == ' ':
			if b.Len() > 0 && !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		default:
			if b.Len() > 0 && !lastWasSpace {
				b.WriteRune(' ')
				lastWasSpace = true
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func pickBestBuildPlatformKey(cfg *config.ProjectConfig, devicePlatform string) string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return ""
	}
	devicePlatform = normalizeCreatePlatform(devicePlatform)
	if devicePlatform != "ios" && devicePlatform != "android" {
		return ""
	}

	type candidate struct {
		key  string
		rank int
	}
	candidates := make([]candidate, 0)

	for key := range cfg.Build.Platforms {
		lower := strings.ToLower(strings.TrimSpace(key))
		if platformFromKey(lower) != devicePlatform {
			continue
		}

		rank := 50
		switch {
		case strings.Contains(lower, "dev") || strings.Contains(lower, "development"):
			rank = 0
		case lower == devicePlatform:
			rank = 1
		case strings.HasPrefix(lower, devicePlatform+"-"):
			rank = 2
		default:
			rank = 3
		}
		candidates = append(candidates, candidate{key: key, rank: rank})
	}

	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].key < candidates[j].key
	})

	return candidates[0].key
}

func platformFromKey(key string) string {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch {
	case strings.Contains(lower, "android"):
		return "android"
	case strings.Contains(lower, "ios"):
		return "ios"
	default:
		return ""
	}
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for _, idx := range []int{8, 13, 18, 23} {
		if s[idx] != '-' {
			return false
		}
	}
	return true
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
//   - cfg: Project config (unused, kept for API compatibility)
//   - params: Parameters including test name/ID
//
// Returns:
//   - *OpenTestEditorResult: Result with test ID and URL
func OpenTestEditor(_ *config.ProjectConfig, params OpenTestEditorParams) *OpenTestEditorResult {
	// Resolve test ID from local YAML
	testID := params.TestNameOrID
	cwd, err := os.Getwd()
	if err == nil {
		testsDir := filepath.Join(cwd, ".revyl", "tests")
		if id, ltErr := config.GetLocalTestRemoteID(testsDir, params.TestNameOrID); ltErr == nil && id != "" {
			testID = id
		}
	}

	testURL := fmt.Sprintf(
		"%s/tests/execute?testUid=%s",
		config.GetAppURL(params.DevMode),
		url.QueryEscape(testID),
	)

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
	workflowID := params.WorkflowNameOrID

	workflowURL := fmt.Sprintf("%s/workflows/%s", config.GetAppURL(params.DevMode), workflowID)

	return &OpenWorkflowEditorResult{
		WorkflowID:  workflowID,
		WorkflowURL: workflowURL,
	}
}
