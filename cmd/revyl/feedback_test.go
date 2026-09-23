package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func TestResolveFeedbackTypeRejectsUnknownValues(t *testing.T) {
	if _, err := resolveFeedbackType("blocker"); err == nil || !strings.Contains(err.Error(), "bug, feature, or other") {
		t.Fatalf("error = %v, want local type rejection", err)
	}
	got, err := resolveFeedbackType(" feature ")
	if err != nil {
		t.Fatal(err)
	}
	if got != api.SupportRequestTypeFeature {
		t.Fatalf("type = %q", got)
	}
}

func TestFeedbackCommandPostsTypedJSONAndPrintsResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/support/requests" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("body = %s: %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sent":                 true,
			"attachments_uploaded": true,
		})
	}))
	t.Cleanup(server.Close)

	original := feedbackSetupClient
	feedbackSetupClient = func(*cobra.Command) (*api.Client, error) {
		return api.NewClientWithBaseURL("test-key", server.URL), nil
	}
	t.Cleanup(func() { feedbackSetupClient = original })

	command := newFeedbackCommand()
	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	root.AddCommand(command)
	_ = root.PersistentFlags().Set("json", "true")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	root.SetArgs([]string{
		"feedback",
		"--type", "bug",
		"--body", "revyl dev rebuild hangs after a Metro restart",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	if captured["type"] != "bug" {
		t.Fatalf("type = %#v", captured["type"])
	}
	if captured["message"] != "revyl dev rebuild hangs after a Metro restart" {
		t.Fatalf("message = %#v", captured["message"])
	}
	context, _ := captured["context"].(map[string]any)
	if context["source"] != "cli" {
		t.Fatalf("context = %#v", captured["context"])
	}
	if context["os"] == "" || context["arch"] == "" || context["cli_version"] == "" {
		t.Fatalf("context missing runtime fields: %#v", context)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout = %q: %v", stdout.String(), err)
	}
	if payload["sent"] != true || payload["attachments_uploaded"] != true {
		t.Fatalf("json = %#v", payload)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFeedbackCommandHumanOutputGoesToStderr(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sent": true, "attachments_uploaded": true})
	}))
	t.Cleanup(server.Close)

	original := feedbackSetupClient
	feedbackSetupClient = func(*cobra.Command) (*api.Client, error) {
		return api.NewClientWithBaseURL("test-key", server.URL), nil
	}
	t.Cleanup(func() { feedbackSetupClient = original })

	command := newFeedbackCommand()
	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	root.AddCommand(command)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	root.SetArgs([]string{"feedback", "--type", "other", "--body", "thanks"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Feedback submitted.") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFeedbackCommandRejectsOversizedBodyLocally(t *testing.T) {
	command := newFeedbackCommand()
	command.SilenceUsage = true
	if err := command.Flags().Set("type", "bug"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("body", strings.Repeat("x", feedbackBodyLimit+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandBody(command, commandBodyOptions{body: strings.Repeat("x", feedbackBodyLimit+1)}, "feedback body", 0, feedbackBodyLimit); err == nil || !strings.Contains(err.Error(), "4000 characters") {
		t.Fatalf("error = %v, want local 4000-character rejection", err)
	}
}

func TestReadCommandBodySupportsStdinAndRejectsBothFlags(t *testing.T) {
	command := newFeedbackCommand()
	command.SetIn(strings.NewReader("  grounded feedback from stdin  \n"))
	if err := command.Flags().Set("body-file", "-"); err != nil {
		t.Fatal(err)
	}
	body, err := readCommandBody(command, commandBodyOptions{bodyFile: "-"}, "feedback body", 0, feedbackBodyLimit)
	if err != nil {
		t.Fatal(err)
	}
	if body != "grounded feedback from stdin" {
		t.Fatalf("body = %q", body)
	}

	both := newFeedbackCommand()
	_ = both.Flags().Set("body", "one")
	_ = both.Flags().Set("body-file", "two")
	if _, err := readCommandBody(both, commandBodyOptions{body: "one", bodyFile: "two"}, "feedback body", 0, feedbackBodyLimit); err == nil {
		t.Fatal("expected mutually exclusive body source error")
	}
}

func TestReadCommandBodyRejectsOversizedFile(t *testing.T) {
	path := t.TempDir() + "/body.txt"
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), feedbackBodyLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newFeedbackCommand()
	if err := command.Flags().Set("body-file", path); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandBody(command, commandBodyOptions{bodyFile: path}, "feedback body", 0, feedbackBodyLimit); err == nil || !strings.Contains(err.Error(), "4000 characters") {
		t.Fatalf("error = %v, want body size rejection", err)
	}
}
