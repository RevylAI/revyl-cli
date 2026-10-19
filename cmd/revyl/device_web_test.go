package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newWebStartCommand() *cobra.Command {
	cmd := newDeviceStartTestCommand(context.Background())
	cmd.Flags().String("app", "", "")
	cmd.Flags().String("url", "", "")
	cmd.Flags().String("commit", "", "")
	cmd.Flags().String("viewport", "1280x800", "")
	_ = cmd.Flags().Set("platform", "web")
	return cmd
}

func TestWebStartCLIValidatesBeforeTransport(t *testing.T) {
	for _, test := range []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"no app", map[string]string{"url": "https://example.test/"}, "requires an app"},
		{"empty URL", map[string]string{"url": ""}, "requires an HTTPS URL"},
		{"viewport", map[string]string{"viewport": "1x1"}, "WIDTHxHEIGHT"},
		{"conflicting apps", map[string]string{"app": "one", "app-id": "two"}, "same app"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := newWebStartCommand()
			for name, value := range test.flags {
				_ = cmd.Flags().Set(name, value)
			}
			if err := deviceStartCmd.RunE(cmd, nil); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want %s", err, test.want)
			}
		})
	}
	for _, name := range []string{"project", "header", "cookie", "profile"} {
		if deviceStartCmd.Flags().Lookup(name) != nil {
			t.Fatalf("Web start exposes removed %s input", name)
		}
	}
}

func TestWebStartCLIURLBuildProducesOneSessionJSON(t *testing.T) {
	output, startBody := runWebStartFixture(t, "")
	if web, _ := startBody["web"].(map[string]any); web == nil || web["sign_in"] != nil {
		t.Fatalf("web start without project config sent sign_in: %#v", startBody["web"])
	}
	if strings.Contains(output, "private") || strings.Contains(output, "parent_session") {
		t.Fatal("output leaked input or hierarchy")
	}
}

func TestWebStartCLISendsProjectSignInWithoutSecretValue(t *testing.T) {
	const config = "project:\n  id: 33333333-3333-4333-8333-333333333333\nsession:\n  idle_timeout_seconds: 900\n  auth_bypass:\n    sign_in:\n      path: /__e2e__/login\n      secret: REVYL_SIGN_IN_SECRET\n"
	_, startBody := runWebStartFixture(t, config)
	web, _ := startBody["web"].(map[string]any)
	want := map[string]any{"path": "/__e2e__/login", "secret_name": "REVYL_SIGN_IN_SECRET"}
	if web == nil || !reflect.DeepEqual(web["sign_in"], want) {
		t.Fatalf("web.sign_in = %#v, want %#v", web, want)
	}
	if startBody["idle_timeout_seconds"] != float64(900) || startBody["env_vars"] != nil || startBody["launch_env_var_ids"] != nil {
		t.Fatalf("web start carried launch inputs or wrong timeout: %#v", startBody)
	}
}

func runWebStartFixture(t *testing.T, projectConfig string) (string, map[string]any) {
	t.Helper()
	projectDir := t.TempDir()
	if projectConfig != "" {
		if err := os.MkdirAll(filepath.Join(projectDir, ".revyl"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, ".revyl", "config.yaml"), []byte(projectConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withWorkingDirectory(t, projectDir)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv(sessionIDEnvVar, "")
	const appID = "11111111-1111-4111-8111-111111111111"
	const buildID = "22222222-2222-4222-8222-222222222222"
	var sessionID string
	var startBody map[string]any
	var created, started int
	var frame bytes.Buffer
	if err := png.Encode(&frame, image.NewRGBA(image.Rect(0, 0, 1280, 800))); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/entity/users/get_user_uuid":
			_, _ = w.Write([]byte(`{"org_id":"fixture-org","email":"fixture@example.test"}`))
		case "/api/v1/execution/device-sessions/active":
			_, _ = w.Write([]byte(`{"org_id":"fixture-org","sessions":[]}`))
		case "/api/v1/apps/" + appID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": appID, "platform": "Web", "versions_count": 1, "latest_version": "1", "current_version": "1"})
		case "/api/v1/apps/" + appID + "/builds/external":
			created++
			_ = json.NewEncoder(w).Encode(map[string]any{"id": buildID, "app_id": appID, "artifact_kind": "web_external", "was_reused": true})
		case "/api/v1/telemetry/cli-traces":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/execution/start_device":
			started++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			sessionID, _ = body["session_id"].(string)
			startBody = body
			if body["app_id"] != appID || body["build_id"] != buildID {
				t.Errorf("missing app/build: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "workflow_run_id": flowWorkflowRunID})
		case "/api/v1/execution/device-sessions/" + sessionID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": sessionID, "platform": "web", "status": "running", "viewer_kind": "whep", "workflow_run_id": flowWorkflowRunID, "build_id": buildID})
		case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/screenshot":
			w.Header().Set("X-Revyl-Screen-Token", "fixture-token")
			_, _ = w.Write(frame.Bytes())
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	cmd := newWebStartCommand()
	_ = cmd.Flags().Set("app", appID)
	_ = cmd.Flags().Set("url", "https://example.test/private")
	_ = cmd.Flags().Set("json", "true")
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := deviceStartCmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["session_id"] != sessionID || result["build_id"] != buildID || created != 1 || started != 1 {
		t.Fatalf("incorrect output: %s", output.String())
	}
	return output.String(), startBody
}

func TestWebStartCLIReadsAppFromWebProfile(t *testing.T) {
	repository := t.TempDir()
	gitInitBuildRepository(t, repository)
	writeProjectBuildConfig(t, repository, "project: {id: 33333333-3333-4333-8333-333333333333}\nbuild:\n  profiles:\n    preview:\n      web:\n        app_id: 11111111-1111-4111-8111-111111111111\n")
	withWorkingDirectory(t, repository)
	cmd := newWebStartCommand()
	_ = cmd.Flags().Set("url", "http://example.test/")
	err := deviceStartCmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTPS URL") {
		t.Fatalf("error = %v, want URL validation after the configured app resolved", err)
	}
}
