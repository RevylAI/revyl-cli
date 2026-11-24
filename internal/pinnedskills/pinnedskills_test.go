package pinnedskills

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRenderReplacesListItemsThePinnedRuntimeCannotFollow(t *testing.T) {
	source := "# Auth bypass\n\n- Pass @--intent \"<goal>\"@ on @revyl@ commands.\n- Report problems with Revyl yourself. Run @revyl feedback --type friction@\n  with --ref <id>.\n- Billing: follow the line.\n- When a @revyl@ command says a newer Revyl CLI is available, run the upgrade command.\n  Report a failed upgrade.\n- Keep going.\n"
	rendered, err := Render("revyl-cli-auth-bypass/SKILL.md", []byte(strings.ReplaceAll(source, "@", "`")))
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	for _, removed := range []string{"--intent", "friction", "--ref", "upgrade command", "failed upgrade"} {
		if strings.Contains(text, removed) {
			t.Errorf("rendered skill keeps %q:\n%s", removed, text)
		}
	}
	for _, kept := range []string{"never upgrade or replace the CLI", "revyl feedback --type other", "- Billing: follow the line.\n- Keep going.\n"} {
		if !strings.Contains(text, kept) {
			t.Errorf("rendered skill lost %q:\n%s", kept, text)
		}
	}
}

func TestRenderFailsWhenAnOverriddenItemDisappears(t *testing.T) {
	if _, err := Render("revyl-cli-dev-loop/SKILL.md", []byte("# Dev loop\n")); err == nil {
		t.Fatal("Render() error = nil, want the missing list item named")
	}
}

func markdown(text string) string {
	return strings.ReplaceAll(text, "@", "`")
}

// testCommandTree is a small recorded release, so these tests do not depend on
// which releases the plugins pin.
func testCommandTree(t *testing.T, feedbackFlags string) *CommandTree {
	t.Helper()
	tree, err := ParseCommandTree("v1.2.3", []byte("global --chdir= --debug --json --quiet\n"+
		"feedback-type Feedback type: bug, feature, or other\n"+
		"command feedback "+feedbackFlags+"\n"+
		"command dev --context= --platform=\n"+
		"command dev rebuild --context= --wait --json\n"+
		"command device\n"+
		"command device start --platform= --json --open\n"+
		"command device stop --all\n"+
		"command device validation --session-id=\n"+
		"command test\n"+
		"command test run --build --platform=\n"))
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestProblemsReportsWhatThePinnedReleaseLacks(t *testing.T) {
	tree := testCommandTree(t, "--type= --body= --body-file=")
	cases := []struct{ text, want string }{
		{"Run @revyl setup detect --json@.", "revyl setup"},
		{"Run @revyl dev --intent \"x\"@.", "--intent"},
		{"If it fails, run @revyl upgrade@.", "upgrade"},
		{"Run @revyl feedback --type friction --body x@.", "friction"},
		{"Run @revyl feedback --type bug --ref 1 --body x@.", "no --ref flag"},
		{"Report it with @revyl feedback --type bug\n  --ref 1 --body x@ right away.", "no --ref flag on \"revyl feedback\""},
		{"@@@sh\n\"$LAUNCHER\" device permissions set -s 0\n@@@\n", "no command \"revyl device permissions\""},
		{"Run @revyl --json device permissions set@.", "no command \"revyl device permissions\""},
		{"Run @revyl --chdir ./my-project\n  device permissions set@.", "no command \"revyl device permissions\""},
		{"Run @revyl --chdir \"./my project\" device permissions set@.", "no command \"revyl device permissions\""},
		{"Run @revyl --chdir './my project' -C my\\ project device permissions set@.", "no command \"revyl device permissions\""},
		{"Run @revyl --debug teleport@.", "no command \"revyl teleport\""},
		{"Run @revyl teleport now@.", "no command \"revyl teleport\""},
		{"Run @revyl \"device\" start --no-open@.", "no --no-open flag on \"revyl device start\""},
		{"Run @revyl device start --open_extra@.", "no --open_extra flag on \"revyl device start\""},
		{"Run @revyl device start --nope -- --json@.", "no --nope flag on \"revyl device start\""},
	}
	for _, c := range cases {
		problems := strings.Join(tree.Problems(markdown(c.text)), "\n")
		if !strings.Contains(problems, c.want) {
			t.Errorf("Problems(%q) = %q, want one naming %q", c.text, problems, c.want)
		}
	}
}

func TestProblemsAcceptsCommandsThePinnedReleaseHas(t *testing.T) {
	tree := testCommandTree(t, "--type= --body= --body-file= --ref=")
	text := "Run @revyl feedback --type bug --ref 1 --body x@ or @revyl device start\n  --platform ios --json@.\n\n@@@sh\nrevyl dev --help\n\"$LAUNCHER\" dev rebuild --context main --wait \\\n  --json\nrevyl test run login-flow --build --platform android\nrevyl dev --context main rebuild --wait\nrevyl --chdir ./app -q dev --context=main rebuild\n@@@\n\nRun @revyl --chdir \"./my project\" device validation --session-id x \"the sample shows --sample-text\"@.\n\nStart one with revyl device start --json, then stop.\n\nRun @revyl device \"stop\" --all@.\n\nRun @revyl device start --platform ios --open=false --json@, @revyl device start --platform ios --open=0@ or @revyl device start --platform ios --open=False@.\n\nPass text that looks like flags after the end of options: @revyl device validation -- --sample-text teleport@, @revyl device validation \"--\" --sample-text@ or @revyl device validation '--' --sample-text@.\n\nRecommend upgrading to Pro. Use the revyl CLI.\n"
	if problems := tree.Problems(markdown(text)); len(problems) != 0 {
		t.Errorf("Problems() = %q, want none", problems)
	}
}

func TestParseCommandTreeRejectsAnEmptyOrMalformedRecord(t *testing.T) {
	const valid = "global --json\nfeedback-type Feedback type: bug or other\ncommand device start --platform= --open\n"
	if _, err := ParseCommandTree("v1.2.3", []byte(valid)); err != nil {
		t.Fatalf("ParseCommandTree(valid) error = %v", err)
	}
	for name, record := range map[string]string{
		"empty":             "",
		"no commands":       "global --json\nfeedback-type Feedback type: bug or other\n",
		"no global line":    "feedback-type Feedback type: bug or other\ncommand device start --open\n",
		"unknown line":      valid + "device stop --all\n",
		"blank line":        valid + "\n",
		"second global":     valid + "global --quiet\n",
		"bad flag":          "global --json\nfeedback-type x\ncommand device start --Open\n",
		"flag before path":  "global --json\nfeedback-type x\ncommand device --open start\n",
		"command with none": valid + "command --open\n",
	} {
		if _, err := ParseCommandTree("v1.2.3", []byte(record)); err == nil {
			t.Errorf("ParseCommandTree(%s) error = nil, want the record rejected", name)
		}
	}
}

func TestLoadCommandTreeNamesTheRecordingStepForAnUnrecordedPin(t *testing.T) {
	_, err := LoadCommandTree("v0.0.1")
	if err == nil || !strings.Contains(err.Error(), "record it") {
		t.Fatalf("LoadCommandTree() error = %v, want the recording instruction", err)
	}
}

// The pin advance records the new release's tree and removes trees no plugin
// pins, so commands/ holds exactly the pinned releases.
func TestRecordedCommandTreesAreExactlyThePinnedReleases(t *testing.T) {
	pinned := map[string]bool{}
	for _, manifest := range []string{"../../plugins/revyl/runtime-manifest.json", "../../cursor-plugin/runtime-manifest.json"} {
		content, err := os.ReadFile(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var runtimeManifest struct {
			ReleaseTag string `json:"release_tag"`
		}
		if err := json.Unmarshal(content, &runtimeManifest); err != nil {
			t.Fatal(err)
		}
		pinned[runtimeManifest.ReleaseTag] = true
	}
	recorded, err := RecordedReleaseTags()
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range recorded {
		if !pinned[tag] {
			t.Errorf("commands/%s.txt records a release no plugin pins; run make -C revyl-cli record-pinned-commands", tag)
		}
		delete(pinned, tag)
	}
	for tag := range pinned {
		t.Errorf("no command tree is recorded for pinned release %s; run make -C revyl-cli record-pinned-commands", tag)
	}
}
