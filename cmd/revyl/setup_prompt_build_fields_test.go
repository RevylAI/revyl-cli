package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/testutil"
)

// jsonFieldNames returns the JSON names of a struct's fields.
func jsonFieldNames(value any) map[string]bool {
	names := map[string]bool{}
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		names[name] = true
	}
	return names
}

// TestSetupPromptsNameTheBuildFieldsTheCLIPrints keeps the build fields the
// setup prompts tell agents to read in step with the JSON the CLI prints: a
// cloud build's job and build ID, a followed job's build ID, and a registered
// Web preview's build ID.
func TestSetupPromptsNameTheBuildFieldsTheCLIPrints(t *testing.T) {
	printedBy := map[string][]any{
		"status":           {remoteBuildJSONResult{}, api.RemoteBuildStatusResponse{}},
		"build_job_id":     {remoteBuildJSONResult{}},
		"build_version_id": {remoteBuildJSONResult{}},
		"version_id":       {webURLUploadJSON{}, api.RemoteBuildStatusResponse{}},
	}
	for field, outputs := range printedBy {
		for _, output := range outputs {
			if !jsonFieldNames(output)[field] {
				t.Errorf("%T prints no %q field, but the setup prompts tell agents to read it", output, field)
			}
		}
	}
	for _, path := range []string{"frontend/src/lib/agentSetupPrompts.ts", "gtm/apps/website/src/lib/setup-command.ts"} {
		source := testutil.ReadMonorepoFile(t, path)
		for field := range printedBy {
			if !strings.Contains(source, "`"+field+"`") {
				t.Errorf("%s does not tell agents to read %q", path, field)
			}
		}
		if strings.Contains(source, "<build-job-id>") {
			t.Errorf("%s names the job placeholder <build-job-id> instead of the build_job_id field", path)
		}
	}
}

// TestSetupPromptsTakeAURLUploadBuildIDWithoutAJob keeps the agent's
// registered-preview upload on its own path: it creates no build job, so each
// passage that runs it takes version_id at once and never polls a job.
func TestSetupPromptsTakeAURLUploadBuildIDWithoutAJob(t *testing.T) {
	for _, path := range []string{"frontend/src/lib/agentSetupPrompts.ts", "gtm/apps/website/src/lib/setup-command.ts"} {
		passages := urlUploadPassages(testutil.ReadMonorepoFile(t, path))
		if len(passages) == 0 {
			t.Errorf("%s no longer has the agent run build upload --platform web --url with --json", path)
		}
		for _, passage := range passages {
			if !strings.Contains(passage, "`version_id`") {
				t.Errorf("%s: the URL upload passage does not take the build's ID from version_id:\n%s", path, passage)
			}
			for _, jobOnly := range []string{"build status", "build_job_id"} {
				if strings.Contains(passage, jobOnly) {
					t.Errorf("%s: the URL upload passage mentions %q, but that upload creates no build job:\n%s", path, jobOnly, passage)
				}
			}
		}
	}
}

// urlUploadPassages returns, for each line where the agent itself runs the
// registered-preview upload (with --json, unlike the CI job a prompt
// describes), the source lines from it to the end of its paragraph, the first
// blank prompt line after it.
func urlUploadPassages(source string) []string {
	lines := strings.Split(source, "\n")
	var passages []string
	for start, line := range lines {
		if !strings.Contains(line, "build upload --platform web --url") || !strings.Contains(line, "--json`") {
			continue
		}
		end := start + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "\"\"," && strings.TrimSpace(lines[end]) != "''," {
			end++
		}
		passages = append(passages, strings.Join(lines[start:end], "\n"))
	}
	return passages
}
