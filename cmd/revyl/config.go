// Package main provides project settings commands for .revyl/config.yaml.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
  open-browser                true|false
  timeout                     positive integer seconds
  hotreload.provider          expo|react-native|swift|android
  hotreload.app-scheme        URL scheme (e.g. myapp)
  hotreload.port              dev server port (e.g. 8081)
  hotreload.use-exp-prefix    true|false`,
	Args: cobra.ExactArgs(2),
	RunE: runConfigSet,
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config in your editor",
	RunE:  runConfigEdit,
}

func init() {
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configEditCmd)
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
		if cfg.HotReload.IsConfigured() {
			hrOut := map[string]interface{}{
				"default": cfg.HotReload.Default,
			}
			if provCfg := cfg.HotReload.GetProviderConfig(cfg.HotReload.Default); provCfg != nil {
				hrOut["app_scheme"] = provCfg.AppScheme
				hrOut["port"] = provCfg.GetPort(cfg.HotReload.Default)
				hrOut["use_exp_prefix"] = provCfg.UseExpPrefix
				hrOut["platform_keys"] = provCfg.PlatformKeys
			}
			out["hotreload"] = hrOut
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	ui.PrintInfo("Project config: %s", configPath)
	ui.Println()
	ui.PrintInfo("Defaults")
	ui.PrintKeyValue("  open_browser:", fmt.Sprintf("%t", openBrowser))
	ui.PrintKeyValue("  timeout:", fmt.Sprintf("%d", timeout))

	if cfg.HotReload.IsConfigured() {
		ui.Println()
		ui.PrintInfo("Dev Mode (Hot Reload)")
		ui.PrintKeyValue("  provider:", cfg.HotReload.Default)
		if provCfg := cfg.HotReload.GetProviderConfig(cfg.HotReload.Default); provCfg != nil {
			if provCfg.AppScheme != "" {
				ui.PrintKeyValue("  app_scheme:", provCfg.AppScheme)
			}
			ui.PrintKeyValue("  port:", fmt.Sprintf("%d", provCfg.GetPort(cfg.HotReload.Default)))
			if provCfg.UseExpPrefix {
				ui.PrintKeyValue("  use_exp_prefix:", "true")
			}
			if len(provCfg.PlatformKeys) > 0 {
				ui.PrintKeyValue("  platform_keys:", "")
				for k, v := range provCfg.PlatformKeys {
					ui.PrintKeyValue("    "+k+":", v)
				}
			}
		}
	} else {
		ui.Println()
		ui.PrintDim("Dev mode not configured. Run: revyl dev")
	}

	ui.Println()
	if cfg.Build.System != "" {
		ui.PrintInfo("Build")
		ui.PrintKeyValue("  system:", cfg.Build.System)
		if len(cfg.Build.Platforms) > 0 {
			ui.PrintKeyValue("  platforms:", "")
			for name, plat := range cfg.Build.Platforms {
				appID := plat.AppID
				if appID == "" {
					appID = "(none)"
				}
				ui.PrintKeyValue("    "+name+":", fmt.Sprintf("app_id=%s", appID))
			}
		}
	}

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

	case "hotreload.provider":
		validProviders := map[string]bool{"expo": true, "react-native": true, "swift": true, "android": true}
		if !validProviders[value] {
			return fmt.Errorf("invalid provider %q (expected: expo, react-native, swift, android)", value)
		}
		cfg.HotReload.Default = value

	case "hotreload.app-scheme":
		provCfg := ensureActiveProviderConfig(cfg)
		provCfg.AppScheme = value

	case "hotreload.port":
		port, parseErr := strconv.Atoi(value)
		if parseErr != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("invalid port %q (expected 1-65535)", value)
		}
		provCfg := ensureActiveProviderConfig(cfg)
		provCfg.Port = port

	case "hotreload.use-exp-prefix":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" {
			return fmt.Errorf("invalid use-exp-prefix value %q (expected true or false)", value)
		}
		provCfg := ensureActiveProviderConfig(cfg)
		provCfg.UseExpPrefix = lower == "true"

	default:
		return fmt.Errorf("unsupported key %q\nSupported: open-browser, timeout, hotreload.provider, hotreload.app-scheme, hotreload.port, hotreload.use-exp-prefix", key)
	}

	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	ui.PrintSuccess("Updated %s = %s", key, value)
	return nil
}

// ensureActiveProviderConfig returns the ProviderConfig for the active provider,
// creating it if necessary.
func ensureActiveProviderConfig(cfg *config.ProjectConfig) *config.ProviderConfig {
	provider := cfg.HotReload.Default
	if provider == "" {
		provider = "expo"
		cfg.HotReload.Default = provider
	}
	if cfg.HotReload.Providers == nil {
		cfg.HotReload.Providers = make(map[string]*config.ProviderConfig)
	}
	provCfg := cfg.HotReload.Providers[provider]
	if provCfg == nil {
		provCfg = &config.ProviderConfig{}
		cfg.HotReload.Providers[provider] = provCfg
	}
	return provCfg
}

func runConfigEdit(cmd *cobra.Command, args []string) error {
	configPath, err := projectConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found at %s\nRun 'revyl init' first", configPath)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	ui.PrintDim("Opening %s in %s...", configPath, editor)

	editorCmd := exec.Command(editor, configPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	return editorCmd.Run()
}
