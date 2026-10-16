package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestDeviceTypeEnvironmentValidationBeforeTransport(t *testing.T) {
	for _, tc := range []struct {
		name, text, envName string
		want                string
	}{
		{name: "both inputs", text: "synthetic", envName: "TEST_PASSWORD", want: "mutually exclusive"},
		{name: "empty name", envName: "", want: "environment variable name"},
		{name: "invalid name", envName: "NOT=A_NAME", want: "environment variable name"},
		{name: "oversized name", envName: strings.Repeat("A", 129), want: "at most 128"},
		{name: "missing variable", envName: "UNSET_DEVICE_TYPE_TEST", want: "non-empty local environment variable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newLeafCommand("type", deviceTypeCmd.RunE)
			cmd.Flags().String("text", "", "")
			cmd.Flags().String("text-env", "", "")
			if err := cmd.Flags().Set("text-env", tc.envName); err != nil {
				t.Fatal(err)
			}
			if tc.text != "" {
				if err := cmd.Flags().Set("text", tc.text); err != nil {
					t.Fatal(err)
				}
			}
			if err := deviceTypeCmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), tc.text) && tc.text != "" {
				t.Fatalf("input validation did not fail safely: %v", err)
			}
		})
	}
}

func TestDeviceTypeOutputPrivacyAndPhoneCompatibility(t *testing.T) {
	const typedText = "synthetic-typed-value-\"-\\-Ω"
	for _, platform := range []string{"web", "ios", "android"} {
		for _, envInput := range []bool{false, true} {
			for _, jsonOutput := range []bool{false, true} {
				for _, failed := range []bool{false, true} {
					name := fmt.Sprintf("%s/env=%t/json=%t/failed=%t", platform, envInput, jsonOutput, failed)
					t.Run(name, func(t *testing.T) {
						withWorkingDirectory(t, t.TempDir())
						t.Setenv("REVYL_API_KEY", "test-key")
						t.Setenv("TYPE_TEST_INPUT", typedText)
						t.Setenv(sessionIDEnvVar, "")
						calls := 0
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("Content-Type", "application/json")
							switch r.URL.Path {
							case "/api/v1/execution/device-sessions/" + flowSessionID:
								if platform == "web" {
									fmt.Fprintf(w, `{"id":%q,"org_id":"org-1","platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
								} else {
									fmt.Fprintf(w, `{"id":%q,"org_id":"org-1","platform":%q,"status":"running","workflow_run_id":%q}`, flowSessionID, platform, flowWorkflowRunID)
								}
							case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/input":
								calls++
								var body map[string]any
								if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
									t.Error(err)
								}
								if body["text"] != typedText || body["clear_first"] != true || body["x"] != float64(10) || body["y"] != float64(20) {
									t.Error("actual text was not delivered with the unchanged input contract")
								}
								passwordOnly, present := body["password_only"]
								if platform == "web" && envInput {
									if passwordOnly != true {
										t.Error("Web environment input omitted password-only guard")
									}
								} else if present {
									t.Error("ordinary Web or phone input changed its request contract")
								}
								if _, present := body["text_env"]; present {
									t.Error("input transported the environment variable name")
								}
								_ = json.NewEncoder(w).Encode(map[string]any{"success": !failed, "text": typedText, "error": typedText, "target": typedText, "latency_ms": 8})
							default:
								http.NotFound(w, r)
							}
						}))
						t.Cleanup(server.Close)
						t.Setenv("REVYL_BACKEND_URL", server.URL)
						cmd := newStopTestCommand(t, "-s", flowSessionID)
						cmd.SetContext(context.Background())
						cmd.Flags().String("target", "", "")
						cmd.Flags().Int("x", 0, "")
						cmd.Flags().Int("y", 0, "")
						cmd.Flags().String("text", "", "")
						cmd.Flags().String("text-env", "", "")
						cmd.Flags().Bool("clear-first", true, "")
						flags := map[string]string{"x": "10", "y": "20", "json": fmt.Sprint(jsonOutput)}
						if envInput {
							flags["text-env"] = "TYPE_TEST_INPUT"
						} else {
							flags["text"] = typedText
						}
						for name, value := range flags {
							if err := cmd.Flags().Set(name, value); err != nil {
								t.Fatal(err)
							}
						}
						var runErr error
						stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = deviceTypeCmd.RunE(cmd, nil) })
						if calls != 1 || (runErr != nil) != failed {
							t.Fatalf("calls=%d failed=%t err=%v", calls, failed, runErr)
						}
						if platform == "web" || envInput {
							for _, output := range []string{stdout, stderr, fmt.Sprint(runErr)} {
								if strings.Contains(output, "synthetic-typed-value") || strings.Contains(output, "TYPE_TEST_INPUT") {
									t.Fatal("private typing output exposed the input or worker echo")
								}
							}
						} else if jsonOutput {
							var result ActionResult
							if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Text != typedText {
								t.Fatal("legacy phone --text JSON contract changed")
							}
						} else if !failed && !strings.Contains(stderr, typedText) {
							t.Fatal("legacy phone --text human output changed")
						}
					})
				}
			}
		}
	}
}

func TestWebHierarchyFailsBeforeWorkerTransport(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/execution/device-sessions/"+flowSessionID {
			t.Fatal("Web hierarchy reached worker transport")
		}
		fmt.Fprintf(w, `{"id":%q,"platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	cmd := newStopTestCommand(t, "-s", flowSessionID)
	cmd.Use = "hierarchy"
	var unavailable *api.APIError
	err := deviceHierarchyCmd.RunE(cmd, nil)
	if !errors.As(err, &unavailable) || unavailable.StatusCode != 501 || unavailable.Code != "not_available_on_platform" {
		t.Fatalf("hierarchy error=%v", err)
	}
}

func TestWebLiveRequestsPassThePlatformGuard(t *testing.T) {
	if err := validateLiveNetworkPlatform("web"); err != nil {
		t.Fatal(err)
	}
}
