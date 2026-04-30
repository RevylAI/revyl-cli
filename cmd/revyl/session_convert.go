package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/execution"
	"github.com/revyl/cli/internal/ui"
)

const sessionCompilePollInterval = 1500 * time.Millisecond

type sessionConvertResult struct {
	SessionID            string   `json:"session_id"`
	JobID                string   `json:"job_id"`
	TestID               string   `json:"test_id,omitempty"`
	TestName             string   `json:"test_name"`
	TestURL              string   `json:"test_url,omitempty"`
	Platform             string   `json:"platform"`
	AppID                string   `json:"app_id,omitempty"`
	BlocksCount          int      `json:"blocks_count"`
	LocalPath            string   `json:"local_path,omitempty"`
	Warnings             []string `json:"warnings,omitempty"`
	DryRun               bool     `json:"dry_run,omitempty"`
	TotalActionsCompiled int      `json:"total_actions_compiled,omitempty"`
}

func runCreateTestFromSession(cmd *cobra.Command, args []string) error {
	sessionID := strings.TrimSpace(createTestFromSession)
	if sessionID == "" {
		return fmt.Errorf("--from-session requires a session id")
	}
	if createTestInteractive {
		return fmt.Errorf("--interactive cannot be combined with --from-session")
	}
	if createTestHotReload {
		return fmt.Errorf("hot reload test creation cannot be combined with --from-session")
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}
	devMode, _ := cmd.Root().PersistentFlags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	repoRoot := resolveSessionConvertRepoRoot()
	cfg := loadSessionConvertConfig(repoRoot)
	testsDir := filepath.Join(repoRoot, ".revyl", "tests")

	session, err := client.GetDeviceSessionByID(cmd.Context(), sessionID)
	if err != nil {
		return fmt.Errorf("failed to load session %s: %w", sessionID, err)
	}

	platform := resolveSessionConvertPlatform(createTestPlatform, session)
	if platform != "ios" && platform != "android" {
		return fmt.Errorf("could not resolve platform for session %s: pass --platform ios or --platform android", sessionID)
	}

	appID := resolveSessionConvertAppID(cfg, session, platform)
	if appID == "" {
		return fmt.Errorf("app is required for session conversion: pass --app <app-id> or configure a default %s app in .revyl/config.yaml", platform)
	}

	appName, err := resolveSessionConvertAppName(cmd.Context(), client, session, appID)
	if err != nil {
		return err
	}
	pinnedVersion := metadataString(session.SourceMetadata, "build_version")

	if !jsonOutput {
		ui.PrintInfo("Compiling session %s into test steps...", sessionID)
	}

	startResp, err := client.StartRecordingCompile(cmd.Context(), &api.CompileRecordingRequest{
		SourceType: api.Session,
		SourceId:   sessionID,
	})
	if err != nil {
		return fmt.Errorf("failed to start session compilation: %w", err)
	}

	status, err := waitForSessionCompile(cmd.Context(), client, startResp.JobId, time.Duration(createTestCompileTimeout)*time.Second, jsonOutput)
	if err != nil {
		return err
	}
	if status.Result == nil {
		return fmt.Errorf("session compilation finished without a result")
	}
	if status.Result.Blocks == nil || len(*status.Result.Blocks) == 0 {
		return fmt.Errorf("session compilation produced no test blocks")
	}

	blocks := normalizeCompiledBlocksForCreate(*status.Result.Blocks)
	testName := resolveSessionConvertTestName(args, status.Result, appName)
	if err := validateResourceName(testName, "test"); err != nil {
		return err
	}

	if err := ensureSessionConvertLocalPathAvailable(testsDir, testName); err != nil {
		return err
	}

	metadata := map[string]interface{}{
		"name":     testName,
		"platform": platform,
		"app_id":   appID,
	}
	if pinnedVersion != "" {
		metadata["pinned_version"] = pinnedVersion
	}

	totalActionsCompiled := 0
	if status.Result.TotalActionsCompiled != nil {
		totalActionsCompiled = *status.Result.TotalActionsCompiled
	}
	warnings := []string{}
	if status.Result.Warnings != nil {
		warnings = *status.Result.Warnings
	}

	result := sessionConvertResult{
		SessionID:            sessionID,
		JobID:                startResp.JobId,
		TestName:             testName,
		Platform:             platform,
		AppID:                appID,
		BlocksCount:          len(blocks),
		Warnings:             warnings,
		DryRun:               createTestDryRun,
		TotalActionsCompiled: totalActionsCompiled,
	}

	if createTestDryRun {
		if jsonOutput {
			return printSessionConvertJSON(result)
		}
		ui.PrintSuccess("Compiled %d block(s) from %d action(s)", len(blocks), totalActionsCompiled)
		ui.PrintInfo("Dry-run mode - no test created")
		ui.PrintInfo("Test Name: %s", testName)
		ui.PrintInfo("Platform:  %s", platform)
		ui.PrintInfo("App:       %s (%s)", appName, appID)
		return nil
	}

	if !jsonOutput {
		ui.PrintInfo("Creating test '%s' (%s)...", testName, platform)
	}
	createResp, err := client.CreateTestFromBlocks(cmd.Context(), &api.CreateTestFromBlocksRequest{
		Blocks:   blocks,
		Metadata: metadata,
	})
	if err != nil {
		return fmt.Errorf("failed to create test from session: %w", err)
	}
	if !createResp.Success {
		message := strings.Join(createResp.Errors, "; ")
		if message == "" {
			message = "backend returned success=false"
		}
		return fmt.Errorf("failed to create test from session: %s", message)
	}

	if len(createTestTags) > 0 {
		_, tagErr := client.SyncTestTags(cmd.Context(), createResp.TestID, &api.CLISyncTagsRequest{
			TagNames: createTestTags,
		})
		if tagErr != nil && !jsonOutput {
			ui.PrintWarning("Failed to assign tags: %v", tagErr)
		} else if tagErr == nil && !jsonOutput {
			ui.PrintSuccess("Tagged: %s", strings.Join(createTestTags, ", "))
		}
	}

	localPath, err := saveSessionConvertedLocalTest(testsDir, testName, platform, appName, pinnedVersion, createResp.TestID, createTestTags, blocks)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to save local YAML: %v", err))
		if !jsonOutput {
			ui.PrintWarning("Failed to save local YAML: %v", err)
		}
	}
	if err == nil {
		result.LocalPath = localPath
	}

	testURL := fmt.Sprintf("%s/tests/execute?testUid=%s", config.GetAppURL(devMode), url.QueryEscape(createResp.TestID))
	result.TestID = createResp.TestID
	result.TestURL = testURL

	if jsonOutput {
		return printSessionConvertJSON(result)
	}

	ui.PrintSuccess("Created test: %s (id: %s)", testName, createResp.TestID)
	if localPath != "" {
		ui.PrintSuccess("Saved %s", localPath)
	}
	for _, warning := range warnings {
		ui.PrintWarning("%s", warning)
	}

	ui.Println()
	if !createTestNoOpen {
		ui.PrintInfo("Opening test...")
		ui.PrintLink("Test", testURL)
		if err := ui.OpenBrowser(testURL); err != nil {
			ui.PrintWarning("Could not open browser: %v", err)
			ui.PrintInfo("Open manually: %s", testURL)
		}
	} else {
		ui.PrintInfo("Test URL: %s", testURL)
	}

	return nil
}

func waitForSessionCompile(ctx context.Context, client *api.Client, jobID string, timeout time.Duration, quiet bool) (*api.CompileRecordingStatusResponse, error) {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(sessionCompilePollInterval)
	defer ticker.Stop()

	lastStage := ""
	for {
		status, err := client.GetRecordingCompileStatus(ctx, jobID)
		if err != nil {
			return nil, fmt.Errorf("failed to poll session compilation: %w", err)
		}
		if !quiet && status.Status != lastStage {
			lastStage = status.Status
			ui.PrintInfo("Compile status: %s", status.Status)
		}
		switch status.Status {
		case string(api.CompileJobStatusCompleted):
			return status, nil
		case string(api.CompileJobStatusFailed):
			message := "session compilation failed"
			if status.Error != nil && strings.TrimSpace(*status.Error) != "" {
				message = *status.Error
			}
			return nil, fmt.Errorf("%s", message)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("session compilation timed out after %s", timeout.Round(time.Second))
		case <-ticker.C:
		}
	}
}

func resolveSessionConvertRepoRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, err := config.FindRepoRoot(cwd); err == nil {
		return root
	}
	return cwd
}

func loadSessionConvertConfig(repoRoot string) *config.ProjectConfig {
	cfg, err := config.LoadProjectConfig(filepath.Join(repoRoot, ".revyl", "config.yaml"))
	if err != nil {
		return &config.ProjectConfig{}
	}
	return cfg
}

func resolveSessionConvertPlatform(flagValue string, session *api.DeviceSessionDetail) string {
	if platform := strings.ToLower(strings.TrimSpace(flagValue)); platform != "" {
		return platform
	}
	if session != nil && session.Platform != nil {
		return strings.ToLower(strings.TrimSpace(*session.Platform))
	}
	if session != nil {
		return strings.ToLower(strings.TrimSpace(metadataString(session.SourceMetadata, "platform")))
	}
	return ""
}

func resolveSessionConvertAppID(cfg *config.ProjectConfig, session *api.DeviceSessionDetail, platform string) string {
	if appID := strings.TrimSpace(createTestAppID); appID != "" {
		return appID
	}
	if session != nil {
		if appID := metadataString(session.SourceMetadata, "app_id"); appID != "" {
			return appID
		}
	}
	return execution.ResolveConfiguredAppID(cfg, platform)
}

func resolveSessionConvertAppName(ctx context.Context, client *api.Client, session *api.DeviceSessionDetail, appID string) (string, error) {
	if session != nil {
		if name := metadataString(session.SourceMetadata, "app_name"); name != "" {
			return name, nil
		}
	}

	appInfo, err := client.GetApp(ctx, appID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve app %s: %w", appID, err)
	}
	if strings.TrimSpace(appInfo.Name) == "" {
		return "", fmt.Errorf("app %s has no name", appID)
	}
	return appInfo.Name, nil
}

func resolveSessionConvertTestName(args []string, result *api.CompileResult, appName string) string {
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		return strings.TrimSpace(args[0])
	}
	if result != nil && result.SuggestedTitle != nil && strings.TrimSpace(*result.SuggestedTitle) != "" {
		return strings.TrimSpace(*result.SuggestedTitle)
	}
	if appName != "" {
		return appName + " Session Test"
	}
	return "Compiled Session Test"
}

func metadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return strings.TrimSpace(str)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func normalizeCompiledBlocksForCreate(blocks []map[string]interface{}) []map[string]interface{} {
	normalized := make([]map[string]interface{}, 0, len(blocks))
	for _, block := range blocks {
		normalized = append(normalized, normalizeCompiledBlockForCreate(block))
	}
	return normalized
}

func normalizeCompiledBlockForCreate(block map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(block)+1)
	for key, value := range block {
		out[key] = value
	}

	switch strings.ToLower(strings.TrimSpace(metadataString(out, "type"))) {
	case "manual", "validation", "extraction", "code_execution", "module_import", "if", "while":
	default:
		out["type"] = "instructions"
	}

	if _, ok := out["step_description"].(string); !ok {
		out["step_description"] = ""
	}

	return out
}

func ensureSessionConvertLocalPathAvailable(testsDir, testName string) error {
	if createTestForce {
		return nil
	}
	path := filepath.Join(testsDir, testName+".yaml")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("test %q already exists locally at %s (use --force to overwrite local YAML)", testName, path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect local test path %s: %w", path, err)
	}
	return nil
}

func saveSessionConvertedLocalTest(testsDir, testName, platform, appName, pinnedVersion, testID string, tags []string, blocks []map[string]interface{}) (string, error) {
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		return "", err
	}

	localBlocks, err := convertCompiledBlocksToLocalBlocks(blocks)
	if err != nil {
		return "", err
	}

	localTest := &config.LocalTest{
		Meta: config.TestMeta{RemoteID: testID},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{
				Name:     testName,
				Platform: platform,
				Tags:     tags,
			},
			Build: config.TestBuildConfig{
				Name:          appName,
				PinnedVersion: pinnedVersion,
			},
			Blocks: localBlocks,
		},
	}

	path := filepath.Join(testsDir, testName+".yaml")
	if err := config.SaveLocalTest(path, localTest); err != nil {
		return "", err
	}
	return path, nil
}

func convertCompiledBlocksToLocalBlocks(blocks []map[string]interface{}) ([]config.TestBlock, error) {
	data, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	var localBlocks []config.TestBlock
	if err := json.Unmarshal(data, &localBlocks); err != nil {
		return nil, err
	}
	return localBlocks, nil
}

func printSessionConvertJSON(result sessionConvertResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
