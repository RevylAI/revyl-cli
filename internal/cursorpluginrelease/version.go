package cursorpluginrelease

import (
	"fmt"
	"strings"
)

const (
	// PluginBumpPatch increments the patch component.
	PluginBumpPatch = "patch"
	// PluginBumpMinor increments the minor component and resets patch.
	PluginBumpMinor = "minor"
	// PluginBumpMajor increments the major component and resets minor and patch.
	PluginBumpMajor = "major"
)

// NextPluginVersion increments current by bump. Empty bump is patch.
//
// Parameters:
//   - current: The current plugin.json version, optionally with a prerelease suffix.
//   - bump: patch, minor, or major. Empty is patch.
//
// Returns:
//   - string: The next semantic version, with any prerelease suffix dropped.
//   - error: An unparseable current version or an unknown bump.
func NextPluginVersion(current string, bump string) (string, error) {
	switch strings.TrimSpace(bump) {
	case "", PluginBumpPatch:
		return NextPluginPatchVersion(current)
	case PluginBumpMinor:
		return NextPluginMinorVersion(current)
	case PluginBumpMajor:
		return NextPluginMajorVersion(current)
	default:
		return "", fmt.Errorf("invalid plugin bump %q", bump)
	}
}

// ComparePluginVersions reports whether left is greater, equal, or less than right.
//
// Release versions rank above matching-core prereleases. Equal cores with
// different prerelease labels compare as strings, matching the previous guard.
//
// Parameters:
//   - left: The first semantic version.
//   - right: The second semantic version.
//
// Returns:
//   - int: 1 when left > right, 0 when equal, -1 when left < right.
//   - error: An unparseable version string.
func ComparePluginVersions(left string, right string) (int, error) {
	leftMajor, leftMinor, leftPatch, err := pluginVersionParts(left)
	if err != nil {
		return 0, err
	}
	rightMajor, rightMinor, rightPatch, err := pluginVersionParts(right)
	if err != nil {
		return 0, err
	}
	leftCore := [3]int{leftMajor, leftMinor, leftPatch}
	rightCore := [3]int{rightMajor, rightMinor, rightPatch}
	for index := range leftCore {
		if leftCore[index] > rightCore[index] {
			return 1, nil
		}
		if leftCore[index] < rightCore[index] {
			return -1, nil
		}
	}
	leftPre := prereleaseSuffix(left)
	rightPre := prereleaseSuffix(right)
	if leftPre == rightPre {
		return 0, nil
	}
	if leftPre == "" {
		return 1, nil
	}
	if rightPre == "" {
		return -1, nil
	}
	if leftPre > rightPre {
		return 1, nil
	}
	return -1, nil
}

// ReadPluginVersion returns plugin.json version from a maintained file.
//
// Parameters:
//   - path: Path to plugin.json.
//
// Returns:
//   - string: The semantic version.
//   - error: Read, decode, or missing-version failure.
func ReadPluginVersion(path string) (string, error) {
	plugin, _, err := readJSONFile[pluginDocument](path)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(plugin.Version)
	if version == "" {
		return "", fmt.Errorf("plugin.json missing version")
	}
	return version, nil
}

// prereleaseSuffix returns the prerelease label, or empty for a release version.
func prereleaseSuffix(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	_, suffix, found := strings.Cut(version, "-")
	if !found {
		return ""
	}
	return suffix
}
