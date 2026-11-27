package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/testutil"
)

const computerInferenceTestID = "11111111-1111-4111-8111-111111111111"

func runComputerInference(t *testing.T, answer string, args ...string) (string, error) {
	t.Helper()
	testutil.SetHomeDir(t, t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/computer-runs/"+computerInferenceTestID+"/inference/endpoint" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, answer)
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"computer", "inference", computerInferenceTestID}, args...))
	err := root.Execute()
	return output.String(), err
}

func TestComputerInferenceEnvPrintsQuotedExportLines(t *testing.T) {
	answer := `{"available":true,"base_url":"http://192.168.64.1:8870/v1","api_key":"fixture-key-it's-0123456789abcdef0123","model":"fixture-model","max_output_tokens":4096}`

	env, err := runComputerInference(t, answer, "--env")

	want := "export OPENAI_BASE_URL='http://192.168.64.1:8870/v1'\nexport OPENAI_API_KEY='fixture-key-it'\\''s-0123456789abcdef0123'\nexport OPENAI_MODEL='fixture-model'\n"
	if err != nil || env != want {
		t.Fatalf("--env printed\n%s\nwant\n%s (%v)", env, want, err)
	}
}

func TestComputerInferenceUnavailableExitsNonZeroAndPrintsNoExports(t *testing.T) {
	answer := `{"available":false,"reason":"host_unsupported"}`

	printed, err := runComputerInference(t, answer, "--env")

	if err == nil || !strings.Contains(err.Error(), "doesn't serve an inference endpoint") {
		t.Fatalf("unavailable endpoint returned %v", err)
	}
	if printed != "" {
		t.Fatalf("unavailable endpoint printed %q", printed)
	}
}
