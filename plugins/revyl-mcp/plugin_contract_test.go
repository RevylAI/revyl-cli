package mcpskill_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hostNeutralEdits turns the Grok copy of the workspace skill into this
// host-neutral copy. Every other line must match, so an edit to the Grok copy
// fails this test until the same edit lands here.
var hostNeutralEdits = [][2]string{
	{"live cloud session inside Grok, start", "live cloud session, start"},
	{
		"The tool returns `workspace_url` and links an embedded view. A successful call\nis not proof that the view loaded. Say the view is ready, and offer\n`workspace_url` as the browser fallback; do not claim you can see it.",
		"The tool returns `workspace_url`. Hosts that show MCP Apps views also embed\nit, but many hosts show none, and a successful call is not proof that the view\nloaded. Always give the user `workspace_url` as a link to open the view in\ntheir browser, and do not claim you can see it.",
	},
	{
		"Record the returned session ID and reuse it; do not start another\nsession to retry rendering.",
		"Record the returned session ID and reuse it. Give the user the returned\n`viewer_url` to watch and control the device or browser in their browser; do\nnot start another session to retry rendering.",
	},
	{"in the embedded view: give the user", "in an embedded view: give the user"},
}

var hostNames = regexp.MustCompile("(?i)claude|grok|chatgpt|openai")

func readSkill(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func TestSkillStaysInStepWithTheGrokCopy(t *testing.T) {
	want := readSkill(t, filepath.Join("..", "revyl-grok", "skills", "revyl-workspace", "SKILL.md"))
	for _, edit := range hostNeutralEdits {
		if strings.Count(want, edit[0]) != 1 {
			t.Fatalf("the Grok copy no longer contains %q once; update hostNeutralEdits and this copy together", edit[0])
		}
		want = strings.Replace(want, edit[0], edit[1], 1)
	}
	if got := readSkill(t, filepath.Join("skills", "revyl-workspace", "SKILL.md")); got != want {
		t.Fatal("skills/revyl-workspace/SKILL.md differs from the Grok copy beyond hostNeutralEdits; apply the same change to both")
	}
}

func TestSkillNamesNoHost(t *testing.T) {
	skill := readSkill(t, filepath.Join("skills", "revyl-workspace", "SKILL.md"))
	if !strings.HasPrefix(skill, "---\nname: revyl-workspace\n") {
		t.Fatal("SKILL.md frontmatter must start with name: revyl-workspace")
	}
	if host := hostNames.FindString(skill); host != "" {
		t.Fatalf("SKILL.md names the host %q; this copy serves every MCP host", host)
	}
	for _, cliOnly := range []string{"revyl auth", "revyl dev", "manage_tests"} {
		if strings.Contains(skill, cliOnly) {
			t.Fatalf("SKILL.md references %q, which the hosted server does not provide", cliOnly)
		}
	}
}
