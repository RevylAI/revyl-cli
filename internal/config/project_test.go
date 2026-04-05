// Package config provides project configuration management.
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComputeTestChecksum verifies that checksum computation is deterministic
// and produces different values for different content.
func TestComputeTestChecksum(t *testing.T) {
	tests := []struct {
		name     string
		test     *TestDefinition
		wantNil  bool
		wantSame bool // if true, compare with previous test's checksum
	}{
		{
			name:    "nil test returns empty string",
			test:    nil,
			wantNil: true,
		},
		{
			name: "basic test produces checksum",
			test: &TestDefinition{
				Metadata: TestMetadata{
					Name:     "test1",
					Platform: "android",
				},
				Blocks: []TestBlock{
					{Type: "instructions", StepDescription: "tap button"},
				},
			},
			wantNil: false,
		},
		{
			name: "different content produces different checksum",
			test: &TestDefinition{
				Metadata: TestMetadata{
					Name:     "test2",
					Platform: "ios",
				},
				Blocks: []TestBlock{
					{Type: "instructions", StepDescription: "swipe left"},
				},
			},
			wantNil: false,
		},
	}

	var prevChecksum string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checksum := ComputeTestChecksum(tt.test)

			if tt.wantNil {
				if checksum != "" {
					t.Errorf("ComputeTestChecksum() = %q, want empty string", checksum)
				}
				return
			}

			if checksum == "" {
				t.Error("ComputeTestChecksum() returned empty string for non-nil test")
			}

			// Verify checksum is hex-encoded (64 chars for SHA-256)
			if len(checksum) != 64 {
				t.Errorf("ComputeTestChecksum() length = %d, want 64", len(checksum))
			}

			// Verify determinism - same input produces same output
			checksum2 := ComputeTestChecksum(tt.test)
			if checksum != checksum2 {
				t.Errorf("ComputeTestChecksum() not deterministic: %q != %q", checksum, checksum2)
			}

			// Verify different content produces different checksum
			if prevChecksum != "" && checksum == prevChecksum {
				t.Error("Different test content produced same checksum")
			}
			prevChecksum = checksum
		})
	}
}

// TestHasLocalChanges verifies that local change detection works correctly.
func TestHasLocalChanges(t *testing.T) {
	tests := []struct {
		name       string
		localTest  *LocalTest
		modifyTest func(*LocalTest) // optional modification before checking
		want       bool
	}{
		{
			name: "no checksum stored returns false",
			localTest: &LocalTest{
				Meta: TestMeta{
					Checksum: "",
				},
				Test: TestDefinition{
					Metadata: TestMetadata{Name: "test1"},
				},
			},
			want: false,
		},
		{
			name: "matching checksum returns false",
			localTest: func() *LocalTest {
				test := &LocalTest{
					Test: TestDefinition{
						Metadata: TestMetadata{Name: "test1", Platform: "android"},
						Blocks:   []TestBlock{{Type: "instructions", StepDescription: "tap"}},
					},
				}
				// Store the correct checksum
				test.Meta.Checksum = ComputeTestChecksum(&test.Test)
				return test
			}(),
			want: false,
		},
		{
			name: "modified content returns true",
			localTest: func() *LocalTest {
				test := &LocalTest{
					Test: TestDefinition{
						Metadata: TestMetadata{Name: "test1", Platform: "android"},
						Blocks:   []TestBlock{{Type: "instructions", StepDescription: "tap"}},
					},
				}
				// Store the checksum
				test.Meta.Checksum = ComputeTestChecksum(&test.Test)
				return test
			}(),
			modifyTest: func(lt *LocalTest) {
				// Modify the test content after checksum was stored
				lt.Test.Blocks[0].StepDescription = "swipe"
			},
			want: true,
		},
		{
			name: "added block returns true",
			localTest: func() *LocalTest {
				test := &LocalTest{
					Test: TestDefinition{
						Metadata: TestMetadata{Name: "test1", Platform: "android"},
						Blocks:   []TestBlock{{Type: "instructions", StepDescription: "tap"}},
					},
				}
				test.Meta.Checksum = ComputeTestChecksum(&test.Test)
				return test
			}(),
			modifyTest: func(lt *LocalTest) {
				// Add a new block
				lt.Test.Blocks = append(lt.Test.Blocks, TestBlock{
					Type:            "validation",
					StepDescription: "verify button exists",
				})
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.modifyTest != nil {
				tt.modifyTest(tt.localTest)
			}

			got := tt.localTest.HasLocalChanges()
			if got != tt.want {
				t.Errorf("HasLocalChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSaveLocalTestStoresChecksum verifies that SaveLocalTest computes and stores checksum.
func TestSaveLocalTestStoresChecksum(t *testing.T) {
	// Create a temp directory for the test
	tmpDir, err := os.MkdirTemp("", "revyl-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	localTest := &LocalTest{
		Meta: TestMeta{
			RemoteID:      "test-123",
			RemoteVersion: 1,
			LocalVersion:  1,
		},
		Test: TestDefinition{
			Metadata: TestMetadata{
				Name:     "my-test",
				Platform: "android",
			},
			Blocks: []TestBlock{
				{Type: "instructions", StepDescription: "tap login button"},
			},
		},
	}

	// Verify checksum is empty before save
	if localTest.Meta.Checksum != "" {
		t.Error("Checksum should be empty before save")
	}

	// Save the test
	path := filepath.Join(tmpDir, "my-test.yaml")
	if err := SaveLocalTest(path, localTest); err != nil {
		t.Fatalf("SaveLocalTest() error = %v", err)
	}

	// Verify checksum was computed and stored
	if localTest.Meta.Checksum == "" {
		t.Error("Checksum should be set after save")
	}

	// Verify the checksum is correct
	expectedChecksum := ComputeTestChecksum(&localTest.Test)
	if localTest.Meta.Checksum != expectedChecksum {
		t.Errorf("Checksum = %q, want %q", localTest.Meta.Checksum, expectedChecksum)
	}

	// Load the test back and verify checksum is persisted
	loaded, err := LoadLocalTest(path)
	if err != nil {
		t.Fatalf("LoadLocalTest() error = %v", err)
	}

	if loaded.Meta.Checksum != expectedChecksum {
		t.Errorf("Loaded checksum = %q, want %q", loaded.Meta.Checksum, expectedChecksum)
	}

	// Verify HasLocalChanges returns false for freshly loaded test
	if loaded.HasLocalChanges() {
		t.Error("HasLocalChanges() should return false for freshly loaded test")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &ProjectConfig{}
	ApplyDefaults(cfg)

	if cfg.Defaults.OpenBrowser == nil {
		t.Fatal("ApplyDefaults() did not set OpenBrowser")
	}
	if got := EffectiveOpenBrowser(cfg); got {
		t.Errorf("EffectiveOpenBrowser() = %v, want false", got)
	}
	if got := EffectiveTimeoutSeconds(cfg, 30); got != DefaultTimeoutSeconds {
		t.Errorf("EffectiveTimeoutSeconds() = %d, want %d", got, DefaultTimeoutSeconds)
	}
}

func TestEffectiveOpenBrowserExplicitFalse(t *testing.T) {
	open := false
	cfg := &ProjectConfig{
		Defaults: Defaults{
			OpenBrowser: &open,
			Timeout:     90,
		},
	}

	if got := EffectiveOpenBrowser(cfg); got {
		t.Errorf("EffectiveOpenBrowser() = %v, want false", got)
	}
	if got := EffectiveTimeoutSeconds(cfg, 30); got != 90 {
		t.Errorf("EffectiveTimeoutSeconds() = %d, want 90", got)
	}
}

func TestEffectiveTimeoutFallback(t *testing.T) {
	if got := EffectiveTimeoutSeconds(nil, 45); got != 45 {
		t.Errorf("EffectiveTimeoutSeconds(nil, 45) = %d, want 45", got)
	}
	if got := EffectiveTimeoutSeconds(nil, 0); got != DefaultTimeoutSeconds {
		t.Errorf("EffectiveTimeoutSeconds(nil, 0) = %d, want %d", got, DefaultTimeoutSeconds)
	}
}

func TestValidateProviderConfig_ReactNative_Valid(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"react-native": {
				Port: 8081,
				PlatformKeys: map[string]string{
					"ios":     "ios-dev",
					"android": "android-dev",
				},
			},
		},
	}

	if err := hr.ValidateProvider("react-native"); err != nil {
		t.Fatalf("ValidateProvider(react-native) unexpected error: %v", err)
	}
}

func TestValidateProviderConfig_ReactNative_EmptyPlatformKey(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"react-native": {
				Port: 8081,
				PlatformKeys: map[string]string{
					"ios": "",
				},
			},
		},
	}

	err := hr.ValidateProvider("react-native")
	if err == nil {
		t.Fatal("ValidateProvider(react-native) expected error for empty platform key")
	}
	if got := err.Error(); got != "platform_keys.ios cannot be empty" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestValidateProviderConfig_ReactNative_InvalidPlatform(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"react-native": {
				Port: 8081,
				PlatformKeys: map[string]string{
					"web": "web-dev",
				},
			},
		},
	}

	err := hr.ValidateProvider("react-native")
	if err == nil {
		t.Fatal("ValidateProvider(react-native) expected error for invalid platform")
	}
	if got := err.Error(); got != "platform_keys.web must be ios or android" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestValidateProviderConfig_ReactNative_NoPlatformKeys(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"react-native": {
				Port: 8081,
			},
		},
	}

	if err := hr.ValidateProvider("react-native"); err != nil {
		t.Fatalf("ValidateProvider(react-native) with no platform_keys should pass: %v", err)
	}
}

func TestValidateProviderConfig_ReactNative_NoAppSchemeRequired(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"react-native": {
				Port:         8081,
				PlatformKeys: map[string]string{"ios": "ios-dev"},
			},
		},
	}

	if err := hr.ValidateProvider("react-native"); err != nil {
		t.Fatalf("react-native should not require app_scheme: %v", err)
	}
}

func TestValidateProviderConfig_UnknownProvider(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"flutter": {Port: 8081},
		},
	}

	err := hr.ValidateProvider("flutter")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if got := err.Error(); got != "unknown provider: flutter (supported: expo, react-native, swift, android)" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestValidateProviderConfig_Expo_Valid(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"expo": {
				Port:      8081,
				AppScheme: "bug-bazaar",
				PlatformKeys: map[string]string{
					"ios":     "ios-dev",
					"android": "android-dev",
				},
				UseExpPrefix: true,
			},
		},
	}

	if err := hr.ValidateProvider("expo"); err != nil {
		t.Fatalf("ValidateProvider(expo) unexpected error: %v", err)
	}
}

func TestValidateProviderConfig_Expo_MissingAppScheme(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"expo": {
				Port:         8081,
				PlatformKeys: map[string]string{"ios": "ios-dev"},
			},
		},
	}

	err := hr.ValidateProvider("expo")
	if err == nil {
		t.Fatal("ValidateProvider(expo) expected error for missing app_scheme")
	}
	if got := err.Error(); got != "app_scheme is required for Expo" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestValidateProviderConfig_Expo_InvalidPlatform(t *testing.T) {
	hr := &HotReloadConfig{
		Providers: map[string]*ProviderConfig{
			"expo": {
				Port:      8081,
				AppScheme: "bug-bazaar",
				PlatformKeys: map[string]string{
					"web": "web-dev",
				},
			},
		},
	}

	err := hr.ValidateProvider("expo")
	if err == nil {
		t.Fatal("ValidateProvider(expo) expected error for invalid platform")
	}
	if got := err.Error(); got != "platform_keys.web must be ios or android" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestWriteLoadProjectConfig_HotReloadExpoRoundTrip(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &ProjectConfig{
		Project: Project{
			Name: "bug-bazaar",
		},
		Build: BuildConfig{
			System: "Expo",
			Platforms: map[string]BuildPlatform{
				"ios-dev": {
					AppID: "ios-app-id",
				},
				"android-dev": {
					AppID: "android-app-id",
				},
			},
		},
		HotReload: HotReloadConfig{
			Default: "expo",
			Providers: map[string]*ProviderConfig{
				"expo": {
					Port:      8081,
					AppScheme: "bug-bazaar",
					PlatformKeys: map[string]string{
						"ios":     "ios-dev",
						"android": "android-dev",
					},
					UseExpPrefix: true,
				},
			},
		},
	}

	if err := WriteProjectConfig(configPath, cfg); err != nil {
		t.Fatalf("WriteProjectConfig() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	content := string(data)
	expectedIndentedLines := []string{
		"        expo:",
		"            # Metro bundler port (default 8081). Change if port conflicts.",
		"            port: 8081",
		"            # URL scheme from app.json or app.config.js (required for Expo deep linking)",
		"            app_scheme: bug-bazaar",
		"            # Maps platform to build.platforms key for dev build resolution",
		"            platform_keys:",
		"                android: android-dev",
		"                ios: ios-dev",
		"            # Use \"exp+\" prefix in deep links. Try true if deep links fail.",
		"            use_exp_prefix: true",
	}
	for _, expectedLine := range expectedIndentedLines {
		if !strings.Contains(content, expectedLine) {
			t.Fatalf("written config missing expected line %q\n%s", expectedLine, content)
		}
	}

	loaded, err := LoadProjectConfig(configPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig() error = %v", err)
	}

	if loaded.HotReload.Default != "expo" {
		t.Fatalf("loaded hotreload default = %q, want expo", loaded.HotReload.Default)
	}

	expoConfig := loaded.HotReload.GetProviderConfig("expo")
	if expoConfig == nil {
		t.Fatal("loaded hotreload.providers.expo is nil")
	}
	if expoConfig.Port != 8081 {
		t.Fatalf("loaded expo port = %d, want 8081", expoConfig.Port)
	}
	if expoConfig.AppScheme != "bug-bazaar" {
		t.Fatalf("loaded expo app scheme = %q, want bug-bazaar", expoConfig.AppScheme)
	}
	if !expoConfig.UseExpPrefix {
		t.Fatal("loaded expo use_exp_prefix = false, want true")
	}
	if got := expoConfig.PlatformKeys["ios"]; got != "ios-dev" {
		t.Fatalf("loaded expo ios platform key = %q, want ios-dev", got)
	}
	if got := expoConfig.PlatformKeys["android"]; got != "android-dev" {
		t.Fatalf("loaded expo android platform key = %q, want android-dev", got)
	}
}
