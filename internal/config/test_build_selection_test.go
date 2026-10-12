package config

import (
	"path/filepath"
	"testing"
)

func TestSavedBuildSelectorYAMLRoundTripAndChecksum(t *testing.T) {
	original := &LocalTest{Test: TestDefinition{Metadata: TestMetadata{Name: "selector"}, Build: TestBuildConfig{Name: "App", BranchID: "11111111-1111-4111-8111-111111111111", BuildSelection: "pinned", PinnedBuildID: "22222222-2222-4222-8222-222222222222"}}}
	path := filepath.Join(t.TempDir(), "test.yaml")
	if err := SaveLocalTest(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadLocalTest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Test.Build != original.Test.Build {
		t.Fatalf("build=%+v", loaded.Test.Build)
	}
	before := ComputeTestChecksum(&loaded.Test)
	loaded.Test.Build.BuildSelection = "latest"
	loaded.Test.Build.PinnedBuildID = ""
	if ComputeTestChecksum(&loaded.Test) == before {
		t.Fatal("selector edit did not change checksum")
	}
}
