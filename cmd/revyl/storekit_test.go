package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

// Extension is validated before any upload, so client may be nil here.
func TestStoreKitSetRejectsNonStorekitPath(t *testing.T) {
	for _, path := range []string{"config.json", "Makefile"} {
		err := storeKitSet(&cobra.Command{}, nil, "app", "id", "app \"x\"", path, "")
		if err == nil {
			t.Fatalf("expected error for %q, got nil", path)
		}
		if !strings.Contains(err.Error(), ".storekit") {
			t.Fatalf("expected .storekit in error for %q, got %v", path, err)
		}
	}
}

func TestStoreKitSetRejectsBothPathAndFileID(t *testing.T) {
	err := storeKitSet(&cobra.Command{}, nil, "app", "id", "app \"x\"", "Premium.storekit", "Existing.storekit")
	if err == nil {
		t.Fatal("expected error when both a path and --file-id are given, got nil")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected 'not both' error, got %v", err)
	}
}

func TestBuildStoreKitShowJSONUnconfigured(t *testing.T) {
	out := buildStoreKitShowJSON("app", "a1", nil)
	if out.Configured {
		t.Fatal("expected Configured=false for nil ref")
	}
	if out.Mode != nil || out.FileID != nil || out.Filename != nil {
		t.Fatalf("expected nil mode/file_id/filename, got %+v", out)
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	for _, key := range []string{"scope_type", "scope_id", "configured", "mode", "file_id", "filename"} {
		if !strings.Contains(string(data), "\""+key+"\"") {
			t.Fatalf("missing key %q in %s", key, data)
		}
	}
}

func TestBuildStoreKitShowJSONFileMode(t *testing.T) {
	fileID, filename := "f1", "App.storekit"
	out := buildStoreKitShowJSON("test", "t1", &api.StoreKitConfigRef{
		Mode: "file", FileID: &fileID, Filename: &filename,
	})
	if !out.Configured {
		t.Fatal("expected Configured=true")
	}
	if out.Mode == nil || *out.Mode != "file" {
		t.Fatalf("expected mode=file, got %+v", out.Mode)
	}
	if out.Filename == nil || *out.Filename != "App.storekit" {
		t.Fatalf("expected filename App.storekit, got %+v", out.Filename)
	}
}

func TestBuildStoreKitShowJSONDisabledMode(t *testing.T) {
	mode := "disabled"
	out := buildStoreKitShowJSON("app", "a1", &api.StoreKitConfigRef{Mode: mode})
	if !out.Configured {
		t.Fatal("expected Configured=true for an explicit disabled ref")
	}
	if out.Mode == nil || *out.Mode != "disabled" {
		t.Fatalf("expected mode=disabled, got %+v", out.Mode)
	}
	if out.FileID != nil || out.Filename != nil {
		t.Fatalf("expected nil file_id/filename for disabled, got %+v", out)
	}
}
