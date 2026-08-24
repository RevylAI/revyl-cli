package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
	mcppkg "github.com/revyl/cli/internal/mcp"
)

// newBeforeSessionRepo creates a repo-shaped temp directory with a
// .revyl/config.yaml and an executable setup script, and returns its
// symlink-resolved root.
func newBeforeSessionRepo(t *testing.T, configYAML, scriptBody string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".revyl"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".revyl", "config.yaml"), []byte(configYAML), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if scriptBody != "" {
		if err := os.WriteFile(filepath.Join(root, "setup.sh"), []byte(scriptBody), 0o755); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	return root
}

// skipBeforeSessionPOSIXShellFixture skips tests that exec a #!/bin/sh setup
// script on Windows, where bare shell scripts are not host-native executables.
func skipBeforeSessionPOSIXShellFixture(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture is not executable on Windows")
	}
}

// resetSessionRuntimes clears the package-level runtimes both blocks use, so
// tests do not leak state into each other.
func resetSessionRuntimes(t *testing.T) {
	t.Helper()
	priorBypass, priorBefore := devAuthBypass, devBeforeSession
	devAuthBypass, devBeforeSession = nil, nil
	t.Cleanup(func() { devAuthBypass, devBeforeSession = priorBypass, priorBefore })
}

func testAuthoredBeforeScript(scriptPath string) *config.AuthoredBeforeScript {
	return &config.AuthoredBeforeScript{ScriptPath: &scriptPath}
}

func testAuthoredAuthBypass(launchVars []string, deepLink string) *config.AuthoredAuthBypass {
	cfg := &config.AuthoredAuthBypass{LaunchVars: append([]string(nil), launchVars...)}
	if deepLink != "" {
		cfg.DeepLink = &deepLink
	}
	return cfg
}

func TestPrepareSessionStartOptions_AppliesMintedValuesAsSessionScoped(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"E2E_AUTH_TOKEN=tok-123\"\n")
	initDevAuthBypass(testAuthoredAuthBypass(
		[]string{"E2E_AUTH_TOKEN"},
		"myapp://auth?token=${E2E_AUTH_TOKEN}",
	))
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)

	opts, err := prepareSessionStartOptions(context.Background(), mcppkg.StartSessionOptions{Platform: "ios"})
	if err != nil {
		t.Fatalf("prepareSessionStartOptions() error = %v", err)
	}
	if got := opts.LaunchEnv["E2E_AUTH_TOKEN"]; got != "tok-123" {
		t.Fatalf("LaunchEnv[E2E_AUTH_TOKEN] = %q, want tok-123", got)
	}
	// An inline key must not also be looked up in org state, where it very
	// likely does not exist.
	if len(opts.LaunchVars) != 0 {
		t.Fatalf("LaunchVars = %v, want the inline key dropped", opts.LaunchVars)
	}
}

func TestPrepareSessionStartOptions_ExplicitLaunchEnvWins(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"TOKEN=from-script\"\n")
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)

	opts, err := prepareSessionStartOptions(context.Background(), mcppkg.StartSessionOptions{
		Platform:  "ios",
		LaunchEnv: map[string]string{"TOKEN": "from-flag"},
	})
	if err != nil {
		t.Fatalf("prepareSessionStartOptions() error = %v", err)
	}
	if got := opts.LaunchEnv["TOKEN"]; got != "from-flag" {
		t.Fatalf("LaunchEnv[TOKEN] = %q, want the explicit flag value", got)
	}
}

func TestPrepareSessionStartOptions_ScriptFailureIsFatal(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"leaked-token\" >&2\nexit 1\n")
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)

	_, err := prepareSessionStartOptions(context.Background(), mcppkg.StartSessionOptions{Platform: "ios"})
	if err == nil {
		t.Fatal("prepareSessionStartOptions() error = nil, want a fatal setup failure")
	}
	if strings.Contains(err.Error(), "leaked-token") {
		t.Fatalf("error leaked script output: %q", err)
	}
	if status := devBeforeSession.Status(); status.State != "failed" || status.Error == "" {
		t.Fatalf("Status() = %+v, want a recorded failure", status)
	}
}

func TestPrepareSessionStartOptions_RejectsMixedDeepLinkResolution(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"INLINE_TOKEN=tok-123\"\n")
	initDevAuthBypass(testAuthoredAuthBypass(nil, "myapp://auth?token=${INLINE_TOKEN}&env=${ORG_ENV}"))
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)

	_, err := prepareSessionStartOptions(context.Background(), mcppkg.StartSessionOptions{Platform: "ios"})
	if err == nil {
		t.Fatal("prepareSessionStartOptions() error = nil, want mixed resolution rejected")
	}
	if !strings.Contains(err.Error(), "INLINE_TOKEN") || !strings.Contains(err.Error(), "ORG_ENV") {
		t.Fatalf("error = %q, want both placeholder names", err)
	}
	if !strings.Contains(err.Error(), "session.auth_bypass.deep_link") ||
		!strings.Contains(err.Error(), "session.before_script") {
		t.Fatalf("error = %q, want canonical config fields", err)
	}
	if strings.Contains(err.Error(), "tok-123") {
		t.Fatalf("error leaked a minted value: %q", err)
	}
}

func TestPrepareSessionStartOptions_AllowsFullyOrgResolvedDeepLink(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"UNRELATED=value\"\n")
	deepLink := "myapp://auth?token=${ORG_TOKEN}"
	initDevAuthBypass(testAuthoredAuthBypass(nil, deepLink))
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)

	if _, err := prepareSessionStartOptions(context.Background(), mcppkg.StartSessionOptions{Platform: "ios"}); err != nil {
		t.Fatalf("prepareSessionStartOptions() error = %v, want org-only resolution allowed", err)
	}
	// Nothing to substitute, so the template goes to the backend unchanged.
	if got := applySessionValuesToDeepLink(deepLink); got != deepLink {
		t.Fatalf("applySessionValuesToDeepLink() = %q, want the template unchanged", got)
	}
}

func TestApplySessionValuesToDeepLink_SubstitutesFullyInlineTemplate(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"E2E_AUTH_TOKEN=tok-123\"\n")
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)
	if err := devBeforeSession.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := applySessionValuesToDeepLink("myapp://auth?token=${E2E_AUTH_TOKEN}")
	if got != "myapp://auth?token=tok-123" {
		t.Fatalf("applySessionValuesToDeepLink() = %q", got)
	}
	// A fully substituted link has no ${...} left, so it posts to /open_url
	// as a literal rather than to /open_url_template.
	if strings.Contains(got, "${") {
		t.Fatalf("resolved link still carries a placeholder: %q", got)
	}
}

func TestHydrateBeforeSessionForRefresh_ReusesBootTokenWithoutRemint(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"E2E_AUTH_TOKEN=tok-boot\"\n")
	cfg := testAuthoredBeforeScript("./setup.sh")
	initDevBeforeSession(cfg, root)
	if err := devBeforeSession.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	rememberBeforeSessionBootValues(root, "sess-boot")

	// Simulate a fresh `revyl dev auth refresh` process: config reloads with
	// an empty runtime, and must not remint a divergent token.
	devBeforeSession = nil
	initDevBeforeSession(cfg, root)
	if len(devBeforeSession.Values()) != 0 {
		t.Fatal("fresh runtime unexpectedly retained minted values")
	}
	if err := hydrateBeforeSessionForRefresh(root, "sess-boot"); err != nil {
		t.Fatalf("hydrateBeforeSessionForRefresh() error = %v", err)
	}

	got := applySessionValuesToDeepLink("myapp://auth?token=${E2E_AUTH_TOKEN}")
	if got != "myapp://auth?token=tok-boot" {
		t.Fatalf("applySessionValuesToDeepLink() = %q, want boot token", got)
	}
}

func TestInitDevBeforeSession_PreservesValuesAcrossUnchangedReload(t *testing.T) {
	skipBeforeSessionPOSIXShellFixture(t)
	resetSessionRuntimes(t)

	root := newBeforeSessionRepo(t, "", "#!/bin/sh\necho \"TOKEN=tok-123\"\n")
	cfg := testAuthoredBeforeScript("./setup.sh")
	initDevBeforeSession(cfg, root)
	if err := devBeforeSession.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// A rebuild reloads the config and re-fires the deep link without starting
	// a new session, so the already-minted values must survive.
	initDevBeforeSession(testAuthoredBeforeScript("./setup.sh"), root)
	if got := devBeforeSession.Values()["TOKEN"]; got != "tok-123" {
		t.Fatalf("Values()[TOKEN] = %q after unchanged reload, want tok-123", got)
	}

	initDevBeforeSession(nil, root)
	if devBeforeSession != nil {
		t.Fatal("removing before_session left the runtime active")
	}
}

func TestResolveOptionalProjectContext_FindsNearestConfigFromSubdirectory(t *testing.T) {
	configYAML := `project:
  id: 11111111-1111-4111-8111-111111111111
session:
  before_script:
    script_path: ./setup.sh
  auth_bypass:
    launch_vars:
      - E2E_AUTH_TOKEN
`
	root := newBeforeSessionRepo(t, configYAML, "#!/bin/sh\ntrue\n")
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	nested := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	project, err := resolveOptionalProjectContext(nested)
	if err != nil {
		t.Fatalf("resolveOptionalProjectContext() error = %v", err)
	}
	if project == nil {
		t.Fatal("resolveOptionalProjectContext() = nil, want the nearest config")
	}
	if project.ProjectRoot != root {
		t.Fatalf("projectRoot = %q, want %q", project.ProjectRoot, root)
	}
	if authoredBeforeScriptPath(project.Aggregate.Session.BeforeScript) == "" ||
		!authoredAuthBypassConfigured(project.Aggregate.Session.AuthBypass) {
		t.Fatal("canonical config lost before_script or auth_bypass")
	}
}

func TestResolveOptionalProjectContextRejectsLegacyConfig(t *testing.T) {
	root := newBeforeSessionRepo(t, `project:
  name: legacy-app
build:
  system: Xcode
`, "")
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	project, err := resolveOptionalProjectContext(root)
	if err == nil || !strings.Contains(err.Error(), "revyl config migrate") {
		t.Fatalf("resolveOptionalProjectContext() = (%+v, %v), want migration error", project, err)
	}
}

func TestResolveOptionalProjectContextAllowsStandaloneDirectory(t *testing.T) {
	project, err := resolveOptionalProjectContext(t.TempDir())
	if err != nil || project != nil {
		t.Fatalf("resolveOptionalProjectContext() = (%+v, %v), want standalone mode", project, err)
	}
}

func TestResolveOptionalProjectContextRejectsConfigOutsideGit(t *testing.T) {
	root := newBeforeSessionRepo(t, `project:
  id: 11111111-1111-4111-8111-111111111111
`, "")

	project, err := resolveOptionalProjectContext(root)
	if err == nil || project != nil {
		t.Fatalf("resolveOptionalProjectContext() = (%+v, %v), want Git boundary error", project, err)
	}
}
