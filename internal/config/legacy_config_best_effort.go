package config

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const legacyDroppedValueMessage = "the legacy value is not representable and was omitted"

func addLegacyMigrationChange(changes *[]LegacyConfigOmission, code string, path []string, message string) {
	*changes = append(*changes, LegacyConfigOmission{
		Code:    code,
		Path:    append([]string(nil), path...),
		Message: message,
	})
}

func sortedLegacyMigrationChanges(changes []LegacyConfigOmission) []LegacyConfigOmission {
	unique := make(map[string]LegacyConfigOmission, len(changes))
	for _, change := range changes {
		if change.Disposition == "" {
			change.Disposition = legacyMigrationDisposition(change.Code)
		}
		key := strings.Join(change.Path, "\x00") + "\x00" + change.Code
		unique[key] = change
	}
	result := make([]LegacyConfigOmission, 0, len(unique))
	for _, change := range unique {
		result = append(result, change)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := strings.Join(result[i].Path, ".") + "\x00" + result[i].Code
		right := strings.Join(result[j].Path, ".") + "\x00" + result[j].Code
		return left < right
	})
	return result
}

func legacyMigrationDisposition(code string) string {
	switch code {
	case "canonical_project_root", "legacy_app_reference_resolved", "legacy_label_filter_normalized", "legacy_project_id_invalid", "legacy_workflow_reference_resolved", "mixed_canonical_key_preserved", "mixed_canonical_section_preserved":
		return "resolved"
	case "legacy_boolean_invalid", "legacy_integer_invalid":
		return "defaulted"
	default:
		return "omitted"
	}
}

func prepareLegacyMigrationAppReferencesBestEffort(
	document map[string]any,
	resolved map[string]map[string]string,
	changes *[]LegacyConfigOmission,
) error {
	review := stringMap(document["pr_review"])
	if review == nil {
		return nil
	}
	reviewEnabled, err := legacyBool(review, "enabled", true, []string{"pr_review", "enabled"})
	if err != nil {
		reviewEnabled = true
	}
	if !reviewEnabled {
		return nil
	}
	builds := stringMap(review["builds"])
	lookups := []LegacyAppLookup{}
	for _, platform := range []string{"ios", "android"} {
		entry := stringMap(builds[platform])
		if entry == nil {
			continue
		}
		enabled, enabledErr := legacyBool(entry, "enabled", true, []string{"pr_review", "builds", platform, "enabled"})
		if enabledErr != nil {
			enabled = true
		}
		if !enabled {
			continue
		}
		name, ok := entry["app"].(string)
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		if parsed, parseErr := uuid.Parse(name); parseErr == nil {
			entry["app"] = parsed.String()
			continue
		}
		path := []string{"pr_review", "builds", platform, "app"}
		if resolved == nil {
			lookups = append(lookups, LegacyAppLookup{Platform: platform, Name: name, Path: path})
			continue
		}
		appID := strings.TrimSpace(resolved[platform][name])
		parsed, parseErr := uuid.Parse(appID)
		if parseErr != nil {
			delete(builds, platform)
			addLegacyMigrationChange(
				changes,
				"legacy_app_reference_unresolved",
				path,
				"the legacy app name could not be resolved and the review build was omitted",
			)
			continue
		}
		entry["app"] = parsed.String()
		addLegacyMigrationChange(changes, "legacy_app_reference_resolved", path, "the legacy app name was resolved to its canonical ID")
	}
	if len(lookups) == 0 {
		return nil
	}
	sort.SliceStable(lookups, func(i, j int) bool {
		return strings.Join(lookups[i].Path, ".") < strings.Join(lookups[j].Path, ".")
	})
	cause := newConfigError("legacy_translation", "legacy_server_lookup_required", lookups[0].Path, "")
	return &LegacyAppLookupsRequired{Lookups: lookups, cause: cause}
}

func cloneLegacyMigrationDocument(document map[string]any) (map[string]any, error) {
	encoded, err := yaml.Marshal(document)
	if err != nil {
		return nil, newConfigError("legacy_translation", "translation_failed", nil, "")
	}
	var cloned map[string]any
	if err := yaml.Unmarshal(encoded, &cloned); err != nil {
		return nil, newConfigError("legacy_translation", "translation_failed", nil, "")
	}
	return cloned, nil
}

func preserveMixedCanonicalSections(original, translated map[string]any, context CompilationContext, changes *[]LegacyConfigOmission) map[string]struct{} {
	project := translated["project"]
	preserved := map[string]struct{}{}
	trySection := func(section string, value any, dependencies ...string) {
		if value == nil {
			return
		}
		candidateValue := mergeMixedCanonicalValue(translated[section], value, []string{section}, changes)
		candidate := map[string]any{"project": project, section: candidateValue}
		for _, dependency := range dependencies {
			if dependencyValue, exists := translated[dependency]; exists {
				candidate[dependency] = dependencyValue
			}
		}
		encoded, err := yaml.Marshal(candidate)
		if err != nil {
			addLegacyMigrationChange(changes, "mixed_canonical_section_invalid", []string{section}, "the invalid canonical section in a mixed document was omitted")
			return
		}
		if _, err := legacyMigrationResult(encoded, context, false); err != nil {
			addLegacyMigrationChange(changes, "mixed_canonical_section_invalid", []string{section}, "the invalid canonical section in a mixed document was omitted")
			return
		}
		translated[section] = candidateValue
		preserved[section] = struct{}{}
		addLegacyMigrationChange(changes, "mixed_canonical_section_preserved", []string{section}, "the valid canonical section in a mixed document was preserved")
	}

	trySection("session", original["session"])
	build := selectLegacyMigrationKeys(stringMap(original["build"]), "framework", "env", "secrets", "caches", "profiles")
	if len(build) > 0 {
		trySection("build", build)
	}
	review := selectLegacyMigrationKeys(stringMap(original["pr_review"]), "enabled", "review_triggers", "build", "proof_of_changes", "workflow_ids", "strict_ci_check")
	if len(review) > 0 {
		trySection("pr_review", review, "build")
	}
	return preserved
}

func removePreservedMixedCanonicalOmissions(canonical []byte, preservedSections map[string]struct{}, changes []LegacyConfigOmission) ([]LegacyConfigOmission, error) {
	_, document, err := loadYAMLDocument(canonical)
	if err != nil {
		return nil, err
	}
	result := make([]LegacyConfigOmission, 0, len(changes))
	for _, change := range changes {
		if change.Code == "legacy_unsupported_field" && len(change.Path) > 0 {
			if _, preserved := preservedSections[change.Path[0]]; preserved && legacyMigrationPathExists(document, change.Path) {
				continue
			}
		}
		result = append(result, change)
	}
	return result, nil
}

func legacyMigrationPathExists(document map[string]any, path []string) bool {
	var current any = document
	for _, segment := range path {
		values, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = values[segment]
		if !ok {
			return false
		}
	}
	return true
}

func mergeMixedCanonicalValue(translated, canonical any, path []string, changes *[]LegacyConfigOmission) any {
	translatedValues, translatedMapping := translated.(map[string]any)
	canonicalValues, canonicalMapping := canonical.(map[string]any)
	if translatedMapping && canonicalMapping {
		merged := make(map[string]any, len(translatedValues)+len(canonicalValues))
		for key, value := range translatedValues {
			merged[key] = value
		}
		for _, key := range sortedMapKeys(canonicalValues) {
			canonicalValue := canonicalValues[key]
			if translatedValue, collision := merged[key]; collision {
				merged[key] = mergeMixedCanonicalValue(translatedValue, canonicalValue, appendPath(path, key), changes)
			} else {
				merged[key] = canonicalValue
			}
		}
		return merged
	}
	if translated != nil {
		addLegacyMigrationChange(
			changes,
			"mixed_canonical_key_preserved",
			path,
			"the authored canonical value took precedence over translated legacy meaning",
		)
	}
	return canonical
}

func selectLegacyMigrationKeys(values map[string]any, keys ...string) map[string]any {
	result := map[string]any{}
	for _, key := range keys {
		if value, exists := values[key]; exists {
			result[key] = value
		}
	}
	return result
}

func sanitizeLegacyMigrationDocument(document map[string]any, changes *[]LegacyConfigOmission) {
	dropUnsupportedLegacyKeys(document, nil, changes,
		"project", "build", "defaults", "hotreload", "pr_review", "before_session", "auth_bypass", "last_synced_at", "tests", "workflows",
	)
	project := sanitizeLegacyMapping(document, "project", []string{"project"}, changes)
	dropUnsupportedLegacyKeys(project, []string{"project"}, changes, "id")
	if raw, exists := project["id"]; exists {
		value, ok := raw.(string)
		if _, err := uuid.Parse(strings.TrimSpace(value)); !ok || err != nil {
			delete(project, "id")
			addLegacyMigrationChange(changes, "legacy_project_id_invalid", []string{"project", "id"}, "the invalid legacy project ID was replaced")
		}
	}

	defaults := sanitizeLegacyMapping(document, "defaults", []string{"defaults"}, changes)
	dropUnsupportedLegacyKeys(defaults, []string{"defaults"}, changes, "timeout")
	sanitizeLegacyPositiveInteger(defaults, "timeout", []string{"defaults", "timeout"}, changes)

	before := sanitizeLegacyMapping(document, "before_session", []string{"before_session"}, changes)
	dropUnsupportedLegacyKeys(before, []string{"before_session"}, changes, "script", "timeout_seconds")
	sanitizeLegacyOptionalString(before, "script", []string{"before_session", "script"}, changes)
	sanitizeLegacyPositiveInteger(before, "timeout_seconds", []string{"before_session", "timeout_seconds"}, changes)

	bypass := sanitizeLegacyMapping(document, "auth_bypass", []string{"auth_bypass"}, changes)
	dropUnsupportedLegacyKeys(bypass, []string{"auth_bypass"}, changes, "launch_vars", "deep_link")
	sanitizeLegacyStringList(bypass, "launch_vars", []string{"auth_bypass", "launch_vars"}, changes)
	sanitizeLegacyOptionalString(bypass, "deep_link", []string{"auth_bypass", "deep_link"}, changes)

	build := sanitizeLegacyMapping(document, "build", []string{"build"}, changes)
	dropUnsupportedLegacyKeys(build, []string{"build"}, changes, "system", "command", "output", "caches", "platforms")
	sanitizeLegacyOptionalString(build, "system", []string{"build", "system"}, changes)
	sanitizeLegacyOptionalString(build, "command", []string{"build", "command"}, changes)
	sanitizeLegacyOutputPath(build, "output", []string{"build", "output"}, changes)
	sanitizeLegacyCaches(build, "caches", []string{"build", "caches"}, changes)
	platforms := sanitizeLegacyMapping(build, "platforms", []string{"build", "platforms"}, changes)
	sanitizeLegacyPlatforms(platforms, changes)

	review := sanitizeLegacyMapping(document, "pr_review", []string{"pr_review"}, changes)
	sanitizeLegacyReview(review, changes)
}

func sanitizeLegacyPlatforms(platforms map[string]any, changes *[]LegacyConfigOmission) {
	seen := map[string]struct{}{}
	for _, key := range sortedMapKeys(platforms) {
		path := []string{"build", "platforms", key}
		entry, ok := platforms[key].(map[string]any)
		if !ok {
			delete(platforms, key)
			addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
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
			delete(platforms, key)
			addLegacyMigrationChange(changes, "legacy_platform_ambiguous", path, "the ambiguous legacy platform recipe was omitted")
			continue
		}
		profileName := legacyProfileName(key, matches[0])
		if err := validateProfileName("build.profiles", profileName); err != nil {
			delete(platforms, key)
			addLegacyMigrationChange(changes, "legacy_profile_ambiguous", path, "the legacy profile name could not be represented and was omitted")
			continue
		}
		identity := profileName + "\x00" + matches[0]
		if _, exists := seen[identity]; exists {
			delete(platforms, key)
			addLegacyMigrationChange(changes, "legacy_profile_ambiguous", path, "a conflicting legacy profile recipe was omitted")
			continue
		}
		seen[identity] = struct{}{}
		sanitizeLegacyRecipe(entry, path, changes)
	}
}

func sanitizeLegacyRecipe(entry map[string]any, path []string, changes *[]LegacyConfigOmission) {
	dropUnsupportedLegacyKeys(entry, path, changes,
		"command", "commands", "output", "image", "app_id", "scheme", "setup", "timeout", "env", "secrets", "caches",
	)
	sanitizeLegacyOptionalString(entry, "command", appendPath(path, "command"), changes)
	sanitizeLegacyStringList(entry, "commands", appendPath(path, "commands"), changes)
	sanitizeLegacyOutputPath(entry, "output", appendPath(path, "output"), changes)
	for _, field := range []string{"image", "scheme", "setup"} {
		sanitizeLegacyOptionalString(entry, field, appendPath(path, field), changes)
	}
	sanitizeLegacyUUID(entry, "app_id", appendPath(path, "app_id"), changes)
	sanitizeLegacyPositiveInteger(entry, "timeout", appendPath(path, "timeout"), changes)
	sanitizeLegacyStringMap(entry, "env", appendPath(path, "env"), changes)
	sanitizeLegacyStringList(entry, "secrets", appendPath(path, "secrets"), changes)
	sanitizeLegacyCaches(entry, "caches", appendPath(path, "caches"), changes)
}

func sanitizeLegacyReview(review map[string]any, changes *[]LegacyConfigOmission) {
	dropUnsupportedLegacyKeys(review, []string{"pr_review"}, changes,
		"enabled", "preset", "skip_drafts", "path_filters", "label_filters", "actions", "builds",
	)
	for _, field := range []string{"enabled", "skip_drafts"} {
		sanitizeLegacyBoolean(review, field, []string{"pr_review", field}, changes)
	}
	sanitizeLegacyReviewPathFilters(review, changes)
	sanitizeLegacyLabelFilters(review, changes)
	if preset, exists := review["preset"]; exists {
		value, ok := preset.(string)
		if !ok || (value != "" && value != "preview_only" && value != "adaptive_report" && value != "smoke_every_pr") {
			delete(review, "preset")
			addLegacyMigrationChange(changes, "legacy_unsupported_field", []string{"pr_review", "preset"}, "the unsupported legacy review preset was omitted")
		}
	}
	actions := sanitizeLegacyMapping(review, "actions", []string{"pr_review", "actions"}, changes)
	dropUnsupportedLegacyKeys(actions, []string{"pr_review", "actions"}, changes,
		"strict_build_checks", "proof_of_changes", "checks", "system_prompt", "workflows", "proof_harness",
	)
	for _, field := range []string{"strict_build_checks", "proof_of_changes"} {
		sanitizeLegacyBoolean(actions, field, []string{"pr_review", "actions", field}, changes)
	}
	sanitizeLegacyStringList(actions, "checks", []string{"pr_review", "actions", "checks"}, changes)
	sanitizeLegacyOptionalString(actions, "system_prompt", []string{"pr_review", "actions", "system_prompt"}, changes)
	sanitizeLegacyStringList(actions, "workflows", []string{"pr_review", "actions", "workflows"}, changes)
	harness := sanitizeLegacyMapping(actions, "proof_harness", []string{"pr_review", "actions", "proof_harness"}, changes)
	dropUnsupportedLegacyKeys(harness, []string{"pr_review", "actions", "proof_harness"}, changes, "kind", "model_id")
	sanitizeLegacyOptionalString(harness, "kind", []string{"pr_review", "actions", "proof_harness", "kind"}, changes)
	sanitizeLegacyOptionalString(harness, "model_id", []string{"pr_review", "actions", "proof_harness", "model_id"}, changes)
	if kind := stringValue(harness["kind"]); kind != "" && kind != "revyl" && kind != "cursor" {
		delete(actions, "proof_harness")
		addLegacyMigrationChange(changes, "legacy_unsupported_field", []string{"pr_review", "actions", "proof_harness"}, "the unsupported proof harness was omitted")
	} else if kind == "revyl" {
		if _, exists := harness["model_id"]; exists {
			delete(harness, "model_id")
			addLegacyMigrationChange(changes, "legacy_unsupported_field", []string{"pr_review", "actions", "proof_harness", "model_id"}, "the cursor-only model ID was omitted from the Revyl proof harness")
		}
	} else if modelID := stringValue(harness["model_id"]); len([]rune(modelID)) > 255 {
		delete(harness, "model_id")
		addLegacyMigrationChange(changes, "legacy_value_invalid", []string{"pr_review", "actions", "proof_harness", "model_id"}, legacyDroppedValueMessage)
	}

	builds := sanitizeLegacyMapping(review, "builds", []string{"pr_review", "builds"}, changes)
	dropUnsupportedLegacyKeys(builds, []string{"pr_review", "builds"}, changes, "ios", "android")
	for _, platform := range []string{"ios", "android"} {
		path := []string{"pr_review", "builds", platform}
		entry := sanitizeLegacyMapping(builds, platform, path, changes)
		if entry == nil {
			continue
		}
		dropUnsupportedLegacyKeys(entry, path, changes,
			"enabled", "framework", "image", "app", "build_command", "artifact_path", "use_existing_ci", "env", "secrets", "caches",
		)
		for _, field := range []string{"enabled", "use_existing_ci"} {
			sanitizeLegacyBoolean(entry, field, appendPath(path, field), changes)
		}
		for _, field := range []string{"framework", "image", "build_command"} {
			sanitizeLegacyOptionalString(entry, field, appendPath(path, field), changes)
		}
		sanitizeLegacyOutputPath(entry, "artifact_path", appendPath(path, "artifact_path"), changes)
		sanitizeLegacyUUID(entry, "app", appendPath(path, "app"), changes)
		sanitizeLegacyReviewEnv(entry, appendPath(path, "env"), changes)
		sanitizeLegacyStringList(entry, "secrets", appendPath(path, "secrets"), changes)
		sanitizeLegacyCaches(entry, "caches", appendPath(path, "caches"), changes)
		enabled, _ := legacyBool(entry, "enabled", true, appendPath(path, "enabled"))
		if enabled {
			if _, exists := entry["app"]; !exists {
				delete(builds, platform)
				addLegacyMigrationChange(changes, "legacy_server_lookup_required", appendPath(path, "app"), "the review build lacked a usable app ID and was omitted")
			}
		}
	}
}

func prepareLegacyMigrationExternalStateBestEffort(document map[string]any, resolved map[string]string, changes *[]LegacyConfigOmission) ([]LegacyTestAlias, error) {
	aliases := []LegacyTestAlias{}
	lookups := []LegacyWorkflowLookup{}
	if raw, exists := document["tests"]; exists {
		mapping, ok := raw.(map[string]any)
		if !ok {
			addLegacyMigrationChange(changes, "legacy_container_invalid", []string{"tests"}, legacyDroppedValueMessage)
		} else {
			for _, alias := range sortedMapKeys(mapping) {
				path := []string{"tests", alias}
				if validatePortableLegacyTestAlias(alias) != nil {
					addLegacyMigrationChange(changes, "legacy_test_alias_invalid", path, "the invalid legacy test alias was omitted")
					continue
				}
				parsed, err := uuid.Parse(stringValue(mapping[alias]))
				if err != nil {
					addLegacyMigrationChange(changes, "legacy_test_id_invalid", path, "the legacy test alias had an invalid remote ID and was omitted")
					continue
				}
				aliases = append(aliases, LegacyTestAlias{Alias: alias, RemoteID: parsed.String()})
			}
		}
		delete(document, "tests")
	}
	if _, exists := document["workflows"]; exists {
		addLegacyMigrationChange(changes, "retired_workflow_alias_cache", []string{"workflows"}, "the legacy workflow alias cache was omitted")
		delete(document, "workflows")
	}
	review := stringMap(document["pr_review"])
	actions := stringMap(review["actions"])
	values, ok := legacyMigrationStringValues(actions["workflows"])
	if ok {
		kept := make([]any, 0, len(values))
		for index, value := range values {
			path := []string{"pr_review", "actions", "workflows", strconv.Itoa(index)}
			value = strings.TrimSpace(value)
			if parsed, err := uuid.Parse(value); err == nil {
				kept = append(kept, parsed.String())
				continue
			}
			if resolvedID, exists := resolved[value]; exists {
				if parsed, err := uuid.Parse(resolvedID); err == nil {
					kept = append(kept, parsed.String())
					addLegacyMigrationChange(changes, "legacy_workflow_reference_resolved", path, "the legacy workflow name was resolved to its canonical ID")
					continue
				}
			}
			if resolved == nil {
				lookups = append(lookups, LegacyWorkflowLookup{Name: value, Path: path})
				continue
			}
			addLegacyMigrationChange(changes, "legacy_workflow_reference_unresolved", path, "the unresolved legacy workflow reference was omitted")
		}
		actions["workflows"] = kept
	}
	if stringValue(review["preset"]) == "smoke_every_pr" {
		if _, explicit := actions["workflows"]; !explicit {
			path := []string{"pr_review", "preset"}
			if resolved == nil {
				lookups = append(lookups, LegacyWorkflowLookup{Name: "smoke", Path: path})
			} else if resolvedID, exists := resolved["smoke"]; exists {
				if parsed, err := uuid.Parse(resolvedID); err == nil {
					if actions == nil {
						actions = map[string]any{}
						review["actions"] = actions
					}
					actions["workflows"] = []any{parsed.String()}
					addLegacyMigrationChange(changes, "legacy_workflow_reference_resolved", path, "the smoke workflow preset was resolved to its canonical ID")
				} else {
					addLegacyMigrationChange(changes, "legacy_workflow_reference_unresolved", path, "the smoke workflow preset could not be resolved and was omitted")
				}
			} else {
				addLegacyMigrationChange(changes, "legacy_workflow_reference_unresolved", path, "the smoke workflow preset could not be resolved and was omitted")
			}
		}
		delete(review, "preset")
	}
	if len(lookups) > 0 {
		sort.SliceStable(lookups, func(i, j int) bool {
			return strings.Join(lookups[i].Path, ".") < strings.Join(lookups[j].Path, ".")
		})
		cause := newConfigError("legacy_translation", "legacy_server_lookup_required", lookups[0].Path, "")
		return nil, &LegacyWorkflowLookupsRequired{Lookups: lookups, cause: cause}
	}
	return aliases, nil
}

func legacyMigrationStringValues(raw any) ([]string, bool) {
	switch values := raw.(type) {
	case []string:
		return values, true
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			item, ok := value.(string)
			if !ok {
				return nil, false
			}
			result = append(result, item)
		}
		return result, true
	default:
		return nil, false
	}
}

func translateLegacyConfigBestEffort(document map[string]any, changes *[]LegacyConfigOmission) map[string]any {
	project := map[string]any{"project": map[string]any{"id": stringValue(stringMap(document["project"])["id"])}}
	for _, key := range []string{"defaults", "before_session", "auth_bypass"} {
		if value, exists := document[key]; exists {
			project[key] = value
		}
	}
	result := map[string]any{"project": project["project"]}
	if translated, err := TranslateLegacyConfig(project); err == nil {
		if session, exists := translated["session"]; exists {
			result["session"] = session
		}
	} else {
		addLegacyTranslationFailure(changes, err, []string{"defaults"})
	}

	buildReview := map[string]any{"project": project["project"]}
	for _, key := range []string{"build", "pr_review"} {
		if value, exists := document[key]; exists {
			buildReview[key] = value
		}
	}
	for attempts := 0; attempts < 16; attempts++ {
		translated, err := TranslateLegacyConfig(buildReview)
		if err == nil {
			for _, key := range []string{"build", "pr_review"} {
				if value, exists := translated[key]; exists {
					result[key] = value
				}
			}
			return result
		}
		if !dropLegacyTranslationFailure(buildReview, err, changes) {
			break
		}
		prepareLegacyBuildForCanonicalTranslation(buildReview)
	}
	if _, exists := buildReview["pr_review"]; exists {
		delete(buildReview, "pr_review")
		addLegacyMigrationChange(changes, "legacy_review_unrepresentable", []string{"pr_review"}, "the legacy review policy was omitted")
		if translated, err := TranslateLegacyConfig(buildReview); err == nil {
			if build, exists := translated["build"]; exists {
				result["build"] = build
			}
			return result
		}
	}
	if _, exists := buildReview["build"]; exists {
		addLegacyMigrationChange(changes, "legacy_build_unrepresentable", []string{"build"}, "the legacy build configuration was omitted")
	}
	return result
}

func dropLegacyTranslationFailure(document map[string]any, err error, changes *[]LegacyConfigOmission) bool {
	var configError *ConfigError
	if !errors.As(err, &configError) {
		return false
	}
	path := append([]string(nil), configError.Path...)
	if len(path) == 0 {
		return false
	}
	addLegacyTranslationFailure(changes, err, path)
	if deleteLegacyPath(document, path) {
		return true
	}
	if path[0] == "pr_review" {
		_, exists := document["pr_review"]
		delete(document, "pr_review")
		return exists
	}
	if path[0] == "build" {
		_, exists := document["build"]
		delete(document, "build")
		return exists
	}
	return false
}

func addLegacyTranslationFailure(changes *[]LegacyConfigOmission, err error, fallbackPath []string) {
	var configError *ConfigError
	if errors.As(err, &configError) {
		path := configError.Path
		if len(path) == 0 {
			path = fallbackPath
		}
		addLegacyMigrationChange(changes, configError.Code, path, legacyDroppedValueMessage)
		return
	}
	addLegacyMigrationChange(changes, "legacy_translation_failed", fallbackPath, legacyDroppedValueMessage)
}

func legacyMigrationResultBestEffort(translated map[string]any, context CompilationContext, changes *[]LegacyConfigOmission) (*LegacyConfigMigrationResult, error) {
	for attempts := 0; attempts < 4; attempts++ {
		translatedBytes, err := yaml.Marshal(translated)
		if err != nil {
			return nil, newConfigError("legacy_translation", "translation_failed", nil, "")
		}
		result, err := legacyMigrationResult(translatedBytes, context, false)
		if err == nil {
			return result, nil
		}
		var configError *ConfigError
		if !errors.As(err, &configError) || len(configError.Path) == 0 {
			return nil, err
		}
		top := configError.Path[0]
		if top != "session" && top != "build" && top != "pr_review" {
			return nil, err
		}
		if _, exists := translated[top]; !exists {
			return nil, err
		}
		delete(translated, top)
		addLegacyMigrationChange(changes, "legacy_canonical_section_invalid", []string{top}, "the translated section was invalid and was omitted")
	}
	return nil, newConfigError("legacy_translation", "translation_failed", nil, "")
}

func dropUnsupportedLegacyKeys(values map[string]any, path []string, changes *[]LegacyConfigOmission, allowed ...string) {
	if values == nil {
		return
	}
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for _, key := range sortedMapKeys(values) {
		if _, ok := set[key]; ok {
			continue
		}
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_unsupported_field", appendPath(path, key), "the unsupported legacy field was omitted")
	}
}

func sanitizeLegacyMapping(parent map[string]any, key string, path []string, changes *[]LegacyConfigOmission) map[string]any {
	if parent == nil {
		return nil
	}
	raw, exists := parent[key]
	if !exists || raw == nil {
		return nil
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		delete(parent, key)
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
		return nil
	}
	return mapping
}

func sanitizeLegacyOptionalString(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_value_invalid", path, legacyDroppedValueMessage)
	}
}

func sanitizeLegacyBoolean(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	if raw, exists := values[key]; exists && raw != nil {
		if _, ok := raw.(bool); !ok {
			delete(values, key)
			addLegacyMigrationChange(changes, "legacy_boolean_invalid", path, "the invalid legacy boolean was omitted and the canonical default is used")
		}
	}
}

func sanitizeLegacyPositiveInteger(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	value, ok := integerValue(raw)
	if !ok || value <= 0 || value > maxDatabaseInteger {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_integer_invalid", path, "the invalid legacy duration was omitted and the canonical default is used")
	}
}

func sanitizeLegacyUUID(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	parsed, err := uuid.Parse(stringValue(raw))
	if err != nil {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_uuid_invalid", path, legacyDroppedValueMessage)
		return
	}
	values[key] = parsed.String()
}

func sanitizeLegacyOutputPath(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	value, ok := raw.(string)
	if !ok || validateOutputPath(strings.Join(path, "."), &value) != nil {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_output_path_invalid", path, legacyDroppedValueMessage)
	}
}

func sanitizeLegacyStringList(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	items, ok := raw.([]any)
	if !ok {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
		return
	}
	kept := make([]any, 0, len(items))
	for index, rawItem := range items {
		item, ok := rawItem.(string)
		if !ok || strings.TrimSpace(item) == "" {
			addLegacyMigrationChange(changes, "legacy_list_item_invalid", appendPath(path, strconv.Itoa(index)), legacyDroppedValueMessage)
			continue
		}
		kept = append(kept, item)
	}
	values[key] = kept
}

func sanitizeLegacyReviewPathFilters(review map[string]any, changes *[]LegacyConfigOmission) {
	if review == nil {
		return
	}
	const key = "path_filters"
	path := []string{"pr_review", key}
	raw, exists := review[key]
	if !exists || raw == nil {
		return
	}
	items, ok := raw.([]any)
	if !ok {
		delete(review, key)
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
		return
	}
	kept := make([]any, 0, len(items))
	for index, rawItem := range items {
		item, ok := rawItem.(string)
		if !ok || validateReviewPathFilter(item) != nil {
			addLegacyMigrationChange(changes, "legacy_review_path_filter_invalid", appendPath(path, strconv.Itoa(index)), legacyDroppedValueMessage)
			continue
		}
		kept = append(kept, item)
	}
	review[key] = kept
}

func sanitizeLegacyLabelFilters(review map[string]any, changes *[]LegacyConfigOmission) {
	if review == nil {
		return
	}
	const key = "label_filters"
	path := []string{"pr_review", key}
	raw, exists := review[key]
	if !exists || raw == nil {
		return
	}
	items, ok := raw.([]any)
	if !ok {
		delete(review, key)
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
		return
	}
	kept := make([]any, 0, len(items))
	seen := map[string]struct{}{}
	for index, rawItem := range items {
		item, ok := rawItem.(string)
		label := strings.TrimSpace(item)
		itemPath := appendPath(path, strconv.Itoa(index))
		if !ok || label == "" || label == "!" {
			addLegacyMigrationChange(changes, "legacy_list_item_invalid", itemPath, legacyDroppedValueMessage)
			continue
		}
		if _, duplicate := seen[label]; duplicate {
			addLegacyMigrationChange(changes, "legacy_label_filter_normalized", itemPath, "the duplicate legacy label filter was removed")
			continue
		}
		seen[label] = struct{}{}
		kept = append(kept, label)
		if label != item {
			addLegacyMigrationChange(changes, "legacy_label_filter_normalized", itemPath, "the legacy label filter was trimmed")
		}
	}
	review[key] = kept
}

func sanitizeLegacyStringMap(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	mapping, ok := raw.(map[string]any)
	if !ok {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
		return
	}
	for _, mapKey := range sortedMapKeys(mapping) {
		if _, ok := mapping[mapKey].(string); !ok {
			delete(mapping, mapKey)
			addLegacyMigrationChange(changes, "legacy_map_value_invalid", appendPath(path, mapKey), legacyDroppedValueMessage)
		}
	}
}

func sanitizeLegacyReviewEnv(values map[string]any, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values["env"]
	if !exists || raw == nil {
		return
	}
	switch raw.(type) {
	case map[string]any:
		sanitizeLegacyStringMap(values, "env", path, changes)
	case []any:
		sanitizeLegacyStringList(values, "env", path, changes)
	default:
		delete(values, "env")
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
	}
}

func sanitizeLegacyCaches(values map[string]any, key string, path []string, changes *[]LegacyConfigOmission) {
	if values == nil {
		return
	}
	raw, exists := values[key]
	if !exists || raw == nil {
		return
	}
	items, ok := raw.([]any)
	if !ok {
		delete(values, key)
		addLegacyMigrationChange(changes, "legacy_container_invalid", path, legacyDroppedValueMessage)
		return
	}
	kept := make([]any, 0, len(items))
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		itemPath := appendPath(path, strconv.Itoa(index))
		if !ok {
			addLegacyMigrationChange(changes, "legacy_cache_invalid", itemPath, legacyDroppedValueMessage)
			continue
		}
		keyValue, keyOK := item["key"].(string)
		pathValues, pathsOK := item["paths"].([]any)
		candidate := BuildCache{Key: keyValue}
		for _, rawPath := range pathValues {
			if value, ok := rawPath.(string); ok {
				candidate.Paths = append(candidate.Paths, value)
			}
		}
		if !keyOK || !pathsOK || len(candidate.Paths) != len(pathValues) || validateCaches(strings.Join(itemPath, "."), []BuildCache{candidate}) != nil {
			addLegacyMigrationChange(changes, "legacy_cache_invalid", itemPath, legacyDroppedValueMessage)
			continue
		}
		kept = append(kept, map[string]any{"key": candidate.Key, "paths": stringSliceToAny(candidate.Paths)})
	}
	values[key] = kept
}

func stringSliceToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func appendPath(path []string, elements ...string) []string {
	result := append([]string(nil), path...)
	return append(result, elements...)
}

func deleteLegacyPath(document map[string]any, path []string) bool {
	if len(path) == 0 {
		return false
	}
	current := document
	for _, element := range path[:len(path)-1] {
		next, ok := current[element].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	key := path[len(path)-1]
	if _, exists := current[key]; !exists {
		return false
	}
	delete(current, key)
	return true
}
