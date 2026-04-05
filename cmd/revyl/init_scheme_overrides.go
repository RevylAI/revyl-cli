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
	initEditDetectedSettingsPrompt = "Customize build settings? (Enter to accept defaults)"
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
	ui.PrintDim("Press Enter on each field to keep the auto-detected default.")
}

func runProjectConfigReview(cfg *config.ProjectConfig, configPath string, overrideOpts *initOverrideOptions) error {
	if cfg == nil || overrideOpts == nil || !overrideOpts.AllowInteractivePrompts {
		return nil
	}
	printProjectConfigReviewPromptContext(cfg)
	return nil
}

// printBuildConceptsBox renders a styled box explaining key Revyl concepts.
// Shown once at the top of the build config review so new users understand
// what "build stream", "dev loop", etc. mean before being asked to edit them.
//
// Parameters:
//   - cfg: project config used to determine which concepts to include
func printBuildConceptsBox(cfg *config.ProjectConfig) {
	if cfg == nil {
		return
	}

	type conceptEntry struct {
		Term string
		Desc []string
	}

	var entries []conceptEntry

	if isExpoBuildSystem(cfg.Build.System) {
		entries = []conceptEntry{
			{
				Term: "Build stream",
				Desc: []string{
					"A named build configuration that produces an app",
					"artifact. Dev streams (e.g. " + ui.InfoStyle.Render("ios-dev") + ui.DimStyle.Render(") are for local"),
					"iteration; CI streams (e.g. " + ui.InfoStyle.Render("ios-ci") + ui.DimStyle.Render(") are for"),
					"automated testing in pull requests and pipelines.",
				},
			},
			{
				Term: "Dev loop",
				Desc: []string{
					ui.InfoStyle.Render("revyl dev") + ui.DimStyle.Render(" connects your local dev server to a"),
					"cloud device. Code changes reload on the device",
					"instantly. It uses dev streams to know which build",
					"to run.",
				},
			},
			{
				Term: "Config file",
				Desc: []string{
					ui.InfoStyle.Render(".revyl/config.yaml") + ui.DimStyle.Render(" stores all of these settings."),
					"You can edit it directly anytime.",
				},
			},
		}
	} else {
		entries = []conceptEntry{
			{
				Term: "Build command",
				Desc: []string{
					"The shell command Revyl runs to produce your app",
					"artifact (e.g. APK, .app bundle). Auto-detected",
					"from your project but you can customize it below.",
				},
			},
			{
				Term: "Platform",
				Desc: []string{
					"The mobile OS this build targets (" + ui.InfoStyle.Render("ios") + ui.DimStyle.Render(" or ") + ui.InfoStyle.Render("android") + ui.DimStyle.Render(")."),
					"Revyl uses this to pick the right device.",
				},
			},
		}

		if len(xcodeSchemePlatformKeys(cfg)) > 0 {
			entries = append(entries, conceptEntry{
				Term: "Xcode scheme",
				Desc: []string{
					"(iOS only) Determines which target, build config,",
					"and test plan Xcode uses. Required for xcodebuild",
					"commands.",
				},
			})
		}

		entries = append(entries, conceptEntry{
			Term: "Config file",
			Desc: []string{
				ui.InfoStyle.Render(".revyl/config.yaml") + ui.DimStyle.Render(" stores all of these settings."),
				"You can edit it directly anytime.",
			},
		})
	}

	var b strings.Builder
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n")
		}
		termStyled := ui.AccentStyle.Render(fmt.Sprintf("%-15s", entry.Term))
		for j, line := range entry.Desc {
			if j == 0 {
				b.WriteString(fmt.Sprintf("  %s  %s", termStyled, ui.DimStyle.Render(line)))
			} else {
				b.WriteString(fmt.Sprintf("\n  %s  %s", fmt.Sprintf("%-15s", ""), ui.DimStyle.Render(line)))
			}
		}
	}

	ui.PrintBox("How Revyl builds work", b.String())
}

func printProjectConfigReviewPromptContext(cfg *config.ProjectConfig) {
	if cfg == nil {
		return
	}

	printBuildConceptsBox(cfg)

	if isExpoBuildSystem(cfg.Build.System) {
		keys := orderedBuildPlatformKeysForReview(cfg)
		if len(keys) > 0 {
			mapping := inferHotReloadPlatformKeys(cfg)
			ui.Println()
			ui.PrintDim("Your project has %d build streams. Streams marked ✦ are used by revyl dev.", len(keys))
			ui.Println()
			table := ui.NewTable("STREAM", "PLATFORM", "PURPOSE", "COMMAND")
			table.SetMaxWidth(3, 60)
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
				streamLabel := key
				if describeRuntimeDefaultForBuildKey(mapping, key) != "-" {
					streamLabel = key + " ✦"
				}
				table.AddRow(
					streamLabel,
					mobile,
					shortBuildPurpose(key),
					buildCommand,
				)
			}
			table.Render()
		}
	} else {
		ui.Println()
		if len(cfg.Build.Platforms) > 0 {
			for _, key := range orderedBuildPlatformKeysForReview(cfg) {
				platformCfg := cfg.Build.Platforms[key]
				ui.PrintKeyValue(fmt.Sprintf("%s command", key), strings.TrimSpace(platformCfg.Command))
				ui.PrintKeyValue(fmt.Sprintf("%s output", key), strings.TrimSpace(platformCfg.Output))
			}
		} else {
			ui.PrintKeyValue("Build command", strings.TrimSpace(cfg.Build.Command))
			ui.PrintKeyValue("Build output", strings.TrimSpace(cfg.Build.Output))
		}
	}
}

func describeBuildPlatformStream(key string) string {
	switch key {
	case "ios-dev":
		return "iOS development build for local iteration"
	case "android-dev":
		return "Android development build for local iteration"
	case "ios-ci":
		return "iOS CI / preview build"
	case "android-ci":
		return "Android CI / preview build"
	default:
		lower := strings.ToLower(strings.TrimSpace(key))
		switch {
		case strings.Contains(lower, "ios") && (strings.Contains(lower, "dev") || strings.Contains(lower, "development")):
			return "iOS development build for local iteration"
		case strings.Contains(lower, "android") && (strings.Contains(lower, "dev") || strings.Contains(lower, "development")):
			return "Android development build for local iteration"
		case strings.Contains(lower, "ios") && (strings.Contains(lower, "ci") || strings.Contains(lower, "preview")):
			return "iOS CI / preview build"
		case strings.Contains(lower, "android") && (strings.Contains(lower, "ci") || strings.Contains(lower, "preview")):
			return "Android CI / preview build"
		case strings.Contains(lower, "ios"):
			return "iOS build"
		case strings.Contains(lower, "android"):
			return "Android build"
		default:
			return "Custom build"
		}
	}
}

// shortBuildPurpose returns a platform-agnostic purpose label for table display.
// Unlike describeBuildPlatformStream which includes the platform name,
// this returns just the purpose since the platform is shown in its own column.
//
// Parameters:
//   - key: build platform key (e.g. "ios-dev", "android-ci")
//
// Returns:
//   - string: short purpose label (e.g. "Dev build for local iteration")
func shortBuildPurpose(key string) string {
	lower := strings.ToLower(strings.TrimSpace(key))
	switch {
	case strings.Contains(lower, "dev") || strings.Contains(lower, "development"):
		return "Dev build for local iteration"
	case strings.Contains(lower, "ci") || strings.Contains(lower, "preview"):
		return "CI / preview build"
	default:
		return "Custom build"
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

	ui.PrintInfo("Build Commands")

	platformKeys := orderedBuildPlatformKeysForReview(cfg)
	if len(platformKeys) == 0 {
		cfg.Build.Command = promptFn("command", cfg.Build.Command)
		cfg.Build.Output = promptFn("output", cfg.Build.Output)
		return
	}

	for i, platformKey := range platformKeys {
		if i > 0 {
			ui.Println()
		}
		ui.PrintInfo("  %s  ·  %s", platformKey, describeBuildPlatformStream(platformKey))
		platformCfg := cfg.Build.Platforms[platformKey]
		platformCfg.Command = promptFn("command", platformCfg.Command)
		platformCfg.Output = promptFn("output", platformCfg.Output)
		cfg.Build.Platforms[platformKey] = platformCfg
	}

	if len(platformKeys) > 0 {
		first := cfg.Build.Platforms[platformKeys[0]]
		cfg.Build.Command = first.Command
		cfg.Build.Output = first.Output
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

	schemeKeys := xcodeSchemePlatformKeys(cfg)
	if len(schemeKeys) == 0 {
		return
	}

	ui.PrintDim("The Xcode scheme determines which target and build settings to use.")

	for _, platformKey := range schemeKeys {
		platformCfg := cfg.Build.Platforms[platformKey]
		current := strings.TrimSpace(platformCfg.Scheme)

		prompt := fmt.Sprintf("scheme for %s (press Enter to keep):", platformKey)
		if current != "" {
			prompt = fmt.Sprintf("scheme for %s [%s]:", platformKey, current)
		}

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
