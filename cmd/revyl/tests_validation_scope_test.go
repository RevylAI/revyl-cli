package main

import (
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func writeLocalTestFixture(t *testing.T, testsDir, name, stepDescription string) {
	t.Helper()
	lt := &config.LocalTest{
		Meta: config.TestMeta{
			RemoteID:      "remote-" + name,
			RemoteVersion: 1,
			LocalVersion:  1,
			LastSyncedAt:  "2026-01-01T00:00:00Z",
		},
		Test: config.TestDefinition{
			Metadata: config.TestMetadata{Name: name, Platform: "ios"},
			Build:    config.TestBuildConfig{Name: "My App"},
			Blocks: []config.TestBlock{
				{Type: "instructions", StepDescription: stepDescription},
			},
		},
	}
	if err := config.SaveLocalTest(filepath.Join(testsDir, name+".yaml"), lt); err != nil {
		t.Fatalf("SaveLocalTest(%s) error = %v", name, err)
	}
}

func TestValidateTestsForPush_ValidatesOnlyProvidedNames(t *testing.T) {
	testsDir := t.TempDir()

	writeLocalTestFixture(t, testsDir, "valid-test", "Tap login")
	writeLocalTestFixture(t, testsDir, "invalid-test", "   ")

	localTests, err := config.LoadLocalTests(testsDir)
	if err != nil {
		t.Fatalf("LoadLocalTests() error = %v", err)
	}

	if err := validateTestsForPush([]string{"valid-test"}, testsDir, localTests); err != nil {
		t.Fatalf("validateTestsForPush(valid-test) error = %v, want nil", err)
	}

	if err := validateTestsForPush([]string{"invalid-test"}, testsDir, localTests); err == nil {
		t.Fatal("validateTestsForPush(invalid-test) error = nil, want validation error")
	}
}
