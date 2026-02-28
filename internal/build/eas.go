// Package build provides EAS (Expo Application Services) config parsing and validation.
package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// EASConfig represents a parsed eas.json file.
type EASConfig struct {
	Build map[string]EASBuildProfile `json:"build"`
}

// EASBuildProfile represents a single build profile in eas.json.
type EASBuildProfile struct {
	DevelopmentClient bool               `json:"developmentClient,omitempty"`
	Distribution      string             `json:"distribution,omitempty"`
	IOS               *EASPlatformConfig `json:"ios,omitempty"`
}

// EASPlatformConfig represents platform-specific config within a build profile.
type EASPlatformConfig struct {
	Simulator bool `json:"simulator,omitempty"`
}

// LoadEASConfig reads and parses eas.json from the given directory.
// Returns nil, nil if the file does not exist.
func LoadEASConfig(dir string) (*EASConfig, error) {
	path := filepath.Join(dir, "eas.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read eas.json: %w", err)
	}

	var cfg EASConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse eas.json: %w", err)
	}
	return &cfg, nil
}

// IsSimulatorProfile returns true if the given profile produces a simulator build for iOS.
func (c *EASConfig) IsSimulatorProfile(profileName string) bool {
	if c == nil || c.Build == nil {
		return false
	}
	profile, ok := c.Build[profileName]
	if !ok {
		return false
	}
	return profile.IOS != nil && profile.IOS.Simulator
}

// FindSimulatorProfiles returns all profile names that have ios.simulator: true.
func (c *EASConfig) FindSimulatorProfiles() []string {
	if c == nil || c.Build == nil {
		return nil
	}
	var profiles []string
	for name, profile := range c.Build {
		if profile.IOS != nil && profile.IOS.Simulator {
			profiles = append(profiles, name)
		}
	}
	return profiles
}

// FindDevSimulatorProfile finds the best simulator profile for dev builds.
// Preference order: "development" (if simulator), "development-simulator", first match.
func (c *EASConfig) FindDevSimulatorProfile() string {
	if c == nil {
		return ""
	}

	if c.IsSimulatorProfile("development") {
		return "development"
	}
	if c.IsSimulatorProfile("development-simulator") {
		return "development-simulator"
	}

	// Fall back to first profile with simulator + developmentClient
	for name, profile := range c.Build {
		if profile.IOS != nil && profile.IOS.Simulator && profile.DevelopmentClient {
			return name
		}
	}
	// Fall back to any simulator profile
	for name, profile := range c.Build {
		if profile.IOS != nil && profile.IOS.Simulator {
			return name
		}
	}
	return ""
}

// FindCISimulatorProfile finds the best simulator profile for CI builds.
// Preference order: "preview" (if simulator), first match.
func (c *EASConfig) FindCISimulatorProfile() string {
	if c == nil {
		return ""
	}

	if c.IsSimulatorProfile("preview") {
		return "preview"
	}

	// Fall back to any simulator profile (excluding development ones)
	for name, profile := range c.Build {
		if profile.IOS != nil && profile.IOS.Simulator && !profile.DevelopmentClient {
			return name
		}
	}
	// Any simulator profile
	for name, profile := range c.Build {
		if profile.IOS != nil && profile.IOS.Simulator {
			return name
		}
	}
	return ""
}

// profileRegex matches --profile <name> in an EAS build command.
var profileRegex = regexp.MustCompile(`--profile\s+(\S+)`)

// ExtractProfileFromCommand parses --profile <name> from an EAS build command.
// Returns the profile name and true if found, or empty string and false if not.
func ExtractProfileFromCommand(command string) (string, bool) {
	matches := profileRegex.FindStringSubmatch(command)
	if len(matches) < 2 {
		return "", false
	}
	return matches[1], true
}

// ReplaceProfileInCommand swaps the --profile value in an EAS build command.
func ReplaceProfileInCommand(command, newProfile string) string {
	return profileRegex.ReplaceAllString(command, "--profile "+newProfile)
}

// SimulatorValidationResult describes the outcome of validating an EAS profile.
type SimulatorValidationResult struct {
	// Valid is true if the profile produces a simulator build.
	Valid bool
	// ProfileName is the profile that was checked.
	ProfileName string
	// Alternatives are simulator-compatible profiles that could be used instead.
	Alternatives []string
	// NoEASConfig is true if eas.json was not found.
	NoEASConfig bool
	// ProfileNotFound is true if the profile doesn't exist in eas.json.
	ProfileNotFound bool
}

// ValidateEASSimulatorProfile checks if a build command's --profile produces a simulator build.
// Returns a validation result with suggestions if invalid.
func ValidateEASSimulatorProfile(easCfg *EASConfig, command string) SimulatorValidationResult {
	if easCfg == nil {
		return SimulatorValidationResult{Valid: true, NoEASConfig: true}
	}

	profileName, found := ExtractProfileFromCommand(command)
	if !found {
		// No --profile in command — skip validation (custom command).
		return SimulatorValidationResult{Valid: true}
	}

	result := SimulatorValidationResult{ProfileName: profileName}

	// Check if profile exists
	if _, ok := easCfg.Build[profileName]; !ok {
		result.Valid = true // Let EAS CLI handle missing profiles
		result.ProfileNotFound = true
		return result
	}

	if easCfg.IsSimulatorProfile(profileName) {
		result.Valid = true
		return result
	}

	// Profile exists but is not a simulator build — find alternatives
	result.Alternatives = easCfg.FindSimulatorProfiles()
	return result
}

// SimulatorFixSnippet returns a JSON snippet users can add to eas.json to fix a profile.
func SimulatorFixSnippet(profileName string) string {
	return fmt.Sprintf(`Add "simulator": true to your "%s" profile in eas.json:

  "%s": {
    ...
    "ios": {
      "simulator": true
    }
  }`, profileName, profileName)
}

// AddRevylBuildProfile adds a "revyl-build" profile to eas.json that extends
// the given base profile with ios.simulator: true.
// Uses sjson to surgically insert the profile, preserving the existing key
// order and formatting of the file.
func AddRevylBuildProfile(dir, baseProfile string) error {
	path := filepath.Join(dir, "eas.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read eas.json: %w", err)
	}

	if !gjson.ValidBytes(data) {
		return fmt.Errorf("eas.json is not valid JSON")
	}
	if !gjson.GetBytes(data, "build").Exists() {
		return fmt.Errorf("eas.json has no build section")
	}

	// Build the profile as a raw JSON string to control key order.
	profileJSON, err := json.Marshal(map[string]interface{}{
		"extends":           baseProfile,
		"developmentClient": true,
		"distribution":      "internal",
		"ios":               map[string]interface{}{"simulator": true},
	})
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	result, err := sjson.SetRawBytes(data, "build.revyl-build", profileJSON)
	if err != nil {
		return fmt.Errorf("failed to update eas.json: %w", err)
	}

	return os.WriteFile(path, result, 0644)
}

// IsIOSPlatformKey returns true if the platform key refers to an iOS build.
func IsIOSPlatformKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(lower, "ios")
}
