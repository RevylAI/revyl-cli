// Package main provides the doctor and ping commands for CLI diagnostics.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/auth"
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
	RunE: runPing,
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
	projectCheck := checkProjectConfig()
	result.Checks = append(result.Checks, projectCheck)
	if projectCheck.Status == "error" {
		result.Issues++
		// Project config is optional, don't mark as unhealthy
	}

	// Check 5: Build System
	buildCheck := checkBuildSystem()
	result.Checks = append(result.Checks, buildCheck)
	// Build system is informational only

	// Output results
	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		printDoctorResults(result)
	}

	if !result.Healthy {
		return fmt.Errorf("health check failed")
	}

	return nil
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

	if err != nil || creds == nil || creds.APIKey == "" {
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
	healthURL := baseURL + "/health"

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		check.Status = "error"
		check.Message = "Failed to create request"
		check.Details = err.Error()
		return check
	}

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
	if devMode {
		check.Details = fmt.Sprintf("Using development server: %s", baseURL)
	}

	return check
}

// checkProjectConfig checks if a valid project configuration exists.
//
// Returns:
//   - DoctorCheck: The check result
func checkProjectConfig() DoctorCheck {
	check := DoctorCheck{
		Name:   "Project Config",
		Status: "ok",
	}

	cwd, err := os.Getwd()
	if err != nil {
		check.Status = "error"
		check.Message = "Could not get current directory"
		check.Details = err.Error()
		return check
	}

	configPath := filepath.Join(cwd, ".revyl", "config.yaml")
	cfg, err := config.LoadProjectConfig(configPath)

	if err != nil {
		check.Status = "warning"
		check.Message = "No project configuration"
		check.Details = "Run 'revyl init' to initialize a project"
		return check
	}

	check.Message = fmt.Sprintf("Found at %s", configPath)

	// Count configured items
	var details []string
	if cfg.Project.Name != "" {
		details = append(details, fmt.Sprintf("Project: %s", cfg.Project.Name))
	}
	if len(cfg.Tests) > 0 {
		details = append(details, fmt.Sprintf("%d test(s)", len(cfg.Tests)))
	}
	if len(cfg.Workflows) > 0 {
		details = append(details, fmt.Sprintf("%d workflow(s)", len(cfg.Workflows)))
	}
	if len(details) > 0 {
		check.Details = fmt.Sprintf("%v", details)
	}

	return check
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
	baseURL := config.GetBackendURL(devMode)
	healthURL := baseURL + "/health"

	ui.PrintInfo("Pinging %s...", baseURL)

	start := time.Now()
	req, err := http.NewRequestWithContext(cmd.Context(), "GET", healthURL, nil)
	if err != nil {
		ui.PrintError("Failed to create request: %v", err)
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		ui.PrintError("Connection failed: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		ui.PrintWarning("Received status %d (expected 200)", resp.StatusCode)
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	ui.PrintSuccess("Connected in %dms", latency.Milliseconds())

	// Check if authenticated and validate API key
	mgr := auth.NewManager()
	creds, err := mgr.GetCredentials()
	if err == nil && creds != nil && creds.APIKey != "" {
		ui.PrintInfo("Validating API key...")

		client := api.NewClientWithDevMode(creds.APIKey, devMode)
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
