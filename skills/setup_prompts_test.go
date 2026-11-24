package skills

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/testutil"
)

// The setup prompts that the Revyl web app and revyl.com hand to coding agents
// live outside this module, so these tests read their sources as text.
const (
	frontendSetupPrompts = "frontend/src/lib/agentSetupPrompts.ts"
	hostedSetupPrompts   = "gtm/apps/website/src/lib/setup-command.ts"
)

// stringLiteralLine matches a source line that holds one prompt line: a single-
// or double-quoted string literal and an optional trailing comma.
var stringLiteralLine = regexp.MustCompile(`^\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"),?\s*$`)

var jsStringEscapes = strings.NewReplacer(`\'`, "'", `\"`, `"`, `\\`, `\`)

// promptLine returns the text of a prompt line written as a lone string literal.
func promptLine(sourceLine string) (string, bool) {
	match := stringLiteralLine.FindStringSubmatch(sourceLine)
	if match == nil {
		return "", false
	}
	return jsStringEscapes.Replace(match[1] + match[2]), true
}

// yamlExamples returns every ```yaml block a prompt source writes line by line.
func yamlExamples(source string) ([]string, error) {
	var examples, block []string
	inYAML := false
	for number, sourceLine := range strings.Split(source, "\n") {
		line, literal := promptLine(sourceLine)
		switch {
		case !inYAML && literal && line == "```yaml":
			inYAML, block = true, nil
		case inYAML && !literal:
			return nil, fmt.Errorf("line %d inside a yaml example is not a lone string literal: %q", number+1, sourceLine)
		case inYAML && line == "```":
			inYAML = false
			examples = append(examples, strings.Join(block, "\n")+"\n")
		case inYAML:
			block = append(block, line)
		}
	}
	if inYAML {
		return nil, fmt.Errorf("a yaml example never closes")
	}
	return examples, nil
}

var (
	idPlaceholders = strings.NewReplacer(
		"<project-id>", "33333333-3333-4333-8333-333333333333",
		"<app-id>", "44444444-4444-4444-8444-444444444444",
		"<web-app-id>", "55555555-5555-4555-8555-555555555555",
	)
	choicePlaceholder = regexp.MustCompile(`<([a-z_]+)\|[^>]*>`)
	textPlaceholder   = regexp.MustCompile(`<[^<>\n]+>`)
)

// concreteConfig fills an example's placeholders the way an agent would: IDs
// become IDs, a choice takes its first option, and other text becomes a word.
func concreteConfig(example string) string {
	filled := idPlaceholders.Replace(example)
	filled = choicePlaceholder.ReplaceAllString(filled, "$1")
	return textPlaceholder.ReplaceAllString(filled, "placeholder")
}

func validateProjectConfig(yaml string) error {
	authored, err := config.ParseAuthoredConfig([]byte(yaml))
	if err != nil {
		return err
	}
	_, err = config.NormalizeAuthoredConfig(*authored, config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."})
	return err
}

func TestSetupPromptConfigExamplesPassConfigValidation(t *testing.T) {
	for _, path := range []string{frontendSetupPrompts, hostedSetupPrompts} {
		examples, err := yamlExamples(testutil.ReadMonorepoFile(t, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(examples) < 3 {
			t.Fatalf("%s shows %d config examples, want the mobile, Web bundle, and registered preview shapes", path, len(examples))
		}
		for _, example := range examples {
			if err := validateProjectConfig(concreteConfig(example)); err != nil {
				t.Errorf("%s config example fails revyl config validate: %v\n%s", path, err, example)
			}
		}
	}
}

// markdownYAMLExamples returns every ```yaml block in a Markdown section, with
// the indentation of a block nested in a list item removed.
func markdownYAMLExamples(section string) []string {
	var examples, block []string
	indent, inYAML := "", false
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inYAML && trimmed == "```yaml":
			inYAML, block = true, nil
			indent = line[:len(line)-len(strings.TrimLeft(line, " "))]
		case inYAML && trimmed == "```":
			inYAML = false
			examples = append(examples, strings.Join(block, "\n")+"\n")
		case inYAML:
			block = append(block, strings.TrimPrefix(line, indent))
		}
	}
	return examples
}

func TestDevLoopSkillWebRecipePassesConfigValidation(t *testing.T) {
	files, err := Files(RevylCLIDevLoopName)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path != SkillFileName {
			continue
		}
		_, web, found := strings.Cut(string(file.Content), "## Web apps\n")
		if !found {
			t.Fatal("the dev-loop skill has no Web apps section")
		}
		web, _, _ = strings.Cut(web, "\n## ")
		examples := markdownYAMLExamples(web)
		if len(examples) == 0 {
			t.Fatal("the dev-loop skill's Web apps section shows no Web recipe")
		}
		for _, example := range examples {
			if err := validateProjectConfig(concreteConfig("project:\n  id: \"<project-id>\"\n" + example)); err != nil {
				t.Errorf("the dev-loop skill's Web recipe fails revyl config validate: %v\n%s", err, example)
			}
		}
		return
	}
	t.Fatal("the dev-loop skill has no SKILL.md")
}

func TestConfigValidationRejectsARevylBuildOfAURLOnlyWebProfile(t *testing.T) {
	urlOnly := concreteConfig("project:\n  id: \"<project-id>\"\nbuild:\n  profiles:\n    preview:\n      web:\n        app_id: \"<app-id>\"\npr_review:\n  enabled: true\n  build:\n    kind: revyl\n    profile: preview\n")
	if validateProjectConfig(urlOnly) == nil {
		t.Fatal("a Revyl-managed build of a Web profile with no recipe passed validation, so the examples test proves nothing")
	}
}

// TestEverySurfaceSkipsTheWebAppWithoutSetup keeps every prompt and skill from
// registering a Web address that revyl setup check-url never checked.
func TestEverySurfaceSkipsTheWebAppWithoutSetup(t *testing.T) {
	collapse := func(text string) string { return strings.Join(strings.Fields(text), " ") }
	surfaces := map[string]string{
		hostedSetupPrompts:   testutil.ReadMonorepoFile(t, hostedSetupPrompts),
		frontendSetupPrompts: testutil.ReadMonorepoFile(t, frontendSetupPrompts),
	}
	for _, source := range []struct{ skill, path string }{
		{RevylCLIDevLoopName, SkillFileName},
		{RevylMCPDevLoopName, SkillFileName},
		{RevylCLIAuthBypassName, "references/web.md"},
	} {
		files, err := Files(source.skill)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if file.Path == source.path {
				surfaces[source.skill+"/"+file.Path] = string(file.Content)
			}
		}
	}
	if len(surfaces) != 5 {
		t.Fatalf("found %d of the 5 setup surfaces", len(surfaces))
	}
	for name, text := range surfaces {
		text = collapse(text)
		if !strings.Contains(text, "if it is still unknown, skip the Web app, because an address can") {
			t.Errorf("%s does not skip the Web app when revyl setup is missing", name)
		}
		if strings.Contains(text, "check the address yourself") {
			t.Errorf("%s still checks a Web address without revyl setup", name)
		}
	}
}
