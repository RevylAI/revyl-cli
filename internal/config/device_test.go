package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeviceConfigContextReadsOnlySessionSettings(t *testing.T) {
	tests := []struct {
		name    string
		content string
		timeout int
		script  string
		vars    []string
		link    string
	}{
		{name: "missing config"},
		{name: "legacy project", content: "project:\n  name: old-app\nbuild:\n  system: Xcode\n"},
		{name: "canonical project without session", content: "project:\n  id: 11111111-1111-4111-8111-111111111111\n"},
		{name: "unrelated invalid build", content: "build:\n  framework: unknown\n  profiles: invalid\n"},
		{
			name: "legacy session",
			content: `project:
  name: old-app
defaults:
  timeout: 600
before_session:
  script: ./setup.sh
  timeout_seconds: 45
auth_bypass:
  launch_vars: [TEST_MODE]
  deep_link: example://auth
build:
  system: Xcode
  platforms: invalid
`,
			timeout: 600, script: "setup.sh", vars: []string{"TEST_MODE"}, link: "example://auth",
		},
		{
			name: "canonical session",
			content: `session:
  idle_timeout_seconds: 600
  before_script:
    script_path: ./setup.sh
    timeout_seconds: 45
  auth_bypass:
    launch_vars: [TEST_MODE]
    deep_link: example://auth
build:
  profiles: invalid
`,
			timeout: 600, script: "setup.sh", vars: []string{"TEST_MODE"}, link: "example://auth",
		},
		{
			name:    "legacy session with YAML anchor",
			content: "vars: &vars [TEST_MODE]\nauth_bypass:\n  launch_vars: *vars\n",
			vars:    []string{"TEST_MODE"},
		},
		{name: "legacy deep link alone", content: "auth_bypass:\n  deep_link: example://auth\n", link: "example://auth"},
		{name: "legacy default timeout", content: "defaults:\n  timeout: 0\n", timeout: DefaultTimeoutSeconds},
		{name: "legacy disabled setup", content: "before_session:\n  script: '  '\n"},
		{name: "legacy empty setup", content: "before_session: {}\n"},
		{name: "null canonical session", content: "session: null\n"},
	}
	for _, test := range tests {
		for _, gitRepository := range []bool{false, true} {
			name := test.name + "/standalone"
			if gitRepository {
				name = test.name + "/git"
			}
			t.Run(name, func(t *testing.T) {
				root := writeDeviceConfigFixture(t, test.content, gitRepository)
				deviceContext, err := ResolveDeviceConfigContext(root)
				if err != nil {
					t.Fatal(err)
				}
				if deviceContext.ProjectRoot != root {
					t.Fatalf("ProjectRoot = %q, want %q", deviceContext.ProjectRoot, root)
				}
				session, err := deviceContext.ReadSession()
				if err != nil {
					t.Fatal(err)
				}
				if test.timeout == 0 {
					if session.IdleTimeoutSeconds != nil {
						t.Fatalf("unexpected timeout: %d", *session.IdleTimeoutSeconds)
					}
				} else if session.IdleTimeoutSeconds == nil || *session.IdleTimeoutSeconds != test.timeout {
					t.Fatalf("timeout = %v, want %d", session.IdleTimeoutSeconds, test.timeout)
				}
				if test.script != "" && (session.BeforeScript == nil || session.BeforeScript.ScriptPath == nil || *session.BeforeScript.ScriptPath != test.script || session.BeforeScript.TimeoutSeconds == nil || *session.BeforeScript.TimeoutSeconds != 45) {
					t.Fatalf("before_script = %+v, want %q with 45 second timeout", session.BeforeScript, test.script)
				}
				if test.script == "" && session.BeforeScript != nil && session.BeforeScript.ScriptPath != nil {
					t.Fatal("unexpected before-script path")
				}
				if test.vars != nil && (session.AuthBypass == nil || !reflect.DeepEqual(session.AuthBypass.LaunchVars, test.vars)) {
					t.Fatalf("auth_bypass = %+v, want vars %v", session.AuthBypass, test.vars)
				}
				if test.link != "" && (session.AuthBypass == nil || session.AuthBypass.DeepLink == nil || *session.AuthBypass.DeepLink != test.link) {
					t.Fatalf("auth_bypass = %+v, want deep link %q", session.AuthBypass, test.link)
				}
				if test.content != "" {
					unchanged, err := os.ReadFile(deviceContext.ConfigPath)
					if err != nil || string(unchanged) != test.content {
						t.Fatalf("config changed: %v", err)
					}
				}
			})
		}
	}
}

func TestDeviceConfigContextRejectsInvalidSessionSettings(t *testing.T) {
	for _, content := range []string{
		"session: [invalid]\n",
		"session:\n  idle_timeout_seconds: 0\n",
		"session:\n  auth_bypass:\n    launch_vars: not-a-list\n",
		"session:\n  before_script:\n    script_path: ../outside.sh\n",
		"before_session:\n  script: /outside.sh\n",
		"before_session: not-a-mapping\n",
		"before_session:\n  script: [invalid]\n",
		"before_session:\n  scripts: ./setup.sh\n",
		"before_session:\n  script: ./setup.sh\n  timeout_second: 45\n",
		"defaults:\n  timeout: bad\n",
		"auth_bypass:\n  launch_vars: [123]\n",
		"auth_bypass:\n  launch_var: [TEST_MODE]\n",
		"auth_bypass:\n  deeplink: example://auth\n",
		"session: {}\nauth_bypass:\n  deep_link: example://auth\n",
		"session: {}\nsession: {}\n",
		"session: [\n",
	} {
		t.Run(content, func(t *testing.T) {
			root := writeDeviceConfigFixture(t, content, true)
			deviceContext, err := ResolveDeviceConfigContext(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := deviceContext.ReadSession(); err == nil {
				t.Fatal("expected invalid session to be rejected")
			}
		})
	}
}

func TestDeviceConfigContextUsesNearestConfigWithinWorktree(t *testing.T) {
	root := writeDeviceConfigFixture(t, "defaults:\n  timeout: 700\n", true)
	appRoot := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(appRoot, ".revyl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appRoot, ".revyl", "config.yaml"), []byte("defaults:\n  timeout: 400\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(appRoot, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	deviceContext, err := ResolveDeviceConfigContext(nested)
	if err != nil {
		t.Fatal(err)
	}
	if deviceContext.ProjectRoot != appRoot {
		t.Fatalf("ProjectRoot = %q, want %q", deviceContext.ProjectRoot, appRoot)
	}
	session, err := deviceContext.ReadSession()
	if err != nil || session.IdleTimeoutSeconds == nil || *session.IdleTimeoutSeconds != 400 {
		t.Fatalf("session = %+v, error = %v", session, err)
	}
	if output, err := exec.Command("git", "init", "--quiet", nested).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	deviceContext, err = ResolveDeviceConfigContext(nested)
	if err != nil || deviceContext.ConfigPath != "" || deviceContext.ProjectRoot != nested {
		t.Fatalf("context = %+v, error = %v, want standalone inside nested repository", deviceContext, err)
	}
}

func TestDeviceConfigContextDoesNotInheritConfigOutsideGit(t *testing.T) {
	root := writeDeviceConfigFixture(t, "before_session:\n  script: setup.sh\n", false)
	nested := filepath.Join(root, "unrelated")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	deviceContext, err := ResolveDeviceConfigContext(nested)
	if err != nil || deviceContext.ConfigPath != "" || deviceContext.ProjectRoot != nested {
		t.Fatalf("context = %+v, error = %v, want standalone directory", deviceContext, err)
	}
}

func writeDeviceConfigFixture(t *testing.T, content string, gitRepository bool) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if gitRepository {
		if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, output)
		}
	}
	if content != "" {
		if err := os.MkdirAll(filepath.Join(root, ".revyl"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".revyl", "config.yaml"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
