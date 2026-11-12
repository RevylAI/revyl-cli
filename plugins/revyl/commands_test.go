package revylplugin_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// pluginSkills lists every bundled skill. commandSkills marks the explicit-only
// commands among them: true when the command spends device or build time.
var pluginSkills = map[string]bool{
	"revyl-dev-loop": true, "revyl-proof-ci": true,
	"revyl-test": true, "revyl-device": true, "revyl-build": true, "revyl-proof": true,
	"revyl-status": true, "revyl-stop": true, "revyl-setup": true,
	"revyl-cli-auth-bypass": true, "revyl-ci-sync": true, "revyl-build-config": true,
	"revyl-build-ci-upload": true, "revyl-find-build": true, "revyl-headless": true,
	"revyl-proof-automation": true, "revyl-github": true,
}

var commandSkills = map[string]bool{
	"revyl-test": true, "revyl-device": true, "revyl-build": true, "revyl-proof": true,
	"revyl-status": false, "revyl-stop": false, "revyl-setup": false,
}

// portedExplicitSkills are ported from the CLI, keep their own wording, and
// must stay explicit-only in both hosts.
var portedExplicitSkills = map[string]bool{"revyl-cli-auth-bypass": true}

func frontmatter(t *testing.T, content string) map[string]string {
	t.Helper()
	parts := strings.SplitN(content, "---\n", 3)
	if len(parts) != 3 || parts[0] != "" {
		t.Fatal("SKILL.md must start with YAML frontmatter")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(parts[1], "\n") {
		if key, value, ok := strings.Cut(line, ": "); ok {
			fields[key] = strings.Trim(value, "\"")
		}
	}
	return fields
}

func TestCommandSkillsAreExplicitOnlyInBothHosts(t *testing.T) {
	root := pluginRoot(t)
	for name := range pluginSkills {
		content := string(readFile(t, filepath.Join(root, "skills", name, "SKILL.md")))
		fields := frontmatter(t, content)
		if fields["name"] != name || fields["description"] == "" {
			t.Errorf("%s frontmatter = %v", name, fields)
		}
		policyPath := filepath.Join(root, "skills", name, "agents/openai.yaml")
		_, command := commandSkills[name]
		if !command && !portedExplicitSkills[name] {
			if fields["disable-model-invocation"] != "" {
				t.Errorf("workflow skill %s must stay model-invocable", name)
			}
			if _, err := os.Stat(policyPath); !os.IsNotExist(err) {
				t.Errorf("workflow skill %s must not restrict Codex invocation: %v", name, err)
			}
			continue
		}
		if fields["disable-model-invocation"] != "true" {
			t.Errorf("command %s must set disable-model-invocation: true for Claude Code", name)
		}
		policy := string(readFile(t, policyPath))
		if !strings.Contains(policy, "allow_implicit_invocation: false") || !strings.Contains(policy, "display_name: ") {
			t.Errorf("command %s must ship a Codex explicit-only policy, got %q", name, policy)
		}
	}
}

func TestSpendingCommandsConfirmOnceAndNameTheCost(t *testing.T) {
	root := pluginRoot(t)
	for name, spends := range commandSkills {
		content := string(readFile(t, filepath.Join(root, "skills", name, "SKILL.md")))
		if spends {
			if !strings.Contains(content, "Confirm once") || !regexp.MustCompile(`device time|build minutes`).MatchString(content) {
				t.Errorf("%s spends org time and must confirm once, naming the cost", name)
			}
			continue
		}
		for _, forbidden := range []string{" dev --", "device start", "build --profile", "build upload", "start_device_session("} {
			if strings.Contains(content, forbidden) {
				t.Errorf("non-spending command %s mentions %q", name, forbidden)
			}
		}
	}
}

func TestGitHubSkillsChangeStateOnlyAfterAYes(t *testing.T) {
	for _, name := range []string{"revyl-setup", "revyl-github"} {
		content := string(readFile(t, filepath.Join(pluginRoot(t), "skills", name, "SKILL.md")))
		for _, paragraph := range strings.Split(content, "\n\n") {
			for _, action := range []string{"github connect", "github setup", "config push", "app create", "init --non-interactive", "config migrate --write", "config pull"} {
				if strings.Contains(paragraph, action) && !strings.Contains(paragraph, "yes") {
					t.Errorf("%s runs %q without asking for a yes in the same step:\n%s", name, action, paragraph)
				}
			}
		}
		if !strings.Contains(content, "--no-open --no-wait --json") {
			t.Errorf("%s must issue the install link without opening a browser or blocking", name)
		}
	}
}

func TestSkillsPublishWithConfigPushOnlyWhereRevylAcceptsIt(t *testing.T) {
	for name := range pluginSkills {
		content := string(readFile(t, filepath.Join(pluginRoot(t), "skills", name, "SKILL.md")))
		for _, paragraph := range strings.Split(content, "\n\n") {
			if strings.Contains(paragraph, "config push") && !(strings.Contains(paragraph, "git_default_branch") && strings.Contains(paragraph, "refuses")) {
				t.Errorf("%s offers config push without saying it refuses under git_default_branch authority:\n%s", name, paragraph)
			}
		}
	}
}

var hostedToolNames = map[string]bool{
	"open_revyl_workspace": true, "list_apps": true, "list_device_sessions": true, "start_device_session": true,
	"stop_device_session": true, "screenshot": true, "device_instruction": true, "device_validation": true,
	"get_device_step": true, "start_exploration": true, "get_exploration": true, "cancel_exploration": true,
	"atlas_search": true, "atlas_graph": true, "list_builds": true, "get_build": true,
}

func TestVerifierAgentRefusesUnauthorizedWorkAndLoadsHostedToolsFirst(t *testing.T) {
	content := string(readFile(t, filepath.Join(pluginRoot(t), "agents/revyl-verifier.md")))
	fields := frontmatter(t, content)
	if fields["name"] != "revyl-verifier" || fields["description"] == "" {
		t.Fatalf("verifier frontmatter = %v", fields)
	}
	for _, unsupported := range []string{"hooks", "mcpServers", "permissionMode"} {
		if _, ok := fields[unsupported]; ok {
			t.Errorf("plugin agents ignore %s; the verifier must not rely on it", unsupported)
		}
	}
	for _, tool := range strings.Split(fields["tools"], ", ") {
		switch {
		case tool == "Bash" || tool == "Read" || tool == "Glob" || tool == "Grep" || tool == "ToolSearch":
		case strings.HasPrefix(tool, "mcp__plugin_revyl-cli_revyl-workspace__") && hostedToolNames[strings.TrimPrefix(tool, "mcp__plugin_revyl-cli_revyl-workspace__")]:
		default:
			t.Errorf("verifier tool %q is neither a read/shell tool nor a hosted Revyl tool", tool)
		}
	}
	if !strings.Contains(content, "Authorized:") || !strings.Contains(content, "Not run: no authorization line.") {
		t.Error("verifier must refuse a task without an authorization line")
	}
	if !strings.Contains(fields["tools"], "ToolSearch") {
		t.Error("Claude Code defers plugin MCP tools, so the verifier must be able to load the hosted tools with ToolSearch")
	}
	selected := regexp.MustCompile("select:([^`]*)`").FindStringSubmatch(content)
	if selected == nil {
		t.Fatal("the verifier must name the hosted tools to load in a ToolSearch select")
	}
	loaded := strings.Split(selected[1], ",")
	for _, tool := range strings.Split(fields["tools"], ", ") {
		if strings.HasPrefix(tool, "mcp__") && !slices.Contains(loaded, tool) {
			t.Errorf("the verifier's ToolSearch select must load %s, or it stays deferred", tool)
		}
	}
	for _, skill := range []string{"revyl-test", "revyl-device", "revyl-find-build"} {
		if !strings.Contains(string(readFile(t, filepath.Join(pluginRoot(t), "skills", skill, "SKILL.md"))), "ToolSearch") {
			t.Errorf("%s must load the deferred hosted tools before preferring them", skill)
		}
	}
	for _, skill := range []string{"revyl-test", "revyl-proof"} {
		if !strings.Contains(string(readFile(t, filepath.Join(pluginRoot(t), "skills", skill, "SKILL.md"))), "revyl-verifier") {
			t.Errorf("%s must hand device work to the verifier in Claude Code", skill)
		}
	}
}

func TestClaudeManifestOffersProofAfterCommitOnlyWhenEnabled(t *testing.T) {
	var manifest struct {
		UserConfig map[string]struct {
			Type    string `json:"type"`
			Title   string `json:"title"`
			Default *bool  `json:"default"`
		} `json:"userConfig"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(pluginRoot(t), ".claude-plugin/plugin.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	option, ok := manifest.UserConfig["offer_proof_after_commit"]
	if !ok || option.Type != "boolean" || option.Title == "" || option.Default == nil || *option.Default {
		t.Fatalf("userConfig.offer_proof_after_commit = %+v, want a boolean that defaults to false", option)
	}
	hooks := readHooks(t)
	if len(hooks["PostToolUse"]) != 1 || hooks["PostToolUse"][0].Matcher != "Bash" {
		t.Fatalf("PostToolUse hooks = %+v, want one Bash matcher", hooks["PostToolUse"])
	}
}

type offerFixture struct {
	repo string
	data string
}

func newOfferFixture(t *testing.T, changed string) offerFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX hook fixture")
	}
	root := t.TempDir()
	f := offerFixture{repo: filepath.Join(root, "repo"), data: filepath.Join(root, "data")}
	for _, dir := range []string{filepath.Join(f.repo, "apps/mobile/.revyl"), filepath.Join(f.repo, "docs"), filepath.Join(f.repo, "apps/mobile/src")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(f.repo, "apps/mobile/.revyl/config.yaml"), "project:\n  id: 00000000-0000-0000-0000-000000000000\n", 0600)
	git := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", f.repo, "-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v, %s", args, err, output)
		}
	}
	git("init", "-q")
	writeTestFile(t, filepath.Join(f.repo, "README.md"), "base\n", 0600)
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	writeTestFile(t, filepath.Join(f.repo, changed), "change\n", 0600)
	git("add", "-A")
	git("commit", "-q", "-m", "change")
	return f
}

func (f offerFixture) run(t *testing.T, command string, env ...string) string {
	t.Helper()
	return f.runWithOutput(t, command, "", env...)
}

func (f offerFixture) runWithOutput(t *testing.T, command, commandOutput string, env ...string) string {
	t.Helper()
	hook := exec.Command(filepath.Join(pluginRoot(t), "hooks/revyl-offer-proof"))
	hook.Dir = f.repo
	hook.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + f.data, "CLAUDE_PLUGIN_DATA=" + f.data}, env...)
	input, err := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Bash",
		"tool_input":    map[string]string{"command": command, "description": "Run \"git commit\" later"},
		"tool_response": map[string]string{"stdout": commandOutput},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook.Stdin = strings.NewReader(string(input))
	output, err := hook.Output()
	if err != nil {
		t.Fatalf("offer hook must exit 0: %v, %s", err, output)
	}
	return string(output)
}

func TestOfferProofHookIsOffByDefault(t *testing.T) {
	f := newOfferFixture(t, "apps/mobile/src/screen.tsx")
	if output := f.run(t, "git commit -m change", "CLAUDECODE=1"); output != "" {
		t.Fatalf("hook offered without the option: %q", output)
	}
	if entries, _ := os.ReadDir(f.data); len(entries) != 0 {
		t.Fatalf("hook wrote state without the option: %v", entries)
	}
}

func TestOfferProofHookOffersOncePerCommitThatTouchesARevylApp(t *testing.T) {
	for _, test := range []struct {
		env  string
		want string
	}{
		{"CLAUDE_PLUGIN_OPTION_OFFER_PROOF_AFTER_COMMIT=true", "/revyl-cli:revyl-test"},
		{"REVYL_PLUGIN_OFFER_PROOF=1", "$revyl-test"},
	} {
		f := newOfferFixture(t, "apps/mobile/src/screen.tsx")
		env := []string{test.env}
		if strings.HasPrefix(test.env, "CLAUDE") {
			env = append(env, "CLAUDECODE=1")
		}
		var output struct {
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(f.run(t, "git commit -m change", env...)), &output); err != nil {
			t.Fatal(err)
		}
		context := output.HookSpecificOutput.AdditionalContext
		if output.HookSpecificOutput.HookEventName != "PostToolUse" || !strings.Contains(context, "apps/mobile") || !strings.Contains(context, test.want) || !strings.Contains(context, "without a yes") {
			t.Fatalf("offer = %+v, want an offer for apps/mobile naming %s", output, test.want)
		}
		if again := f.run(t, "git push", env...); again != "" {
			t.Fatalf("hook offered twice for one commit: %q", again)
		}
	}
}

func TestOfferProofHookHonorsEitherEnablementSetting(t *testing.T) {
	f := newOfferFixture(t, "apps/mobile/src/screen.tsx")
	if output := f.run(t, "git commit -m change", "CLAUDE_PLUGIN_OPTION_OFFER_PROOF_AFTER_COMMIT=false", "REVYL_PLUGIN_OFFER_PROOF=1"); !strings.Contains(output, "apps/mobile") {
		t.Fatalf("REVYL_PLUGIN_OFFER_PROOF=1 with the Claude option off = %q, want an offer", output)
	}
	if output := newOfferFixture(t, "apps/mobile/src/screen.tsx").run(t, "git commit -m change", "REVYL_PLUGIN_OFFER_PROOF=10"); output != "" {
		t.Fatalf("hook treated an unrecognized value as enabled: %q", output)
	}
}

func TestOfferProofHookSeesACommitOnAnotherLineOfTheScript(t *testing.T) {
	output := newOfferFixture(t, "apps/mobile/src/screen.tsx").run(t, "git add -A\ngit commit -m change", "REVYL_PLUGIN_OFFER_PROOF=1")
	if !strings.Contains(output, "apps/mobile") {
		t.Fatalf("multi-line commit offer = %q, want one for apps/mobile", output)
	}
}

func TestOfferProofHookIgnoresOtherCommandsAndCommits(t *testing.T) {
	enabled := "REVYL_PLUGIN_OFFER_PROOF=1"
	if output := newOfferFixture(t, "apps/mobile/src/screen.tsx").run(t, "npm test", enabled); output != "" {
		t.Fatalf("hook offered after a non-commit command: %q", output)
	}
	if output := newOfferFixture(t, "docs/guide.md").run(t, "git commit -m docs", enabled); output != "" {
		t.Fatalf("hook offered for a commit outside any Revyl app: %q", output)
	}
	if output := newOfferFixture(t, "apps/mobile/src/screen.tsx").runWithOutput(t, "git status", "nothing to commit; use git commit or git push", enabled); output != "" {
		t.Fatalf("hook offered because command output mentioned a commit: %q", output)
	}
}

func TestOfferProofHookSeesAppChangesInAMergeCommit(t *testing.T) {
	f := newOfferFixture(t, "docs/guide.md")
	git := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", f.repo, "-c", "user.name=Test", "-c", "user.email=test@example.com"}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v, %s", args, err, output)
		}
	}
	git("checkout", "-q", "-b", "feature", "HEAD~1")
	writeTestFile(t, filepath.Join(f.repo, "apps/mobile/src/screen.tsx"), "change\n", 0600)
	git("add", "-A")
	git("commit", "-q", "-m", "app change")
	git("checkout", "-q", "-")
	git("merge", "-q", "--no-ff", "-m", "merge feature", "feature")
	if output := f.run(t, "git push origin HEAD", "REVYL_PLUGIN_OFFER_PROOF=1"); !strings.Contains(output, "apps/mobile") {
		t.Fatalf("merge commit offer = %q, want one for apps/mobile", output)
	}
}
