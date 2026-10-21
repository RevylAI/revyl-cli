package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
	"github.com/spf13/cobra"
)

func TestComputerInvalidWriteResultCannotSupplyTerminalFacts(t *testing.T) {
	for _, execution := range []string{
		`{"outcome":"succeeded","result":{"kind":"file_write","byte_count":0}}`,
		`{"outcome":"timed_out","termination":"confirmed","observed_exit_code":7}`,
	} {
		t.Run(execution, func(t *testing.T) {
			testutil.SetHomeDir(t, t.TempDir())
			file := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(file, []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			id, requestID := uuid.New(), uuid.New()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"computer_session_id":%q,"operation_id":%q,"evidence":"recorded","execution":%s}`, id, api.ComputerOperationID(id, requestID), execution)
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "")
			root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
			root.AddCommand(newComputerCommand())
			args := []string{"computer", "files", "write", id.String(), "file", "--from", file, "--request-id", requestID.String(), "--json"}
			leaf, _, err := root.Find(args)
			if err != nil {
				t.Fatal(err)
			}
			var payloads []analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { payloads = append(payloads, payload) })
			run := recorder.StartCommand(leaf, nil)
			root.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
			t.Setenv("REVYL_API_KEY", "synthetic-only")
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)
			err = root.Execute()
			run.Complete(err)
			run.Flush()
			if err == nil || !json.Valid(stdout.Bytes()) || bytes.Contains(stdout.Bytes(), []byte(`"execution"`)) {
				t.Fatal("invalid outcome reached command output")
			}
			if len(payloads) != 1 || len(payloads[0].Events) != 2 {
				t.Fatal("command lifecycle was not retained")
			}
			for _, key := range []string{"computer_outcome", "computer_evidence_write_status", "computer_release"} {
				if _, exists := payloads[0].Events[1].Properties[key]; exists {
					t.Fatal("invalid outcome reached terminal analytics")
				}
			}
		})
	}
}
