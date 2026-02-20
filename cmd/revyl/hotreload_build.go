package main

import (
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

// pickBestBuildPlatformKey selects a build.platforms key for a target device platform.
func pickBestBuildPlatformKey(cfg *config.ProjectConfig, devicePlatform string) string {
	if cfg == nil || len(cfg.Build.Platforms) == 0 {
		return ""
	}
	devicePlatform = strings.ToLower(strings.TrimSpace(devicePlatform))
	if devicePlatform != "ios" && devicePlatform != "android" {
		return ""
	}

	type candidate struct {
		key  string
		rank int
	}
	candidates := make([]candidate, 0)

	for key := range cfg.Build.Platforms {
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
// for hot reload flows.
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
) (string, string, error) {
	if cfg == nil {
		return "", "", fmt.Errorf("project config is required")
	}
	devicePlatform, err := normalizeMobilePlatform("", defaultPlatform)
	if err != nil {
		return "", "", err
	}

	platformOrKey = strings.TrimSpace(platformOrKey)
	platformKey := ""

	if platformOrKey != "" {
		if normalizedPlatform, nErr := normalizeMobilePlatform(platformOrKey, defaultPlatform); nErr == nil {
			devicePlatform = normalizedPlatform
		} else {
			if _, ok := cfg.Build.Platforms[platformOrKey]; !ok {
				return "", "", fmt.Errorf(
					"unknown platform/platform-key '%s' (available: %s)",
					platformOrKey,
					strings.Join(availableBuildPlatformKeys(cfg), ", "),
				)
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
		platformKey = pickBestBuildPlatformKey(cfg, devicePlatform)
	}

	if platformKey == "" {
		return "", "", fmt.Errorf(
			"no build platform configured for %s (available: %s)",
			devicePlatform,
			strings.Join(availableBuildPlatformKeys(cfg), ", "),
		)
	}

	if _, ok := cfg.Build.Platforms[platformKey]; !ok {
		return "", "", fmt.Errorf(
			"mapped platform key '%s' not found in build.platforms (available: %s)",
			platformKey,
			strings.Join(availableBuildPlatformKeys(cfg), ", "),
		)
	}

	return platformKey, devicePlatform, nil
}
