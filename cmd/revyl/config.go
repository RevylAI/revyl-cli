// Package main provides project settings commands for .revyl/config.yaml.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "View and edit project settings",
	Long: `View and edit local project settings in .revyl/config.yaml.

EXAMPLES:
  revyl config path
  revyl config show
  revyl config set open-browser false
  revyl config set timeout 900`,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show project config path",
	RunE:  runConfigPath,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show project settings",
	RunE:  runConfigShow,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a project setting",
	Long: `Set a project setting.

Supported keys:
  open-browser   true|false
  timeout        positive integer seconds`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

func init() {
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
}

func projectConfigPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}

	root := cwd
	if repoRoot, findErr := config.FindRepoRoot(cwd); findErr == nil {
		root = repoRoot
	}

	return filepath.Join(root, ".revyl", "config.yaml"), nil
}

func loadProjectConfigForCommand() (string, *config.ProjectConfig, error) {
	configPath, err := projectConfigPath()
	if err != nil {
		return "", nil, err
	}

	cfg, err := config.LoadProjectConfig(configPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load %s: %w\nrun 'revyl init' first", configPath, err)
	}
	return configPath, cfg, nil
}

func runConfigPath(cmd *cobra.Command, args []string) error {
	configPath, err := projectConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(configPath)
	return nil
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	configPath, cfg, err := loadProjectConfigForCommand()
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")
	if localJSON, _ := cmd.Flags().GetBool("json"); localJSON {
		jsonOutput = true
	}

	openBrowser := config.EffectiveOpenBrowser(cfg)
	timeout := config.EffectiveTimeoutSeconds(cfg, config.DefaultTimeoutSeconds)

	if jsonOutput {
		out := map[string]interface{}{
			"path": configPath,
			"defaults": map[string]interface{}{
				"open_browser": openBrowser,
				"timeout":      timeout,
			},
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintInfo("Project config: %s", configPath)
	ui.Println()
	ui.PrintInfo("Defaults")
	ui.PrintKeyValue("open_browser:", fmt.Sprintf("%t", openBrowser))
	ui.PrintKeyValue("timeout:", fmt.Sprintf("%d", timeout))
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key := strings.TrimSpace(strings.ToLower(args[0]))
	value := strings.TrimSpace(args[1])

	configPath, cfg, err := loadProjectConfigForCommand()
	if err != nil {
		return err
	}

	switch key {
	case "open-browser":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" {
			return fmt.Errorf("invalid open-browser value %q (expected true or false)", value)
		}
		open := lower == "true"
		cfg.Defaults.OpenBrowser = &open

	case "timeout":
		secs, parseErr := strconv.Atoi(value)
		if parseErr != nil || secs <= 0 {
			return fmt.Errorf("invalid timeout value %q (expected positive integer seconds)", value)
		}
		cfg.Defaults.Timeout = secs

	default:
		return fmt.Errorf("unsupported key %q (supported: open-browser, timeout)", key)
	}

	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	ui.PrintSuccess("Updated %s", key)
	return runConfigShow(cmd, nil)
}
