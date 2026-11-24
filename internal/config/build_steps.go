package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	BuildStepRun                   = "run"
	BuildStepIOSSigning            = "ios-signing"
	BuildStepAndroidSigning        = "android-signing"
	BuildStepAppStoreConnectDeploy = "app-store-connect-deploy"
	BuildStepGooglePlayDeploy      = "google-play-deploy"
)

const buildStepNameKey = "name"

type BuildStepItem struct {
	Command string
	Step    map[string]any
}

func CommandStepItem(command string) BuildStepItem {
	return BuildStepItem{Command: command}
}

func CommandStepItems(commands []string) []BuildStepItem {
	if commands == nil {
		return nil
	}
	items := make([]BuildStepItem, 0, len(commands))
	for _, command := range commands {
		items = append(items, CommandStepItem(command))
	}
	return items
}

func (i BuildStepItem) IsCommand() bool {
	return i.Step == nil
}

func (i BuildStepItem) Type() string {
	if i.Step == nil {
		return BuildStepRun
	}
	for key := range i.Step {
		if key != buildStepNameKey {
			return key
		}
	}
	return ""
}

func (i BuildStepItem) Name() string {
	if i.Step == nil {
		return ""
	}
	name, _ := i.Step[buildStepNameKey].(string)
	return name
}

func (i BuildStepItem) RunCommand() (string, bool) {
	if i.Step == nil {
		return i.Command, true
	}
	command, ok := i.Step[BuildStepRun].(string)
	return command, ok
}

func (i BuildStepItem) Inputs() map[string]any {
	if i.Step == nil {
		return nil
	}
	inputs, _ := i.Step[i.Type()].(map[string]any)
	return inputs
}

func (i BuildStepItem) MarshalJSON() ([]byte, error) {
	if i.Step == nil {
		return json.Marshal(i.Command)
	}
	return json.Marshal(i.Step)
}

func (i *BuildStepItem) UnmarshalJSON(data []byte) error {
	var command string
	if err := json.Unmarshal(data, &command); err == nil {
		*i = BuildStepItem{Command: command}
		return nil
	}
	var step map[string]any
	if err := json.Unmarshal(data, &step); err != nil {
		return fmt.Errorf("build step must be a string or a mapping")
	}
	*i = BuildStepItem{Step: step}
	return nil
}

func (i BuildStepItem) MarshalYAML() (any, error) {
	if i.Step == nil {
		return i.Command, nil
	}
	return i.Step, nil
}

func (i *BuildStepItem) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var command string
		if err := node.Decode(&command); err != nil {
			return err
		}
		*i = BuildStepItem{Command: command}
		return nil
	case yaml.MappingNode:
		var step map[string]any
		if err := node.Decode(&step); err != nil {
			return err
		}
		*i = BuildStepItem{Step: step}
		return nil
	default:
		return fmt.Errorf("build step must be a string or a mapping")
	}
}

func CommandStrings(items []BuildStepItem) ([]string, error) {
	commands := make([]string, 0, len(items))
	for _, item := range items {
		command, ok := item.RunCommand()
		if !ok {
			return nil, fmt.Errorf("%s steps run only in Revyl remote builds; use --remote", item.Type())
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func CommandSummary(items []BuildStepItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if command, ok := item.RunCommand(); ok {
			parts = append(parts, command)
			continue
		}
		parts = append(parts, "<"+item.Type()+">")
	}
	return strings.Join(parts, " && ")
}

func cloneBuildStepItems(items []BuildStepItem) []BuildStepItem {
	if len(items) == 0 {
		return []BuildStepItem{}
	}
	cloned := make([]BuildStepItem, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, BuildStepItem{Command: item.Command, Step: cloneAnyMap(item.Step)})
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			cloned[key] = cloneAnyMap(typed)
		case []any:
			cloned[key] = append([]any(nil), typed...)
		default:
			cloned[key] = value
		}
	}
	return cloned
}

type typedBuildStepSpec struct {
	platform        string
	required        []string
	requireAnyOf    []string
	allOrNone       []string
	strings         []string
	stringList      []string
	stringOrBoolMap []string
}

var typedBuildStepSpecs = map[string]typedBuildStepSpec{
	BuildStepIOSSigning: {
		platform:        "ios",
		required:        []string{"certificate"},
		requireAnyOf:    []string{"provisioning_profiles", "api_key"},
		allOrNone:       []string{"api_key", "key_id", "issuer_id"},
		strings:         []string{"certificate", "certificate_password", "api_key", "key_id", "issuer_id", "export_method"},
		stringList:      []string{"provisioning_profiles"},
		stringOrBoolMap: []string{"export_options"},
	},
	BuildStepAndroidSigning: {
		platform: "android",
		required: []string{"keystore", "keystore_password"},
		strings:  []string{"keystore", "keystore_password", "key_alias", "key_password"},
	},
	BuildStepAppStoreConnectDeploy: {
		platform: "ios",
		required: []string{"api_key", "key_id", "issuer_id"},
		strings:  []string{"api_key", "key_id", "issuer_id", "ipa", "apple_id"},
	},
	BuildStepGooglePlayDeploy: {
		platform: "android",
		required: []string{"service_account", "package_name"},
		strings:  []string{"service_account", "aab", "track", "release_status", "mapping_file", "package_name"},
	},
}

func TypedBuildStepTypes() []string {
	types := make([]string, 0, len(typedBuildStepSpecs))
	for key := range typedBuildStepSpecs {
		types = append(types, key)
	}
	sort.Strings(types)
	return types
}

func validateBuildStepSequence(node *yaml.Node, path []string) error {
	if node == nil {
		return nil
	}
	if err := validateSequenceNode(node, path); err != nil {
		return err
	}
	for index, item := range node.Content {
		itemPath := append(append([]string{}, path...), strconv.Itoa(index))
		if item.Kind == yaml.ScalarNode {
			if err := validateStringNode(item, itemPath, false); err != nil {
				return err
			}
			continue
		}
		if err := validateBuildStepMappingNode(item, itemPath); err != nil {
			return err
		}
	}
	return nil
}

func validateBuildStepMappingNode(node *yaml.Node, path []string) error {
	if err := validateMappingNode(node, path, false); err != nil {
		return err
	}
	stepKeys := []string{}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		if key == buildStepNameKey {
			if err := validateStringNode(node.Content[index+1], append(append([]string{}, path...), key), false); err != nil {
				return err
			}
			continue
		}
		if key != BuildStepRun {
			if _, known := typedBuildStepSpecs[key]; !known {
				return newConfigError("contract", "unknown_field", append(append([]string{}, path...), key), "")
			}
		}
		stepKeys = append(stepKeys, key)
	}
	if len(stepKeys) != 1 {
		return newConfigError("contract", "invalid_contract", path, "must contain exactly one run or typed build step key, plus an optional name")
	}
	stepKey := stepKeys[0]
	stepPath := append(append([]string{}, path...), stepKey)
	value := mappingValue(node, stepKey)
	if stepKey == BuildStepRun {
		return validateStringNode(value, stepPath, false)
	}
	return validateTypedBuildStepInputs(value, stepPath, typedBuildStepSpecs[stepKey])
}

func validateTypedBuildStepInputs(node *yaml.Node, path []string, spec typedBuildStepSpec) error {
	if err := validateMappingNode(node, path, false); err != nil {
		return err
	}
	allowed := append(append(append([]string{}, spec.strings...), spec.stringList...), spec.stringOrBoolMap...)
	if err := validateAllowedKeys(node, path, allowed...); err != nil {
		return err
	}
	for _, key := range spec.required {
		if mappingValue(node, key) == nil {
			return newConfigError("contract", "missing_field", append(append([]string{}, path...), key), "")
		}
	}
	for _, key := range spec.strings {
		if err := validateStringNode(mappingValue(node, key), append(append([]string{}, path...), key), false); err != nil {
			return err
		}
	}
	for _, key := range spec.stringList {
		value := mappingValue(node, key)
		keyPath := append(append([]string{}, path...), key)
		if err := validateStringSequence(value, keyPath); err != nil {
			return err
		}
		if value != nil && len(value.Content) == 0 {
			return authoredContractError(keyPath, key+" must not be empty when given")
		}
	}
	if len(spec.requireAnyOf) > 0 && countPresentKeys(node, spec.requireAnyOf) == 0 {
		return authoredContractError(path, "needs "+strings.Join(spec.requireAnyOf, " or "))
	}
	if present := countPresentKeys(node, spec.allOrNone); present > 0 && present < len(spec.allOrNone) {
		return authoredContractError(path, strings.Join(spec.allOrNone, ", ")+" must be given together")
	}
	for _, key := range spec.stringOrBoolMap {
		value := mappingValue(node, key)
		if value == nil {
			continue
		}
		valuePath := append(append([]string{}, path...), key)
		if err := validateMappingNode(value, valuePath, false); err != nil {
			return err
		}
		for index := 0; index < len(value.Content); index += 2 {
			entry := value.Content[index+1]
			entryPath := append(append([]string{}, valuePath...), value.Content[index].Value)
			if entry.Kind != yaml.ScalarNode || (entry.Tag != "!!str" && entry.Tag != "!!bool") {
				return newConfigError("contract", "invalid_contract", entryPath, "must be a string or boolean")
			}
		}
	}
	return nil
}

func countPresentKeys(node *yaml.Node, keys []string) int {
	present := 0
	for _, key := range keys {
		if mappingValue(node, key) != nil {
			present++
		}
	}
	return present
}

// validateBuildStepItems checks decoded run commands, known typed-step
// identities, and optional platform compatibility (empty skips platform check).
func validateBuildStepItems(items []BuildStepItem, path []string, platform string) error {
	for index, item := range items {
		itemPath := append(append([]string{}, path...), strconv.Itoa(index))
		if command, ok := item.RunCommand(); ok {
			if strings.TrimSpace(command) == "" {
				return authoredContractError(itemPath, "build commands must not contain blank commands")
			}
			continue
		}
		spec, known := typedBuildStepSpecs[item.Type()]
		if !known || item.Inputs() == nil {
			return authoredContractError(itemPath, "build step mapping must name exactly one known step")
		}
		if platform != "" && spec.platform != platform {
			return authoredContractError(itemPath, fmt.Sprintf("%s is not valid in an %s recipe", item.Type(), platform))
		}
	}
	return nil
}
