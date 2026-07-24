package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/revyl/cli/internal/auth"
)

// TestDevProfileStartsWithoutCredentials verifies deferred local authentication and structured errors.
func TestDevProfileStartsWithoutCredentials(t *testing.T) {
	prepareServerAuthTest(t)
	runner := &fakeDevLoopRunner{}

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() without credentials: %v", err)
	}
	if got := server.apiClient.GetAPIKey(); got != "" {
		t.Fatalf("initial API key = %q, want empty", got)
	}
	tools := listServerTools(t, server)
	if len(tools) != 11 {
		t.Fatalf("dev tool count without credentials = %d, want 11", len(tools))
	}
	serverToolByName(t, tools, "setup_status")

	statusResult := callServerTool(t, server, "setup_status", nil)
	if statusResult.IsError {
		t.Fatalf("setup_status result = %+v, want success", statusResult)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](t, statusResult)
	if status.Ready || status.AuthState != authenticationStateRequired {
		t.Fatalf("setup status = %+v, want local auth required", status)
	}
	if status.Environment != setupEnvironmentLocal || status.ProjectState != projectStateNotInitialized {
		t.Fatalf("setup environment/project = %q/%q", status.Environment, status.ProjectState)
	}
	if status.Remediation == nil || status.Remediation.ActionKind != remediationActionCommand ||
		status.Remediation.Command != "revyl auth login" || status.Remediation.RestartRequired {
		t.Fatalf("setup remediation = %+v, want local login command", status.Remediation)
	}

	startResult := callServerTool(t, server, "start_dev_loop", nil)
	if !startResult.IsError {
		t.Fatalf("start_dev_loop result = %+v, want auth error", startResult)
	}
	start := decodeStructuredToolResult[DevLoopStartOutput](t, startResult)
	if start.Outcome.OutcomeCode != string(authenticationStateRequired) {
		t.Fatalf("start outcome = %+v, want local auth required", start.Outcome)
	}
	requireRemediationParity(t, start.Remediation, status.Remediation)
	requireRunnerStartCalls(t, runner, 0)

	screenshotResult := callServerTool(t, server, "screenshot", nil)
	if !screenshotResult.IsError {
		t.Fatalf("screenshot result = %+v, want auth error", screenshotResult)
	}
	screenshot := decodeStructuredToolResult[ScreenshotOutput](t, screenshotResult)
	if screenshot.ErrorCode != string(authenticationStateRequired) {
		t.Fatalf("screenshot error code = %q, want %q", screenshot.ErrorCode, authenticationStateRequired)
	}
}

// TestDevProfileReportsExpiredCredentials verifies stale browser auth remains distinguishable.
func TestDevProfileReportsExpiredCredentials(t *testing.T) {
	prepareServerAuthTest(t)
	expiresAt := time.Now().Add(-time.Hour)
	runner := &fakeDevLoopRunner{}
	saveServerCredentials(t, &auth.Credentials{
		AccessToken: "expired-access-token",
		ExpiresAt:   &expiresAt,
		AuthMethod:  "browser",
	})

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() with expired credentials: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.AuthState != authenticationStateExpired {
		t.Fatalf("auth state = %q, want %q", status.AuthState, authenticationStateExpired)
	}
	if status.Remediation == nil ||
		status.Remediation.ActionKind != remediationActionCommand ||
		status.Remediation.Command != "revyl auth login" ||
		status.Remediation.RestartRequired {
		t.Fatalf("expired remediation = %+v, want login without restart", status.Remediation)
	}

	startResult := callServerTool(t, server, "start_dev_loop", nil)
	if !startResult.IsError {
		t.Fatalf("start_dev_loop result = %+v, want expired auth error", startResult)
	}
	start := decodeStructuredToolResult[DevLoopStartOutput](t, startResult)
	if start.Outcome.OutcomeCode != string(authenticationStateExpired) {
		t.Fatalf("start outcome = %+v, want expired auth", start.Outcome)
	}
	requireRemediationParity(t, start.Remediation, status.Remediation)
	requireJSONSecretFree(t, start, "expired-access-token")
	requireRunnerStartCalls(t, runner, 0)

	result := callServerTool(t, server, "get_dev_status", nil)
	if !result.IsError {
		t.Fatalf("get_dev_status result = %+v, want auth error", result)
	}
	output := decodeStructuredToolResult[GetDevStatusOutput](t, result)
	if output.Outcome.OutcomeCode != string(authenticationStateExpired) {
		t.Fatalf("outcome code = %q, want %q", output.Outcome.OutcomeCode, authenticationStateExpired)
	}
}

// TestDevProfileReportsCloudSecretRequirement verifies Cloud remediation never suggests browser login.
func TestDevProfileReportsCloudSecretRequirement(t *testing.T) {
	prepareServerAuthTest(t)
	if err := auth.NewManager().SaveCloudRuntimeContext("", false); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}
	runner := &fakeDevLoopRunner{}

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() without Cloud secret: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.AuthState != authenticationStateCloudSecretRequired || status.Environment != setupEnvironmentCloud {
		t.Fatalf("cloud setup status = %+v", status)
	}
	if status.Remediation == nil ||
		status.Remediation.ActionKind != remediationActionEnvironmentVariable ||
		status.Remediation.EnvName != "REVYL_API_KEY" ||
		!status.Remediation.RestartRequired ||
		status.Remediation.Command != "" {
		t.Fatalf("cloud remediation = %+v, want Runtime Secret action", status.Remediation)
	}
	startResult := callServerTool(t, server, "start_dev_loop", nil)
	if !startResult.IsError {
		t.Fatalf("start_dev_loop result = %+v, want Cloud auth error", startResult)
	}
	start := decodeStructuredToolResult[DevLoopStartOutput](t, startResult)
	if start.Outcome.OutcomeCode != string(authenticationStateCloudSecretRequired) {
		t.Fatalf("start outcome = %+v, want Cloud secret required", start.Outcome)
	}
	requireRemediationParity(t, start.Remediation, status.Remediation)
	requireRunnerStartCalls(t, runner, 0)

	screenshot := decodeStructuredToolResult[ScreenshotOutput](
		t,
		callServerTool(t, server, "screenshot", nil),
	)
	if screenshot.ErrorCode != string(authenticationStateCloudSecretRequired) {
		t.Fatalf("cloud screenshot error code = %q", screenshot.ErrorCode)
	}
}

// TestDevProfileAcceptsPersistedCloudAPIKey verifies the bootstrap bridge works without MCP env propagation.
func TestDevProfileAcceptsPersistedCloudAPIKey(t *testing.T) {
	prepareServerAuthTest(t)
	projectDir := os.Getenv("REVYL_PROJECT_DIR")
	writeDevLoopProjectAt(t, projectDir)
	const apiKey = "persisted-cloud-api-key"
	if err := auth.NewManager().SaveCloudRuntimeContext(apiKey, true); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}

	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer() with persisted Cloud API key: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if !status.Ready ||
		status.AuthState != authenticationStateAuthenticated ||
		status.Environment != setupEnvironmentCloud ||
		status.ProjectState != projectStateInitialized ||
		status.Remediation != nil {
		t.Fatalf("persisted Cloud setup status = %+v", status)
	}
	if got := server.apiClient.GetAPIKey(); got != apiKey {
		t.Fatalf("API key = %q, want persisted Cloud credential", got)
	}
	requireSetupStatusSecretFree(t, status, apiKey)
}

// TestDevProfileReportsPersistedCloudSecretRequirement verifies missing-secret guidance survives host filtering.
func TestDevProfileReportsPersistedCloudSecretRequirement(t *testing.T) {
	prepareServerAuthTest(t)
	if err := auth.NewManager().SaveCloudRuntimeContext("", false); err != nil {
		t.Fatalf("SaveCloudRuntimeContext() error = %v", err)
	}

	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer() with persisted Cloud context: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.AuthState != authenticationStateCloudSecretRequired ||
		status.Environment != setupEnvironmentCloud ||
		status.Remediation == nil ||
		status.Remediation.EnvName != "REVYL_API_KEY" ||
		!status.Remediation.RestartRequired {
		t.Fatalf("persisted missing-secret setup status = %+v", status)
	}
}

// TestDevProfileReportsInvalidCloudContext verifies corrupted bootstrap state has exact restart guidance.
func TestDevProfileReportsInvalidCloudContext(t *testing.T) {
	prepareServerAuthTest(t)
	contextDirectory := filepath.Join(os.Getenv("HOME"), ".revyl")
	if err := os.MkdirAll(contextDirectory, 0o700); err != nil {
		t.Fatalf("create Cloud context directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(contextDirectory, "cloud-runtime.json"),
		[]byte("not-json"),
		0o600,
	); err != nil {
		t.Fatalf("write malformed Cloud context: %v", err)
	}

	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer() with malformed Cloud context: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.AuthState != authenticationStateCloudContextInvalid ||
		status.Environment != setupEnvironmentCloud ||
		status.Remediation == nil ||
		status.Remediation.ActionKind != remediationActionRestartSession ||
		!status.Remediation.RestartRequired {
		t.Fatalf("malformed Cloud context status = %+v", status)
	}
}

// TestDevProfileKeepsNormalFileCredentialsLocal verifies ordinary CLI login does not imply Cloud execution.
func TestDevProfileKeepsNormalFileCredentialsLocal(t *testing.T) {
	prepareServerAuthTest(t)
	projectDir := os.Getenv("REVYL_PROJECT_DIR")
	writeDevLoopProjectAt(t, projectDir)
	saveServerCredentials(t, &auth.Credentials{
		APIKey:     "normal-file-api-key",
		AuthMethod: "api_key",
	})

	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer() with normal file API key: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if !status.Ready || status.Environment != setupEnvironmentLocal {
		t.Fatalf("normal file credential setup status = %+v, want ready local setup", status)
	}
}

// TestSetupStatusReportsProjectInitializationAction verifies authenticated projects receive one exact command.
func TestSetupStatusReportsProjectInitializationAction(t *testing.T) {
	prepareServerAuthTest(t)
	t.Setenv("REVYL_API_KEY", "environment-api-key")
	runner := &fakeDevLoopRunner{}

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() with environment API key: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.ProjectState != projectStateNotInitialized || status.Remediation == nil {
		t.Fatalf("project setup status = %+v", status)
	}
	if status.Remediation.ActionKind != remediationActionCommand ||
		status.Remediation.Command != "revyl init --non-interactive" ||
		status.Remediation.WorkingDirectory != os.Getenv("REVYL_PROJECT_DIR") ||
		status.Remediation.RestartRequired {
		t.Fatalf("project remediation = %+v", status.Remediation)
	}

	startResult := callServerTool(t, server, "start_dev_loop", nil)
	if !startResult.IsError {
		t.Fatalf("start_dev_loop result = %+v, want project setup error", startResult)
	}
	start := decodeStructuredToolResult[DevLoopStartOutput](t, startResult)
	if start.Outcome.OutcomeCode != "project_not_initialized" {
		t.Fatalf("start outcome = %+v, want project_not_initialized", start.Outcome)
	}
	requireRemediationParity(t, start.Remediation, status.Remediation)
	requireJSONSecretFree(t, start, "environment-api-key")
	requireRunnerStartCalls(t, runner, 0)
}

// TestPluginRuntimeRemediationsUsePinnedExecutable verifies one-install recovery avoids PATH.
func TestPluginRuntimeRemediationsUsePinnedExecutable(t *testing.T) {
	prepareServerAuthTest(t)
	executablePath := filepath.Join(t.TempDir(), "Revyl Runtime", "revyl")
	t.Setenv(remediationExecutableEnvironment, executablePath)
	executableCommand := quoteRemediationExecutable(executablePath)

	authRemediation := authenticationRemediation(authenticationStateRequired)
	if authRemediation == nil ||
		authRemediation.Command != executableCommand+" auth login" {
		t.Fatalf("auth remediation = %+v, want plugin runtime executable", authRemediation)
	}

	projectStatus := resolveSetupProjectState(os.Getenv("REVYL_PROJECT_DIR"))
	if projectStatus.Remediation == nil ||
		projectStatus.Remediation.Command != executableCommand+" init --non-interactive" {
		t.Fatalf("project remediation = %+v, want plugin runtime executable", projectStatus.Remediation)
	}
}

// TestSetupStatusReportsAmbiguousProjectRemediation verifies deterministic project selection guidance.
func TestSetupStatusReportsAmbiguousProjectRemediation(t *testing.T) {
	prepareServerAuthTest(t)
	workDir := os.Getenv("REVYL_PROJECT_DIR")
	alphaRoot := filepath.Join(workDir, "apps", "alpha")
	zetaRoot := filepath.Join(workDir, "apps", "zeta")
	writeDevLoopProjectAt(t, zetaRoot)
	writeDevLoopProjectAt(t, alphaRoot)
	runner := &fakeDevLoopRunner{}

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() without credentials: %v", err)
	}
	setupTool := serverToolByName(t, listServerTools(t, server), "setup_status")
	requireConcreteToolSchema(t, setupTool)

	unauthenticatedStatus := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if unauthenticatedStatus.ProjectState != projectStateAmbiguous ||
		unauthenticatedStatus.Remediation == nil ||
		unauthenticatedStatus.Remediation.ActionKind != remediationActionCommand {
		t.Fatalf("unauthenticated ambiguous status = %+v, want auth remediation precedence", unauthenticatedStatus)
	}

	const apiKey = "ambiguous-project-api-key"
	t.Setenv("REVYL_API_KEY", apiKey)
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.Ready || status.ProjectState != projectStateAmbiguous || status.Remediation == nil {
		t.Fatalf("ambiguous setup status = %+v", status)
	}
	if status.Remediation.ActionKind != remediationActionSelectProjectDir ||
		status.Remediation.WorkingDirectory != workDir ||
		status.Remediation.Command != "" ||
		status.Remediation.EnvName != "" ||
		status.Remediation.ConfigPath != "" ||
		status.Remediation.RestartRequired {
		t.Fatalf("ambiguous remediation = %+v", status.Remediation)
	}
	expectedRoots := []string{alphaRoot, zetaRoot}
	if !slices.Equal(status.Remediation.CandidateRoots, expectedRoots) {
		t.Fatalf("candidate roots = %v, want %v", status.Remediation.CandidateRoots, expectedRoots)
	}
	requireSetupStatusSecretFree(t, status, apiKey)

	startResult := callServerTool(t, server, "start_dev_loop", nil)
	if !startResult.IsError {
		t.Fatalf("start_dev_loop result = %+v, want ambiguous project error", startResult)
	}
	start := decodeStructuredToolResult[DevLoopStartOutput](t, startResult)
	if start.Outcome.OutcomeCode != "project_ambiguous" {
		t.Fatalf("start outcome = %+v, want project_ambiguous", start.Outcome)
	}
	requireRemediationParity(t, start.Remediation, status.Remediation)
	requireJSONSecretFree(t, start, apiKey)
	requireRunnerStartCalls(t, runner, 0)
}

// TestSetupStatusReportsInvalidProjectRemediation verifies malformed config repair guidance.
func TestSetupStatusReportsInvalidProjectRemediation(t *testing.T) {
	prepareServerAuthTest(t)
	workDir := os.Getenv("REVYL_PROJECT_DIR")
	revylDir := filepath.Join(workDir, ".revyl")
	if err := os.MkdirAll(revylDir, 0o755); err != nil {
		t.Fatalf("create .revyl directory: %v", err)
	}
	configPath := filepath.Join(revylDir, "config.yaml")
	const invalidConfig = "project: [invalid"
	if err := os.WriteFile(configPath, []byte(invalidConfig), 0o600); err != nil {
		t.Fatalf("write invalid project config: %v", err)
	}
	const apiKey = "invalid-project-api-key"
	t.Setenv("REVYL_API_KEY", apiKey)
	runner := &fakeDevLoopRunner{}

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() with invalid project config: %v", err)
	}
	setupTool := serverToolByName(t, listServerTools(t, server), "setup_status")
	requireConcreteToolSchema(t, setupTool)
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if status.Ready || status.ProjectState != projectStateInvalid || status.Remediation == nil {
		t.Fatalf("invalid setup status = %+v", status)
	}
	if status.Remediation.ActionKind != remediationActionRepairProjectConfig ||
		status.Remediation.ConfigPath != configPath ||
		status.Remediation.WorkingDirectory != workDir ||
		status.Remediation.Command != "" ||
		status.Remediation.EnvName != "" ||
		len(status.Remediation.CandidateRoots) != 0 ||
		status.Remediation.RestartRequired {
		t.Fatalf("invalid remediation = %+v", status.Remediation)
	}
	requireSetupStatusSecretFree(t, status, apiKey)
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal invalid setup status: %v", err)
	}
	if strings.Contains(string(encodedStatus), "--force") {
		t.Fatal("invalid project remediation suggested --force")
	}

	startResult := callServerTool(t, server, "start_dev_loop", nil)
	if !startResult.IsError {
		t.Fatalf("start_dev_loop result = %+v, want invalid project error", startResult)
	}
	start := decodeStructuredToolResult[DevLoopStartOutput](t, startResult)
	if start.Outcome.OutcomeCode != "project_invalid" {
		t.Fatalf("start outcome = %+v, want project_invalid", start.Outcome)
	}
	requireRemediationParity(t, start.Remediation, status.Remediation)
	requireJSONSecretFree(t, start, apiKey)
	requireRunnerStartCalls(t, runner, 0)
	encodedStart, err := json.Marshal(start)
	if err != nil {
		t.Fatalf("marshal invalid start output: %v", err)
	}
	if strings.Contains(string(encodedStart), "--force") {
		t.Fatal("start_dev_loop invalid project remediation suggested --force")
	}

	configAfterStatus, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read invalid project config after setup_status: %v", err)
	}
	if string(configAfterStatus) != invalidConfig {
		t.Fatal("setup_status mutated the invalid project config")
	}
}

// TestDevProfileAcceptsEnvironmentAPIKey verifies env credentials can make setup ready.
func TestDevProfileAcceptsEnvironmentAPIKey(t *testing.T) {
	prepareServerAuthTest(t)
	projectDir := os.Getenv("REVYL_PROJECT_DIR")
	writeDevLoopProjectAt(t, projectDir)
	t.Setenv("REVYL_API_KEY", "environment-api-key")

	server, err := NewServer("test", false, WithProfile(ProfileDev))
	if err != nil {
		t.Fatalf("NewServer() with environment API key: %v", err)
	}
	status := decodeStructuredToolResult[SetupStatusOutput](
		t,
		callServerTool(t, server, "setup_status", nil),
	)
	if !status.Ready ||
		status.AuthState != authenticationStateAuthenticated ||
		status.ProjectState != projectStateInitialized ||
		status.Remediation != nil {
		t.Fatalf("ready setup status = %+v", status)
	}
	if got := server.apiClient.GetAPIKey(); got != "environment-api-key" {
		t.Fatalf("API key = %q, want environment credential", got)
	}
	requireSetupStatusSecretFree(t, status, "environment-api-key")
}

// TestDevProfileRefreshesCredentialsWithoutResettingSessions verifies post-startup login is applied in place.
func TestDevProfileRefreshesCredentialsWithoutResettingSessions(t *testing.T) {
	prepareServerAuthTest(t)
	projectDir := os.Getenv("REVYL_PROJECT_DIR")
	writeDevLoopProjectAt(t, projectDir)
	runner := &fakeDevLoopRunner{}

	server, err := NewServer(
		"test",
		false,
		WithProfile(ProfileDev),
		WithDevLoopRunner(runner),
	)
	if err != nil {
		t.Fatalf("NewServer() without credentials: %v", err)
	}
	sessionManager := server.sessionMgr
	sessionManager.mu.Lock()
	sessionManager.sessions[3] = &DeviceSession{Index: 3, SessionID: "existing-session"}
	sessionManager.activeIndex = 3
	sessionManager.mu.Unlock()

	saveServerCredentials(t, &auth.Credentials{
		APIKey:     "post-startup-api-key",
		AuthMethod: "api_key",
	})
	result := callServerTool(t, server, "get_dev_status", map[string]any{
		"project_dir": projectDir,
	})
	if result.IsError {
		t.Fatalf("get_dev_status after login = %+v, want success", result)
	}
	if got := server.apiClient.GetAPIKey(); got != "post-startup-api-key" {
		t.Fatalf("refreshed API key = %q, want post-startup credential", got)
	}
	runner.mu.Lock()
	statusCalls := runner.statusCallCount
	runner.mu.Unlock()
	if statusCalls != 1 {
		t.Fatalf("status calls after credential refresh = %d, want 1", statusCalls)
	}
	if server.sessionMgr != sessionManager {
		t.Fatal("credential refresh replaced the device session manager")
	}
	sessionManager.mu.RLock()
	existingSession := sessionManager.sessions[3]
	activeIndex := sessionManager.activeIndex
	sessionManager.mu.RUnlock()
	if existingSession == nil || existingSession.SessionID != "existing-session" || activeIndex != 3 {
		t.Fatalf("session state changed after credential refresh: session=%+v active=%d", existingSession, activeIndex)
	}
}

// callServerTool invokes one tool against an initialized in-memory MCP server.
//
// Parameters:
//   - t: Active test.
//   - server: MCP server under test.
//   - name: Exact tool name.
//   - arguments: Structured tool arguments.
//
// Returns:
//   - *mcpsdk.CallToolResult: Client-visible tool result.
func callServerTool(
	t *testing.T,
	server *Server,
	name string,
	arguments map[string]any,
) *mcpsdk.CallToolResult {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.mcpServer.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "setup-test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return result
}

// decodeStructuredToolResult converts an MCP structured result into its typed contract.
//
// Parameters:
//   - t: Active test.
//   - result: MCP call result to decode.
//
// Returns:
//   - T: Decoded structured result.
func decodeStructuredToolResult[T any](t *testing.T, result *mcpsdk.CallToolResult) T {
	t.Helper()
	var output T
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured result: %v", err)
	}
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatalf("decode structured result: %v", err)
	}
	return output
}

// requireSetupStatusSecretFree verifies setup output does not contain credential values.
//
// Parameters:
//   - t: Active test.
//   - status: Typed setup status to inspect.
//   - secretValues: Credential fixtures that must never be returned.
func requireSetupStatusSecretFree(t *testing.T, status SetupStatusOutput, secretValues ...string) {
	t.Helper()
	requireJSONSecretFree(t, status, secretValues...)
}

// requireJSONSecretFree verifies a structured output does not contain credential fixtures.
//
// Parameters:
//   - t: Active test.
//   - value: Structured output to inspect.
//   - secretValues: Credential fixtures that must never be returned.
func requireJSONSecretFree(t *testing.T, value any, secretValues ...string) {
	t.Helper()
	encodedValue, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal secret-free output: %v", err)
	}
	for _, secretValue := range secretValues {
		if secretValue != "" && strings.Contains(string(encodedValue), secretValue) {
			t.Fatal("structured output exposed a credential fixture")
		}
	}
}

// requireRemediationParity verifies two outputs expose the same recovery action.
//
// Parameters:
//   - t: Active test.
//   - actual: Remediation returned by the operation under test.
//   - expected: Canonical remediation returned by setup_status.
func requireRemediationParity(t *testing.T, actual, expected *Remediation) {
	t.Helper()
	if actual == nil || expected == nil {
		if actual != expected {
			t.Fatalf("remediation parity mismatch: actual=%+v expected=%+v", actual, expected)
		}
		return
	}
	if actual.ActionKind != expected.ActionKind ||
		actual.Command != expected.Command ||
		actual.EnvName != expected.EnvName ||
		actual.WorkingDirectory != expected.WorkingDirectory ||
		!slices.Equal(actual.CandidateRoots, expected.CandidateRoots) ||
		actual.ConfigPath != expected.ConfigPath ||
		actual.RestartRequired != expected.RestartRequired {
		t.Fatalf("remediation parity mismatch: actual=%+v expected=%+v", actual, expected)
	}
}

// requireRunnerStartCalls verifies setup failures do not cross the runner boundary.
//
// Parameters:
//   - t: Active test.
//   - runner: Fake runner whose call count is inspected.
//   - expected: Required number of Start calls.
func requireRunnerStartCalls(t *testing.T, runner *fakeDevLoopRunner, expected int) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.startCallCount != expected {
		t.Fatalf("runner Start calls = %d, want %d", runner.startCallCount, expected)
	}
}
