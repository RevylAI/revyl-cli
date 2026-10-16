package config

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var webRunVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (r AuthoredWebRecipe) MarshalYAML() (any, error) {
	if !r.HasBuildRecipe() {
		return struct {
			AppID *string `yaml:"app_id,omitempty"`
		}{AppID: r.AppID}, nil
	}
	return AuthoredBuildRecipe(r), nil
}

func (r AuthoredWebRecipe) MarshalJSON() ([]byte, error) {
	if !r.HasBuildRecipe() {
		return json.Marshal(struct {
			AppID *string `json:"app_id,omitempty"`
		}{AppID: r.AppID})
	}
	return json.Marshal(AuthoredBuildRecipe(r))
}

func DefaultWebRun() AuthoredWebRun {
	return AuthoredWebRun{Port: 3000, Healthcheck: "/", StartTimeoutSeconds: 60, EnvKeys: []string{}, SecretNames: []string{}}
}

func (r *AuthoredWebRun) UnmarshalYAML(node *yaml.Node) error {
	type plain AuthoredWebRun
	value := plain(DefaultWebRun())
	if err := node.Decode(&value); err != nil {
		return err
	}
	*r = AuthoredWebRun(value)
	return nil
}

func (r *AuthoredWebRun) UnmarshalJSON(data []byte) error {
	type plain AuthoredWebRun
	value := plain(DefaultWebRun())
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*r = AuthoredWebRun(value)
	return nil
}

func CloneWebRun(run *AuthoredWebRun) *AuthoredWebRun {
	if run == nil {
		return nil
	}
	cloned := *run
	cloned.Start = cloneStringPointer(run.Start)
	cloned.EnvKeys = append([]string{}, run.EnvKeys...)
	cloned.SecretNames = append([]string{}, run.SecretNames...)
	return &cloned
}

func validateWebOutputPath(output string) error {
	if output != strings.TrimSpace(output) || strings.ContainsAny(output, `\*?[]`) || path.IsAbs(output) || strings.Contains(output, ":") {
		return fmt.Errorf("web output_path must name one relative output directory, without globs")
	}
	for _, component := range strings.Split(output, "/") {
		if component == ".." {
			return fmt.Errorf("web output_path must not traverse outside the project")
		}
	}
	if path.Clean(output) == "." || output == "" {
		return fmt.Errorf("web output_path must name a dedicated output directory, not the source root")
	}
	return nil
}

func validateWebRun(run *AuthoredWebRun, fieldPath []string) error {
	fail := func(field, message string) error {
		return authoredContractError(append(append([]string{}, fieldPath...), field), message)
	}
	if run.Start != nil && (strings.TrimSpace(*run.Start) == "" || utf8.RuneCountInString(*run.Start) > 8192 || strings.ContainsRune(*run.Start, '\x00')) {
		return fail("start", "run.start must contain a command when provided")
	}
	if run.Port < 1 || run.Port > 65535 {
		return fail("port", "run.port must be between 1 and 65535")
	}
	if utf8.RuneCountInString(run.Healthcheck) > 2048 || !strings.HasPrefix(run.Healthcheck, "/") || strings.HasPrefix(run.Healthcheck, "//") || strings.ContainsAny(run.Healthcheck, "\x00\r\n\\#") {
		return fail("healthcheck", "run.healthcheck must be a local absolute HTTP path")
	}
	if run.StartTimeoutSeconds < 10 || run.StartTimeoutSeconds > 600 {
		return fail("start_timeout_seconds", "run.start_timeout_seconds must be between 10 and 600")
	}
	seen := map[string]bool{}
	for _, group := range []struct {
		field string
		names []string
	}{{"env_keys", run.EnvKeys}, {"secret_names", run.SecretNames}} {
		if len(group.names) > 64 {
			return fail(group.field, "run permits at most 64 variable names per list")
		}
		for _, name := range group.names {
			if !webRunVariableName.MatchString(name) {
				return fail(group.field, "run variables must be environment variable names, never KEY=VALUE assignments")
			}
			if seen[name] {
				return fail(group.field, "run variable names must be unique across env_keys and secret_names")
			}
			seen[name] = true
		}
	}
	return nil
}

func validateWebRunNode(node *yaml.Node, fieldPath []string) error {
	if err := validateMappingNode(node, fieldPath, false); err != nil {
		return err
	}
	if err := validateAllowedKeys(node, fieldPath, "start", "port", "healthcheck", "start_timeout_seconds", "env_keys", "secret_names"); err != nil {
		return err
	}
	for _, field := range []string{"start", "healthcheck"} {
		if err := validateStringNode(mappingValue(node, field), append(fieldPath, field), field == "start"); err != nil {
			return err
		}
	}
	for _, field := range []string{"port", "start_timeout_seconds"} {
		if nodeIsNull(mappingValue(node, field)) {
			return authoredContractError(append(fieldPath, field), "run numeric fields must not be null")
		}
		if err := validateSecondsNode(mappingValue(node, field), append(fieldPath, field)); err != nil {
			return err
		}
	}
	for _, field := range []string{"env_keys", "secret_names"} {
		if err := validateStringSequence(mappingValue(node, field), append(fieldPath, field)); err != nil {
			return err
		}
	}
	return nil
}
