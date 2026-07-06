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

type fakeOrgLaunchVarLister struct {
	vars []api.OrgLaunchVariable
	err  error
}

func (f *fakeOrgLaunchVarLister) ListOrgLaunchVariables(ctx context.Context) (*api.OrgLaunchVariablesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &api.OrgLaunchVariablesResponse{Result: f.vars}, nil
}

type fakeWorkerRequester struct {
	paths  []string
	bodies []interface{}
}

func (f *fakeWorkerRequester) WorkerRequestForSession(ctx context.Context, sessionIndex int, path string, body interface{}) ([]byte, error) {
	f.paths = append(f.paths, path)
	f.bodies = append(f.bodies, body)
	return []byte(`{"status":"success"}`), nil
}

func withTestAuthBypass(t *testing.T, cfg *config.AuthBypassConfig, lister orgLaunchVarLister) *authBypassRuntime {
	t.Helper()
	prev := devAuthBypass
	t.Cleanup(func() { devAuthBypass = prev })
	devAuthBypass = &authBypassRuntime{cfg: cfg, client: lister}
	return devAuthBypass
}

func TestAuthBypassResolveDeepLinkSubstitutesOrgVars(t *testing.T) {
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?token=${REVYL_AUTH_BYPASS_TOKEN}&redirect=/home",
	}, &fakeOrgLaunchVarLister{vars: []api.OrgLaunchVariable{
		{Key: "REVYL_AUTH_BYPASS_TOKEN", Value: "tok-123"},
	}})

	link, err := rt.ResolveDeepLink(context.Background())
	if err != nil {
		t.Fatalf("ResolveDeepLink() error = %v", err)
	}
	if link != "myapp://revyl-auth?token=tok-123&redirect=/home" {
		t.Fatalf("ResolveDeepLink() = %q", link)
	}
}

func TestAuthBypassResolveDeepLinkMissingVarFails(t *testing.T) {
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?token=${MISSING_VAR}",
	}, &fakeOrgLaunchVarLister{})

	_, err := rt.ResolveDeepLink(context.Background())
	if err == nil || !strings.Contains(err.Error(), "MISSING_VAR") {
		t.Fatalf("ResolveDeepLink() error = %v, want missing-var error", err)
	}
}

func TestInitDevAuthBypassClearsRuntimeOnRemoval(t *testing.T) {
	prev := devAuthBypass
	t.Cleanup(func() { devAuthBypass = prev })

	lister := &fakeOrgLaunchVarLister{}
	initDevAuthBypass(&config.ProjectConfig{
		AuthBypass: &config.AuthBypassConfig{DeepLink: "myapp://revyl-auth?static=true"},
	}, lister)
	if devAuthBypass == nil {
		t.Fatal("initDevAuthBypass() left runtime nil for a configured section")
	}

	// A reload whose config no longer configures auth bypass must clear the
	// previously active runtime so a removed section stops firing.
	initDevAuthBypass(&config.ProjectConfig{}, lister)
	if devAuthBypass != nil {
		t.Fatalf("initDevAuthBypass() = %+v, want nil after auth_bypass removed", devAuthBypass)
	}
}

func TestAuthBypassResolveDeepLinkLeavesLiteralDollar(t *testing.T) {
	// A literal `$` (e.g. in a query value) must survive resolution, matching
	// the backend's ${VAR}-only substitution. os.Expand would treat `$5` as a
	// special var and error.
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?token=${TOKEN}&price=$5",
	}, &fakeOrgLaunchVarLister{vars: []api.OrgLaunchVariable{
		{Key: "TOKEN", Value: "tok"},
	}})

	link, err := rt.ResolveDeepLink(context.Background())
	if err != nil {
		t.Fatalf("ResolveDeepLink() error = %v", err)
	}
	if link != "myapp://revyl-auth?token=tok&price=$5" {
		t.Fatalf("ResolveDeepLink() = %q", link)
	}
}

func TestAuthBypassResolveDeepLinkIgnoresBareVarForm(t *testing.T) {
	// The bare `$VAR` form is not a placeholder (only `${VAR}` is), so it is
	// left literal rather than expanded or reported as a missing var.
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?a=${TOKEN}&b=$RAW",
	}, &fakeOrgLaunchVarLister{vars: []api.OrgLaunchVariable{
		{Key: "TOKEN", Value: "tok"},
	}})

	link, err := rt.ResolveDeepLink(context.Background())
	if err != nil {
		t.Fatalf("ResolveDeepLink() error = %v", err)
	}
	if link != "myapp://revyl-auth?a=tok&b=$RAW" {
		t.Fatalf("ResolveDeepLink() = %q", link)
	}
}

func TestAuthBypassResolveDeepLinkStaticNoAPICall(t *testing.T) {
	// A deep link without placeholders must not require the API at all.
	rt := withTestAuthBypass(t, &config.AuthBypassConfig{
		DeepLink: "myapp://revyl-auth?static=true",
	}, &fakeOrgLaunchVarLister{err: context.DeadlineExceeded})

	link, err := rt.ResolveDeepLink(context.Background())
	if err != nil {
		t.Fatalf("ResolveDeepLink() error = %v", err)
	}
	if link != "myapp://revyl-auth?static=true" {
		t.Fatalf("ResolveDeepLink() = %q", link)
	}
}

func TestApplyAuthBypassSessionDefaults(t *testing.T) {
	withTestAuthBypass(t, &config.AuthBypassConfig{
		LaunchVars: []string{"REVYL_AUTH_BYPASS_ENABLED", "REVYL_AUTH_BYPASS_TOKEN"},
		DeepLink:   "myapp://revyl-auth?static=true",
	}, &fakeOrgLaunchVarLister{})

	// Defaults apply when the caller provided nothing.
	opts := applyAuthBypassSessionDefaults(context.Background(), mcppkg.StartSessionOptions{})
	if len(opts.LaunchVars) != 2 {
		t.Fatalf("LaunchVars = %v, want 2 config vars", opts.LaunchVars)
	}
	if opts.AppLink != "myapp://revyl-auth?static=true" {
		t.Fatalf("AppLink = %q, want config deep link", opts.AppLink)
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
	}, &fakeOrgLaunchVarLister{vars: []api.OrgLaunchVariable{{Key: "TOKEN", Value: "abc"}}})

	requester := &fakeWorkerRequester{}
	fireAuthBypassAfterLaunch(context.Background(), requester, 0)

	if len(requester.paths) != 1 || requester.paths[0] != "/open_url" {
		t.Fatalf("worker paths = %v, want one /open_url", requester.paths)
	}
	body, ok := requester.bodies[0].(map[string]string)
	if !ok || body["url"] != "myapp://revyl-auth?token=abc" {
		t.Fatalf("open_url body = %#v", requester.bodies[0])
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
