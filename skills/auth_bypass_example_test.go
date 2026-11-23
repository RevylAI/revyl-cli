package skills_test

import (
	"bytes"
	"regexp"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/schema"
	"github.com/revyl/cli/skills"
)

var yamlFence = regexp.MustCompile("(?s)```yaml\n(.*?)```")

func TestAuthBypassTestExampleIsAValidTestFile(t *testing.T) {
	var example []byte
	for _, match := range yamlFence.FindAllSubmatch([]byte(skills.RevylCLIAuthBypassContent), -1) {
		if bytes.HasPrefix(match[1], []byte("test:")) {
			example = match[1]
		}
	}
	if example == nil {
		t.Fatal("the auth-bypass skill has no test YAML example")
	}

	decoder := yaml.NewDecoder(bytes.NewReader(example))
	decoder.KnownFields(true)
	var test config.LocalTest
	if err := decoder.Decode(&test); err != nil {
		t.Fatalf("the example is not a CLI test file: %v", err)
	}
	if test.Test.Metadata.Name == "" || test.Test.Build.Name == "" {
		t.Error("the example needs metadata.name and build.name")
	}
	supportedPlatforms := schema.YAMLTestSchemaJSON()["criticalBehavior"].(map[string]interface{})["supportedPlatforms"].([]string)
	if !slices.Contains(supportedPlatforms, test.Test.Metadata.Platform) {
		t.Errorf("metadata.platform %q is not one of %v", test.Test.Metadata.Platform, supportedPlatforms)
	}
	if len(test.Test.Blocks) == 0 {
		t.Fatal("the example has no blocks")
	}
	blockTypes := schema.YAMLTestSchemaJSON()["blockTypes"].(map[string]interface{})
	manualStepTypes := blockTypes["manual"].(map[string]interface{})["stepTypes"].([]string)
	for index, block := range test.Test.Blocks {
		if !api.ActionBlockType(block.Type).Valid() {
			t.Errorf("block %d has type %q, which Revyl does not accept", index, block.Type)
		}
		if block.Type == string(api.ActionBlockTypeManual) && !slices.Contains(manualStepTypes, block.StepType) {
			t.Errorf("manual block %d has step_type %q, not one of %v", index, block.StepType, manualStepTypes)
		}
		if block.StepDescription == "" {
			t.Errorf("block %d has no step_description", index)
		}
	}
}
