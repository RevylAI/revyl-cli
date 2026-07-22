package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

type fakeWorkerRequester struct {
	paths  []string
	bodies []interface{}
	err    error
}

func (f *fakeWorkerRequester) WorkerRequestForSession(ctx context.Context, sessionIndex int, path string, body interface{}) ([]byte, error) {
	f.paths = append(f.paths, path)
	f.bodies = append(f.bodies, body)
	if f.err != nil {
		return nil, f.err
	}
	return []byte(`{"status":"success"}`), nil
}

func withTestAuthBypass(t *testing.T, cfg *config.AuthBypassConfig) *authBypassRuntime {
	t.Helper()
	prev := devAuthBypass
	t.Cleanup(func() { devAuthBypass = prev })
	devAuthBypass = &authBypassRuntime{cfg: cfg, state: "pending"}
	return devAuthBypass
}

func TestAuthBypassDelegatesTemplateResolutionToWorkerProxy(t *testing.T) {
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}&redirect=/home",
	})
	requester := &fakeWorkerRequester{}

	if err := rt.FireDeepLink(context.Background(), requester, 0); err != nil {
		t.Fatalf("FireDeepLink() error = %v", err)
	}
	if len(requester.paths) != 1 || requester.paths[0] != "/open_url_template" {
		t.Fatalf("worker paths = %v, want one /open_url_template", requester.paths)
	}
	body, ok := requester.bodies[0].(api.DeviceOpenURLTemplateRequest)
	if !ok || body.URLTemplate != rt.cfg.DeepLink {
		t.Fatalf("open_url_template body = %#v", requester.bodies[0])
	}
	status := rt.Status()
	if status == nil || status.State != "ready" || status.Error != "" {
		t.Fatalf("Status() = %+v, want ready", status)
	}
}

func TestAuthBypassFailureRedactsWorkerResponseFromErrorAndStatus(t *testing.T) {
	const secret = "resolved-bypass-token"
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}",
	})
	requester := &fakeWorkerRequester{
		err: &mcppkg.WorkerHTTPError{
			StatusCode: 500,
			Path:       "/open_url_template",
			Body:       `{"error":"failed to open myapp://revyl-auth?token=` + secret + `"}`,
		},
	}

	err := rt.FireDeepLink(context.Background(), requester, 0)
	if err == nil {
		t.Fatal("FireDeepLink() error = nil, want sanitized failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("FireDeepLink() error exposed secret: %q", err)
	}
	if !strings.Contains(err.Error(), "worker status 500") {
		t.Fatalf("FireDeepLink() error = %q, want worker status", err)
	}

	status := rt.Status()
	if status == nil || status.State != "failed" {
		t.Fatalf("Status() = %+v, want failed", status)
	}
	if strings.Contains(status.Error, secret) {
		t.Fatalf("Status().Error exposed secret: %q", status.Error)
	}
	if status.Error != err.Error() {
		t.Fatalf("Status().Error = %q, want %q", status.Error, err.Error())
	}
}

func TestInitDevAuthBypassClearsRuntimeOnRemoval(t *testing.T) {
	prev := devAuthBypass
	t.Cleanup(func() { devAuthBypass = prev })

	initDevAuthBypass(&config.ProjectConfig{
		AuthBypass: &config.AuthBypassConfig{DeepLink: "myapp://revyl-auth?static=true"},
	})
	if devAuthBypass == nil {
		t.Fatal("initDevAuthBypass() left runtime nil for a configured section")
	}

	// A reload whose config no longer configures auth bypass must clear the
	// previously active runtime so a removed section stops firing.
	initDevAuthBypass(&config.ProjectConfig{})
	if devAuthBypass != nil {
		t.Fatalf("initDevAuthBypass() = %+v, want nil after auth_bypass removed", devAuthBypass)
	}
}

func TestInitDevAuthBypassPreservesOutcomeWhenConfigIsUnchanged(t *testing.T) {
	testCases := []struct {
		name      string
		state     string
		lastError string
	}{
		{name: "ready", state: "ready"},
		{name: "failed", state: "failed", lastError: "auth bypass deep link failed to open"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rt := withTestAuthBypass(t, &config.AuthBypassConfig{
				LaunchVars: []string{"REVYL_AUTH_BYPASS_ENABLED", "REVYL_AUTH_BYPASS_TOKEN"},
				DeepLink:   "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}",
			})
			rt.setAttemptState(testCase.state, testCase.lastError)

			initDevAuthBypass(&config.ProjectConfig{
				AuthBypass: &config.AuthBypassConfig{
					LaunchVars: []string{"REVYL_AUTH_BYPASS_ENABLED", "REVYL_AUTH_BYPASS_TOKEN"},
					DeepLink:   "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}",
				},
			})

			if devAuthBypass != rt {
				t.Fatal("initDevAuthBypass() replaced the runtime for unchanged config")
			}
			status := devAuthBypass.Status()
			if status.State != testCase.state || status.Error != testCase.lastError {
				t.Fatalf("Status() = %+v, want state %q and error %q", status, testCase.state, testCase.lastError)
			}
		})
	}
}

func TestInitDevAuthBypassResetsOutcomeWhenConfigChanges(t *testing.T) {
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		LaunchVars: []string{"REVYL_AUTH_BYPASS_ENABLED"},
		DeepLink:   "myapp://revyl-auth?mode=old",
	})
	rt.setAttemptState("ready", "")

	initDevAuthBypass(&config.ProjectConfig{
		AuthBypass: &config.AuthBypassConfig{
			LaunchVars: []string{"REVYL_AUTH_BYPASS_ENABLED"},
			DeepLink:   "myapp://revyl-auth?mode=new",
		},
	})

	if devAuthBypass == rt {
		t.Fatal("initDevAuthBypass() preserved the runtime after config changed")
	}
	status := devAuthBypass.Status()
	if status.State != "pending" || status.Error != "" {
		t.Fatalf("Status() = %+v, want fresh pending outcome", status)
	}
}

func TestApplyAuthBypassSessionDefaults(t *testing.T) {
	withTestAuthBypass(t, &config.AuthBypassConfig{
		LaunchVars: []string{"REVYL_AUTH_BYPASS_ENABLED", "REVYL_AUTH_BYPASS_TOKEN"},
		DeepLink:   "myapp://revyl-auth?static=true",
	})

	// Defaults apply when the caller provided nothing.
	opts := applyAuthBypassSessionDefaults(context.Background(), mcppkg.StartSessionOptions{})
	if len(opts.LaunchVars) != 2 {
		t.Fatalf("LaunchVars = %v, want 2 config vars", opts.LaunchVars)
	}
	if opts.AppLink != "" {
		t.Fatalf("AppLink = %q, want post-launch proxy handling", opts.AppLink)
	}

	// Explicit values win over config.
	opts = applyAuthBypassSessionDefaults(context.Background(), mcppkg.StartSessionOptions{
		LaunchVars: []string{"EXPLICIT_VAR"},
		AppLink:    "exp+app://dev-client",
	})
	if len(opts.LaunchVars) != 1 || opts.LaunchVars[0] != "EXPLICIT_VAR" {
		t.Fatalf("LaunchVars = %v, want explicit flag to win", opts.LaunchVars)
	}
	if opts.AppLink != "exp+app://dev-client" {
		t.Fatalf("AppLink = %q, want explicit app link to win", opts.AppLink)
	}
}

func TestApplyAuthBypassSessionDefaultsNoConfig(t *testing.T) {
	prev := devAuthBypass
	t.Cleanup(func() { devAuthBypass = prev })
	devAuthBypass = nil

	opts := applyAuthBypassSessionDefaults(context.Background(), mcppkg.StartSessionOptions{Platform: "ios"})
	if len(opts.LaunchVars) != 0 || opts.AppLink != "" {
		t.Fatalf("expected no-op without auth bypass config, got %+v", opts)
	}
}

func TestFireAuthBypassAfterLaunchOpensURL(t *testing.T) {
	withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?token=${TOKEN}",
	})

	requester := &fakeWorkerRequester{}
	fireAuthBypassAfterLaunch(context.Background(), requester, 0)

	if len(requester.paths) != 1 || requester.paths[0] != "/open_url_template" {
		t.Fatalf("worker paths = %v, want one /open_url_template", requester.paths)
	}
	body, ok := requester.bodies[0].(api.DeviceOpenURLTemplateRequest)
	if !ok || body.URLTemplate != "myapp://revyl-auth?token=${TOKEN}" {
		t.Fatalf("open_url_template body = %#v", requester.bodies[0])
	}
}

func TestDevAuthRefreshErrorEmitsJSON(t *testing.T) {
	cmd := &cobra.Command{Use: "refresh"}
	cmd.Flags().Bool("json", false, "")
	_ = cmd.Flags().Set("json", "true")

	out := captureStdout(t, func() {
		err := devAuthRefreshError(cmd, "no_deep_link", "auth_bypass.deep_link is not set", "restart_session")
		if err == nil {
			t.Error("expected non-nil error for exit code")
		}
	})

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\n%s", err, out)
	}
	if payload["ok"] != false || payload["code"] != "no_deep_link" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload["action"] != "restart_session" {
		t.Fatalf("missing action: %#v", payload)
	}
}

func TestDevAuthRefreshErrorHumanModeNoJSON(t *testing.T) {
	cmd := &cobra.Command{Use: "refresh"}
	cmd.Flags().Bool("json", false, "")

	out := captureStdout(t, func() {
		_ = devAuthRefreshError(cmd, "no_config", "failed to load config", "run revyl init")
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("human mode should not print JSON to stdout, got %q", out)
	}
}
