// Package main provides the doctor and ping commands for CLI diagnostics.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/backendheaders"
	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

// DoctorCheck represents a single diagnostic check result.
type DoctorCheck struct {
	// Name is the check name (e.g., "Version", "Authentication").
	Name string `json:"name"`

	// Status is the check status: "ok", "warning", "error".
	Status string `json:"status"`

	// Message is the human-readable result message.
	Message string `json:"message"`

	// Details contains additional information (optional).
	Details string `json:"details,omitempty"`

	nextStepLabel   string
	nextStepCommand string
	nextSteps       []ui.NextStep
}

// DoctorResult contains all diagnostic check results.
type DoctorResult struct {
	// Checks contains all individual check results.
	Checks []DoctorCheck `json:"checks"`

	// Issues is the count of checks with status "error" or "warning".
	Issues int `json:"issues"`

	// Healthy is true if no errors were found.
	Healthy bool `json:"healthy"`
}

var doctorOutputJSON bool

// doctorCmd runs diagnostic checks on the CLI installation.
var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check CLI health and connectivity",
	Long: `Run diagnostic checks on the Revyl CLI installation.

CHECKS PERFORMED:
  - CLI version (current vs latest available)
  - Authentication status (valid API key?)
  - API connectivity (can reach backend.revyl.ai?)
  - Project configuration (.revyl/config.yaml exists and valid?)
  - Build system detection (if in project directory)

OUTPUT:
  Human-readable by default, JSON with --json flag.

EXAMPLES:
  revyl doctor              # Run all checks
  revyl doctor --json       # Output as JSON for scripting`,
	Example: `  revyl doctor
  revyl doctor --json`,
	RunE: runDoctor,
}

// pingCmd tests API connectivity.
var pingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Test API connectivity",
	Long: `Test connectivity to the Revyl API.

This command performs a simple health check against the API
and reports the response time.

EXAMPLES:
  revyl ping           # Test production API
  revyl ping --dev     # Test local development API`,
	Example: `  revyl ping`,
	RunE:    runPing,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorOutputJSON, "json", false, "Output results as JSON")
}

// runDoctor executes all diagnostic checks.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runDoctor(cmd *cobra.Command, args []string) error {
	// Check if --json flag is set (either local or global)
	jsonOutput := doctorOutputJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	result := DoctorResult{
		Checks:  make([]DoctorCheck, 0),
		Healthy: true,
	}

	devMode, _ := cmd.Flags().GetBool("dev")

	if !jsonOutput {
		ui.PrintBanner(version)
		ui.PrintInfo("Running diagnostic checks...")
		ui.Println()
	}

	// Check 1: CLI Version
	versionCheck := checkVersion()
	result.Checks = append(result.Checks, versionCheck)
	if versionCheck.Status == "error" {
		result.Healthy = false
		result.Issues++
	} else if versionCheck.Status == "warning" {
		result.Issues++
	}

	// Check 2: Authentication
	authCheck := checkAuthentication()
	result.Checks = append(result.Checks, authCheck)
	if authCheck.Status == "error" {
		result.Healthy = false
		result.Issues++
	}

	// Check 3: API Connectivity
	apiCheck := checkAPIConnectivity(cmd.Context(), devMode)
	result.Checks = append(result.Checks, apiCheck)
	if apiCheck.Status == "error" {
		result.Healthy = false
		result.Issues++
	}

	// Check 4: Project Configuration
	projectCheck, project := inspectProjectConfig()
	result.Checks = append(result.Checks, projectCheck)
	if projectCheck.Status == "error" {
		result.Healthy = false
		result.Issues++
	}

	// Check 5: Build System
	buildCheck := checkBuildSystem()
	result.Checks = append(result.Checks, buildCheck)
	// Build system is informational only

	// Check 6: linked local tests against the remote state when authenticated.
	if project != nil {
		var syncClient *api.Client
		mgr := auth.NewManager()
		if token, tokenErr := mgr.GetActiveToken(); tokenErr == nil && token != "" {
			syncClient = api.NewClientWithDevMode(token, devMode)
		}
		syncCheck := checkSyncStatus(cmd.Context(), project, syncClient)
		result.Checks = append(result.Checks, syncCheck)
		if syncCheck.Status == "warning" {
			result.Issues++
		}
	}

	// Output results
	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		printDoctorResults(result)
	}

	if !result.Healthy {
		return analytics.CompletedWithExitCode(fmt.Errorf("health check failed"), analytics.CommandCompletion{
			ExitCode:     1,
			Domain:       "doctor",
			DomainStatus: "unhealthy",
			Properties: map[string]interface{}{
				"doctor_check_count":   len(result.Checks),
				"doctor_issue_count":   result.Issues,
				"doctor_error_count":   countDoctorChecks(result.Checks, "error"),
				"doctor_warning_count": countDoctorChecks(result.Checks, "warning"),
			},
		})
	}

	return nil
}

func countDoctorChecks(checks []DoctorCheck, status string) int {
	count := 0
	for _, check := range checks {
		if check.Status == status {
			count++
		}
	}
	return count
}

// checkVersion checks the CLI version against the latest release.
//
// Returns:
//   - DoctorCheck: The check result
func checkVersion() DoctorCheck {
	check := DoctorCheck{
		Name:   "Version",
		Status: "ok",
	}

	// For now, just report current version
	// TODO: Check against GitHub releases for latest version
	if version == "dev" {
		check.Status = "warning"
		check.Message = "Development build"
		check.Details = "Running a development build, not a released version"
	} else {
		check.Message = fmt.Sprintf("v%s", version)
		check.Details = fmt.Sprintf("Commit: %s, Built: %s", commit, date)
	}

	return check
}

// checkAuthentication checks if the user is authenticated.
//
// Returns:
//   - DoctorCheck: The check result
func checkAuthentication() DoctorCheck {
	check := DoctorCheck{
		Name:   "Authentication",
		Status: "ok",
	}

	mgr := auth.NewManager()
	creds, err := mgr.GetCredentials()

	if err != nil || creds == nil || !creds.HasValidAuth() {
		check.Status = "error"
		check.Message = "Not authenticated"
		check.Details = "Run 'revyl auth login' to authenticate"
		return check
	}

	if creds.Email != "" {
		check.Message = fmt.Sprintf("Authenticated as %s", creds.Email)
	} else if creds.UserID != "" {
		check.Message = fmt.Sprintf("Authenticated (user: %s)", creds.UserID)
	} else {
		check.Message = "Authenticated"
	}

	if creds.OrgID != "" {
		check.Details = fmt.Sprintf("Organization: %s", creds.OrgID)
	}

	return check
}

// checkAPIConnectivity tests connectivity to the Revyl API.
//
// Parameters:
//   - ctx: Context for cancellation
//   - devMode: Whether to use development server
//
// Returns:
//   - DoctorCheck: The check result
func checkAPIConnectivity(ctx context.Context, devMode bool) DoctorCheck {
	check := DoctorCheck{
		Name:   "API Connection",
		Status: "ok",
	}

	baseURL := config.GetBackendURL(devMode)
	healthURL := baseURL + "/health_check"

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		check.Status = "error"
		check.Message = "Failed to create request"
		check.Details = err.Error()
		return check
	}
	backendheaders.SetCloudAgentConversationContext(req)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		check.Status = "error"
		check.Message = "Connection failed"
		check.Details = fmt.Sprintf("Could not reach %s: %v", baseURL, err)
		return check
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		check.Status = "warning"
		check.Message = fmt.Sprintf("Unexpected status: %d", resp.StatusCode)
		check.Details = fmt.Sprintf("Latency: %dms", latency.Milliseconds())
		return check
	}

	check.Message = fmt.Sprintf("Connected (latency: %dms)", latency.Milliseconds())
	if config.HasURLOverride() {
		check.Details = fmt.Sprintf("Using custom environment: %s", baseURL)
	} else if devMode {
		check.Details = fmt.Sprintf("Using development server: %s", baseURL)
	}

	return check
}

// checkProjectConfig checks if a valid project configuration exists.
//
// Returns:
//   - DoctorCheck: The check result
func checkProjectConfig() DoctorCheck {
	check, _ := inspectProjectConfig()
	return check
}

func inspectProjectConfig() (DoctorCheck, *config.ProjectContext) {
	check := DoctorCheck{
		Name:   "Project Config",
		Status: "ok",
	}

	cwd, err := os.Getwd()
	if err != nil {
		check.Status = "error"
		check.Message = "Could not get current directory"
		check.Details = err.Error()
		return check, nil
	}

	project, err := config.ResolveProjectContext(cwd, "")
	if err != nil {
		var configErr *config.ConfigError
		if errors.As(err, &configErr) && configErr.Code == "git_worktree_unavailable" {
			check.Status = "warning"
			check.Message = "Not in a Git worktree"
			check.Details = "Run from the project directory or pass '-C <project-dir>'; for a new repository, run 'git init', then 'revyl init -y'"
			return check, nil
		}
		if errors.As(err, &configErr) && configErr.Code == "config_not_found" {
			check.Status = "warning"
			projectRoots, nestedErr := config.FindNestedProjectRoots(cwd)
			if nestedErr != nil {
				check.Status = "error"
				check.Message = "Could not inspect nested projects"
				check.Details = nestedErr.Error()
				return check, nil
			}
			if len(projectRoots) > 0 {
				check.Message = "Nested project selection required"
				commands := make([]string, 0, len(projectRoots))
				for _, projectRoot := range projectRoots {
					command := cliRecoveryCommandInDirectory(projectRoot, "doctor")
					commands = append(commands, command)
					check.nextSteps = append(check.nextSteps, ui.NextStep{Label: "Check project:", Command: command})
				}
				check.Details = "Choose a configured project: " + strings.Join(commands, " or ")
				return check, nil
			}
			check.Message = "No project configuration"
			pullCommand := cliRecoveryCommand("config", "pull")
			initCommand := cliRecoveryCommand("init", "-y")
			check.Details = fmt.Sprintf("Run %q to restore an existing project, or %q to create one", pullCommand, initCommand)
			check.nextSteps = []ui.NextStep{
				{Label: "Restore project:", Command: pullCommand},
				{Label: "Create project:", Command: initCommand},
			}
			return check, nil
		}
		check.Status = "error"
		check.Message = "Invalid project configuration"
		check.Details = actionableLocalConfigError(err).Error()
		if errors.As(err, &configErr) && (configErr.Code == "legacy_config_requires_migration" || configErr.Code == "mixed_config_formats") {
			check.nextStepLabel = "Preview migration:"
			check.nextStepCommand = cliRecoveryCommand("config", "migrate", "--check")
		}
		return check, nil
	}

	check.Message = fmt.Sprintf("Found at %s", project.ConfigPath)

	details := []string{fmt.Sprintf("Project ID: %s", project.Authored.Project.ID)}
	if linkedCount := config.CountLinkedTests(project.TestsDir); linkedCount > 0 {
		details = append(details, fmt.Sprintf("%d test(s)", linkedCount))
	}
	check.Details = strings.Join(details, ", ")

	return check, project
}

// checkBuildSystem checks if a build system is detected.
//
// Returns:
//   - DoctorCheck: The check result
func checkBuildSystem() DoctorCheck {
	check := DoctorCheck{
		Name:   "Build System",
		Status: "ok",
	}

	cwd, err := os.Getwd()
	if err != nil {
		check.Status = "warning"
		check.Message = "Could not detect"
		check.Details = err.Error()
		return check
	}

	detected, err := build.Detect(cwd)
	if err != nil || detected.System == build.SystemUnknown {
		check.Status = "warning"
		check.Message = "Not detected"
		check.Details = "Configure build settings in .revyl/config.yaml"
		return check
	}

	check.Message = fmt.Sprintf("Detected: %s", detected.System.String())
	if detected.Command != "" {
		check.Details = fmt.Sprintf("Command: %s", detected.Command)
	}

	switch detected.System {
	case build.SystemBazel:
		hasConfiguredPlatform := false
		for _, p := range detected.Platforms {
			if strings.TrimSpace(p.Command) != "" {
				hasConfiguredPlatform = true
				break
			}
		}
		if !hasConfiguredPlatform {
			check.Status = "warning"
			check.Details = "Bazel workspace detected but a platform-oriented build.framework and named build.profiles recipe need manual configuration in .revyl/config.yaml"
		}
	case build.SystemKMP:
		hasConfiguredPlatform := false
		for _, p := range detected.Platforms {
			if strings.TrimSpace(p.Command) != "" {
				hasConfiguredPlatform = true
				break
			}
		}
		if !hasConfiguredPlatform {
			check.Status = "warning"
			check.Details = "KMP layout detected but platform-oriented native build.profiles recipes need configuration in .revyl/config.yaml"
		}
	}

	return check
}

// printDoctorResults prints the doctor results in human-readable format.
//
// Parameters:
//   - result: The doctor result to print
func printDoctorResults(result DoctorResult) {
	for _, check := range result.Checks {
		var icon string
		switch check.Status {
		case "ok":
			icon = ui.SuccessStyle.Render("✓")
		case "warning":
			icon = ui.WarningStyle.Render("⚠")
		case "error":
			icon = ui.ErrorStyle.Render("✗")
		}

		// Print check name and message
		fmt.Printf("  %s %-16s %s\n", icon, check.Name+":", check.Message)

		// Print details if present
		if check.Details != "" {
			fmt.Printf("    %s\n", ui.DimStyle.Render(check.Details))
		}
	}

	ui.Println()

	if result.Issues > 0 {
		ui.PrintWarning("%d issue(s) found", result.Issues)
	} else {
		ui.PrintSuccess("All checks passed")
	}

	// Print context-aware next steps based on check results
	var steps []ui.NextStep
	for _, check := range result.Checks {
		switch {
		case check.Name == "Authentication" && check.Status == "error":
			steps = append(steps, ui.NextStep{Label: "Authenticate:", Command: "revyl auth login"})
		case check.Name == "Project Config" && (check.Status == "error" || check.Status == "warning"):
			if len(check.nextSteps) > 0 {
				steps = append(steps, check.nextSteps...)
			} else if check.nextStepCommand != "" {
				steps = append(steps, ui.NextStep{Label: check.nextStepLabel, Command: check.nextStepCommand})
			} else {
				steps = append(steps, ui.NextStep{Label: "Initialize project:", Command: "revyl init"})
			}
		case check.Name == "API Connection" && check.Status == "error":
			steps = append(steps, ui.NextStep{Label: "Test connectivity:", Command: "revyl ping"})
		}
	}

	// If all healthy, suggest running a test
	if result.Healthy && len(steps) == 0 {
		steps = append(steps, ui.NextStep{Label: "Run a test:", Command: "revyl test run <name>"})
	}

	ui.PrintNextSteps(steps)
}

// runPing tests API connectivity with timing.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments (unused)
//
// Returns:
//   - error: Any error that occurred
func runPing(cmd *cobra.Command, args []string) error {
	devMode, _ := cmd.Flags().GetBool("dev")
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	baseURL := config.GetBackendURL(devMode)
	healthURL := baseURL + "/health_check"

	if !jsonOutput {
		ui.PrintInfo("Pinging %s...", baseURL)
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(cmd.Context(), "GET", healthURL, nil)
	if err != nil {
		if jsonOutput {
			data, _ := json.MarshalIndent(map[string]interface{}{
				"ok":    false,
				"error": fmt.Sprintf("failed to create request: %v", err),
			}, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		ui.PrintError("Failed to create request: %v", err)
		return err
	}
	backendheaders.SetCloudAgentConversationContext(req)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		if jsonOutput {
			data, _ := json.MarshalIndent(map[string]interface{}{
				"ok":    false,
				"error": fmt.Sprintf("connection failed: %v", err),
			}, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		ui.PrintError("Connection failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if jsonOutput {
			data, _ := json.MarshalIndent(map[string]interface{}{
				"ok":          false,
				"status_code": resp.StatusCode,
				"latency_ms":  latency.Milliseconds(),
			}, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		ui.PrintWarning("Received status %d (expected 200)", resp.StatusCode)
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	if jsonOutput {
		result := map[string]interface{}{
			"ok":         true,
			"latency_ms": latency.Milliseconds(),
		}

		// Check if authenticated and validate API key
		mgr := auth.NewManager()
		creds, err := mgr.GetCredentials()
		if err == nil && creds != nil && creds.HasValidAuth() {
			apiToken, _ := mgr.GetActiveToken()
			apiClient := api.NewClientWithDevMode(apiToken, devMode)
			apiStart := time.Now()
			_, apiErr := apiClient.ValidateAPIKey(cmd.Context())
			apiLatency := time.Since(apiStart)

			result["api_key_valid"] = apiErr == nil
			result["api_key_latency_ms"] = apiLatency.Milliseconds()
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintSuccess("Connected in %dms", latency.Milliseconds())

	// Check if authenticated and validate API key
	mgr := auth.NewManager()
	creds, err := mgr.GetCredentials()
	if err == nil && creds != nil && creds.HasValidAuth() {
		ui.PrintInfo("Validating credentials...")
		apiToken, _ := mgr.GetActiveToken()
		client := api.NewClientWithDevMode(apiToken, devMode)
		apiStart := time.Now()
		_, err := client.ValidateAPIKey(cmd.Context())
		apiLatency := time.Since(apiStart)

		if err != nil {
			ui.PrintWarning("API key validation failed: %v", err)
		} else {
			ui.PrintSuccess("API key valid (%dms)", apiLatency.Milliseconds())
		}
	}

	return nil
}

// checkSyncStatus verifies canonical local test links against the remote state.
//
// Parameters:
//   - ctx: Context for cancellation
//   - project: The already-resolved canonical project context
//   - client: Authenticated API client (nil to skip remote verification)
//
// Returns:
//   - DoctorCheck: The check result
func checkSyncStatus(ctx context.Context, project *config.ProjectContext, client *api.Client) DoctorCheck {
	check := DoctorCheck{
		Name:   "Sync Status",
		Status: "ok",
	}

	if project == nil {
		check.Status = "warning"
		check.Message = "Project context unavailable"
		return check
	}

	localTests, err := config.LoadLocalTests(project.TestsDir)
	if err != nil {
		check.Status = "warning"
		check.Message = "Could not read local tests"
		check.Details = err.Error()
		return check
	}
	linked := 0
	for _, localTest := range localTests {
		if localTest != nil && strings.TrimSpace(localTest.Meta.RemoteID) != "" {
			linked++
		}
	}
	if linked == 0 {
		check.Message = "No linked local tests"
		return check
	}
	if client == nil {
		check.Message = fmt.Sprintf("%d linked local test(s)", linked)
		check.Details = "Remote verification skipped because authentication is unavailable"
		return check
	}

	remoteTests, err := client.ListAllOrgTests(ctx, 200)
	if err != nil {
		check.Status = "warning"
		check.Message = "Could not verify linked tests"
		check.Details = err.Error()
		return check
	}
	remoteIDs := make(map[string]struct{}, len(remoteTests))
	for _, remoteTest := range remoteTests {
		remoteIDs[remoteTest.ID] = struct{}{}
	}

	var issues []string
	for name, localTest := range localTests {
		if localTest == nil || strings.TrimSpace(localTest.Meta.RemoteID) == "" {
			continue
		}
		if _, found := remoteIDs[localTest.Meta.RemoteID]; !found {
			shortID := localTest.Meta.RemoteID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			issues = append(issues, fmt.Sprintf("Test '%s' (%s...) not found on server", name, shortID))
		}
	}

	if len(issues) > 0 {
		check.Status = "warning"
		check.Message = fmt.Sprintf("%d sync issue(s) detected", len(issues))
		check.Details = strings.Join(issues, "\n    ") + "\n    Run 'revyl sync' to reconcile"
	} else {
		check.Message = fmt.Sprintf("%d linked local test(s) verified", linked)
	}

	return check
}
