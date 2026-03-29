// Package devicetargets defines the available device models, OS runtimes, and
// compatibility rules for each mobile platform.
//
// The data (variables iosTargets, androidTargets, platformTargets) lives in
// targets_generated.go and is auto-generated from the backend API by
// scripts/generate-types.sh.  This file contains only the types and helpers.
package devicetargets

import (
	"fmt"
	"strings"
)

// DevicePair is a device model + OS runtime combination.
type DevicePair struct {
	Model   string
	Runtime string
}

// PlatformTargetConfig describes the available targets and default pair for a
// single platform (ios or android).
type PlatformTargetConfig struct {
	DefaultPair        DevicePair
	AvailableRuntimes  []string
	AvailableModels    []string
	CompatibleRuntimes map[string][]string // model -> runtimes it supports
}

// GetPlatformTargets returns the target config for a platform.
//
// Parameters:
//   - platform: lowercase platform name ("ios" or "android")
//
// Returns:
//   - *PlatformTargetConfig: the config for the platform
//   - error: if the platform is unknown
func GetPlatformTargets(platform string) (*PlatformTargetConfig, error) {
	cfg, ok := platformTargets[strings.ToLower(platform)]
	if !ok {
		return nil, fmt.Errorf("unknown platform %q; available: ios, android", platform)
	}
	return cfg, nil
}

// GetDefaultPair returns the default DevicePair for a platform.
//
// Parameters:
//   - platform: lowercase platform name
//
// Returns:
//   - DevicePair: the platform default
//   - error: if the platform is unknown
func GetDefaultPair(platform string) (DevicePair, error) {
	cfg, err := GetPlatformTargets(platform)
	if err != nil {
		return DevicePair{}, err
	}
	return cfg.DefaultPair, nil
}

// GetAvailableTargetPairs enumerates every valid (model, runtime) combination
// for a platform, ordered by runtime then model.
//
// Parameters:
//   - platform: lowercase platform name
//
// Returns:
//   - []DevicePair: all valid combinations
//   - error: if the platform is unknown
func GetAvailableTargetPairs(platform string) ([]DevicePair, error) {
	cfg, err := GetPlatformTargets(platform)
	if err != nil {
		return nil, err
	}

	var pairs []DevicePair
	for _, model := range cfg.AvailableModels {
		runtimes := cfg.CompatibleRuntimes[model]
		if len(runtimes) == 0 {
			runtimes = cfg.AvailableRuntimes
		}
		for _, rt := range runtimes {
			pairs = append(pairs, DevicePair{Model: model, Runtime: rt})
		}
	}
	return pairs, nil
}

// ValidateDevicePair checks that a (model, runtime) combination is supported
// for the given platform.
//
// Parameters:
//   - platform: lowercase platform name
//   - model: device model name (e.g. "iPhone 16")
//   - runtime: OS runtime string (e.g. "iOS 18.5")
//
// Returns:
//   - error: nil when valid; descriptive error otherwise
func ValidateDevicePair(platform, model, runtime string) error {
	cfg, err := GetPlatformTargets(platform)
	if err != nil {
		return err
	}

	if !contains(cfg.AvailableModels, model) {
		return fmt.Errorf("unsupported %s device model %q; available: %v", platform, model, cfg.AvailableModels)
	}
	if !contains(cfg.AvailableRuntimes, runtime) {
		return fmt.Errorf("unsupported %s runtime %q; available: %v", platform, runtime, cfg.AvailableRuntimes)
	}

	compatible := cfg.CompatibleRuntimes[model]
	if len(compatible) == 0 {
		compatible = cfg.AvailableRuntimes
	}
	if !contains(compatible, runtime) {
		return fmt.Errorf("incompatible %s target %q / %q; compatible runtimes for %s: %v",
			platform, model, runtime, model, compatible)
	}
	return nil
}

// FormatPairLabel produces a human-readable label like "iPhone 16 · iOS 18.5".
//
// Parameters:
//   - pair: the device pair to format
//
// Returns:
//   - string: formatted label
func FormatPairLabel(pair DevicePair) string {
	return fmt.Sprintf("%s · %s", pair.Model, pair.Runtime)
}

// DevicePresets maps human-friendly preset names to their platform and
// default (model, runtime) pair. Integrations like Trailblaze reference
// these by name so they don't need to hardcode device/runtime strings.
var DevicePresets = map[string]struct {
	Platform string
	Pair     DevicePair
}{
	"revyl-android-phone": {Platform: "android"},
	"revyl-ios-iphone":    {Platform: "ios"},
}

// ResolvePreset looks up a named device preset and returns the platform,
// device model, and OS runtime. The returned pair uses the platform's
// current default from the generated target catalog, keeping presets
// in sync with backend changes automatically.
//
// Parameters:
//   - name: preset name (e.g. "revyl-android-phone")
//
// Returns:
//   - platform, model, runtime strings
//   - error if the preset name is unknown
func ResolvePreset(name string) (platform, model, runtime string, err error) {
	preset, ok := DevicePresets[strings.ToLower(name)]
	if !ok {
		known := make([]string, 0, len(DevicePresets))
		for k := range DevicePresets {
			known = append(known, k)
		}
		return "", "", "", fmt.Errorf("unknown device preset %q; available: %v", name, known)
	}

	if preset.Pair.Model != "" && preset.Pair.Runtime != "" {
		return preset.Platform, preset.Pair.Model, preset.Pair.Runtime, nil
	}

	defaultPair, defErr := GetDefaultPair(preset.Platform)
	if defErr != nil {
		return "", "", "", fmt.Errorf("preset %q references platform %q: %w", name, preset.Platform, defErr)
	}
	return preset.Platform, defaultPair.Model, defaultPair.Runtime, nil
}

// ListPresets returns all available preset names.
func ListPresets() []string {
	names := make([]string, 0, len(DevicePresets))
	for k := range DevicePresets {
		names = append(names, k)
	}
	return names
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
