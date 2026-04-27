package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/revyl/cli/internal/config"
)

// normalizeMobilePlatform normalizes a platform string to ios/android.
// Empty input resolves to defaultPlatform.
func normalizeMobilePlatform(platform, defaultPlatform string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(platform))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(defaultPlatform))
	}
	switch value {
	case "ios", "android":
		return value, nil
	default:
		return "", fmt.Errorf("platform must be 'ios' or 'android'")
	}
}

// platformFromKey infers ios/android from a build.platforms key.
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

// isRunnableBuildPlatform returns true when a build platform has enough data to execute.
//
// Parameters:
//   - platformCfg: The build platform configuration to inspect
//
// Returns:
//   - bool: True when both command and output are configured
func isRunnableBuildPlatform(platformCfg config.BuildPlatform) bool {
	return strings.TrimSpace(platformCfg.Command) != "" && strings.TrimSpace(platformCfg.Output) != ""
}

// isResolvableBuildPlatform returns true when a build platform can resolve an
// existing build, even without a build command. This is the case when the
// platform has an app_id pointing to pre-uploaded builds.
//
// Parameters:
//   - platformCfg: The build platform configuration to inspect
//
// Returns:
//   - bool: True when the platform can resolve builds (runnable or has app_id)
func isResolvableBuildPlatform(platformCfg config.BuildPlatform) bool {
	return isRunnableBuildPlatform(platformCfg) || strings.TrimSpace(platformCfg.AppID) != ""
}

// hasRunnableBuildPlatforms returns true when at least one build.platforms entry
// has both command and output configured.
func hasRunnableBuildPlatforms(cfg *config.ProjectConfig) bool {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return false
	}
	for _, platformCfg := range cfg.Build.Platforms {
		if isRunnableBuildPlatform(platformCfg) {
			return true
		}
	}
	return false
}

// hasOnlyPlaceholderBuildPlatforms returns true when build.platforms entries
// exist, but none are runnable yet.
func hasOnlyPlaceholderBuildPlatforms(cfg *config.ProjectConfig) bool {
	return cfg != nil && len(cfg.Build.Platforms) > 0 && !hasRunnableBuildPlatforms(cfg)
}

// buildablePlatformKeys returns sorted build.platforms keys that can actually run.
//
// Parameters:
//   - cfg: The project configuration to inspect
//
// Returns:
//   - []string: Sorted platform keys whose command/output are both configured
func buildablePlatformKeys(cfg *config.ProjectConfig) []string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return nil
	}

	keys := make([]string, 0, len(cfg.Build.Platforms))
	for key, platformCfg := range cfg.Build.Platforms {
		if isRunnableBuildPlatform(platformCfg) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// placeholderBuildPlatformKeys returns sorted build.platforms keys that exist
// but are not runnable because command/output are still missing.
func placeholderBuildPlatformKeys(cfg *config.ProjectConfig) []string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return nil
	}

	keys := make([]string, 0, len(cfg.Build.Platforms))
	for key, platformCfg := range cfg.Build.Platforms {
		if !isRunnableBuildPlatform(platformCfg) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

type buildPlatformNeedsSetup struct {
	PlatformKey string
}

func (e *buildPlatformNeedsSetup) Error() string {
	return fmt.Sprintf(
		"build.platforms.%s is not ready yet; finish native setup or add both command/output before using it",
		e.PlatformKey,
	)
}

// buildPlatformNeedsSetupError returns a typed setup error for placeholder platform entries.
func buildPlatformNeedsSetupError(platformKey string) error {
	return &buildPlatformNeedsSetup{PlatformKey: platformKey}
}

func asBuildPlatformNeedsSetupError(err error) (*buildPlatformNeedsSetup, bool) {
	var target *buildPlatformNeedsSetup
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// pickBestBuildPlatformKey selects a build.platforms key for a target device platform.
// When noBuild is true, platforms with only an app_id (no command/output) are accepted.
func pickBestBuildPlatformKey(cfg *config.ProjectConfig, devicePlatform string, noBuild ...bool) string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return ""
	}
	devicePlatform = strings.ToLower(strings.TrimSpace(devicePlatform))
	if devicePlatform != "ios" && devicePlatform != "android" {
		return ""
	}

	acceptPlatform := isRunnableBuildPlatform
	if len(noBuild) > 0 && noBuild[0] {
		acceptPlatform = isResolvableBuildPlatform
	}

	type candidate struct {
		key  string
		rank int
	}
	candidates := make([]candidate, 0)

	for key, platformCfg := range cfg.Build.Platforms {
		if !acceptPlatform(platformCfg) {
			continue
		}

		lower := strings.ToLower(key)
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

// inferHotReloadPlatformKeys derives default hotreload provider mappings from build.platforms.
func inferHotReloadPlatformKeys(cfg *config.ProjectConfig) map[string]string {
	result := make(map[string]string)
	for _, platform := range []string{"ios", "android"} {
		if key := pickBestBuildPlatformKey(cfg, platform); key != "" {
			result[platform] = key
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// mergePlatformKeys merges existing platform mappings with inferred defaults.
// Existing explicit mappings win.
func mergePlatformKeys(existing, inferred map[string]string) map[string]string {
	if len(existing) == 0 && len(inferred) == 0 {
		return nil
	}
	out := make(map[string]string)
	for k, v := range inferred {
		out[k] = v
	}
	for k, v := range existing {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// availableBuildPlatformKeys returns sorted build.platforms keys for user-facing error messages.
func availableBuildPlatformKeys(cfg *config.ProjectConfig) []string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg.Build.Platforms))
	for k := range cfg.Build.Platforms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveHotReloadBuildPlatform selects a build.platforms key and device platform
// for hot reload flows. When noBuild is true, platforms with only an app_id
// (no command/output) are accepted.
//
// platformOrKey accepts:
//   - "" (use defaultPlatform + mapping/inference)
//   - "ios"/"android" (device platform)
//   - a concrete build.platforms key
func resolveHotReloadBuildPlatform(
	cfg *config.ProjectConfig,
	providerCfg *config.ProviderConfig,
	platformOrKey string,
	defaultPlatform string,
	noBuild ...bool,
) (string, string, error) {
	if cfg == nil {
		return "", "", fmt.Errorf("project config is required")
	}
	devicePlatform, err := normalizeMobilePlatform("", defaultPlatform)
	if err != nil {
		return "", "", err
	}

	nb := len(noBuild) > 0 && noBuild[0]
	acceptPlatform := isRunnableBuildPlatform
	if nb {
		acceptPlatform = isResolvableBuildPlatform
	}

	platformOrKey = strings.TrimSpace(platformOrKey)
	platformKey := ""

	if platformOrKey != "" {
		if normalizedPlatform, nErr := normalizeMobilePlatform(platformOrKey, defaultPlatform); nErr == nil {
			devicePlatform = normalizedPlatform
		} else {
			platformCfg, ok := cfg.Build.Platforms[platformOrKey]
			if !ok {
				return "", "", fmt.Errorf(
					"unknown platform/platform-key '%s' (available: %s)",
					platformOrKey,
					strings.Join(availableBuildPlatformKeys(cfg), ", "),
				)
			}
			if !acceptPlatform(platformCfg) {
				return "", "", buildPlatformNeedsSetupError(platformOrKey)
			}
			platformKey = platformOrKey
			if inferredPlatform := platformFromKey(platformOrKey); inferredPlatform != "" {
				devicePlatform = inferredPlatform
			}
		}
	}

	if platformKey == "" && providerCfg != nil && len(providerCfg.PlatformKeys) > 0 {
		if mapped := strings.TrimSpace(providerCfg.PlatformKeys[devicePlatform]); mapped != "" {
			platformKey = mapped
		}
	}

	if platformKey == "" {
		platformKey = pickBestBuildPlatformKey(cfg, devicePlatform, nb)
	}

	if platformKey == "" {
		return "", "", fmt.Errorf(
			"no build platform configured for %s (available: %s)",
			devicePlatform,
			strings.Join(availableBuildPlatformKeys(cfg), ", "),
		)
	}

	platformCfg, ok := cfg.Build.Platforms[platformKey]
	if !ok {
		return "", "", fmt.Errorf(
			"mapped platform key '%s' not found in build.platforms (available: %s)",
			platformKey,
			strings.Join(availableBuildPlatformKeys(cfg), ", "),
		)
	}
	if !acceptPlatform(platformCfg) {
		return "", "", buildPlatformNeedsSetupError(platformKey)
	}

	return platformKey, devicePlatform, nil
}
