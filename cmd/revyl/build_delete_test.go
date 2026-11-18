package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newBuildDeleteTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "delete"}
	cmd.SetContext(context.Background())
	cmd.Flags().BoolP("force", "f", false, "")
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("dev", false, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cmd
}

func TestBuildDeleteIsRegisteredUnderBuild(t *testing.T) {
	found, _, err := buildCmd.Find([]string{"delete"})
	if err != nil || found != buildDeleteCmd {
		t.Fatalf("revyl build delete is not registered: %v", err)
	}
	if buildDeleteCmd.Flags().ShorthandLookup("f") == nil || buildDeleteCmd.Flags().Lookup("json") == nil {
		t.Fatal("revyl build delete is missing --force/-f or --json")
	}
}

func TestBuildDeleteForceJSONDeletesTheVersion(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/apps/builds/bv-1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		deletes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"Build version deleted"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	cmd := newBuildDeleteTestCommand(t, "--force", "--json")
	stdout, _ := captureStdoutAndStderrSeparate(t, func() {
		if err := buildDeleteCmd.RunE(cmd, []string{"bv-1"}); err != nil {
			t.Fatalf("build delete: %v", err)
		}
	})

	if deletes != 1 {
		t.Fatalf("DELETE calls = %d, want 1", deletes)
	}
	output := parseJSON(t, stdout)
	assertJSONString(t, output, "build_version_id", "bv-1")
	if output["success"] != true {
		t.Fatalf("success = %v, want true", output["success"])
	}
}

func TestBuildDeleteWithoutForceRefusesWhenItCannotPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unconfirmed delete reached the backend: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	originalStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = reader.Close()
	})

	var runErr error
	captureStdoutAndStderrSeparate(t, func() {
		runErr = buildDeleteCmd.RunE(newBuildDeleteTestCommand(t), []string{"bv-1"})
	})

	if runErr == nil || !strings.Contains(runErr.Error(), "--force") {
		t.Fatalf("error = %v, want guidance to rerun with --force", runErr)
	}
}

func TestBuildDeleteNotFoundNamesTheListCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Build version not found"}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)

	err := buildDeleteCmd.RunE(newBuildDeleteTestCommand(t, "--force"), []string{"bv-missing"})

	if err == nil || !strings.Contains(err.Error(), "revyl build list") {
		t.Fatalf("error = %v, want not-found guidance", err)
	}
}
