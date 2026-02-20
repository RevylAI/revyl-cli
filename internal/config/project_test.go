// Package config provides project configuration management.
package config

import (
	"os"
	"path/filepath"
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
	if got := EffectiveOpenBrowser(cfg); !got {
		t.Errorf("EffectiveOpenBrowser() = %v, want true", got)
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
