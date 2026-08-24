package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/revyl/cli/internal/util"
)

// LegacyConfigMigrationInput contains only caller-resolved migration facts.
// UUID generation and authenticated server lookup stay outside this pure
// translation boundary.
type LegacyConfigMigrationInput struct {
	Data                          []byte
	Context                       CompilationContext
	ExplicitProjectID             string
	GeneratedProjectID            string
	LegacyAppIDsByPlatformAndName map[string]map[string]string
	// LegacyWorkflowIDsByName contains authenticated, organization-scoped
	// workflow resolutions supplied by CLI orchestration. The compiler never
	// performs server reads itself.
	LegacyWorkflowIDsByName map[string]string
}

// LegacyTestAlias is one legacy test alias that must remain available as a
// local .revyl/tests/<alias>.yaml file after config migration.
type LegacyTestAlias struct {
	Alias    string `json:"alias"`
	RemoteID string `json:"remote_id"`
}

// LegacyWorkflowLookup identifies one unresolved workflow name for optional,
// authenticated orchestration before the best-effort fallback is compiled.
type LegacyWorkflowLookup struct {
	Name string
	Path []string
}

// LegacyAppLookup identifies one enabled legacy PR build whose app name must
// resolve exactly within the authenticated organization and platform.
type LegacyAppLookup struct {
	Platform string
	Name     string
	Path     []string
}

// LegacyAppLookupsRequired asks CLI orchestration to resolve app names before
// the pure migration compiler can safely produce canonical app IDs.
type LegacyAppLookupsRequired struct {
	Lookups []LegacyAppLookup
	cause   *ConfigError
}

func (e *LegacyAppLookupsRequired) Error() string {
	return "enabled legacy PR build apps require authenticated lookup"
}

func (e *LegacyAppLookupsRequired) Unwrap() error { return e.cause }

// LegacyWorkflowLookupsRequired is an orchestration signal, not a terminal
// migration outcome. Callers retry with a non-nil resolution map; an empty map
// deliberately omits unresolved optional workflow references.
type LegacyWorkflowLookupsRequired struct {
	Lookups []LegacyWorkflowLookup
	cause   *ConfigError
}

func (e *LegacyWorkflowLookupsRequired) Error() string {
	return "legacy workflow references can use authenticated lookup"
}

func (e *LegacyWorkflowLookupsRequired) Unwrap() error { return e.cause }

// LegacyConfigOmission describes one recognized legacy value that is not
// represented in canonical YAML. Codes and messages are bounded constants;
// source values are deliberately excluded from machine-readable output.
type LegacyConfigOmission struct {
	Code        string   `json:"code"`
	Path        []string `json:"path"`
	Message     string   `json:"message"`
	Disposition string   `json:"disposition"`
}

// LegacyConfigMigrationResult is a complete, strictly validated local proposal.
// AlreadyCanonical tells orchestration to report success without rewriting bytes.
type LegacyConfigMigrationResult struct {
	AlreadyCanonical bool
	ProjectID        string
	Authored         AuthoredConfig
	Aggregate        *NormalizedProjectAggregate
	CanonicalBytes   []byte
	TestAliases      []LegacyTestAlias
	Omissions        []LegacyConfigOmission
}

// MigrateLegacyConfigBytes classifies one hardened YAML document and returns a
// canonical proposal without reading server state or mutating the file.
func MigrateLegacyConfigBytes(input LegacyConfigMigrationInput) (*LegacyConfigMigrationResult, error) {
	_, document, err := loadLegacyMigrationYAMLDocument(input.Data)
	if err != nil {
		return nil, err
	}
	kind, err := ClassifyConfigDocument(document)
	mixed := false
	if err != nil {
		var configError *ConfigError
		if !errors.As(err, &configError) || configError.Code != "mixed_config_formats" {
			return nil, err
		}
		mixed = true
		kind = "legacy"
	}
	if kind == "canonical" {
		return legacyMigrationResult(input.Data, input.Context, true)
	}
	mixedDocument, err := cloneLegacyMigrationDocument(document)
	if err != nil {
		return nil, err
	}
	omissions, err := applySafeLegacyMigrationOmissions(document)
	if err != nil {
		return nil, err
	}
	if err := prepareLegacyMigrationAppReferencesBestEffort(
		document,
		input.LegacyAppIDsByPlatformAndName,
		&omissions,
	); err != nil {
		return nil, err
	}
	sanitizeLegacyMigrationDocument(document, &omissions)
	testAliases, err := prepareLegacyMigrationExternalStateBestEffort(document, input.LegacyWorkflowIDsByName, &omissions)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveLegacyMigrationProjectID(document, input.ExplicitProjectID, input.GeneratedProjectID)
	if err != nil {
		return nil, err
	}
	project := stringMap(document["project"])
	if project == nil {
		project = map[string]any{}
		document["project"] = project
	}
	project["id"] = projectID
	prepareLegacyBuildForCanonicalTranslation(document)
	translated := translateLegacyConfigBestEffort(document, &omissions)
	preservedMixedSections := map[string]struct{}{}
	if mixed {
		preservedMixedSections = preserveMixedCanonicalSections(mixedDocument, translated, input.Context, &omissions)
	}
	result, err := legacyMigrationResultBestEffort(translated, input.Context, &omissions)
	if err != nil {
		return nil, err
	}
	if mixed {
		omissions, err = removePreservedMixedCanonicalOmissions(result.CanonicalBytes, preservedMixedSections, omissions)
		if err != nil {
			return nil, err
		}
	}
	result.TestAliases = testAliases
	result.Omissions = sortedLegacyMigrationChanges(omissions)
	return result, nil
}

func applySafeLegacyMigrationOmissions(document map[string]any) ([]LegacyConfigOmission, error) {
	omissions := []LegacyConfigOmission{}
	add := func(values map[string]any, key string, path []string, code, message string) {
		if values == nil {
			return
		}
		if _, exists := values[key]; !exists {
			return
		}
		omissions = append(omissions, LegacyConfigOmission{Code: code, Path: path, Message: message})
	}
	project := stringMap(document["project"])
	add(project, "name", []string{"project", "name"}, "retired_project_metadata", "project display metadata is server-owned")
	add(project, "org_id", []string{"project", "org_id"}, "retired_project_metadata", "organization identity is verified by connected project operations")
	delete(project, "name")
	delete(project, "org_id")

	defaults := stringMap(document["defaults"])
	if raw, exists := defaults["open_browser"]; exists && raw != nil {
		openBrowser, err := legacyBool(defaults, "open_browser", true, []string{"defaults", "open_browser"})
		if err == nil && !openBrowser {
			add(defaults, "open_browser", []string{"defaults", "open_browser"}, "retired_browser_preference", "browser auto-open is invocation-owned; use --no-open when starting a session")
		}
	}
	add(defaults, "open_browser", []string{"defaults", "open_browser"}, "retired_browser_preference", "browser auto-open is invocation-owned; use --no-open when starting a session")
	delete(defaults, "open_browser")

	authBypass := stringMap(document["auth_bypass"])
	for _, field := range []string{"refresh_command", "refresh_interval"} {
		path := []string{"auth_bypass", field}
		add(authBypass, field, path, "retired_auth_refresh", "legacy auth refresh fields had no implemented runtime lifecycle")
		delete(authBypass, field)
	}

	build := stringMap(document["build"])
	if raw, exists := build["no_build"]; exists && raw != nil {
		noBuild, err := legacyBool(build, "no_build", false, []string{"build", "no_build"})
		if err == nil && noBuild {
			add(build, "no_build", []string{"build", "no_build"}, "retired_no_build_preference", "build suppression is invocation-owned; use --no-build when supported by the command")
		}
	}
	add(build, "no_build", []string{"build", "no_build"}, "retired_no_build_preference", "use --no-build for each invocation")
	delete(build, "no_build")

	if source, exists := build["source"]; exists {
		message := "legacy source configuration is not represented; the selected local project root is used"
		if len(stringMap(source)) == 0 {
			message = "empty legacy source configuration is not represented"
		}
		add(build, "source", []string{"build", "source"}, "retired_source_configuration", message)
	}
	delete(build, "source")
	add(build, "root", []string{"build", "root"}, "canonical_project_root", "the selected config directory is the canonical project root")
	delete(build, "root")

	platforms := stringMap(build["platforms"])
	if len(platforms) > 0 {
		add(build, "command", []string{"build", "command"}, "shadowed_top_level_build_field", "platform recipes already owned executable build commands")
		add(build, "output", []string{"build", "output"}, "shadowed_top_level_build_field", "platform recipes already owned build outputs")
	}
	for _, platformKey := range sortedMapKeys(platforms) {
		entry := stringMap(platforms[platformKey])
		path := []string{"build", "platforms", platformKey, "keep_derived_data"}
		add(entry, "keep_derived_data", path, "retired_keep_derived_data", "the baseline CLI had no functioning derived-data retention behavior")
		delete(entry, "keep_derived_data")
	}

	hotReload := stringMap(document["hotreload"])
	if _, exists := document["hotreload"]; exists {
		message := "legacy hot-reload configuration is runtime-detected and is not represented"
		if len(stringMap(hotReload)) == 0 {
			message = "empty legacy hot-reload configuration is not represented"
		}
		add(document, "hotreload", []string{"hotreload"}, "retired_hotreload_configuration", message)
	}
	delete(document, "hotreload")

	review := stringMap(document["pr_review"])
	actions := stringMap(review["actions"])
	if raw, exists := actions["preview_link"]; exists && raw != nil {
		previewLink, err := legacyBool(actions, "preview_link", true, []string{"pr_review", "actions", "preview_link"})
		if err == nil && !previewLink {
			add(actions, "preview_link", []string{"pr_review", "actions", "preview_link"}, "retired_preview_link", "canonical PR publication has no per-project preview-link toggle")
		}
	}
	add(actions, "preview_link", []string{"pr_review", "actions", "preview_link"}, "retired_preview_link", "canonical PR publication has no per-project preview-link toggle")
	delete(actions, "preview_link")
	add(actions, "project_root", []string{"pr_review", "actions", "project_root"}, "canonical_project_root", "the selected config directory is the canonical project root")
	delete(actions, "project_root")
	builds := stringMap(review["builds"])
	for _, platform := range []string{"ios", "android"} {
		entry := stringMap(builds[platform])
		add(entry, "root_dir", []string{"pr_review", "builds", platform, "root_dir"}, "canonical_project_root", "the selected config directory is the canonical project root")
		delete(entry, "root_dir")
	}

	add(document, "last_synced_at", []string{"last_synced_at"}, "retired_sync_metadata", "test files now own synchronization metadata")
	add(document, "workflows", []string{"workflows"}, "retired_workflow_alias_cache", "workflow references are resolved to canonical IDs")

	sort.SliceStable(omissions, func(i, j int) bool {
		left := strings.Join(omissions[i].Path, ".") + "\x00" + omissions[i].Code
		right := strings.Join(omissions[j].Path, ".") + "\x00" + omissions[j].Code
		return left < right
	})
	return omissions, nil
}

func validatePortableLegacyTestAlias(alias string) error {
	if alias == "" || len(alias) > 128 || strings.TrimSpace(alias) != alias || strings.ContainsAny(alias, "/\\\x00<>:\"|?*") {
		return fmt.Errorf("alias is not portable")
	}
	if util.SanitizeForFilename(alias) != alias {
		return fmt.Errorf("alias does not map to its exact CLI filename")
	}
	for _, character := range alias {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("alias contains a control character")
		}
	}
	if alias == "." || alias == ".." || strings.HasSuffix(alias, ".") || strings.HasSuffix(alias, " ") {
		return fmt.Errorf("alias is not a portable filename")
	}
	base := strings.ToUpper(strings.SplitN(alias, ".", 2)[0])
	reserved := base == "CON" || base == "PRN" || base == "AUX" || base == "NUL"
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		reserved = true
	}
	if reserved {
		return fmt.Errorf("alias uses a reserved filename")
	}
	return nil
}

func legacyMigrationResult(data []byte, context CompilationContext, alreadyCanonical bool) (*LegacyConfigMigrationResult, error) {
	authored, err := ParseAuthoredConfig(data)
	if err != nil {
		return nil, err
	}
	aggregate, err := NormalizeAuthoredConfig(*authored, context)
	if err != nil {
		return nil, err
	}
	canonical, err := MarshalCanonicalConfig(*authored)
	if err != nil {
		return nil, err
	}
	return &LegacyConfigMigrationResult{
		AlreadyCanonical: alreadyCanonical,
		ProjectID:        authored.Project.ID,
		Authored:         *authored,
		Aggregate:        aggregate,
		CanonicalBytes:   canonical,
	}, nil
}

func resolveLegacyMigrationProjectID(document map[string]any, explicitProjectID, generatedProjectID string) (string, error) {
	if explicitProjectID != "" {
		return canonicalMigrationProjectID(explicitProjectID, "explicit_project_id_invalid")
	}
	project := stringMap(document["project"])
	if rawID, exists := project["id"]; exists && rawID != nil {
		if existingID, err := uuid.Parse(stringValue(rawID)); err == nil {
			return existingID.String(), nil
		}
	}
	if generatedProjectID == "" {
		return "", newConfigError("legacy_translation", "generated_project_id_required", []string{"project", "id"}, "")
	}
	return canonicalMigrationProjectID(generatedProjectID, "generated_project_id_invalid")
}

func canonicalMigrationProjectID(value, code string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", newConfigError("legacy_translation", code, []string{"project", "id"}, "")
	}
	return parsed.String(), nil
}

// prepareLegacyBuildForCanonicalTranslation normalizes legacy build placeholders
// before the explicit migration translator classifies their framework.
func prepareLegacyBuildForCanonicalTranslation(document map[string]any) {
	build := stringMap(document["build"])
	if len(build) == 0 {
		delete(document, "build")
		return
	}

	platforms := stringMap(build["platforms"])
	for _, rawEntry := range platforms {
		entry := stringMap(rawEntry)
		if strings.TrimSpace(stringValue(entry["output"])) == "" {
			delete(entry, "output")
		}
	}

	framework := strings.TrimSpace(stringValue(build["system"]))
	if _, err := translateLegacyFramework(framework); err == nil {
		return
	}
	if strings.EqualFold(framework, "Swift Package Manager") {
		build["system"] = "ios"
		return
	}
	if inferred := inferLegacyBuildFramework(build); inferred != "" {
		build["system"] = inferred
		return
	}

	uniquePlatforms := map[string]struct{}{}
	for key := range platforms {
		lowered := strings.ToLower(key)
		if strings.Contains(lowered, "ios") {
			uniquePlatforms["ios"] = struct{}{}
		}
		if strings.Contains(lowered, "android") {
			uniquePlatforms["android"] = struct{}{}
		}
	}
	if len(uniquePlatforms) == 1 {
		for platform := range uniquePlatforms {
			build["system"] = platform
		}
		return
	}

	_, hasCaches := build["caches"]
	if len(platforms) == 0 && strings.TrimSpace(stringValue(build["command"])) == "" && strings.TrimSpace(stringValue(build["output"])) == "" && !hasCaches {
		delete(document, "build")
	}
}
