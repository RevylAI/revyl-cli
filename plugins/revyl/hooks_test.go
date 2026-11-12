package revylplugin_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHostedMCPServerIsTheProductionWorkspace(t *testing.T) {
	var config struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(pluginRoot(t), ".mcp.json")), &config); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{"revyl-workspace": {"type": "http", "url": "https://backend.revyl.ai/api/v1/mcp"}}
	if !reflect.DeepEqual(config.MCPServers, want) {
		t.Fatalf(".mcp.json = %v, want only the hosted production workspace %v", config.MCPServers, want)
	}
}

type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookHandler `json:"hooks"`
}

func readHooks(t *testing.T) map[string][]hookMatcher {
	t.Helper()
	var config struct {
		Hooks map[string][]hookMatcher `json:"hooks"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(pluginRoot(t), "hooks/hooks.json")), &config); err != nil {
		t.Fatal(err)
	}
	return config.Hooks
}

var hookCommandPattern = regexp.MustCompile(`^"\$\{CLAUDE_PLUGIN_ROOT\}"/hooks/([a-z-]+)$`)

func TestHooksRunBundledScriptsWithinTimeouts(t *testing.T) {
	root := pluginRoot(t)
	hooks := readHooks(t)
	if len(hooks["SessionStart"]) != 1 || hooks["SessionStart"][0].Matcher != "startup|resume|clear" {
		t.Fatalf("SessionStart hooks = %+v, want one startup|resume|clear matcher", hooks["SessionStart"])
	}
	for event, matchers := range hooks {
		for _, matcher := range matchers {
			for _, handler := range matcher.Hooks {
				match := hookCommandPattern.FindStringSubmatch(handler.Command)
				if handler.Type != "command" || match == nil || handler.Timeout < 1 || handler.Timeout > 15 {
					t.Fatalf("%s hook %+v must run a bundled hooks/ script within 15 seconds", event, handler)
				}
				info, err := os.Stat(filepath.Join(root, "hooks", match[1]))
				if err != nil {
					t.Fatal(err)
				}
				if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
					t.Fatalf("hook script %s is not executable", match[1])
				}
			}
		}
	}
}

var hookLauncherCall = regexp.MustCompile(`"\$LAUNCHER"((?: [a-z-]+)+)`)

func TestHookScriptsCallOnlyReadOnlyCLICommands(t *testing.T) {
	allowed := map[string]bool{"doctor --json": true, "github status --json": true, "version": true}
	entries, err := os.ReadDir(filepath.Join(pluginRoot(t), "hooks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "hooks.json" {
			continue
		}
		content := string(readFile(t, filepath.Join(pluginRoot(t), "hooks", entry.Name())))
		if !strings.HasPrefix(content, "#!/bin/sh\n") {
			t.Errorf("%s must be a POSIX sh script", entry.Name())
		}
		for _, forbidden := range []string{"github connect", "github setup", "install-url", "auth login", "config push"} {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s mentions %q; hooks must stay read-only", entry.Name(), forbidden)
			}
		}
		for _, call := range hookLauncherCall.FindAllStringSubmatch(content, -1) {
			if command := strings.TrimSpace(call[1]); !allowed[command] {
				t.Errorf("%s runs the CLI command %q; hooks may run only %v", entry.Name(), command, allowed)
			}
		}
	}
}

type sessionCheckFixture struct {
	project string
	home    string
	data    string
	log     string
	doctor  string
	github  string
	binary  string
	latest  string
}

func newSessionCheckFixture(t *testing.T) sessionCheckFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook fixture")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := sessionCheckFixture{
		project: filepath.Join(root, "repo", "apps", "mobile"), home: filepath.Join(root, "home"), data: filepath.Join(root, "data"),
		log: filepath.Join(root, "calls.log"), doctor: filepath.Join(root, "doctor.json"), github: filepath.Join(root, "github.json"),
		binary: filepath.Join(root, "fake revyl"), latest: filepath.Join(root, "latest.json"),
	}
	for _, dir := range []string{filepath.Join(f.project, ".revyl"), f.home, f.data} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(root, "repo")
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/acme/mobile.git"}} {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v, %s", args, err, output)
		}
	}
	writeTestFile(t, filepath.Join(f.project, ".revyl/config.yaml"), "project:\n  id: 00000000-0000-0000-0000-000000000000\n", 0600)
	writeTestFile(t, f.binary, `#!/bin/sh
printf '%s|%s|%s\n' "$*" "${REVYL_TELEMETRY_DISABLED:-}" "${REVYL_RUNTIME_NO_DOWNLOAD:-}" >>"$FAKE_LOG"
if [ -n "${FAKE_RUNTIME:-}" ] && [ ! -f "$FAKE_RUNTIME" ]; then
  [ "$1" = version ] && [ "${REVYL_RUNTIME_NO_DOWNLOAD:-}" != 1 ] && [ -z "${FAKE_INSTALL_FAILS:-}" ] || exit 1
  sleep "${FAKE_INSTALL_SECONDS:-0}"
  : >"$FAKE_RUNTIME"
  exit 0
fi
case "$1 $2" in
"doctor --json") cat "$FAKE_DOCTOR" ;;
"github status") cat "$FAKE_GITHUB" ;;
*) exit 9 ;;
esac
`, 0700)
	f.setDoctor(t, "ok")
	f.setGitHub(t, `{"connected": true, "repository_count": 3, "repository": {"full_name": "acme/mobile", "access_granted": true}, "project": {"root": "apps/mobile", "status": "enabled", "authority": "manual"}}`)
	writeTestFile(t, f.latest, `{"name": "revyl", "version": "0.0.1"}`, 0600)
	return f
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func (f sessionCheckFixture) setDoctor(t *testing.T, auth string) {
	t.Helper()
	message := "Authenticated"
	if auth != "ok" {
		message = "Not authenticated"
	}
	type check struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	checks := []check{
		{"Version", "ok", "v0.1.215"},
		{"Authentication", auth, message},
		{"Project Config", "ok", "Found at " + filepath.Join(f.project, ".revyl/config.yaml")},
	}
	content, err := json.MarshalIndent(map[string]any{"checks": checks, "healthy": auth == "ok"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, f.doctor, string(content), 0600)
}

func (f sessionCheckFixture) setGitHub(t *testing.T, content string) {
	t.Helper()
	writeTestFile(t, f.github, content, 0600)
}

func (f sessionCheckFixture) run(t *testing.T, withBinary bool, env ...string) string {
	t.Helper()
	command := exec.Command(filepath.Join(pluginRoot(t), "hooks/revyl-session-check"))
	command.Dir = f.project
	command.Env = append([]string{
		"PATH=/usr/bin:/bin", "HOME=" + f.home, "CLAUDE_PLUGIN_DATA=" + f.data, "FAKE_LOG=" + f.log,
		"FAKE_DOCTOR=" + f.doctor, "FAKE_GITHUB=" + f.github, "REVYL_PLUGIN_LATEST_MANIFEST_URL=file://" + f.latest,
	}, env...)
	if withBinary {
		command.Env = append(command.Env, "REVYL_BINARY="+f.binary)
	}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("session check must exit 0: %v, %s", err, output)
	}
	if len(output) > 1000 || strings.Count(string(output), "\n") > 3 {
		t.Fatalf("session check output must stay within three lines and 1000 characters: %q", output)
	}
	return string(output)
}

func (f sessionCheckFixture) calls(t *testing.T) []string {
	t.Helper()
	content, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(content)), "\n")
	for _, call := range calls {
		if call != "doctor --json|1|1" && call != "github status --json|1|1" && call != "version|1|" {
			t.Fatalf("session check ran %q; want only read-only commands with telemetry disabled, downloading only through version", call)
		}
	}
	return calls
}

func TestSessionCheckInstallsTheRuntimeAndReportsStateOnTheFirstSession(t *testing.T) {
	f := newSessionCheckFixture(t)
	runtimeMarker := filepath.Join(f.data, "runtime-installed")
	output := f.run(t, true, "FAKE_RUNTIME="+runtimeMarker)
	if !strings.Contains(output, "CLI v0.1.215 ready, signed in; project config at apps/mobile/.revyl/config.yaml.") {
		t.Fatalf("first session output = %q", output)
	}
	want := []string{"doctor --json|1|1", "version|1|", "doctor --json|1|1", "github status --json|1|1"}
	if calls := f.calls(t); strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("first session calls = %v, want %v", calls, want)
	}
}

func TestSessionCheckSaysTheRuntimeIsStillInstallingAndLetsItFinish(t *testing.T) {
	f := newSessionCheckFixture(t)
	runtimeMarker := filepath.Join(f.data, "runtime-installed")
	output := f.run(t, true, "FAKE_RUNTIME="+runtimeMarker, "FAKE_INSTALL_SECONDS=5")
	if !strings.Contains(output, "is still installing") || !strings.Contains(output, "The next session reports them") {
		t.Fatalf("slow install output = %q", output)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(runtimeMarker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background install did not finish after the hook exited")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSessionCheckSaysWhenTheRuntimeCannotInstall(t *testing.T) {
	f := newSessionCheckFixture(t)
	output := f.run(t, true, "FAKE_RUNTIME="+filepath.Join(f.data, "runtime-installed"), "FAKE_INSTALL_FAILS=1")
	if !strings.Contains(output, "couldn't be installed") {
		t.Fatalf("failed install output = %q", output)
	}
}

func TestSessionCheckReportsReadyProjectAndCachesGitHubStatus(t *testing.T) {
	f := newSessionCheckFixture(t)
	for range 2 {
		output := f.run(t, true, "CLAUDECODE=1")
		for _, want := range []string{"CLI v0.1.215 ready, signed in; project config at apps/mobile/.revyl/config.yaml.", "pull-request automation is on for this project"} {
			if !strings.Contains(output, want) {
				t.Fatalf("output %q is missing %q", output, want)
			}
		}
		if strings.Contains(output, f.project) || strings.Contains(output, "acme") {
			t.Fatalf("output leaks paths outside the repository or account details: %q", output)
		}
	}
	if calls := f.calls(t); strings.Count(strings.Join(calls, "\n"), "github status") != 1 {
		t.Fatalf("GitHub status must be cached between sessions: %v", calls)
	}
}

func TestSessionCheckOffersGitHubAtMostOnceAWeek(t *testing.T) {
	for _, test := range []struct {
		name   string
		github string
		want   string
	}{
		{"not connected", `{"connected": false, "repository_count": 0, "repository": null, "project": null}`, "GitHub isn't connected"},
		{"repository not granted", `{"connected": true, "repository_count": 2, "repository": {"full_name": "acme/mobile", "access_granted": false}, "project": null}`, "isn't granted to the Revyl GitHub App"},
		{"repository not granted, as the CLI prints it", "{\n  \"connected\": true,\n  \"repository_count\": 2,\n  \"repository\": {\n    \"full_name\": \"acme/mobile\",\n    \"access_granted\": false\n  },\n  \"project\": null\n}\n", "isn't granted to the Revyl GitHub App"},
		{"project not published", `{"connected": true, "repository_count": 2, "repository": {"full_name": "acme/mobile", "access_granted": true}, "project": {"root": "apps/mobile", "status": "not_published"}}`, "status: not_published"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSessionCheckFixture(t)
			f.setGitHub(t, test.github)
			first := f.run(t, true)
			if !strings.Contains(first, test.want) || !strings.Contains(first, "yes") {
				t.Fatalf("first session = %q, want an offer containing %q that waits for a yes", first, test.want)
			}
			if second := f.run(t, true); strings.Contains(second, test.want) {
				t.Fatalf("second session repeated the GitHub offer within a week: %q", second)
			}
			f.calls(t)
		})
	}
}

func TestSessionCheckPointsToNestedProjects(t *testing.T) {
	f := newSessionCheckFixture(t)
	writeTestFile(t, f.doctor, `{
  "checks": [
    {"name": "Version", "status": "ok", "message": "v0.1.215"},
    {"name": "Authentication", "status": "ok", "message": "Authenticated"},
    {"name": "Project Config", "status": "warning", "message": "Nested project selection required"}
  ]
}`, 0600)
	output := f.run(t, true)
	if !strings.Contains(output, "configured in subdirectories") || strings.Contains(output, "no .revyl/config.yaml applies") || strings.Contains(output, "GitHub") {
		t.Fatalf("nested-project output = %q", output)
	}
}

func TestSessionCheckSkipsGitHubWhenSignedOut(t *testing.T) {
	f := newSessionCheckFixture(t)
	f.setDoctor(t, "error")
	output := f.run(t, true)
	if !strings.Contains(output, "not signed in") || strings.Contains(output, "GitHub") {
		t.Fatalf("signed-out output = %q", output)
	}
	for _, call := range f.calls(t) {
		if strings.HasPrefix(call, "github") {
			t.Fatalf("signed-out session check ran %q", call)
		}
	}
}

func TestSessionCheckRetriesTheUpdateCheckAfterAFailedFetch(t *testing.T) {
	f := newSessionCheckFixture(t)
	if err := os.Remove(f.latest); err != nil {
		t.Fatal(err)
	}
	f.run(t, true)
	writeTestFile(t, f.latest, `{"name": "revyl", "version": "999.0.0"}`, 0600)
	if output := f.run(t, true); !strings.Contains(output, "plugin 999.0.0 is available") {
		t.Fatalf("a failed fetch suppressed the next update check: %q", output)
	}
}

func TestSessionCheckNamesEachHostsUpdateCommand(t *testing.T) {
	for _, test := range []struct {
		env  []string
		want string
	}{
		{[]string{"CLAUDECODE=1"}, "claude plugin marketplace update revyl"},
		{[]string{"CODEX_THREAD_ID=thread"}, "codex plugin marketplace upgrade revyl"},
	} {
		f := newSessionCheckFixture(t)
		writeTestFile(t, f.latest, `{"name": "revyl", "version": "999.0.0"}`, 0600)
		if output := f.run(t, true, test.env...); !strings.Contains(output, "plugin 999.0.0 is available") || !strings.Contains(output, test.want) {
			t.Fatalf("%v: output %q is missing the update hint %q", test.env, output, test.want)
		}
	}
}
