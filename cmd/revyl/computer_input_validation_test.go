package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/testutil"
	"github.com/spf13/cobra"
)

func TestComputerRejectsInvalidUTF8BeforeSerializingArgv(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(422) }))
	defer server.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	root := &cobra.Command{Use: "revyl", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newComputerCommand())
	root.SetContext(context.Background())
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"computer", "exec", "11111111-1111-4111-8111-111111111111", "--json", "--", string([]byte{255})})
	if err := root.Execute(); err == nil || calls.Load() != 0 {
		t.Fatal("invalid UTF-8 argv reached HTTP dispatch")
	}
}

func TestComputerRejectsResponseCanariesBeforeOutputAndAnalytics(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	const id = "11111111-1111-4111-8111-111111111111"
	const canary = "private-response-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"session_id":%q,"trajectory_id":"22222222-2222-4222-8222-222222222222","steps":[{"step_id":1,"timestamp":"2026-10-10T00:00:00Z","source":"system","message":"Computer created"}],"extra":{"revyl":{"report_id":"22222222-2222-4222-8222-222222222222","os":"macos","as_of":"2026-10-10T00:00:00Z","expires_at":"2026-10-10T00:05:00Z","execution_ended_at":null,"state":"active","release":%q,"released_at":null,"evidence_completeness":"partial","evidence_write_status":%q,"child_reports":[]}}}`, id, canary, canary)
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "")
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	args := []string{"computer", "report", id, "--format", "atif", "--json"}
	leaf, _, err := root.Find(args)
	if err != nil {
		t.Fatal(err)
	}
	var payloads []analytics.TelemetryPayload
	recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) { payloads = append(payloads, payload) })
	run := recorder.StartCommand(leaf, nil)
	root.SetContext(analytics.ContextWithCommandRun(context.Background(), run))
	t.Setenv("REVYL_API_KEY", "synthetic-only")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err = root.Execute()
	run.Complete(err)
	run.Flush()
	if err == nil {
		t.Fatal("untrusted response was accepted")
	}
	encoded, marshalErr := json.Marshal(payloads)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, output := range [][]byte{stdout.Bytes(), stderr.Bytes(), encoded, []byte(err.Error())} {
		if bytes.Contains(output, []byte(canary)) {
			t.Fatal("untrusted response content escaped the boundary")
		}
	}
	if !json.Valid(stdout.Bytes()) {
		t.Fatal("error stdout is not pure JSON")
	}
	if len(payloads) != 1 || len(payloads[0].Events) != 2 {
		t.Fatal("command lifecycle was not retained")
	}
	for _, key := range []string{"computer_outcome", "computer_release", "computer_evidence_write_status"} {
		if _, exists := payloads[0].Events[1].Properties[key]; exists {
			t.Fatal("unvalidated server fact entered telemetry")
		}
	}
}

func TestComputerInvalidPendingBodyProducesNoTerminalFacts(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("\u00a0null"))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "")
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	args := []string{"computer", "operation", "get", "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "--json"}
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
	if err == nil {
		t.Fatal("invalid JSON accepted as pending")
	}
	if !json.Valid(stdout.Bytes()) || strings.Contains(stdout.String(), `"pending"`) {
		t.Fatal("invalid body rendered as pending")
	}
	if len(payloads) != 1 || len(payloads[0].Events) != 2 {
		t.Fatal("command lifecycle was not retained")
	}
	for _, key := range []string{"computer_outcome", "computer_release", "computer_evidence_write_status"} {
		if _, exists := payloads[0].Events[1].Properties[key]; exists {
			t.Fatal("invalid JSON supplied a terminal fact")
		}
	}
}
