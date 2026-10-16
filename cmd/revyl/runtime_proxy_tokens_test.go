package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRuntimeCLIResolvedTokenReachesEveryGroundedAction(t *testing.T) {
	for _, action := range []struct {
		name, path string
		run        func(*cobra.Command, []string) error
	}{
		{"tap", "tap", deviceTapCmd.RunE}, {"double-tap", "double_tap", deviceDoubleTapCmd.RunE},
		{"long-press", "longpress", deviceLongPressCmd.RunE}, {"type", "input", deviceTypeCmd.RunE},
		{"swipe", "swipe", deviceSwipeCmd.RunE}, {"clear-text", "clear_text", deviceClearTextCmd.RunE},
	} {
		t.Run(action.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv(sessionIDEnvVar, "")
			physicalCalls := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/device-sessions/"):
					fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
				case strings.HasSuffix(r.URL.Path, "/resolve_target"):
					_, _ = w.Write([]byte(`{"found":true,"x":12,"y":34,"screen_token":"fixture-runtime-observation"}`))
				case strings.HasSuffix(r.URL.Path, "/"+action.path):
					physicalCalls++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["screen_token"] != "fixture-runtime-observation" || body["x"] != float64(12) || body["y"] != float64(34) {
						t.Error("CLI dropped the exact target observation token")
					}
					_, _ = w.Write([]byte(`{"success":true,"found":true,"x":12,"y":34}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			t.Setenv("REVYL_BACKEND_URL", backend.URL)
			cmd := newStopTestCommand(t, "-s", flowSessionID)
			cmd.SetContext(context.Background())
			cmd.Flags().String("target", "fixture target", "")
			cmd.Flags().Int("x", 0, "")
			cmd.Flags().Int("y", 0, "")
			cmd.Flags().Int("duration", 500, "")
			cmd.Flags().String("text", "fixture text", "")
			cmd.Flags().String("text-env", "", "")
			cmd.Flags().Bool("clear-first", true, "")
			cmd.Flags().String("direction", "up", "")
			if err := action.run(cmd, nil); err != nil || physicalCalls != 1 {
				t.Fatalf("grounded CLI action failed: calls=%d err=%v", physicalCalls, err)
			}
		})
	}
}

func TestRuntimeCLIScreenshotPublishesOnlyLocalAnchorToken(t *testing.T) {
	withWorkingDirectory(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv(sessionIDEnvVar, "")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/device-sessions/") {
			fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":"web","viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, flowWorkflowRunID)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/screenshot") {
			w.Header().Set("X-Revyl-Screen-Token", "fixture-runtime-observation")
			_, _ = w.Write([]byte("fixture-image-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	cmd := newStopTestCommand(t, "-s", flowSessionID)
	cmd.Flags().String("out", "", "")
	_ = cmd.Flags().Set("json", "true")
	output := captureStdout(t, func() {
		if err := deviceScreenshotCmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "fixture-runtime-observation") {
		t.Fatal("runtime observation token reached stdout instead of private cache")
	}
	var result struct {
		ScreenToken string `json:"screen_token"`
	}
	if json.Unmarshal([]byte(output), &result) != nil || !strings.HasPrefix(result.ScreenToken, "screen-") {
		t.Fatal("screenshot did not expose the CLI-local anchor token")
	}
}
