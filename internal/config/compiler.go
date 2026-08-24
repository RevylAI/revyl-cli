package config

import (
	"bytes"
	"errors"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const MaxConfigBytes = 1_048_576
const MaxNormalizedConfigBytes = 8 * MaxConfigBytes
const MaxConfigProfiles = 64
const MaxConfigCollectionItems = 1_024

var developmentProfileToken = regexp.MustCompile(`(?i)(^|[^a-z0-9])(dev|development)($|[^a-z0-9])`)
var unknownYAMLField = regexp.MustCompile(`field ([^ ]+) not found`)
var missingRecipeBuildCommands = regexp.MustCompile(`build\.profiles\.([^.]+)\.(ios|android)\.build_commands is required`)
var yamlBoolean = regexp.MustCompile(`^(true|True|TRUE|false|False|FALSE)$`)
var yamlInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
var yamlNull = regexp.MustCompile(`^(~|null|Null|NULL)$`)
var yamlErrorLine = regexp.MustCompile(`(?:^|[ ,])line ([0-9]+)`)
var yamlErrorColumn = regexp.MustCompile(`(?:^|[ ,])column ([0-9]+)`)

type legacyReviewBuildDefaults struct {
	buildCommand string
	outputPath   string
}

var legacyReviewBuildDefaultsByFramework = map[string]legacyReviewBuildDefaults{
	"expo_ios": {
		buildCommand: "bun install\nbunx expo prebuild --platform ios --non-interactive\ncd ios && pod install\nxcodebuild -workspace ios/App.xcworkspace -scheme App -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		outputPath:   "build/Build/Products/Debug-iphonesimulator/*.app",
	},
	"expo_android": {
		buildCommand: "bun install\ncd android && ./gradlew assembleDebug",
		outputPath:   "android/app/build/outputs/apk/debug/*.apk",
	},
	"react_native_ios": {
		buildCommand: "xcodebuild -workspace ios/App.xcworkspace -scheme App -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		outputPath:   "build/Build/Products/Debug-iphonesimulator/*.app",
	},
	"react_native_android": {
		buildCommand: "cd android && ./gradlew assembleDebug",
		outputPath:   "android/app/build/outputs/apk/debug/*.apk",
	},
	"native_ios": {
		buildCommand: "xcodebuild -scheme App -configuration Debug -sdk iphonesimulator -derivedDataPath build",
		outputPath:   "build/Build/Products/Debug-iphonesimulator/*.app",
	},
	"native_android": {
		buildCommand: "./gradlew assembleDebug",
		outputPath:   "app/build/outputs/apk/debug/*.apk",
	},
}

// ConfigError exposes stable stage/code/path identity. Message is not a contract.
type ConfigError struct {
	Stage   string   `json:"stage"`
	Code    string   `json:"code"`
	Path    []string `json:"path"`
	Message string   `json:"message,omitempty"`
	Line    int      `json:"line,omitempty"`
	Column  int      `json:"column,omitempty"`
}

func (e *ConfigError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Stage + ": " + e.Code
}

func newConfigError(stage, code string, path []string, message string) *ConfigError {
	if path == nil {
		path = []string{}
	}
	return &ConfigError{Stage: stage, Code: code, Path: path, Message: message}
}

func newLocatedConfigError(stage, code string, path []string, message string, line, column int) *ConfigError {
	err := newConfigError(stage, code, path, message)
	err.Line = line
	err.Column = column
	return err
}

func yamlDecodeConfigError(err error) *ConfigError {
	located := newConfigError("yaml_syntax", "invalid_yaml", nil, "")
	if match := yamlErrorLine.FindStringSubmatch(err.Error()); len(match) == 2 {
		located.Line, _ = strconv.Atoi(match[1])
	}
	if match := yamlErrorColumn.FindStringSubmatch(err.Error()); len(match) == 2 {
		located.Column, _ = strconv.Atoi(match[1])
	}
	return located
}

func loadYAMLDocument(data []byte) (*yaml.Node, map[string]any, error) {
	root, err := loadSingleYAMLMappingNode(data)
	if err != nil {
		return nil, nil, err
	}
	if err := validateYAMLNode(root); err != nil {
		return nil, nil, err
	}
	var value map[string]any
	if err := root.Decode(&value); err != nil {
		return nil, nil, yamlDecodeConfigError(err)
	}
	return root, value, nil
}

func loadLegacyMigrationYAMLDocument(data []byte) (*yaml.Node, map[string]any, error) {
	root, err := loadSingleYAMLMappingNode(data)
	if err != nil {
		return nil, nil, err
	}
	if err := validateLegacyMigrationYAMLNode(root); err != nil {
		return nil, nil, err
	}
	var expanded map[string]any
	if err := root.Decode(&expanded); err != nil {
		return nil, nil, yamlDecodeConfigError(err)
	}
	plain, err := yaml.Marshal(expanded)
	if err != nil {
		return nil, nil, newConfigError("yaml_syntax", "invalid_yaml", nil, "")
	}
	return loadYAMLDocument(plain)
}

func loadSingleYAMLMappingNode(data []byte) (*yaml.Node, error) {
	if len(data) > MaxConfigBytes {
		return nil, newConfigError("read", "config_too_large", nil, "")
	}
	if !utf8Valid(data) {
		return nil, newConfigError("read", "invalid_utf8", nil, "")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, yamlDecodeConfigError(err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, yamlDecodeConfigError(err)
		}
		return nil, newConfigError("yaml_syntax", "single_mapping_document_required", nil, "")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, newConfigError("yaml_syntax", "single_mapping_document_required", nil, "")
	}
	return document.Content[0], nil
}

func validateYAMLNode(node *yaml.Node) error {
	if node.Alias != nil || node.Anchor != "" || node.Style&yaml.TaggedStyle != 0 {
		return newLocatedConfigError("yaml_syntax", "unsupported_yaml_structure", nil, "", node.Line, node.Column)
	}
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!bool":
			if !yamlBoolean.MatchString(node.Value) {
				node.Tag = "!!str"
			}
		case "!!int":
			if !yamlInteger.MatchString(node.Value) {
				node.Tag = "!!str"
			}
		case "!!null":
			if !yamlNull.MatchString(node.Value) {
				node.Tag = "!!str"
			}
		case "!!str":
		default:
			node.Tag = "!!str"
		}
	case yaml.MappingNode:
		seen := map[string]struct{}{}
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return newLocatedConfigError("yaml_syntax", "invalid_mapping_key", nil, "", key.Line, key.Column)
			}
			if _, exists := seen[key.Value]; exists {
				return newLocatedConfigError("yaml_syntax", "duplicate_mapping_key", nil, "", key.Line, key.Column)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}

func validateLegacyMigrationYAMLNode(node *yaml.Node) error {
	if node.Style&yaml.TaggedStyle != 0 {
		return newLocatedConfigError("yaml_syntax", "unsupported_yaml_structure", nil, "", node.Line, node.Column)
	}
	if node.Kind == yaml.AliasNode {
		if node.Alias == nil {
			return newLocatedConfigError("yaml_syntax", "unsupported_yaml_structure", nil, "", node.Line, node.Column)
		}
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!bool":
			if !yamlBoolean.MatchString(node.Value) {
				node.Tag = "!!str"
			}
		case "!!int":
			if !yamlInteger.MatchString(node.Value) {
				node.Tag = "!!str"
			}
		case "!!null":
			if !yamlNull.MatchString(node.Value) {
				node.Tag = "!!str"
			}
		case "!!str", "!!merge":
		default:
			node.Tag = "!!str"
		}
	case yaml.MappingNode:
		seen := map[string]struct{}{}
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			validMergeKey := key.Kind == yaml.ScalarNode && key.Tag == "!!merge" && key.Value == "<<"
			if !validMergeKey && (key.Kind != yaml.ScalarNode || key.Tag != "!!str") {
				return newLocatedConfigError("yaml_syntax", "invalid_mapping_key", nil, "", key.Line, key.Column)
			}
			if _, exists := seen[key.Value]; exists {
				return newLocatedConfigError("yaml_syntax", "duplicate_mapping_key", nil, "", key.Line, key.Column)
			}
			seen[key.Value] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := validateLegacyMigrationYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}

func validateContractKeys(root *yaml.Node) error {
	if root.Kind != yaml.MappingNode {
		return newConfigError("contract", "invalid_contract", nil, "")
	}
	if err := validateAllowedKeys(root, nil, "project", "session", "build", "pr_review"); err != nil {
		return err
	}
	project := mappingValue(root, "project")
	if project == nil {
		return newConfigError("contract", "missing_field", []string{"project"}, "")
	}
	if err := validateMappingNode(project, []string{"project"}, false); err != nil {
		return err
	}
	if err := validateAllowedKeys(project, []string{"project"}, "id"); err != nil {
		return err
	}
	if mappingValue(project, "id") == nil {
		return newConfigError("contract", "missing_field", []string{"project", "id"}, "")
	}
	if err := validateStringNode(mappingValue(project, "id"), []string{"project", "id"}, false); err != nil {
		return err
	}

	if session := mappingValue(root, "session"); session != nil && !nodeIsNull(session) {
		if err := validateMappingNode(session, []string{"session"}, true); err != nil {
			return err
		}
		if err := validateAllowedKeys(session, []string{"session"}, "idle_timeout_seconds", "before_script", "auth_bypass"); err != nil {
			return err
		}
		if err := validateSecondsNode(mappingValue(session, "idle_timeout_seconds"), []string{"session", "idle_timeout_seconds"}); err != nil {
			return err
		}
		if before := mappingValue(session, "before_script"); before != nil && !nodeIsNull(before) {
			if err := validateMappingNode(before, []string{"session", "before_script"}, true); err != nil {
				return err
			}
			if err := validateAllowedKeys(before, []string{"session", "before_script"}, "script_path", "timeout_seconds"); err != nil {
				return err
			}
			if err := validateSecondsNode(mappingValue(before, "timeout_seconds"), []string{"session", "before_script", "timeout_seconds"}); err != nil {
				return err
			}
			if err := validateStringNode(mappingValue(before, "script_path"), []string{"session", "before_script", "script_path"}, true); err != nil {
				return err
			}
		}
		if bypass := mappingValue(session, "auth_bypass"); bypass != nil && !nodeIsNull(bypass) {
			if err := validateMappingNode(bypass, []string{"session", "auth_bypass"}, true); err != nil {
				return err
			}
			if err := validateAllowedKeys(bypass, []string{"session", "auth_bypass"}, "launch_vars", "deep_link"); err != nil {
				return err
			}
			if err := validateStringSequence(mappingValue(bypass, "launch_vars"), []string{"session", "auth_bypass", "launch_vars"}); err != nil {
				return err
			}
			if err := validateStringNode(mappingValue(bypass, "deep_link"), []string{"session", "auth_bypass", "deep_link"}, true); err != nil {
				return err
			}
		}
	}

	if build := mappingValue(root, "build"); build != nil && !nodeIsNull(build) {
		if err := validateMappingNode(build, []string{"build"}, true); err != nil {
			return err
		}
		if err := validateAllowedKeys(build, []string{"build"}, "framework", "env", "secrets", "caches", "profiles"); err != nil {
			return err
		}
		if mappingValue(build, "framework") == nil {
			return newConfigError("contract", "missing_field", []string{"build", "framework"}, "")
		}
		if err := validateEnumStringNode(mappingValue(build, "framework"), []string{"build", "framework"}, "ios", "android", "react_native", "expo", "flutter"); err != nil {
			return err
		}
		if err := validateStringMap(mappingValue(build, "env"), []string{"build", "env"}); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(build, "secrets"), []string{"build", "secrets"}); err != nil {
			return err
		}
		if err := validateCacheNodes(mappingValue(build, "caches"), []string{"build", "caches"}); err != nil {
			return err
		}
		profiles := mappingValue(build, "profiles")
		if profiles != nil {
			if err := validateMappingNode(profiles, []string{"build", "profiles"}, false); err != nil {
				return err
			}
			for index := 0; index < len(profiles.Content); index += 2 {
				name := profiles.Content[index].Value
				profile := profiles.Content[index+1]
				profilePath := []string{"build", "profiles", name}
				if err := validateMappingNode(profile, profilePath, false); err != nil {
					return err
				}
				if err := validateAllowedKeys(profile, profilePath, "ios", "android"); err != nil {
					return err
				}
				for _, platform := range []string{"ios", "android"} {
					recipe := mappingValue(profile, platform)
					if recipe == nil || nodeIsNull(recipe) {
						continue
					}
					recipePath := append(append([]string{}, profilePath...), platform)
					if err := validateMappingNode(recipe, recipePath, true); err != nil {
						return err
					}
					if err := validateAllowedKeys(recipe, recipePath, "app_id", "setup_commands", "build_commands", "output_path", "image", "timeout_seconds", "env", "secrets", "caches"); err != nil {
						return err
					}
					if mappingValue(recipe, "build_commands") == nil {
						return newConfigError("contract", "missing_field", append(recipePath, "build_commands"), "")
					}
					if err := validateStringNode(mappingValue(recipe, "app_id"), append(recipePath, "app_id"), true); err != nil {
						return err
					}
					if err := validateStringSequence(mappingValue(recipe, "setup_commands"), append(recipePath, "setup_commands")); err != nil {
						return err
					}
					if err := validateStringSequence(mappingValue(recipe, "build_commands"), append(recipePath, "build_commands")); err != nil {
						return err
					}
					for _, field := range []string{"output_path", "image"} {
						if err := validateStringNode(mappingValue(recipe, field), append(recipePath, field), true); err != nil {
							return err
						}
					}
					if err := validateStringMap(mappingValue(recipe, "env"), append(recipePath, "env")); err != nil {
						return err
					}
					if err := validateStringSequence(mappingValue(recipe, "secrets"), append(recipePath, "secrets")); err != nil {
						return err
					}
					if err := validateSecondsNode(mappingValue(recipe, "timeout_seconds"), append(recipePath, "timeout_seconds")); err != nil {
						return err
					}
					if err := validateCacheNodes(mappingValue(recipe, "caches"), append(recipePath, "caches")); err != nil {
						return err
					}
				}
			}
		}
	}

	return validateReviewKeys(mappingValue(root, "pr_review"))
}

func validateReviewKeys(review *yaml.Node) error {
	if review == nil || nodeIsNull(review) {
		return nil
	}
	path := []string{"pr_review"}
	if err := validateMappingNode(review, path, true); err != nil {
		return err
	}
	if err := validateAllowedKeys(review, path, "enabled", "review_triggers", "build", "proof_of_changes", "workflow_ids", "strict_ci_check"); err != nil {
		return err
	}
	if err := validateBooleanNode(mappingValue(review, "enabled"), append(path, "enabled")); err != nil {
		return err
	}
	if triggers := mappingValue(review, "review_triggers"); triggers != nil && !nodeIsNull(triggers) {
		if err := validateMappingNode(triggers, append(path, "review_triggers"), true); err != nil {
			return err
		}
		if err := validateAllowedKeys(triggers, append(path, "review_triggers"), "paths", "labels", "drafts"); err != nil {
			return err
		}
		if err := validateBooleanNode(mappingValue(triggers, "drafts"), append(path, "review_triggers", "drafts")); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(triggers, "paths"), append(path, "review_triggers", "paths")); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(triggers, "labels"), append(path, "review_triggers", "labels")); err != nil {
			return err
		}
	}
	build := mappingValue(review, "build")
	if build == nil {
		return newConfigError("contract", "missing_field", append(path, "build"), "")
	}
	if err := validateMappingNode(build, append(path, "build"), false); err != nil {
		return err
	}
	if build.Kind == yaml.MappingNode {
		kind := scalarValue(mappingValue(build, "kind"))
		unionPath := append(append([]string{}, path...), "build", kind)
		if err := validateAllowedKeys(build, unionPath, "kind", "profile", "app_ids"); err != nil {
			return err
		}
		if mappingValue(build, "kind") == nil {
			return newConfigError("contract", "missing_field", append(path, "build", "kind"), "")
		}
		if err := validateStringNode(mappingValue(build, "kind"), append(path, "build", "kind"), false); err != nil {
			return err
		}
		if err := validateStringNode(mappingValue(build, "profile"), append(unionPath, "profile"), true); err != nil {
			return err
		}
		if appIDs := mappingValue(build, "app_ids"); appIDs != nil && !nodeIsNull(appIDs) {
			if err := validateMappingNode(appIDs, append(unionPath, "app_ids"), true); err != nil {
				return err
			}
			if err := validateAllowedKeys(appIDs, append(unionPath, "app_ids"), "ios", "android"); err != nil {
				return err
			}
			for _, platform := range []string{"ios", "android"} {
				if err := validateStringNode(mappingValue(appIDs, platform), append(unionPath, "app_ids", platform), true); err != nil {
					return err
				}
			}
		}
	}
	if proof := mappingValue(review, "proof_of_changes"); proof != nil && !nodeIsNull(proof) {
		proofPath := append(path, "proof_of_changes")
		if err := validateMappingNode(proof, proofPath, true); err != nil {
			return err
		}
		if err := validateAllowedKeys(proof, proofPath, "enabled", "harness", "system_prompt", "always_verify"); err != nil {
			return err
		}
		if err := validateBooleanNode(mappingValue(proof, "enabled"), append(proofPath, "enabled")); err != nil {
			return err
		}
		if err := validateStringNode(mappingValue(proof, "system_prompt"), append(proofPath, "system_prompt"), true); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(proof, "always_verify"), append(proofPath, "always_verify")); err != nil {
			return err
		}
		if harness := mappingValue(proof, "harness"); harness != nil && !nodeIsNull(harness) {
			if err := validateMappingNode(harness, append(proofPath, "harness"), true); err != nil {
				return err
			}
			kind := scalarValue(mappingValue(harness, "kind"))
			if mappingValue(harness, "kind") == nil {
				return newConfigError("contract", "invalid_contract", append(proofPath, "harness"), "")
			}
			if err := validateAllowedKeys(harness, append(proofPath, "harness", kind), "kind", "model_id"); err != nil {
				return err
			}
			if err := validateStringNode(mappingValue(harness, "kind"), append(proofPath, "harness", "kind"), false); err != nil {
				return err
			}
			if err := validateStringNode(mappingValue(harness, "model_id"), append(proofPath, "harness", kind, "model_id"), true); err != nil {
				return err
			}
		}
	}
	if err := validateStringSequence(mappingValue(review, "workflow_ids"), append(path, "workflow_ids")); err != nil {
		return err
	}
	if strict := mappingValue(review, "strict_ci_check"); strict != nil && !nodeIsNull(strict) {
		if err := validateMappingNode(strict, append(path, "strict_ci_check"), true); err != nil {
			return err
		}
		if err := validateAllowedKeys(strict, append(path, "strict_ci_check"), "build"); err != nil {
			return err
		}
		if err := validateBooleanNode(mappingValue(strict, "build"), append(path, "strict_ci_check", "build")); err != nil {
			return err
		}
		if mappingValue(strict, "build") == nil {
			return newConfigError("contract", "missing_field", append(path, "strict_ci_check", "build"), "")
		}
	}
	return nil
}

func validateCacheNodes(caches *yaml.Node, path []string) error {
	if caches == nil {
		return nil
	}
	if err := validateSequenceNode(caches, path); err != nil {
		return err
	}
	for index, cache := range caches.Content {
		cachePath := append(path, strconv.Itoa(index))
		if err := validateMappingNode(cache, cachePath, false); err != nil {
			return err
		}
		if err := validateAllowedKeys(cache, cachePath, "key", "paths"); err != nil {
			return err
		}
		if mappingValue(cache, "key") == nil {
			return newConfigError("contract", "missing_field", append(cachePath, "key"), "")
		}
		if mappingValue(cache, "paths") == nil {
			return newConfigError("contract", "missing_field", append(cachePath, "paths"), "")
		}
		if err := validateStringNode(mappingValue(cache, "key"), append(cachePath, "key"), false); err != nil {
			return err
		}
		if err := validateStringSequence(mappingValue(cache, "paths"), append(cachePath, "paths")); err != nil {
			return err
		}
	}
	return nil
}

func validateAllowedKeys(node *yaml.Node, path []string, allowed ...string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		if _, ok := allowedSet[key]; !ok {
			return newConfigError("contract", "unknown_field", append(append([]string{}, path...), key), "")
		}
	}
	return nil
}

func validateSecondsNode(node *yaml.Node, path []string) error {
	if node == nil || nodeIsNull(node) {
		return nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return newConfigError("contract", "invalid_contract", path, "")
	}
	value, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil || value <= 0 || value > maxDatabaseInteger {
		return newConfigError("contract", "invalid_contract", path, "")
	}
	return nil
}

func validateBooleanNode(node *yaml.Node, path []string) error {
	if node != nil && (node.Kind != yaml.ScalarNode || node.Tag != "!!bool") {
		return newConfigError("contract", "invalid_contract", path, "")
	}
	return nil
}

func validateMappingNode(node *yaml.Node, path []string, nullable bool) error {
	if node == nil || (nullable && nodeIsNull(node)) {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return newConfigError("contract", "invalid_contract", path, "")
	}
	return nil
}

func validateSequenceNode(node *yaml.Node, path []string) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return newConfigError("contract", "invalid_contract", path, "")
	}
	return nil
}

func validateStringNode(node *yaml.Node, path []string, nullable bool) error {
	if node == nil || (nullable && nodeIsNull(node)) {
		return nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return newConfigError("contract", "invalid_contract", path, "")
	}
	return nil
}

func validateEnumStringNode(node *yaml.Node, path []string, allowed ...string) error {
	if err := validateStringNode(node, path, false); err != nil {
		return err
	}
	for _, value := range allowed {
		if node.Value == value {
			return nil
		}
	}
	return newConfigError("contract", "invalid_contract", path, "")
}

func validateStringSequence(node *yaml.Node, path []string) error {
	if node == nil {
		return nil
	}
	if err := validateSequenceNode(node, path); err != nil {
		return err
	}
	for index, item := range node.Content {
		if err := validateStringNode(item, append(path, strconv.Itoa(index)), false); err != nil {
			return err
		}
	}
	return nil
}

func validateStringMap(node *yaml.Node, path []string) error {
	if node == nil {
		return nil
	}
	if err := validateMappingNode(node, path, false); err != nil {
		return err
	}
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index].Value
		if err := validateStringNode(node.Content[index+1], append(path, key), false); err != nil {
			return err
		}
	}
	return nil
}

func nodeIsNull(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!null"
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

// ClassifyConfigDocument makes legacy entry explicit; canonical runtime parsing never translates it.
func ClassifyConfigDocument(document map[string]any) (string, error) {
	build := stringMap(document["build"])
	project := stringMap(document["project"])
	review := stringMap(document["pr_review"])
	canonical := hasKey(document, "session") || hasAnyKey(build, "framework", "profiles")
	if nested := stringMap(review["build"]); len(nested) > 0 {
		canonical = true
	}
	legacy := hasAnyKey(document, "defaults", "hotreload", "before_session", "auth_bypass", "last_synced_at", "tests", "workflows") ||
		hasAnyKey(build, "system", "root", "command", "output", "platforms", "source", "no_build") ||
		hasAnyKey(project, "name", "org_id") ||
		hasAnyKey(review, "preset", "skip_drafts", "path_filters", "label_filters", "actions", "builds")
	if canonical && legacy {
		return "", newConfigError("classification", "mixed_config_formats", nil, "")
	}
	if legacy {
		return "legacy", nil
	}
	return "canonical", nil
}

// ParseAuthoredConfig is the only parser intended for canonical runtime consumers.
func ParseAuthoredConfig(data []byte) (*AuthoredConfig, error) {
	_, document, err := loadYAMLDocument(data)
	if err != nil {
		return nil, err
	}
	kind, err := ClassifyConfigDocument(document)
	if err != nil {
		return nil, err
	}
	if kind != "canonical" {
		return nil, newConfigError("classification", "legacy_config_requires_migration", nil, "")
	}
	return decodeAuthoredConfig(data)
}

func decodeAuthoredConfig(data []byte) (*AuthoredConfig, error) {
	root, _, err := loadYAMLDocument(data)
	if err != nil {
		return nil, err
	}
	if err := validateContractKeys(root); err != nil {
		return nil, err
	}
	normalizedData, err := yaml.Marshal(root)
	if err != nil {
		return nil, newConfigError("yaml_syntax", "invalid_yaml", nil, "")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(normalizedData))
	decoder.KnownFields(true)
	var authored AuthoredConfig
	if err := decoder.Decode(&authored); err != nil {
		code := "invalid_contract"
		path := []string{}
		if strings.Contains(err.Error(), "field ") && strings.Contains(err.Error(), "not found") {
			code = "unknown_field"
			if match := unknownYAMLField.FindStringSubmatch(err.Error()); len(match) == 2 {
				path = []string{match[1]}
			}
		}
		return nil, newConfigError("contract", code, path, "")
	}
	if err := canonicalizeAuthoredConfigUUIDs(&authored); err != nil {
		return nil, err
	}
	if err := authored.ValidateContract(); err != nil {
		var configError *ConfigError
		if errors.As(err, &configError) {
			return nil, configError
		}
		code := "invalid_contract"
		path := []string{}
		if strings.Contains(err.Error(), "is required") {
			code = "missing_field"
			if match := missingRecipeBuildCommands.FindStringSubmatch(err.Error()); len(match) == 3 {
				path = []string{"build", "profiles", match[1], match[2], "build_commands"}
			}
		} else if profileName, found := strings.CutPrefix(err.Error(), "build.profiles."); found {
			if profileName, found = strings.CutSuffix(profileName, " must declare a platform recipe"); found {
				path = []string{"build", "profiles", profileName}
			}
		}
		return nil, newConfigError("contract", code, path, "")
	}
	return &authored, nil
}

func canonicalizeAuthoredConfigUUIDs(authored *AuthoredConfig) error {
	canonical, err := canonicalUUID(authored.Project.ID, []string{"project", "id"})
	if err != nil {
		return err
	}
	authored.Project.ID = canonical
	if authored.Build != nil {
		profileNames := sortedMapKeys(authored.Build.Profiles)
		for _, profileName := range profileNames {
			profile := authored.Build.Profiles[profileName]
			for _, platform := range []string{"ios", "android"} {
				var recipe *AuthoredBuildRecipe
				if platform == "ios" {
					recipe = profile.IOS
				} else {
					recipe = profile.Android
				}
				if recipe == nil || recipe.AppID == nil {
					continue
				}
				value, parseErr := canonicalUUID(*recipe.AppID, []string{"build", "profiles", profileName, platform, "app_id"})
				if parseErr != nil {
					return parseErr
				}
				recipe.AppID = &value
			}
		}
	}
	if authored.PRReview == nil {
		return nil
	}
	for index, workflowID := range authored.PRReview.WorkflowIDs {
		value, parseErr := canonicalUUID(workflowID, []string{"pr_review", "workflow_ids", strconv.Itoa(index)})
		if parseErr != nil {
			return parseErr
		}
		authored.PRReview.WorkflowIDs[index] = value
	}
	if authored.PRReview.Build.AppIDs != nil {
		for _, candidate := range []struct {
			platform string
			value    **string
		}{
			{platform: "ios", value: &authored.PRReview.Build.AppIDs.IOS},
			{platform: "android", value: &authored.PRReview.Build.AppIDs.Android},
		} {
			if *candidate.value == nil {
				continue
			}
			value, parseErr := canonicalUUID(**candidate.value, []string{"pr_review", "build", "ci_upload_to_revyl", "app_ids", candidate.platform})
			if parseErr != nil {
				return parseErr
			}
			*candidate.value = &value
		}
	}
	return nil
}

func canonicalUUID(value string, path []string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", newConfigError("contract", "invalid_contract", path, "")
	}
	return parsed.String(), nil
}

// CompileConfigBytes compiles only the canonical authored contract. Legacy
// translation belongs to explicit migration entrypoints such as
// MigrateLegacyConfigBytes and is never a runtime compiler option.
func CompileConfigBytes(data []byte, context CompilationContext) (*NormalizedProjectAggregate, error) {
	authored, err := ParseAuthoredConfig(data)
	if err != nil {
		return nil, err
	}
	return NormalizeAuthoredConfig(*authored, context)
}

type ResolvedProfilePlatform struct {
	Profile  string `json:"profile"`
	Platform string `json:"platform"`
}

type SelectionAmbiguity struct {
	RequiredFlag string   `json:"required_flag"`
	Choices      []string `json:"choices"`
}

type ProfilePlatformSelection struct {
	Resolved  *ResolvedProfilePlatform `json:"resolved,omitempty"`
	Ambiguity *SelectionAmbiguity      `json:"ambiguity,omitempty"`
}

// PublicationValidationContext contains authenticated organization facts.
type PublicationValidationContext struct {
	ActiveAppIDs        map[string]struct{}
	ActiveWorkflowIDs   map[string]struct{}
	AvailableSecretRefs map[string]struct{}
	AvailableLaunchVars map[string]struct{}
}

// ResolveProfilePlatform applies explicit, development-like, and sole-choice ordering.
func ResolveProfilePlatform(aggregate NormalizedProjectAggregate, explicitProfile, explicitPlatform string, developmentCommand bool) (ProfilePlatformSelection, error) {
	if len(aggregate.Profiles) == 0 {
		return ProfilePlatformSelection{}, newConfigError("selection", "no_build_profiles", []string{"build", "profiles"}, "")
	}
	if explicitProfile != "" {
		profileExists := false
		for _, profile := range aggregate.Profiles {
			if profile.Name == explicitProfile {
				profileExists = true
				break
			}
		}
		if !profileExists {
			return ProfilePlatformSelection{}, newConfigError("selection", "unknown_or_ineligible_profile", []string{"build", "profiles", explicitProfile}, "")
		}
	}

	eligible := map[string][]string{}
	for _, profile := range aggregate.Profiles {
		platforms := []string{}
		for _, configuration := range profile.Configurations {
			if explicitPlatform == "" || configuration.Platform == explicitPlatform {
				platforms = append(platforms, configuration.Platform)
			}
		}
		if len(platforms) > 0 {
			eligible[profile.Name] = platforms
		}
	}
	if explicitProfile != "" && explicitPlatform != "" {
		if _, profileEligible := eligible[explicitProfile]; !profileEligible {
			return ProfilePlatformSelection{}, newConfigError(
				"selection",
				"profile_platform_not_configured",
				[]string{"build", "profiles", explicitProfile, explicitPlatform},
				"",
			)
		}
	}
	if len(eligible) == 0 && explicitPlatform != "" {
		return ProfilePlatformSelection{}, newConfigError(
			"selection",
			"no_build_profile_for_platform",
			[]string{"build", "profiles", explicitPlatform},
			"",
		)
	}
	profileName := explicitProfile
	if profileName == "" {
		candidates := sortedMapKeys(eligible)
		if developmentCommand {
			development := []string{}
			for _, candidate := range candidates {
				if developmentProfileToken.MatchString(candidate) {
					development = append(development, candidate)
				}
			}
			if len(development) == 1 {
				candidates = development
			}
		}
		if len(candidates) != 1 {
			return ProfilePlatformSelection{Ambiguity: &SelectionAmbiguity{RequiredFlag: "--profile", Choices: candidates}}, nil
		}
		profileName = candidates[0]
	}
	platforms := eligible[profileName]
	if explicitPlatform != "" {
		return ProfilePlatformSelection{Resolved: &ResolvedProfilePlatform{Profile: profileName, Platform: explicitPlatform}}, nil
	}
	if len(platforms) != 1 {
		return ProfilePlatformSelection{Ambiguity: &SelectionAmbiguity{RequiredFlag: "--platform", Choices: platforms}}, nil
	}
	return ProfilePlatformSelection{Resolved: &ResolvedProfilePlatform{Profile: profileName, Platform: platforms[0]}}, nil
}

// ValidateExecutionRecipe applies runnability only after selection.
func ValidateExecutionRecipe(recipe EffectiveBuildRecipe) error {
	if err := recipe.ValidateContract(); err != nil {
		return newConfigError("validation", "recipe_not_runnable", []string{"build_commands"}, "")
	}
	return nil
}

// ValidatePublication validates server-owned references supplied by an authenticated caller.
func ValidatePublication(aggregate NormalizedProjectAggregate, context PublicationValidationContext) error {
	managedProfile := ""
	if aggregate.ReviewPolicy != nil && aggregate.ReviewPolicy.Build.Kind == "revyl" && aggregate.ReviewPolicy.Build.Profile != nil {
		managedProfile = *aggregate.ReviewPolicy.Build.Profile
	}
	for _, profile := range aggregate.Profiles {
		for _, configuration := range profile.Configurations {
			path := []string{"build", "profiles", profile.Name, configuration.Platform}
			if configuration.AppID == nil {
				if profile.Name == managedProfile {
					return newConfigError("validation", "published_recipe_app_id_required", append(path, "app_id"), "")
				}
			} else if _, exists := context.ActiveAppIDs[*configuration.AppID]; !exists {
				return newConfigError("validation", "app_not_active_for_organization", append(path, "app_id"), "")
			}
			for _, secret := range configuration.Recipe.SecretRefs {
				if _, exists := context.AvailableSecretRefs[secret]; !exists {
					return newConfigError("validation", "secret_not_available_for_organization", append(path, "secrets"), "")
				}
			}
		}
	}
	if aggregate.ReviewPolicy != nil {
		for _, workflowID := range aggregate.ReviewPolicy.WorkflowIDs {
			if _, exists := context.ActiveWorkflowIDs[workflowID]; !exists {
				return newConfigError("validation", "workflow_not_active_for_organization", []string{"pr_review", "workflow_ids"}, "")
			}
		}
		if aggregate.ReviewPolicy.Build.AppIDs != nil {
			for platform, appID := range map[string]*string{"ios": aggregate.ReviewPolicy.Build.AppIDs.IOS, "android": aggregate.ReviewPolicy.Build.AppIDs.Android} {
				if appID != nil {
					if _, exists := context.ActiveAppIDs[*appID]; !exists {
						return newConfigError("validation", "app_not_active_for_organization", []string{"pr_review", "build", "app_ids", platform}, "")
					}
				}
			}
		}
	}
	if aggregate.Session.AuthBypass != nil {
		for _, launchVar := range aggregate.Session.AuthBypass.LaunchVars {
			if _, exists := context.AvailableLaunchVars[launchVar]; !exists {
				return newConfigError("validation", "launch_var_not_available_for_organization", []string{"session", "auth_bypass", "launch_vars"}, "")
			}
		}
	}
	return nil
}

// TranslateLegacyConfig is isolated migration logic, never a runtime fallback.
func TranslateLegacyConfig(document map[string]any) (map[string]any, error) {
	project := stringMap(document["project"])
	projectID, err := requireLegacyUUID(project["id"], []string{"project", "id"})
	if err != nil {
		return nil, err
	}
	result := map[string]any{"project": map[string]any{"id": projectID}}
	session := map[string]any{}
	defaults := stringMap(document["defaults"])
	timeout := DefaultTimeoutSeconds
	if rawTimeout, exists := defaults["timeout"]; exists && rawTimeout != nil {
		if parsed, ok := integerValue(rawTimeout); !ok {
			session["idle_timeout_seconds"] = rawTimeout
		} else if parsed > 0 {
			timeout = parsed
		}
	}
	session["idle_timeout_seconds"] = timeout
	if rawBefore, exists := document["before_session"]; exists {
		if before := stringMap(rawBefore); before != nil {
			if script, scriptExists := before["script"]; scriptExists {
				before["script_path"] = script
				delete(before, "script")
			}
			if rawTimeout, timeoutExists := before["timeout_seconds"]; !timeoutExists || rawTimeout == nil {
				before["timeout_seconds"] = DefaultBeforeSessionTimeoutSeconds
			} else if parsed, ok := integerValue(rawTimeout); ok && parsed <= 0 {
				before["timeout_seconds"] = DefaultBeforeSessionTimeoutSeconds
			}
			session["before_script"] = before
		}
	}
	if rawBypass, exists := document["auth_bypass"]; exists {
		if bypass := stringMap(rawBypass); bypass != nil {
			session["auth_bypass"] = bypass
		}
	}
	if len(session) > 0 {
		result["session"] = session
	}

	legacyBuild := stringMap(document["build"])
	profiles := map[string]any{}
	platforms := stringMap(legacyBuild["platforms"])
	for _, key := range sortedMapKeys(platforms) {
		entry := stringMap(platforms[key])
		if len(entry) == 0 {
			continue
		}
		lowered := strings.ToLower(key)
		matches := []string{}
		for _, platform := range []string{"ios", "android"} {
			if strings.Contains(lowered, platform) {
				matches = append(matches, platform)
			}
		}
		if len(matches) != 1 {
			return nil, newConfigError("legacy_translation", "legacy_platform_ambiguous", []string{"build", "platforms", key}, "")
		}
		platform := matches[0]
		name := legacyProfileName(key, platform)
		profile := stringMap(profiles[name])
		if profile == nil {
			profile = map[string]any{}
		}
		if _, exists := profile[platform]; exists {
			return nil, newConfigError("legacy_translation", "legacy_profile_ambiguous", []string{"build", "platforms"}, "")
		}
		profile[platform] = translateLegacyRecipe(entry, nil, nil)
		profiles[name] = profile
	}
	if len(profiles) == 0 && (hasKey(legacyBuild, "command") || hasKey(legacyBuild, "output")) {
		platform := legacyPlatformHint(legacyBuild["system"])
		if platform == "" {
			return nil, newConfigError("legacy_translation", "legacy_platform_ambiguous", []string{"build", "command"}, "")
		}
		profiles["development"] = map[string]any{
			platform: translateLegacyRecipe(map[string]any{}, legacyBuild["command"], legacyBuild["output"]),
		}
	}
	if len(legacyBuild) > 0 || len(profiles) > 0 {
		frameworkValue := stringValue(legacyBuild["system"])
		if frameworkValue == "" && len(profiles) > 0 {
			unique := map[string]struct{}{}
			for _, rawProfile := range profiles {
				for platform := range stringMap(rawProfile) {
					unique[platform] = struct{}{}
				}
			}
			if len(unique) == 1 {
				frameworkValue = sortedMapKeys(unique)[0]
			}
		}
		framework, frameworkErr := translateLegacyFramework(frameworkValue)
		if frameworkErr != nil {
			return nil, frameworkErr
		}
		build := map[string]any{"framework": framework, "profiles": profiles}
		if caches, exists := legacyBuild["caches"]; exists {
			build["caches"] = caches
		}
		result["build"] = build
	}

	legacyReview, reviewPresent := document["pr_review"]
	if reviewPresent && stringMap(legacyReview) != nil {
		legacyReview := stringMap(legacyReview)
		preset := stringValue(legacyReview["preset"])
		if preset == "smoke_every_pr" {
			return nil, newConfigError("legacy_translation", "legacy_server_lookup_required", []string{"pr_review", "preset"}, "")
		}
		reviewEnabled, boolErr := legacyBool(legacyReview, "enabled", true, []string{"pr_review", "enabled"})
		if boolErr != nil {
			return nil, boolErr
		}
		skipDrafts, boolErr := legacyBool(legacyReview, "skip_drafts", true, []string{"pr_review", "skip_drafts"})
		if boolErr != nil {
			return nil, boolErr
		}
		review := map[string]any{
			"enabled": reviewEnabled,
			"review_triggers": map[string]any{
				"paths": listDefault(legacyReview["path_filters"]), "labels": normalizeLegacyLabelFilters(listDefault(legacyReview["label_filters"])),
				"drafts": !skipDrafts,
			},
		}
		actions := stringMap(legacyReview["actions"])
		if len(actions) > 0 || preset == "adaptive_report" {
			proofEnabled := preset == "adaptive_report"
			if _, exists := actions["proof_of_changes"]; exists {
				proofEnabled, boolErr = legacyBool(actions, "proof_of_changes", false, []string{"pr_review", "actions", "proof_of_changes"})
				if boolErr != nil {
					return nil, boolErr
				}
			}
			proof := map[string]any{
				"enabled": proofEnabled, "always_verify": listDefault(actions["checks"]),
			}
			if prompt := stringValue(actions["system_prompt"]); prompt != "" {
				proof["system_prompt"] = prompt
			}
			if harness, exists := actions["proof_harness"]; exists {
				proof["harness"] = harness
			}
			review["proof_of_changes"] = proof
			review["workflow_ids"] = listDefault(actions["workflows"])
		}
		strictBuildChecks, boolErr := legacyBool(actions, "strict_build_checks", true, []string{"pr_review", "actions", "strict_build_checks"})
		if boolErr != nil {
			return nil, boolErr
		}
		review["strict_ci_check"] = map[string]any{"build": strictBuildChecks}
		builds := stringMap(legacyReview["builds"])
		enabled := map[string]map[string]any{}
		for _, platform := range []string{"ios", "android"} {
			rawEntry, exists := builds[platform]
			if !exists {
				continue
			}
			entry := stringMap(rawEntry)
			if entry == nil {
				continue
			}
			entryEnabled, enabledErr := legacyBool(entry, "enabled", true, []string{"pr_review", "builds", platform, "enabled"})
			if enabledErr != nil {
				return nil, enabledErr
			}
			if entryEnabled {
				enabled[platform] = entry
			}
		}
		if len(enabled) > 0 {
			ciMode := false
			modeSet := false
			for _, platform := range []string{"ios", "android"} {
				entry, exists := enabled[platform]
				if !exists {
					continue
				}
				candidate, modeErr := legacyBool(entry, "use_existing_ci", false, []string{"pr_review", "builds", platform, "use_existing_ci"})
				if modeErr != nil {
					return nil, modeErr
				}
				if modeSet && ciMode != candidate {
					return nil, newConfigError("legacy_translation", "legacy_review_mode_ambiguous", []string{"pr_review", "builds"}, "")
				}
				ciMode, modeSet = candidate, true
			}
			if ciMode {
				appIDs := map[string]any{}
				for platform, entry := range enabled {
					appID, appErr := requireLegacyUUID(entry["app"], []string{"pr_review", "builds", platform, "app"})
					if appErr != nil {
						return nil, appErr
					}
					appIDs[platform] = appID
				}
				review["build"] = map[string]any{"kind": "ci_upload_to_revyl", "app_ids": appIDs}
			} else {
				reviewProfile := map[string]any{}
				frameworks := map[string]struct{}{}
				for platform, entry := range enabled {
					appID, appErr := requireLegacyUUID(entry["app"], []string{"pr_review", "builds", platform, "app"})
					if appErr != nil {
						return nil, appErr
					}
					legacyFramework := firstNonEmpty(stringValue(entry["framework"]), "expo_"+platform)
					frameworkDefaults := legacyReviewBuildDefaultsByFramework[strings.ToLower(legacyFramework)]
					buildCommand := stringValue(entry["build_command"])
					if strings.TrimSpace(buildCommand) == "" {
						buildCommand = frameworkDefaults.buildCommand
					}
					commands := []any{}
					for _, command := range strings.Split(buildCommand, "\n") {
						if command = strings.TrimSpace(command); command != "" {
							commands = append(commands, command)
						}
					}
					recipe := map[string]any{"app_id": appID, "build_commands": commands}
					copyPresent(entry, recipe, map[string]string{"image": "image", "secrets": "secrets", "caches": "caches"})
					artifactPath := stringValue(entry["artifact_path"])
					if strings.TrimSpace(artifactPath) == "" {
						artifactPath = frameworkDefaults.outputPath
					}
					if artifactPath != "" {
						recipe["output_path"] = artifactPath
					}
					if legacyEnv, exists := entry["env"]; exists {
						switch value := legacyEnv.(type) {
						case map[string]any:
							recipe["env"] = value
						case []any:
							secrets := listDefault(recipe["secrets"])
							recipe["secrets"] = orderedUniqueAnyStrings(secrets, value)
						default:
							recipe["env"] = legacyEnv
						}
					}
					reviewProfile[platform] = recipe
					framework, frameworkErr := translateLegacyFramework(legacyFramework)
					if frameworkErr != nil {
						return nil, frameworkErr
					}
					frameworks[framework] = struct{}{}
				}
				reviewFramework := ""
				if len(frameworks) == 1 {
					reviewFramework = sortedMapKeys(frameworks)[0]
				} else {
					reviewFramework = inferLegacyReviewFramework(enabled)
					if reviewFramework == "" {
						return nil, newConfigError("legacy_translation", "legacy_framework_ambiguous", []string{"pr_review", "builds"}, "")
					}
				}
				build := stringMap(result["build"])
				if build == nil {
					build = map[string]any{"framework": reviewFramework, "profiles": map[string]any{}}
					result["build"] = build
				} else if stringValue(build["framework"]) != reviewFramework {
					return nil, newConfigError("legacy_translation", "legacy_framework_ambiguous", []string{"pr_review", "builds"}, "")
				}
				profileMap := stringMap(build["profiles"])
				if existing, exists := profileMap["pr-review"]; exists && !reflect.DeepEqual(existing, reviewProfile) {
					return nil, newConfigError("legacy_translation", "legacy_review_profile_collision", []string{"pr_review", "builds"}, "")
				}
				profileMap["pr-review"] = reviewProfile
				build["profiles"] = profileMap
				review["build"] = map[string]any{"kind": "revyl", "profile": "pr-review"}
			}
		} else {
			build := stringMap(result["build"])
			profiles := stringMap(build["profiles"])
			names := sortedMapKeys(profiles)
			if len(names) == 0 {
				return nil, newConfigError("legacy_translation", "legacy_review_build_missing", []string{"pr_review", "builds"}, "")
			}
			if len(names) != 1 {
				return nil, newConfigError("legacy_translation", "legacy_review_profile_ambiguous", []string{"pr_review", "builds"}, "")
			}
			review["build"] = map[string]any{"kind": "revyl", "profile": names[0]}
		}
		result["pr_review"] = review
	}
	return result, nil
}

func translateLegacyRecipe(entry map[string]any, fallbackCommand, fallbackOutput any) map[string]any {
	rawCommands, commandsExist := entry["commands"]
	commands, commandsAreList := rawCommands.([]any)
	var translatedCommands any
	if commandsExist && !commandsAreList {
		translatedCommands = rawCommands
	} else if len(commands) == 0 {
		command := stringValue(entry["command"])
		if command == "" {
			command = stringValue(fallbackCommand)
		}
		if command != "" {
			commands = []any{command}
		}
		translatedCommands = commands
	} else {
		translatedCommands = commands
	}
	translatedCommands = materializeLegacyScheme(translatedCommands, stringValue(entry["scheme"]))
	result := map[string]any{"build_commands": translatedCommands}
	copyPresentIncludingEmpty(entry, result, map[string]string{
		"app_id": "app_id", "output": "output_path", "image": "image",
		"env": "env", "secrets": "secrets", "caches": "caches",
	})
	if timeout, exists := entry["timeout"]; exists && timeout != nil {
		if parsed, ok := integerValue(timeout); !ok || parsed != 0 {
			result["timeout_seconds"] = timeout
		}
	}
	if _, exists := entry["output"]; !exists && fallbackOutput != nil {
		result["output_path"] = fallbackOutput
	}
	if setup := stringValue(entry["setup"]); setup != "" {
		result["setup_commands"] = []any{setup}
	}
	return result
}

func materializeLegacyScheme(commands any, scheme string) any {
	if scheme == "" {
		return commands
	}
	values, ok := commands.([]any)
	if !ok {
		return commands
	}
	escaped := strings.ReplaceAll(scheme, "'", "'\\''")
	for index, value := range values {
		command, ok := value.(string)
		if !ok || !strings.Contains(command, "-scheme *") {
			continue
		}
		values[index] = strings.Replace(command, "-scheme *", "-scheme '"+escaped+"'", 1)
	}
	return values
}

func translateLegacyFramework(value string) (string, error) {
	frameworks := map[string]string{
		"ios": "ios", "xcode": "ios", "swift": "ios", "android": "android", "gradle": "android",
		"react-native": "react_native", "react_native": "react_native", "expo": "expo", "flutter": "flutter",
		"expo_ios": "expo", "expo_android": "expo", "react_native_ios": "react_native", "react_native_android": "react_native",
		"native_ios": "ios", "native_android": "android", "react native": "react_native", "gradle (android)": "android",
	}
	if framework, exists := frameworks[strings.ToLower(value)]; exists {
		return framework, nil
	}
	return "", newConfigError("legacy_translation", "legacy_framework_ambiguous", []string{"build", "system"}, "")
}

func inferLegacyReviewFramework(builds map[string]map[string]any) string {
	commands := make([]string, 0, len(builds))
	for _, platform := range []string{"ios", "android"} {
		entry, exists := builds[platform]
		if !exists {
			continue
		}
		commands = append(commands, stringValue(entry["build_command"]))
	}
	return inferLegacyFrameworkFromCommands(commands)
}

func inferLegacyBuildFramework(build map[string]any) string {
	commands := []string{stringValue(build["command"])}
	for _, key := range sortedMapKeys(stringMap(build["platforms"])) {
		entry := stringMap(stringMap(build["platforms"])[key])
		commands = append(commands, stringValue(entry["command"]))
		if values, ok := entry["commands"].([]any); ok {
			for _, value := range values {
				commands = append(commands, stringValue(value))
			}
		}
	}
	return inferLegacyFrameworkFromCommands(commands)
}

func inferLegacyFrameworkFromCommands(commands []string) string {
	frameworks := map[string]struct{}{}
	for _, command := range commands {
		lowered := strings.ToLower(command)
		switch {
		case strings.Contains(lowered, "npx expo"),
			strings.Contains(lowered, "bunx expo"),
			strings.Contains(lowered, "eas-cli"),
			strings.Contains(lowered, "npx eas "),
			strings.Contains(lowered, " expo prebuild"):
			frameworks["expo"] = struct{}{}
		case strings.Contains(lowered, "flutter build"):
			frameworks["flutter"] = struct{}{}
		case strings.Contains(lowered, "react-native "):
			frameworks["react_native"] = struct{}{}
		}
	}
	if len(frameworks) != 1 {
		return ""
	}
	return sortedMapKeys(frameworks)[0]
}

func legacyPlatformHint(value any) string {
	switch strings.ToLower(strings.TrimSpace(stringValue(value))) {
	case "ios", "xcode", "swift", "native_ios", "expo_ios", "react_native_ios":
		return "ios"
	case "android", "gradle", "gradle (android)", "native_android", "expo_android", "react_native_android":
		return "android"
	default:
		return ""
	}
}

func legacyMapping(values map[string]any, key string, path []string) (map[string]any, error) {
	value, exists := values[key]
	if !exists || value == nil {
		return nil, nil
	}
	mapping, ok := value.(map[string]any)
	if !ok {
		return nil, newConfigError("legacy_translation", "legacy_container_invalid", path, "")
	}
	return mapping, nil
}

func legacyProfileName(key, platform string) string {
	tokens := regexp.MustCompile(`[^A-Za-z0-9]+`).Split(key, -1)
	remaining := []string{}
	for _, token := range tokens {
		if token != "" && !strings.EqualFold(token, platform) {
			remaining = append(remaining, token)
		}
	}
	if len(remaining) == 0 {
		return "development"
	}
	return strings.Join(remaining, "-")
}

func requireLegacyUUID(value any, path []string) (string, error) {
	parsed, err := uuid.Parse(stringValue(value))
	if err != nil {
		return "", newConfigError("legacy_translation", "legacy_server_lookup_required", path, "")
	}
	return parsed.String(), nil
}

func validateLegacyProjectRootEvidence(document map[string]any, context CompilationContext) error {
	selectedRoot, err := normalizeRelativeDirectory(context.RepositoryRelativeProjectRoot, "repository_relative_project_root")
	if err != nil {
		return err
	}
	add := func(value any, path []string) error {
		if value == nil || value == "" {
			return nil
		}
		raw, ok := value.(string)
		if !ok {
			return newConfigError("legacy_translation", "legacy_project_root_invalid", path, "")
		}
		if raw != strings.TrimSpace(raw) || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, "\\\x00\r\n") {
			return newConfigError("legacy_translation", "legacy_project_root_invalid", path, "")
		}
		normalized, err := normalizeRelativeDirectory(pathpkg.Clean(raw), strings.Join(path, "."))
		if err != nil {
			return newConfigError("legacy_translation", "legacy_project_root_invalid", path, "")
		}
		if normalized == "." {
			normalized = selectedRoot
		}
		if normalized != selectedRoot {
			return newConfigError("legacy_translation", "legacy_project_root_mismatch", path, "")
		}
		return nil
	}
	build := stringMap(document["build"])
	if err := add(build["root"], []string{"build", "root"}); err != nil {
		return err
	}
	// build.source is deliberately retired. Its subdir is not project identity
	// evidence and cannot move the selected config/project root during migration.
	review := stringMap(document["pr_review"])
	if actions := stringMap(review["actions"]); actions != nil {
		if err := add(actions["project_root"], []string{"pr_review", "actions", "project_root"}); err != nil {
			return err
		}
	}
	if builds := stringMap(review["builds"]); builds != nil {
		for _, platform := range []string{"ios", "android"} {
			if entry := stringMap(builds[platform]); entry != nil {
				if err := add(entry["root_dir"], []string{"pr_review", "builds", platform, "root_dir"}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ResolveEffectiveDirectory applies -C before any config lookup or hook.
func ResolveEffectiveDirectory(cwd, changeDirectory string) (string, error) {
	candidate := cwd
	if changeDirectory != "" {
		candidate = changeDirectory
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, candidate)
		}
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", newConfigError("read", "effective_directory_unavailable", nil, "")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", newConfigError("read", "effective_directory_unavailable", nil, "")
	}
	metadata, err := os.Stat(resolved)
	if err != nil || !metadata.IsDir() {
		return "", newConfigError("read", "effective_directory_not_directory", nil, "")
	}
	return resolved, nil
}

// DiscoverConfigPath returns the nearest config without crossing worktreeRoot.
func DiscoverConfigPath(effectiveDirectory, worktreeRoot string) (string, error) {
	effective, err := filepath.EvalSymlinks(effectiveDirectory)
	if err != nil {
		return "", newConfigError("read", "effective_directory_unavailable", nil, "")
	}
	root, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return "", newConfigError("read", "worktree_root_unavailable", nil, "")
	}
	relative, err := filepath.Rel(root, effective)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", newConfigError("read", "effective_directory_outside_worktree", nil, "")
	}
	current := effective
	for {
		configDirectory := filepath.Join(current, ".revyl")
		directoryMetadata, directoryErr := os.Lstat(configDirectory)
		if directoryErr == nil && (!directoryMetadata.IsDir() || directoryMetadata.Mode()&os.ModeSymlink != 0) {
			return "", newConfigError("read", "config_not_regular_file", nil, "")
		}
		if directoryErr != nil && !errors.Is(directoryErr, os.ErrNotExist) {
			return "", newConfigError("read", "config_read_failed", nil, "")
		}
		candidate := filepath.Join(configDirectory, "config.yaml")
		metadata, statErr := os.Lstat(candidate)
		if statErr == nil {
			if !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 {
				return "", newConfigError("read", "config_not_regular_file", nil, "")
			}
			if metadata.Size() > MaxConfigBytes {
				return "", newConfigError("read", "config_too_large", nil, "")
			}
			return candidate, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", newConfigError("read", "config_read_failed", nil, "")
		}
		if current == root {
			break
		}
		current = filepath.Dir(current)
	}
	return "", newConfigError("read", "config_not_found", nil, "")
}

func ReadConfigFile(configPath string) ([]byte, error) {
	directoryPath := filepath.Dir(configPath)
	directoryMetadata, err := os.Lstat(directoryPath)
	if err != nil {
		return nil, newConfigError("read", "config_read_failed", nil, "")
	}
	if !directoryMetadata.IsDir() || directoryMetadata.Mode()&os.ModeSymlink != 0 {
		return nil, newConfigError("read", "config_not_regular_file", nil, "")
	}
	directory, err := os.OpenRoot(directoryPath)
	if err != nil {
		return nil, newConfigError("read", "config_read_failed", nil, "")
	}
	defer directory.Close()
	openedDirectoryMetadata, err := directory.Stat(".")
	if err != nil || !os.SameFile(directoryMetadata, openedDirectoryMetadata) {
		return nil, newConfigError("read", "config_not_regular_file", nil, "")
	}
	name := filepath.Base(configPath)
	metadata, err := directory.Lstat(name)
	if err != nil {
		return nil, newConfigError("read", "config_read_failed", nil, "")
	}
	if !metadata.Mode().IsRegular() || metadata.Mode()&os.ModeSymlink != 0 {
		return nil, newConfigError("read", "config_not_regular_file", nil, "")
	}
	file, err := directory.Open(name)
	if err != nil {
		return nil, newConfigError("read", "config_read_failed", nil, "")
	}
	defer file.Close()
	openedMetadata, err := file.Stat()
	if err != nil {
		return nil, newConfigError("read", "config_read_failed", nil, "")
	}
	if !openedMetadata.Mode().IsRegular() || !os.SameFile(metadata, openedMetadata) {
		return nil, newConfigError("read", "config_not_regular_file", nil, "")
	}
	if openedMetadata.Size() > MaxConfigBytes {
		return nil, newConfigError("read", "config_too_large", nil, "")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxConfigBytes+1))
	if err != nil {
		return nil, newConfigError("read", "config_read_failed", nil, "")
	}
	if len(data) > MaxConfigBytes {
		return nil, newConfigError("read", "config_too_large", nil, "")
	}
	return data, nil
}

func stringMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return nil
}

func hasKey(values map[string]any, key string) bool {
	_, exists := values[key]
	return exists
}

func hasAnyKey(values map[string]any, keys ...string) bool {
	for _, key := range keys {
		if hasKey(values, key) {
			return true
		}
	}
	return false
}

func sortedMapKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func stringValue(value any) string {
	if result, ok := value.(string); ok {
		return result
	}
	return ""
}

func integerValue(value any) (int, bool) {
	result, ok := value.(int)
	return result, ok
}

func legacyBool(values map[string]any, key string, fallback bool, path []string) (bool, error) {
	value, exists := values[key]
	if !exists {
		return fallback, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, newConfigError("legacy_translation", "legacy_boolean_invalid", path, "")
	}
	return result, nil
}

func listDefault(value any) []any {
	if result, ok := value.([]any); ok {
		return result
	}
	return []any{}
}

func copyPresent(source, target map[string]any, mapping map[string]string) {
	for old, next := range mapping {
		value, exists := source[old]
		if !exists || value == nil || value == "" {
			continue
		}
		target[next] = value
	}
}

func copyPresentIncludingEmpty(source, target map[string]any, mapping map[string]string) {
	for old, next := range mapping {
		value, exists := source[old]
		if !exists || value == nil {
			continue
		}
		target[next] = value
	}
}

func orderedUniqueAnyStrings(groups ...[]any) []any {
	result := []any{}
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, value := range group {
			text, ok := value.(string)
			if !ok {
				result = append(result, value)
				continue
			}
			if _, exists := seen[text]; exists {
				continue
			}
			seen[text] = struct{}{}
			result = append(result, text)
		}
	}
	return result
}

func normalizeLegacyLabelFilters(values []any) []any {
	result := make([]any, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		label := strings.TrimSpace(text)
		if label == "" || label == "!" {
			continue
		}
		if _, exists := seen[label]; exists {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, label)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func utf8Valid(value []byte) bool {
	return utf8.Valid(value)
}
