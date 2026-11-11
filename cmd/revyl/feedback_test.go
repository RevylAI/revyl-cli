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
	if _, err := resolveFeedbackType("blocker"); err == nil || !strings.Contains(err.Error(), "bug, friction, feature, or other") {
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
	if !strings.Contains(stderr.String(), "Feedback sent to the Revyl team.") {
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

func newFeedbackTestRoot(t *testing.T, captured *map[string]any) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, captured); err != nil {
			t.Fatalf("body = %s: %v", body, err)
		}
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
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	root.AddCommand(command)
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	return root, &stdout, &stderr
}

func TestFeedbackCommandAcceptsPositionalMessageAndRef(t *testing.T) {
	var captured map[string]any
	root, _, stderr := newFeedbackTestRoot(t, &captured)
	root.SetArgs([]string{"feedback", "--type", "bug", "--ref", "6f1c2d3e-0000-4000-8000-000000000000", "  rebuild hung after Metro restarted  "})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	want := "rebuild hung after Metro restarted\n\nRef: 6f1c2d3e-0000-4000-8000-000000000000"
	if captured["message"] != want {
		t.Fatalf("message = %#v, want %q", captured["message"], want)
	}
	if strings.TrimSpace(stderr.String()) != "Feedback sent to the Revyl team." {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFeedbackCommandRejectsAmbiguousOrMissingMessageWithoutPrompting(t *testing.T) {
	for name, args := range map[string][]string{
		"argument and flag": {"feedback", "--type", "bug", "--body", "one", "two"},
		"no message":        {"feedback", "--type", "bug"},
		"bad ref":           {"feedback", "--type", "bug", "--ref", "has space", "msg"},
		"two arguments":     {"feedback", "--type", "bug", "one", "two"},
	} {
		t.Run(name, func(t *testing.T) {
			var captured map[string]any
			root, _, _ := newFeedbackTestRoot(t, &captured)
			root.SetArgs(args)
			if err := root.Execute(); err == nil {
				t.Fatal("expected a local error")
			}
			if captured != nil {
				t.Fatalf("request sent: %#v", captured)
			}
		})
	}
}

func TestFeedbackRefCountsTowardMessageLimit(t *testing.T) {
	var captured map[string]any
	root, _, _ := newFeedbackTestRoot(t, &captured)
	root.SetArgs([]string{"feedback", "--type", "bug", "--ref", "run-1", strings.Repeat("x", feedbackBodyLimit)})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "characters") {
		t.Fatalf("error = %v, want length rejection", err)
	}
}

func newFrictionFeedbackRoot(t *testing.T, handle func(requestType string) int) (*cobra.Command, *[]map[string]any) {
	t.Helper()
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var captured map[string]any
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, captured)
		w.Header().Set("Content-Type", "application/json")
		status := handle(captured["type"].(string))
		w.WriteHeader(status)
		if status != http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": []map[string]any{{"loc": []string{"body", "type"}, "msg": "Input should be 'bug', 'feature' or 'other'"}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sent": true, "attachments_uploaded": true})
	}))
	t.Cleanup(server.Close)
	original := feedbackSetupClient
	feedbackSetupClient = func(*cobra.Command) (*api.Client, error) {
		return api.NewClientWithBaseURL("test-key", server.URL), nil
	}
	t.Cleanup(func() { feedbackSetupClient = original })

	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("json", false, "")
	root.PersistentFlags().Bool("dev", false, "")
	root.AddCommand(newFeedbackCommand())
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root, &requests
}

func TestFrictionFeedbackSendsFrictionToACurrentBackend(t *testing.T) {
	root, requests := newFrictionFeedbackRoot(t, func(string) int { return http.StatusOK })
	root.SetArgs([]string{"feedback", "--type", "friction", "had to guess the build flag"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 1 || (*requests)[0]["type"] != "friction" || (*requests)[0]["message"] != "had to guess the build flag" {
		t.Fatalf("requests = %#v", *requests)
	}
}

func TestFrictionFeedbackFallsBackToOtherOnABackendWithoutFriction(t *testing.T) {
	root, requests := newFrictionFeedbackRoot(t, func(requestType string) int {
		if requestType == "friction" {
			return http.StatusUnprocessableEntity
		}
		return http.StatusOK
	})
	root.SetArgs([]string{"feedback", "--type", "friction", "--ref", "run-1", "had to guess the build flag"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 {
		t.Fatalf("requests = %#v, want the friction attempt and one fallback", *requests)
	}
	fallback := (*requests)[1]
	if fallback["type"] != "other" || fallback["message"] != "[friction] had to guess the build flag\n\nRef: run-1" {
		t.Fatalf("fallback = %#v", fallback)
	}
}

func TestFeedbackFallbackOnlyAppliesToFriction(t *testing.T) {
	root, requests := newFrictionFeedbackRoot(t, func(string) int { return http.StatusUnprocessableEntity })
	root.SetArgs([]string{"feedback", "--type", "bug", "broken"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected the rejection to surface")
	}
	if len(*requests) != 1 {
		t.Fatalf("requests = %#v, want no retry", *requests)
	}
}

func TestFrictionFallbackPrefixFitsTheMessageLimit(t *testing.T) {
	root, requests := newFrictionFeedbackRoot(t, func(string) int { return http.StatusOK })
	root.SetArgs([]string{"feedback", "--type", "friction", strings.Repeat("x", feedbackBodyLimit-len(frictionFallbackPrefix)+1)})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "characters") {
		t.Fatalf("error = %v, want length rejection", err)
	}
	if len(*requests) != 0 {
		t.Fatalf("requests = %#v", *requests)
	}
}
