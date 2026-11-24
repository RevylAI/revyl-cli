package skills

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/schema"
)

func agentRulesSection(t *testing.T, name string) string {
	t.Helper()
	files, err := Files(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path != SkillFileName {
			continue
		}
		text := string(file.Content)
		start := strings.Index(text, "## Revyl agent rules\n")
		if start == -1 {
			t.Fatalf("%s has no Revyl agent rules section", name)
		}
		section := text[start:]
		if end := strings.Index(section[len("## "):], "\n## "); end != -1 {
			section = section[:len("## ")+end]
		}
		return section
	}
	t.Fatalf("%s has no %s", name, SkillFileName)
	return ""
}

func TestDefaultSkillsCarryIdenticalAgentRules(t *testing.T) {
	names := []string{RevylCLIDevLoopName, RevylCLICreateName, RevylCLIAtlasName, RevylCLIAuthBypassName}
	want := agentRulesSection(t, names[0])
	for _, fragment := range []string{"--intent", "revyl feedback --type bug", "--type other", "REVYL_AGENT", "billing_url", "upgrade command"} {
		if !strings.Contains(want, fragment) {
			t.Errorf("agent rules missing %q", fragment)
		}
	}
	for _, name := range names[1:] {
		if got := agentRulesSection(t, name); got != want {
			t.Errorf("%s agent rules differ from %s; edit both copies together", name, names[0])
		}
	}
}

// TestSkillTestYAMLExamplesMatchTheCLITestSchema decodes every test YAML example
// the embedded skills show into the CLI's local test model, rejecting unknown
// keys such as steps:, and checks it against the published test schema.
func TestSkillTestYAMLExamplesMatchTheCLITestSchema(t *testing.T) {
	examples := 0
	for _, name := range []string{RevylCLIName, RevylCLICreateName, RevylCLIAnalyzeName, RevylCLIOptimizeName, RevylCLIImportTestsName,
		RevylCLIDevLoopName, RevylCLIAtlasName, RevylCLIAtlasReviewName, RevylCLIAuthBypassName, RevylCLIFigmaReviewName,
		RevylCLICrossPlatformReviewName, RevylMCPName, RevylMCPCreateName, RevylMCPAnalyzeName, RevylMCPDevLoopName} {
		files, err := Files(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			for _, example := range testYAMLExamples(string(file.Content)) {
				examples++
				if err := validateTestYAML(example); err != nil {
					t.Errorf("%s/%s test YAML example: %v\n%s", name, file.Path, err, example)
				}
			}
		}
	}
	files, err := Files(RevylCLIAuthBypassName)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Path == SkillFileName && len(testYAMLExamples(string(file.Content))) == 0 {
			t.Error("the auth-bypass skill no longer shows the test YAML example this test validates")
		}
	}
	if examples == 0 {
		t.Fatal("no test YAML examples found")
	}
}

func TestValidateTestYAMLRejectsStepsAndAMissingBuild(t *testing.T) {
	for _, example := range []string{
		"test:\n  metadata:\n    name: a\n    platform: ios\n  build:\n    name: app\n  steps:\n    - type: validation\n      step_description: x\n",
		"test:\n  metadata:\n    name: a\n    platform: ios\n  blocks:\n    - type: validation\n      step_description: x\n",
		"test:\n  metadata:\n    name: a\n    platform: ios\n  build:\n    name: app\n  blocks:\n    - type: manual\n      step_type: teleport\n",
	} {
		if validateTestYAML(example) == nil {
			t.Errorf("validateTestYAML accepted an invalid test:\n%s", example)
		}
	}
}

// testYAMLExamples returns the YAML code blocks whose top-level key is test:.
func testYAMLExamples(markdown string) []string {
	var examples []string
	var block []string
	inYAML := false
	for _, line := range strings.Split(markdown, "\n") {
		switch {
		case !inYAML && strings.TrimSpace(line) == "```yaml":
			inYAML, block = true, nil
		case inYAML && strings.TrimSpace(line) == "```":
			inYAML = false
			if len(block) > 0 && block[0] == "test:" {
				examples = append(examples, strings.Join(block, "\n")+"\n")
			}
		case inYAML:
			block = append(block, line)
		}
	}
	return examples
}

func validateTestYAML(example string) error {
	decoder := yaml.NewDecoder(strings.NewReader(example))
	decoder.KnownFields(true)
	var local config.LocalTest
	if err := decoder.Decode(&local); err != nil {
		return err
	}
	test := local.Test
	testSchema := schema.YAMLTestSchemaJSON()
	platforms := testSchema["criticalBehavior"].(map[string]interface{})["supportedPlatforms"].([]string)
	if test.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if !slices.Contains(platforms, test.Metadata.Platform) {
		return fmt.Errorf("metadata.platform %q is not one of %v", test.Metadata.Platform, platforms)
	}
	if test.Build.Name == "" {
		return fmt.Errorf("build.name is required")
	}
	if len(test.Blocks) == 0 {
		return fmt.Errorf("blocks is empty")
	}
	return validateTestBlocks(test.Blocks, testSchema["blockTypes"].(map[string]interface{}))
}

func validateTestBlocks(blocks []config.TestBlock, blockTypes map[string]interface{}) error {
	for _, block := range blocks {
		definition, ok := blockTypes[block.Type].(map[string]interface{})
		if !ok {
			return fmt.Errorf("block type %q is not in the test schema", block.Type)
		}
		present := map[string]bool{
			"step_description": block.StepDescription != "",
			"step_type":        block.StepType != "",
			"variable_name":    block.VariableName != "",
			"condition":        block.Condition != "",
			"then":             len(block.Then) > 0,
			"body":             len(block.Body) > 0,
		}
		for field, description := range definition["fields"].(map[string]string) {
			if strings.Contains(description, "(required") && !present[field] {
				return fmt.Errorf("%s block needs %s", block.Type, field)
			}
		}
		if stepTypes, ok := definition["stepTypes"].([]string); ok && !slices.Contains(stepTypes, block.StepType) {
			return fmt.Errorf("%s step_type %q is not one of %v", block.Type, block.StepType, stepTypes)
		}
		for _, children := range [][]config.TestBlock{block.Then, block.Else, block.Body} {
			if err := validateTestBlocks(children, blockTypes); err != nil {
				return err
			}
		}
	}
	return nil
}
