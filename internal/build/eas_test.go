package build

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestLoadEASConfig(t *testing.T) {
	t.Run("valid eas.json", func(t *testing.T) {
		dir := t.TempDir()
		content := `{
			"build": {
				"development": {
					"developmentClient": true,
					"distribution": "internal",
					"ios": { "simulator": true }
				},
				"preview": {
					"distribution": "internal",
					"ios": { "simulator": true }
				},
				"production": {}
			}
		}`
		if err := os.WriteFile(filepath.Join(dir, "eas.json"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadEASConfig(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil {
			t.Fatal("expected non-nil config")
		}
		if len(cfg.Build) != 3 {
			t.Errorf("expected 3 profiles, got %d", len(cfg.Build))
		}
	})

	t.Run("missing eas.json returns nil", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := LoadEASConfig(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg != nil {
			t.Fatal("expected nil config for missing file")
		}
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "eas.json"), []byte(`{invalid`), 0644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadEASConfig(dir)
		if err == nil {
			t.Fatal("expected error for malformed JSON")
		}
		if cfg != nil {
			t.Fatal("expected nil config for malformed JSON")
		}
	})
}

func TestIsSimulatorProfile(t *testing.T) {
	cfg := &EASConfig{
		Build: map[string]EASBuildProfile{
			"development": {
				DevelopmentClient: true,
				IOS:               &EASPlatformConfig{Simulator: true},
			},
			"production": {
				Distribution: "store",
			},
			"no-sim": {
				IOS: &EASPlatformConfig{Simulator: false},
			},
		},
	}

	tests := []struct {
		profile string
		want    bool
	}{
		{"development", true},
		{"production", false},
		{"no-sim", false},
		{"nonexistent", false},
	}

	for _, tt := range tests {
		t.Run(tt.profile, func(t *testing.T) {
			got := cfg.IsSimulatorProfile(tt.profile)
			if got != tt.want {
				t.Errorf("IsSimulatorProfile(%q) = %v, want %v", tt.profile, got, tt.want)
			}
		})
	}

	t.Run("nil config", func(t *testing.T) {
		var nilCfg *EASConfig
		if nilCfg.IsSimulatorProfile("development") {
			t.Error("expected false for nil config")
		}
	})
}

func TestFindSimulatorProfiles(t *testing.T) {
	cfg := &EASConfig{
		Build: map[string]EASBuildProfile{
			"development": {
				IOS: &EASPlatformConfig{Simulator: true},
			},
			"preview": {
				IOS: &EASPlatformConfig{Simulator: true},
			},
			"production": {},
		},
	}

	profiles := cfg.FindSimulatorProfiles()
	sort.Strings(profiles)
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %v", len(profiles), profiles)
	}
	if profiles[0] != "development" || profiles[1] != "preview" {
		t.Errorf("unexpected profiles: %v", profiles)
	}

	t.Run("nil config", func(t *testing.T) {
		var nilCfg *EASConfig
		if profiles := nilCfg.FindSimulatorProfiles(); len(profiles) != 0 {
			t.Errorf("expected empty list for nil config, got %v", profiles)
		}
	})
}

func TestFindDevSimulatorProfile(t *testing.T) {
	t.Run("prefers development if simulator", func(t *testing.T) {
		cfg := &EASConfig{
			Build: map[string]EASBuildProfile{
				"development": {
					DevelopmentClient: true,
					IOS:               &EASPlatformConfig{Simulator: true},
				},
				"development-simulator": {
					DevelopmentClient: true,
					IOS:               &EASPlatformConfig{Simulator: true},
				},
			},
		}
		if got := cfg.FindDevSimulatorProfile(); got != "development" {
			t.Errorf("expected 'development', got %q", got)
		}
	})

	t.Run("falls back to development-simulator", func(t *testing.T) {
		cfg := &EASConfig{
			Build: map[string]EASBuildProfile{
				"development": {
					DevelopmentClient: true,
				},
				"development-simulator": {
					DevelopmentClient: true,
					IOS:               &EASPlatformConfig{Simulator: true},
				},
			},
		}
		if got := cfg.FindDevSimulatorProfile(); got != "development-simulator" {
			t.Errorf("expected 'development-simulator', got %q", got)
		}
	})

	t.Run("falls back to any devClient+simulator profile", func(t *testing.T) {
		cfg := &EASConfig{
			Build: map[string]EASBuildProfile{
				"development": {
					DevelopmentClient: true,
				},
				"custom-dev": {
					DevelopmentClient: true,
					IOS:               &EASPlatformConfig{Simulator: true},
				},
			},
		}
		if got := cfg.FindDevSimulatorProfile(); got != "custom-dev" {
			t.Errorf("expected 'custom-dev', got %q", got)
		}
	})

	t.Run("no simulator profiles returns empty", func(t *testing.T) {
		cfg := &EASConfig{
			Build: map[string]EASBuildProfile{
				"development": {DevelopmentClient: true},
			},
		}
		if got := cfg.FindDevSimulatorProfile(); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}

func TestFindCISimulatorProfile(t *testing.T) {
	t.Run("prefers preview if simulator", func(t *testing.T) {
		cfg := &EASConfig{
			Build: map[string]EASBuildProfile{
				"preview": {
					IOS: &EASPlatformConfig{Simulator: true},
				},
				"staging": {
					IOS: &EASPlatformConfig{Simulator: true},
				},
			},
		}
		if got := cfg.FindCISimulatorProfile(); got != "preview" {
			t.Errorf("expected 'preview', got %q", got)
		}
	})

	t.Run("falls back to non-dev simulator profile", func(t *testing.T) {
		cfg := &EASConfig{
			Build: map[string]EASBuildProfile{
				"preview": {
					Distribution: "internal",
				},
				"staging": {
					IOS: &EASPlatformConfig{Simulator: true},
				},
			},
		}
		if got := cfg.FindCISimulatorProfile(); got != "staging" {
			t.Errorf("expected 'staging', got %q", got)
		}
	})
}

func TestExtractProfileFromCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
		found   bool
	}{
		{
			name:    "standard command",
			command: "npx --yes eas-cli build --platform ios --profile development --local",
			want:    "development",
			found:   true,
		},
		{
			name:    "profile at end",
			command: "eas build --platform ios --profile preview",
			want:    "preview",
			found:   true,
		},
		{
			name:    "no profile flag",
			command: "eas build --platform ios --local",
			want:    "",
			found:   false,
		},
		{
			name:    "custom command without eas",
			command: "xcodebuild -scheme MyApp",
			want:    "",
			found:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := ExtractProfileFromCommand(tt.command)
			if got != tt.want || found != tt.found {
				t.Errorf("ExtractProfileFromCommand(%q) = (%q, %v), want (%q, %v)", tt.command, got, found, tt.want, tt.found)
			}
		})
	}
}

func TestReplaceProfileInCommand(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		newProfile string
		want       string
	}{
		{
			name:       "replace development with development-simulator",
			command:    "npx --yes eas-cli build --platform ios --profile development --local",
			newProfile: "development-simulator",
			want:       "npx --yes eas-cli build --platform ios --profile development-simulator --local",
		},
		{
			name:       "replace at end of command",
			command:    "eas build --platform ios --profile preview",
			newProfile: "preview-sim",
			want:       "eas build --platform ios --profile preview-sim",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceProfileInCommand(tt.command, tt.newProfile)
			if got != tt.want {
				t.Errorf("ReplaceProfileInCommand(%q, %q) = %q, want %q", tt.command, tt.newProfile, got, tt.want)
			}
		})
	}
}

func TestValidateEASSimulatorProfile(t *testing.T) {
	cfg := &EASConfig{
		Build: map[string]EASBuildProfile{
			"development": {
				DevelopmentClient: true,
			},
			"development-simulator": {
				DevelopmentClient: true,
				IOS:               &EASPlatformConfig{Simulator: true},
			},
			"production": {},
		},
	}

	t.Run("nil config is valid", func(t *testing.T) {
		result := ValidateEASSimulatorProfile(nil, "npx eas build --profile development")
		if !result.Valid {
			t.Error("expected valid for nil config")
		}
		if !result.NoEASConfig {
			t.Error("expected NoEASConfig=true")
		}
	})

	t.Run("no profile flag is valid", func(t *testing.T) {
		result := ValidateEASSimulatorProfile(cfg, "xcodebuild -scheme MyApp")
		if !result.Valid {
			t.Error("expected valid when no --profile flag")
		}
	})

	t.Run("simulator profile is valid", func(t *testing.T) {
		result := ValidateEASSimulatorProfile(cfg, "eas build --profile development-simulator")
		if !result.Valid {
			t.Error("expected valid for simulator profile")
		}
	})

	t.Run("non-simulator profile is invalid with alternatives", func(t *testing.T) {
		result := ValidateEASSimulatorProfile(cfg, "eas build --profile development")
		if result.Valid {
			t.Error("expected invalid for non-simulator profile")
		}
		if result.ProfileName != "development" {
			t.Errorf("expected profile name 'development', got %q", result.ProfileName)
		}
		if len(result.Alternatives) != 1 || result.Alternatives[0] != "development-simulator" {
			t.Errorf("expected alternatives [development-simulator], got %v", result.Alternatives)
		}
	})

	t.Run("unknown profile is valid (deferred to EAS CLI)", func(t *testing.T) {
		result := ValidateEASSimulatorProfile(cfg, "eas build --profile nonexistent")
		if !result.Valid {
			t.Error("expected valid for unknown profile")
		}
		if !result.ProfileNotFound {
			t.Error("expected ProfileNotFound=true")
		}
	})
}

func TestIsIOSPlatformKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"ios", true},
		{"ios-dev", true},
		{"ios-ci", true},
		{"IOS", true},
		{"android", false},
		{"android-dev", false},
		{"web", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := IsIOSPlatformKey(tt.key); got != tt.want {
				t.Errorf("IsIOSPlatformKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
