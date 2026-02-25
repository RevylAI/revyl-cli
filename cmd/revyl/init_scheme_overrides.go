package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/revyl/cli/internal/build"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
)

type initOverrideOptions struct {
	AllowInteractivePrompts bool
	XcodeSchemeOverrides    map[string]string
	HotReloadAppScheme      string
	schemeEditState         *initSchemeEditState
}

const (
	initEditDetectedSettingsPrompt = "Review/edit project config now? (press Enter to continue setup)"
)

func newInitOverrideOptions(xcodeSchemeArgs []string, hotReloadAppScheme string, allowInteractivePrompts bool) (*initOverrideOptions, error) {
	xcodeSchemeOverrides, err := parseXcodeSchemeOverrides(xcodeSchemeArgs)
	if err != nil {
		return nil, err
	}

	appScheme := strings.TrimSpace(hotReloadAppScheme)
	hasExplicitOverrides := len(xcodeSchemeOverrides) > 0 || appScheme != ""

	return &initOverrideOptions{
		AllowInteractivePrompts: allowInteractivePrompts,
		XcodeSchemeOverrides:    xcodeSchemeOverrides,
		HotReloadAppScheme:      appScheme,
		schemeEditState:         newInitSchemeEditState(allowInteractivePrompts && !hasExplicitOverrides, nil, nil),
	}, nil
}

func (o *initOverrideOptions) ShouldPromptForDetectedEdits() bool {
	if o == nil || o.schemeEditState == nil {
		return false
	}
	return o.schemeEditState.ShouldEdit()
}

func (o *initOverrideOptions) WillAskDetectedEditPrompt() bool {
	if o == nil || o.schemeEditState == nil {
		return false
	}
	return o.schemeEditState.CanAsk()
}

type confirmPromptFunc func(message string, defaultYes bool) (bool, error)
type printContextFunc func()

type initSchemeEditState struct {
	canPrompt     bool
	promptConfirm confirmPromptFunc
	printContext  printContextFunc
	asked         bool
	enabled       bool
}

func newInitSchemeEditState(canPrompt bool, promptConfirm confirmPromptFunc, printContext printContextFunc) *initSchemeEditState {
	return &initSchemeEditState{
		canPrompt:     canPrompt,
		promptConfirm: promptConfirm,
		printContext:  printContext,
	}
}

func (s *initSchemeEditState) CanAsk() bool {
	if s == nil {
		return false
	}
	return s.canPrompt && !s.asked
}

func (s *initSchemeEditState) ShouldEdit() bool {
	if s == nil || !s.canPrompt {
		return false
	}
	if s.asked {
		return s.enabled
	}
	s.asked = true

	prompt := s.promptConfirm
	if prompt == nil {
		prompt = ui.PromptConfirm
	}

	edit, err := prompt(initEditDetectedSettingsPrompt, false)
	if err != nil {
		return false
	}

	s.enabled = edit
	if s.enabled {
		printContext := s.printContext
		if printContext == nil {
			printContext = printProjectConfigReviewContext
		}
		printContext()
	}
	return s.enabled
}

func printProjectConfigReviewContext() {
	ui.PrintInfo("Project configuration file: .revyl/config.yaml")
	ui.PrintInfo("You're editing general project setup. Build settings come first; hot reload is optional.")
	ui.PrintInfo("Current .revyl/config.yaml:")
}

func runProjectConfigReview(cfg *config.ProjectConfig, configPath string, overrideOpts *initOverrideOptions) error {
	if cfg == nil || overrideOpts == nil || !overrideOpts.AllowInteractivePrompts {
		return nil
	}

	if overrideOpts.WillAskDetectedEditPrompt() {
		printProjectConfigReviewPromptContext(cfg)
	}
	if !overrideOpts.ShouldPromptForDetectedEdits() {
		return nil
	}

	printCurrentProjectConfig(configPath)
	ui.Println()
	promptBuildSetupReview(cfg)
	promptForXcodeSchemeEdits(cfg)

	customizeHotReload, err := ui.PromptConfirm("Also customize optional hot reload app scheme for dev-client deep links?", false)
	if err == nil && customizeHotReload {
		currentScheme := strings.TrimSpace(overrideOpts.HotReloadAppScheme)
		if currentScheme == "" {
			currentScheme = inferredExpoAppScheme(cfg)
		}
		overrideOpts.HotReloadAppScheme = promptHotReloadAppSchemeWithDefault(currentScheme)
	}

	if err := config.WriteProjectConfig(configPath, cfg); err != nil {
		return fmt.Errorf("failed to save project setup updates: %w", err)
	}

	ui.PrintSuccess("Saved project setup updates to .revyl/config.yaml")
	ui.PrintDim("Hot reload setup (Step 4) will use these settings and defaults.")
	return nil
}

func printProjectConfigReviewPromptContext(cfg *config.ProjectConfig) {
	if cfg == nil {
		return
	}

	ui.PrintInfo("Detected build setup in .revyl/config.yaml")

	if isExpoBuildSystem(cfg.Build.System) {
		keys := orderedBuildPlatformKeysForReview(cfg)
		if len(keys) > 0 {
			ui.PrintDim("Build streams (Step 3 links each stream to an app):")
			mapping := inferHotReloadPlatformKeys(cfg)
			table := ui.NewTable("STREAM KEY", "MOBILE", "BUILD COMMAND", "REVYL DEV DEFAULT")
			table.SetMaxWidth(2, 72)
			for _, key := range keys {
				mobile := strings.TrimSpace(mobilePlatformForBuildKey(key))
				if mobile == "" {
					mobile = "custom"
				}
				platformCfg := cfg.Build.Platforms[key]
				buildCommand := strings.TrimSpace(platformCfg.Command)
				if buildCommand == "" {
					buildCommand = "-"
				}
				table.AddRow(
					fmt.Sprintf("build.platforms.%s", key),
					mobile,
					buildCommand,
					describeRuntimeDefaultForBuildKey(mapping, key),
				)
			}
			table.Render()
		} else {
			ui.PrintDim("No build.platforms streams detected yet.")
		}

		ui.PrintDim("Default runtime mapping is used by `revyl dev`; you can change it later in .revyl/config.yaml.")
		return
	}

	ui.PrintInfo("Build setup")
	ui.PrintKeyValue("build.command", strings.TrimSpace(cfg.Build.Command))
	ui.PrintKeyValue("build.output", strings.TrimSpace(cfg.Build.Output))
}

func describeBuildPlatformStream(key string) string {
	switch key {
	case "ios-dev":
		return "iOS development stream for local iteration"
	case "android-dev":
		return "Android development stream for local iteration"
	case "ios-ci":
		return "iOS CI/preview stream"
	case "android-ci":
		return "Android CI/preview stream"
	default:
		lower := strings.ToLower(strings.TrimSpace(key))
		switch {
		case strings.Contains(lower, "ios") && (strings.Contains(lower, "dev") || strings.Contains(lower, "development")):
			return "iOS development stream for local iteration"
		case strings.Contains(lower, "android") && (strings.Contains(lower, "dev") || strings.Contains(lower, "development")):
			return "Android development stream for local iteration"
		case strings.Contains(lower, "ios") && (strings.Contains(lower, "ci") || strings.Contains(lower, "preview")):
			return "iOS CI/preview stream"
		case strings.Contains(lower, "android") && (strings.Contains(lower, "ci") || strings.Contains(lower, "preview")):
			return "Android CI/preview stream"
		case strings.Contains(lower, "ios"):
			return "iOS stream"
		case strings.Contains(lower, "android"):
			return "Android stream"
		default:
			return "custom stream"
		}
	}
}

func describeBuildPlatformLink(cfg *config.ProjectConfig, platformKey string) string {
	purpose := describeBuildPlatformStream(platformKey)
	mobile := strings.TrimSpace(mobilePlatformForBuildKey(platformKey))
	if mobile == "" {
		mobile = "custom"
	}

	appName := expectedInitAppName(cfg, platformKey)
	if appName == "" {
		return fmt.Sprintf("%s (mobile: %s)", purpose, mobile)
	}
	return fmt.Sprintf("%s (mobile: %s, app stream name: %s)", purpose, mobile, appName)
}

func expectedInitAppName(cfg *config.ProjectConfig, platformKey string) string {
	if cfg == nil {
		return ""
	}

	projectName := strings.TrimSpace(cfg.Project.Name)
	streamKey := strings.TrimSpace(platformKey)
	if projectName == "" || streamKey == "" {
		return ""
	}
	return fmt.Sprintf("%s-%s", projectName, streamKey)
}

func describeRuntimeDefaultForBuildKey(mapping map[string]string, buildKey string) string {
	if strings.TrimSpace(buildKey) == "" {
		return "-"
	}

	labels := make([]string, 0, 2)
	if strings.TrimSpace(mapping["ios"]) == strings.TrimSpace(buildKey) {
		labels = append(labels, "ios")
	}
	if strings.TrimSpace(mapping["android"]) == strings.TrimSpace(buildKey) {
		labels = append(labels, "android")
	}
	if len(labels) == 0 {
		return "-"
	}
	return strings.Join(labels, ", ")
}

type promptWithDefaultFunc func(label, current string) string

func promptBuildSetupReview(cfg *config.ProjectConfig) {
	promptBuildSetupReviewWithPrompt(cfg, promptStringWithDefault)
}

func promptBuildSetupReviewWithPrompt(cfg *config.ProjectConfig, promptFn promptWithDefaultFunc) {
	if cfg == nil {
		return
	}
	if promptFn == nil {
		promptFn = promptStringWithDefault
	}

	if !isExpoBuildSystem(cfg.Build.System) {
		ui.PrintInfo("Build Setup")
		cfg.Build.Command = promptFn("Build command", cfg.Build.Command)
		cfg.Build.Output = promptFn("Build output path", cfg.Build.Output)
		return
	}

	ui.PrintInfo("Build Setup (platform-specific for Expo)")
	ui.PrintDim("Revyl uses build.platforms.<key> for dev/ci streams in Expo projects.")

	platformKeys := orderedBuildPlatformKeysForReview(cfg)
	if len(platformKeys) == 0 {
		ui.PrintDim("No build.platforms entries found; editing top-level build defaults.")
		cfg.Build.Command = promptFn("Build command", cfg.Build.Command)
		cfg.Build.Output = promptFn("Build output path", cfg.Build.Output)
		return
	}

	for _, platformKey := range platformKeys {
		platformCfg := cfg.Build.Platforms[platformKey]
		ui.PrintDim("Saved under build.platforms.%s.{command,output} in .revyl/config.yaml", platformKey)
		platformCfg.Command = promptFn(fmt.Sprintf("Build command for %s", platformKey), platformCfg.Command)
		platformCfg.Output = promptFn(fmt.Sprintf("Build output path for %s", platformKey), platformCfg.Output)
		cfg.Build.Platforms[platformKey] = platformCfg
	}
}

func orderedBuildPlatformKeysForReview(cfg *config.ProjectConfig) []string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return nil
	}

	priorityOrder := []string{"ios-dev", "android-dev", "ios-ci", "android-ci"}
	keys := make([]string, 0, len(cfg.Build.Platforms))
	seen := make(map[string]bool, len(cfg.Build.Platforms))

	for _, key := range priorityOrder {
		if _, ok := cfg.Build.Platforms[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}

	remaining := make([]string, 0, len(cfg.Build.Platforms))
	for key := range cfg.Build.Platforms {
		if !seen[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	keys = append(keys, remaining...)
	return keys
}

func printCurrentProjectConfig(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		ui.PrintWarning("Could not read %s: %v", configPath, err)
		return
	}

	content := strings.TrimRight(string(data), "\n")
	if content == "" {
		ui.PrintDim("  (empty)")
		return
	}

	for _, line := range strings.Split(content, "\n") {
		ui.PrintDim("  %s", line)
	}
}

func promptStringWithDefault(label, current string) string {
	trimmedCurrent := strings.TrimSpace(current)
	prompt := fmt.Sprintf("%s (optional, press Enter to keep current):", label)
	if trimmedCurrent != "" {
		prompt = fmt.Sprintf("%s [%s]:", label, trimmedCurrent)
	}

	input, err := ui.Prompt(prompt)
	if err != nil {
		return current
	}

	input = strings.TrimSpace(input)
	if input == "" {
		return current
	}
	return input
}

func promptHotReloadAppSchemeWithDefault(current string) string {
	trimmedCurrent := strings.TrimSpace(current)
	prompt := "Expo app URL scheme for launching dev client (optional, press Enter to keep current or auto-detected value):"
	if trimmedCurrent != "" {
		prompt = fmt.Sprintf("Expo app URL scheme for launching dev client [%s]:", trimmedCurrent)
	}
	ui.PrintDim("Saved as hotreload.providers.expo.app_scheme in .revyl/config.yaml")
	input, err := ui.Prompt(prompt)
	if err != nil {
		return trimmedCurrent
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return trimmedCurrent
	}
	return input
}

func inferredExpoAppScheme(cfg *config.ProjectConfig) string {
	if cfg == nil {
		return ""
	}
	expoCfg := cfg.HotReload.GetProviderConfig("expo")
	if expoCfg == nil {
		return ""
	}
	return strings.TrimSpace(expoCfg.AppScheme)
}

func parseXcodeSchemeOverrides(entries []string) (map[string]string, error) {
	overrides := make(map[string]string, len(entries))

	for _, rawEntry := range entries {
		entry := strings.TrimSpace(rawEntry)
		if entry == "" {
			continue
		}

		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --xcode-scheme %q (expected <platform_key>=<scheme>)", rawEntry)
		}

		platformKey := strings.TrimSpace(parts[0])
		scheme := strings.TrimSpace(parts[1])
		if platformKey == "" {
			return nil, fmt.Errorf("invalid --xcode-scheme %q: platform key cannot be empty", rawEntry)
		}
		if scheme == "" {
			return nil, fmt.Errorf("invalid --xcode-scheme %q: scheme cannot be empty", rawEntry)
		}

		overrides[platformKey] = scheme
	}

	return overrides, nil
}

func applyXcodeSchemeOverrides(cfg *config.ProjectConfig, overrides map[string]string) error {
	if cfg == nil || len(overrides) == 0 {
		return nil
	}

	availableKeys := make([]string, 0, len(cfg.Build.Platforms))
	for key := range cfg.Build.Platforms {
		availableKeys = append(availableKeys, key)
	}
	sort.Strings(availableKeys)

	for platformKey := range overrides {
		if _, ok := cfg.Build.Platforms[platformKey]; !ok {
			available := "(none)"
			if len(availableKeys) > 0 {
				available = strings.Join(availableKeys, ", ")
			}
			return fmt.Errorf("unknown build platform key %q in --xcode-scheme (available: %s)", platformKey, available)
		}
	}

	for platformKey, scheme := range overrides {
		platformCfg := cfg.Build.Platforms[platformKey]
		cfg.Build.Platforms[platformKey] = setBuildPlatformScheme(platformCfg, scheme)
	}

	return nil
}

func promptForXcodeSchemeEdits(cfg *config.ProjectConfig) {
	if cfg == nil {
		return
	}

	for _, platformKey := range xcodeSchemePlatformKeys(cfg) {
		platformCfg := cfg.Build.Platforms[platformKey]
		current := strings.TrimSpace(platformCfg.Scheme)

		prompt := fmt.Sprintf(
			"Xcode build scheme for %s (optional, press Enter to keep current):",
			platformKey,
		)
		if current != "" {
			prompt = fmt.Sprintf(
				"Xcode build scheme for %s [%s]:",
				platformKey,
				current,
			)
		}
		ui.PrintDim("Saved as build.platforms.%s.scheme in .revyl/config.yaml", platformKey)

		input, err := ui.Prompt(prompt)
		if err != nil {
			continue
		}
		scheme := strings.TrimSpace(input)
		if scheme == "" {
			continue
		}

		cfg.Build.Platforms[platformKey] = setBuildPlatformScheme(platformCfg, scheme)
	}
}

func xcodeSchemePlatformKeys(cfg *config.ProjectConfig) []string {
	if cfg == nil {
		return nil
	}

	keys := make([]string, 0, len(cfg.Build.Platforms))
	for key, platformCfg := range cfg.Build.Platforms {
		command := strings.TrimSpace(platformCfg.Command)
		if strings.Contains(command, "-scheme ") || strings.TrimSpace(platformCfg.Scheme) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func applyExpoAppSchemeOverride(providerCfg *config.ProviderConfig, explicitScheme string, allowInteractiveEdit bool) {
	if providerCfg == nil {
		return
	}

	trimmedExplicitScheme := strings.TrimSpace(explicitScheme)
	if trimmedExplicitScheme != "" {
		providerCfg.AppScheme = trimmedExplicitScheme
		return
	}

	if !allowInteractiveEdit {
		return
	}

	current := strings.TrimSpace(providerCfg.AppScheme)
	prompt := "Expo app URL scheme for launching dev client (optional, press Enter to keep current):"
	if current != "" {
		prompt = fmt.Sprintf(
			"Expo app URL scheme for launching dev client [%s]:",
			current,
		)
	}
	ui.PrintDim("Saved as hotreload.providers.expo.app_scheme in .revyl/config.yaml")

	input, err := ui.Prompt(prompt)
	if err != nil {
		return
	}
	input = strings.TrimSpace(input)
	if input != "" {
		providerCfg.AppScheme = input
	}
}

var xcodeSchemeArgPattern = regexp.MustCompile(`-scheme\s+('[^']*'|"[^"]*"|\S+)`)

func setBuildPlatformScheme(platformCfg config.BuildPlatform, scheme string) config.BuildPlatform {
	trimmedScheme := strings.TrimSpace(scheme)
	if trimmedScheme == "" {
		return platformCfg
	}

	platformCfg.Scheme = trimmedScheme
	command := strings.TrimSpace(platformCfg.Command)
	if command == "" {
		return platformCfg
	}

	if strings.Contains(command, "-scheme *") {
		platformCfg.Command = build.ApplySchemeToCommand(command, trimmedScheme)
		return platformCfg
	}

	if xcodeSchemeArgPattern.MatchString(command) {
		templatedCommand := xcodeSchemeArgPattern.ReplaceAllString(command, "-scheme *")
		platformCfg.Command = build.ApplySchemeToCommand(templatedCommand, trimmedScheme)
	}

	return platformCfg
}
