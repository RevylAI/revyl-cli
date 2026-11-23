package schema

import (
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func TestLLMFormatExampleProjectConfigPassesConfigValidation(t *testing.T) {
	reference := ToLLMFormat(&CLISchema{}, YAMLTestSchema)
	if !strings.Contains(reference, ExampleProjectConfig) {
		t.Fatal("the LLM reference does not show ExampleProjectConfig")
	}
	authored, err := config.ParseAuthoredConfig([]byte(ExampleProjectConfig))
	if err != nil {
		t.Fatalf("ParseAuthoredConfig(example): %v", err)
	}
	if _, err := config.NormalizeAuthoredConfig(*authored, config.CompilationContext{RepositoryRelativeProjectRoot: ".", ExecutionDirectory: "."}); err != nil {
		t.Fatalf("NormalizeAuthoredConfig(example): %v", err)
	}
}

// Every place the LLM reference teaches revyl init -y also says to run it from
// the app's own folder.
func TestLLMFormatRunsInitFromTheAppFolder(t *testing.T) {
	reference := ToLLMFormat(&CLISchema{Workflows: getCommonWorkflows()}, YAMLTestSchema)
	lines := strings.Split(reference, "\n")
	teaches := 0
	for i, line := range lines {
		if !strings.Contains(line, "revyl init -y") {
			continue
		}
		teaches++
		if !strings.Contains(line, "app's own folder") && (i == 0 || !strings.Contains(lines[i-1], "app's own folder")) {
			t.Errorf("%q does not say to run init from the app's own folder", strings.TrimSpace(line))
		}
	}
	if teaches == 0 {
		t.Fatal("the LLM reference never teaches revyl init -y")
	}
}

func TestLLMFormatCreatesTestsWithAnApp(t *testing.T) {
	reference := ToLLMFormat(&CLISchema{Workflows: getCommonWorkflows()}, YAMLTestSchema)
	creates := 0
	for _, line := range strings.Split(reference, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "revyl test create") {
			continue
		}
		creates++
		if !strings.Contains(line, "--app <app-id>") || !(strings.Contains(line, "--platform") || strings.Contains(line, "--from-session")) {
			t.Errorf("%q would prompt for a platform or fail with no app linked after revyl init -y", strings.TrimSpace(line))
		}
	}
	if creates == 0 {
		t.Fatal("the LLM reference shows no revyl test create command")
	}
}

func TestLLMFormatTeachesNoRetiredProjectConfigKeys(t *testing.T) {
	reference := ToLLMFormat(&CLISchema{}, YAMLTestSchema)
	for _, retired := range []string{"build.platforms", "  platforms:", "\ntests:\n", "under `tests:`"} {
		if strings.Contains(reference, retired) {
			t.Errorf("the LLM reference teaches the retired config key %q", retired)
		}
	}
}
