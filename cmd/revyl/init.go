// Package main provides the init command for project setup.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/hotreload"
	_ "github.com/revyl/cli/internal/hotreload/providers" // Register providers
	"github.com/revyl/cli/internal/ui"
)

// initCmd initializes a Revyl project in the current directory.
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize Revyl project configuration",
	Long: `Initialize a Revyl project in the current directory.

This command will:
  1. Detect your build system (Gradle, Xcode, Expo, Flutter, React Native)
  2. Create a .revyl/ directory with configuration
  3. Optionally link to an existing Revyl project

Examples:
  revyl init                    # Auto-detect and create config
  revyl init --project ID       # Link to existing Revyl project
  revyl init --detect           # Re-run build system detection`,
	RunE: runInit,
}

var (
	initProjectID string
	initDetect    bool
	initForce     bool
)

func init() {
	initCmd.Flags().StringVar(&initProjectID, "project", "", "Link to existing Revyl project ID")
	initCmd.Flags().BoolVar(&initDetect, "detect", false, "Re-run build system detection")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing configuration")
}

// runInit executes the init command logic.
//
// Parameters:
//   - cmd: The cobra command being executed
//   - args: Command line arguments
//
// Returns:
//   - error: Any error that occurred during initialization
func runInit(cmd *cobra.Command, args []string) error {
	ui.PrintBanner(version)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	revylDir := filepath.Join(cwd, ".revyl")
	configPath := filepath.Join(revylDir, "config.yaml")

	// Check if already initialized
	if _, err := os.Stat(configPath); err == nil && !initForce && !initDetect {
		ui.PrintWarning("Project already initialized")
		ui.PrintInfo("Use --force to overwrite or --detect to re-run detection")
		return nil
	}

	ui.PrintInfo("Initializing Revyl project in %s", cwd)
	ui.Println()

	// Detect build system
	ui.StartSpinner("Detecting build system...")
	detected, err := build.Detect(cwd)
	ui.StopSpinner()

	if err != nil {
		ui.PrintWarning("Could not auto-detect build system: %v", err)
		detected = &build.DetectedBuild{
			System: build.SystemUnknown,
		}
	}

	if detected.System != build.SystemUnknown {
		ui.PrintSuccess("Detected: %s", detected.System.String())
		ui.PrintInfo("Build command: %s", detected.Command)
		ui.PrintInfo("Output: %s", detected.Output)
	} else {
		ui.PrintWarning("Could not detect build system")
		ui.PrintInfo("You can configure this manually in .revyl/config.yaml")
	}

	ui.Println()

	// Get project name from directory
	projectName := filepath.Base(cwd)

	// Create config
	cfg := &config.ProjectConfig{
		Project: config.Project{
			ID:   initProjectID,
			Name: projectName,
		},
		Build: config.BuildConfig{
			System:  detected.System.String(),
			Command: detected.Command,
			Output:  detected.Output,
		},
		Tests:     make(map[string]string),
		Workflows: make(map[string]string),
		Defaults: config.Defaults{
			OpenBrowser: true,
			Timeout:     600,
		},
	}

	// Add variants if detected
	if len(detected.Variants) > 0 {
		cfg.Build.Variants = make(map[string]config.BuildVariant)
		for name, variant := range detected.Variants {
			cfg.Build.Variants[name] = config.BuildVariant{
				Command: variant.Command,
				Output:  variant.Output,
			}
		}
	}

	// Create .revyl directory
	if err := os.MkdirAll(revylDir, 0755); err != nil {
		return fmt.Errorf("failed to create .revyl directory: %w", err)
	}

	// Create tests directory
	testsDir := filepath.Join(revylDir, "tests")
	if err := os.MkdirAll(testsDir, 0755); err != nil {
		return fmt.Errorf("failed to create tests directory: %w", err)
	}

	// Write config file
	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	// Create .gitignore for .revyl directory
	gitignorePath := filepath.Join(revylDir, ".gitignore")
	gitignoreContent := `# Revyl CLI local files
# Keep credentials out of version control
credentials.json

# Local overrides (optional)
config.local.yaml
`
	if err := os.WriteFile(gitignorePath, []byte(gitignoreContent), 0644); err != nil {
		ui.PrintWarning("Failed to create .gitignore: %v", err)
	}

	ui.Println()
	ui.PrintSuccess("Project initialized!")
	ui.Println()
	ui.PrintInfo("Created:")
	ui.PrintInfo("  .revyl/config.yaml    - Project configuration")
	ui.PrintInfo("  .revyl/tests/         - Local test definitions")
	ui.PrintInfo("  .revyl/.gitignore     - Git ignore rules")
	ui.Println()

	// Check for hot reload compatible providers
	registry := hotreload.DefaultRegistry()
	detections := registry.DetectAllProviders(cwd)

	if len(detections) > 0 {
		// Filter to supported providers
		var supportedDetections []hotreload.ProviderDetection
		for _, d := range detections {
			if d.Provider.IsSupported() {
				supportedDetections = append(supportedDetections, d)
			}
		}

		if len(supportedDetections) > 0 {
			ui.PrintInfo("Found compatible hot reload provider(s):")
			for _, d := range supportedDetections {
				ui.PrintInfo("  • %s (fully supported)", d.Provider.DisplayName())
			}

			// Show coming soon providers
			for _, d := range detections {
				if !d.Provider.IsSupported() {
					ui.PrintDim("  • %s (coming soon)", d.Provider.DisplayName())
				}
			}
			ui.Println()

			if ui.Confirm("Set up hot reload now?") {
				ui.Println()
				ui.PrintInfo("Run 'revyl hotreload setup' to configure hot reload.")
				ui.PrintInfo("This requires authentication: 'revyl auth login'")
			}
			ui.Println()
		}
	}

	ui.PrintInfo("Next steps:")
	ui.PrintInfo("  1. Run 'revyl auth login' to authenticate")
	ui.PrintInfo("  2. Run 'revyl test <name>' to run a test")
	ui.PrintInfo("  3. Edit .revyl/config.yaml to add test aliases")

	return nil
}
