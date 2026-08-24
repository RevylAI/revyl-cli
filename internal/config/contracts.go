package config

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxDatabaseInteger      = 2_147_483_647
	maxBuildOutputPathRunes = 1_200
	maxProfileNameRunes     = 128
	maxReviewPathRunes      = 1_024
)

var safeCacheKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// AuthoredConfig is the closed canonical .revyl/config.yaml contract.
//
// It intentionally coexists with ProjectConfig until the explicit migration and
// runtime cutover. Parsing, defaulting, hashing, and execution do not belong to
// this shape-only contract.
type AuthoredConfig struct {
	Project  AuthoredProject   `json:"project" yaml:"project"`
	Session  *AuthoredSession  `json:"session,omitempty" yaml:"session,omitempty"`
	Build    *AuthoredBuild    `json:"build,omitempty" yaml:"build,omitempty"`
	PRReview *AuthoredPRReview `json:"pr_review,omitempty" yaml:"pr_review,omitempty"`
}

// AuthoredProject carries only the stable server project identity.
type AuthoredProject struct {
	ID string `json:"id" yaml:"id"`
}

// AuthoredSession groups project-wide session behavior.
type AuthoredSession struct {
	IdleTimeoutSeconds *int                  `json:"idle_timeout_seconds,omitempty" yaml:"idle_timeout_seconds,omitempty"`
	BeforeScript       *AuthoredBeforeScript `json:"before_script,omitempty" yaml:"before_script,omitempty"`
	AuthBypass         *AuthoredAuthBypass   `json:"auth_bypass,omitempty" yaml:"auth_bypass,omitempty"`
}

// AuthoredBeforeScript is the canonical session setup shape.
type AuthoredBeforeScript struct {
	ScriptPath     *string `json:"script_path,omitempty" yaml:"script_path,omitempty"`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

// AuthoredAuthBypass is the canonical session auth-bypass shape.
type AuthoredAuthBypass struct {
	LaunchVars []string `json:"launch_vars,omitempty" yaml:"launch_vars,omitempty"`
	DeepLink   *string  `json:"deep_link,omitempty" yaml:"deep_link,omitempty"`
}

// AuthoredBuild contains the project framework, inherited defaults, and profiles.
type AuthoredBuild struct {
	Framework string                          `json:"framework" yaml:"framework"`
	Env       map[string]string               `json:"env,omitempty" yaml:"env,omitempty"`
	Secrets   []string                        `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Caches    []BuildCache                    `json:"caches,omitempty" yaml:"caches,omitempty"`
	Profiles  map[string]AuthoredBuildProfile `json:"profiles,omitempty" yaml:"profiles,omitempty"`
}

// AuthoredBuildProfile has at most one recipe for each supported platform.
type AuthoredBuildProfile struct {
	IOS     *AuthoredBuildRecipe `json:"ios,omitempty" yaml:"ios,omitempty"`
	Android *AuthoredBuildRecipe `json:"android,omitempty" yaml:"android,omitempty"`
}

// AuthoredBuildRecipe is one pre-inheritance platform recipe.
// BuildCommands is a pointer so structural validation can distinguish an omitted
// required field from an explicitly scaffolded empty command list.
type AuthoredBuildRecipe struct {
	AppID          *string           `json:"app_id,omitempty" yaml:"app_id,omitempty"`
	SetupCommands  []string          `json:"setup_commands,omitempty" yaml:"setup_commands,omitempty"`
	BuildCommands  *[]string         `json:"build_commands" yaml:"build_commands"`
	OutputPath     *string           `json:"output_path,omitempty" yaml:"output_path,omitempty"`
	Image          *string           `json:"image,omitempty" yaml:"image,omitempty"`
	TimeoutSeconds *int              `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	Env            map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Secrets        []string          `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Caches         []BuildCache      `json:"caches,omitempty" yaml:"caches,omitempty"`
}

// AuthoredPRReview is the canonical project-scoped review policy.
type AuthoredPRReview struct {
	Enabled        *bool                   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	ReviewTriggers *AuthoredReviewTriggers `json:"review_triggers,omitempty" yaml:"review_triggers,omitempty"`
	Build          AuthoredReviewBuild     `json:"build" yaml:"build"`
	ProofOfChanges *AuthoredProofOfChanges `json:"proof_of_changes,omitempty" yaml:"proof_of_changes,omitempty"`
	WorkflowIDs    []string                `json:"workflow_ids,omitempty" yaml:"workflow_ids,omitempty"`
	StrictCICheck  *AuthoredStrictCICheck  `json:"strict_ci_check,omitempty" yaml:"strict_ci_check,omitempty"`
}

// AuthoredReviewTriggers contains canonical changed-path, label, and draft filters.
type AuthoredReviewTriggers struct {
	Paths  []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Labels []string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Drafts *bool    `json:"drafts,omitempty" yaml:"drafts,omitempty"`
}

// AuthoredReviewBuild is a discriminated managed or external-CI declaration.
// Exactly one mode-specific payload is accepted by ValidateContract.
type AuthoredReviewBuild struct {
	Kind    string                    `json:"kind" yaml:"kind"`
	Profile *string                   `json:"profile,omitempty" yaml:"profile,omitempty"`
	AppIDs  *AuthoredExternalCIAppIDs `json:"app_ids,omitempty" yaml:"app_ids,omitempty"`
}

// AuthoredExternalCIAppIDs maps upload platforms to active Revyl app IDs.
type AuthoredExternalCIAppIDs struct {
	IOS     *string `json:"ios,omitempty" yaml:"ios,omitempty"`
	Android *string `json:"android,omitempty" yaml:"android,omitempty"`
}

// AuthoredProofOfChanges is the canonical proof policy.
type AuthoredProofOfChanges struct {
	Enabled      *bool                 `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Harness      *AuthoredProofHarness `json:"harness,omitempty" yaml:"harness,omitempty"`
	SystemPrompt *string               `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	AlwaysVerify []string              `json:"always_verify,omitempty" yaml:"always_verify,omitempty"`
}

// AuthoredProofHarness selects the Revyl or Cursor proof executor.
type AuthoredProofHarness struct {
	Kind    string  `json:"kind" yaml:"kind"`
	ModelID *string `json:"model_id,omitempty" yaml:"model_id,omitempty"`
}

// AuthoredStrictCICheck controls whether a failed build emits a failing SCM check.
type AuthoredStrictCICheck struct {
	Build *bool `json:"build" yaml:"build"`
}

// EffectiveBuildRecipe is resolved executable meaning after pure compilation.
// Selection and routing facts such as profile name, app, and platform stay outside it.
type EffectiveBuildRecipe struct {
	Framework           string            `json:"framework"`
	SetupCommands       []string          `json:"setup_commands"`
	BuildCommands       []string          `json:"build_commands"`
	SelectedProjectRoot string            `json:"selected_project_root"`
	ExecutionDirectory  string            `json:"execution_directory"`
	OutputPath          *string           `json:"output_path,omitempty"`
	Image               *string           `json:"image,omitempty"`
	TimeoutSeconds      *int              `json:"timeout_seconds,omitempty"`
	Env                 map[string]string `json:"env"`
	SecretRefs          []string          `json:"secret_refs"`
	Caches              []BuildCache      `json:"caches"`
}

// ConfigurationContractError is the stable machine-readable compilation error.
// Message is intentionally non-contractual; stage, code, and path are stable.
type ConfigurationContractError struct {
	Stage   string   `json:"stage"`
	Code    string   `json:"code"`
	Path    []string `json:"path"`
	Message *string  `json:"message,omitempty"`
}

// ValidateContract checks only canonical authored structural invariants.
func (c AuthoredConfig) ValidateContract() error {
	if _, err := uuid.Parse(c.Project.ID); err != nil {
		return authoredContractError([]string{"project", "id"}, "project.id must be a UUID")
	}

	if c.Session != nil {
		if err := validateDatabaseSeconds("session.idle_timeout_seconds", c.Session.IdleTimeoutSeconds); err != nil {
			return authoredContractError([]string{"session", "idle_timeout_seconds"}, err.Error())
		}
		if c.Session.BeforeScript != nil {
			if err := validateDatabaseSeconds("session.before_script.timeout_seconds", c.Session.BeforeScript.TimeoutSeconds); err != nil {
				return authoredContractError([]string{"session", "before_script", "timeout_seconds"}, err.Error())
			}
		}
	}

	if c.Build != nil {
		if !validProjectFramework(c.Build.Framework) {
			return authoredContractError([]string{"build", "framework"}, "build.framework is unsupported")
		}
		if err := validateCachesAtPath([]string{"build", "caches"}, c.Build.Caches); err != nil {
			return err
		}
		profileNames := make([]string, 0, len(c.Build.Profiles))
		for profileName := range c.Build.Profiles {
			profileNames = append(profileNames, profileName)
		}
		sort.Strings(profileNames)
		for _, profileName := range profileNames {
			profile := c.Build.Profiles[profileName]
			if err := validateProfileName("build.profiles", profileName); err != nil {
				return authoredContractError([]string{"build", "profiles", profileName}, err.Error())
			}
			if profile.IOS == nil && profile.Android == nil {
				return authoredContractError(
					[]string{"build", "profiles", profileName},
					fmt.Sprintf("build.profiles.%s must declare a platform recipe", profileName),
				)
			}
			if err := validateAuthoredRecipe(profileName, "ios", profile.IOS); err != nil {
				return err
			}
			if err := validateAuthoredRecipe(profileName, "android", profile.Android); err != nil {
				return err
			}
		}
	}

	if c.PRReview == nil {
		return nil
	}
	if c.PRReview.ReviewTriggers != nil {
		for index, pathFilter := range c.PRReview.ReviewTriggers.Paths {
			if err := validateReviewPathFilter(pathFilter); err != nil {
				return authoredContractError(
					[]string{"pr_review", "review_triggers", "paths", fmt.Sprintf("%d", index)},
					err.Error(),
				)
			}
		}
		seenLabels := make(map[string]struct{}, len(c.PRReview.ReviewTriggers.Labels))
		for index, labelFilter := range c.PRReview.ReviewTriggers.Labels {
			if labelFilter == "" || labelFilter == "!" || labelFilter != strings.TrimSpace(labelFilter) {
				return authoredContractError(
					[]string{"pr_review", "review_triggers", "labels", fmt.Sprintf("%d", index)},
					"pr_review.review_triggers.labels must contain trimmed non-empty label names",
				)
			}
			if _, exists := seenLabels[labelFilter]; exists {
				return authoredContractError(
					[]string{"pr_review", "review_triggers", "labels", fmt.Sprintf("%d", index)},
					"pr_review.review_triggers.labels must not contain duplicates",
				)
			}
			seenLabels[labelFilter] = struct{}{}
		}
	}
	for _, workflowID := range c.PRReview.WorkflowIDs {
		if _, err := uuid.Parse(workflowID); err != nil {
			return authoredContractError([]string{"pr_review", "workflow_ids"}, "pr_review.workflow_ids must contain UUIDs")
		}
	}
	if c.PRReview.StrictCICheck != nil && c.PRReview.StrictCICheck.Build == nil {
		return fmt.Errorf("pr_review.strict_ci_check.build is required")
	}
	if c.PRReview.ProofOfChanges != nil && c.PRReview.ProofOfChanges.Harness != nil {
		harness := c.PRReview.ProofOfChanges.Harness
		switch harness.Kind {
		case "revyl":
			if harness.ModelID != nil {
				return authoredContractError(
					[]string{"pr_review", "proof_of_changes", "harness", "model_id"},
					"pr_review.proof_of_changes.harness.model_id is cursor-only",
				)
			}
		case "cursor":
			if harness.ModelID != nil && (strings.TrimSpace(*harness.ModelID) == "" || utf8.RuneCountInString(*harness.ModelID) > 255) {
				return authoredContractError(
					[]string{"pr_review", "proof_of_changes", "harness", "model_id"},
					"pr_review.proof_of_changes.harness.model_id must contain 1-255 nonblank characters",
				)
			}
		default:
			return authoredContractError(
				[]string{"pr_review", "proof_of_changes", "harness", "kind"},
				"pr_review.proof_of_changes.harness.kind is unsupported",
			)
		}
	}

	switch c.PRReview.Build.Kind {
	case "revyl":
		if c.PRReview.Build.Profile == nil {
			return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile is required for revyl")
		}
		if err := validateProfileName("pr_review.build.profile", *c.PRReview.Build.Profile); err != nil {
			return authoredContractError([]string{"pr_review", "build", "profile"}, err.Error())
		}
		if c.PRReview.Build.AppIDs != nil {
			return authoredContractError([]string{"pr_review", "build", "app_ids"}, "pr_review.build.app_ids is external-CI-only")
		}
		if c.Build == nil {
			return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile requires build profiles")
		}
		if _, ok := c.Build.Profiles[*c.PRReview.Build.Profile]; !ok {
			return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile must name a declared profile")
		}
	case "ci_upload_to_revyl":
		if c.PRReview.Build.Profile != nil {
			return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile is managed-build-only")
		}
		if c.PRReview.Build.AppIDs == nil || (c.PRReview.Build.AppIDs.IOS == nil && c.PRReview.Build.AppIDs.Android == nil) {
			return authoredContractError([]string{"pr_review", "build", "app_ids"}, "pr_review.build.app_ids must contain at least one platform")
		}
		for _, candidate := range []struct {
			platform string
			appID    *string
		}{
			{platform: "ios", appID: c.PRReview.Build.AppIDs.IOS},
			{platform: "android", appID: c.PRReview.Build.AppIDs.Android},
		} {
			if candidate.appID != nil {
				if _, err := uuid.Parse(*candidate.appID); err != nil {
					return authoredContractError(
						[]string{"pr_review", "build", "app_ids", candidate.platform},
						"pr_review.build.app_ids must contain UUIDs",
					)
				}
			}
		}
	default:
		return authoredContractError([]string{"pr_review", "build", "kind"}, "pr_review.build.kind is unsupported")
	}
	return nil
}

// ValidateContract checks execution-time recipe structure without performing work.
func (r EffectiveBuildRecipe) ValidateContract() error {
	if !validProjectFramework(r.Framework) {
		return fmt.Errorf("framework is unsupported")
	}
	if len(r.BuildCommands) == 0 {
		return fmt.Errorf("build_commands must contain at least one command")
	}
	for _, command := range r.BuildCommands {
		if strings.TrimSpace(command) == "" {
			return fmt.Errorf("build_commands must not contain blank commands")
		}
	}
	if strings.TrimSpace(r.SelectedProjectRoot) == "" {
		return fmt.Errorf("selected_project_root must not be empty")
	}
	if strings.TrimSpace(r.ExecutionDirectory) == "" {
		return fmt.Errorf("execution_directory must not be empty")
	}
	if err := validateDatabaseSeconds("timeout_seconds", r.TimeoutSeconds); err != nil {
		return err
	}
	if err := validateOutputPath("output_path", r.OutputPath); err != nil {
		return authoredContractError([]string{"output_path"}, err.Error())
	}
	if err := validateCachesAtPath([]string{"caches"}, r.Caches); err != nil {
		return err
	}
	return nil
}

func validateAuthoredRecipe(profileName, platform string, recipe *AuthoredBuildRecipe) error {
	if recipe == nil {
		return nil
	}
	fieldPath := fmt.Sprintf("build.profiles.%s.%s", profileName, platform)
	configPath := []string{"build", "profiles", profileName, platform}
	if recipe.BuildCommands == nil {
		return newConfigError(
			"contract",
			"missing_field",
			append(append([]string{}, configPath...), "build_commands"),
			fmt.Sprintf("%s.build_commands is required", fieldPath),
		)
	}
	for index, command := range *recipe.BuildCommands {
		if strings.TrimSpace(command) == "" {
			return authoredContractError(
				append(append([]string{}, configPath...), "build_commands", fmt.Sprintf("%d", index)),
				fmt.Sprintf("%s.build_commands must not contain blank commands", fieldPath),
			)
		}
	}
	if recipe.AppID != nil {
		if _, err := uuid.Parse(*recipe.AppID); err != nil {
			return authoredContractError(append(append([]string{}, configPath...), "app_id"), fmt.Sprintf("%s.app_id must be a UUID", fieldPath))
		}
	}
	if err := validateDatabaseSeconds(fieldPath+".timeout_seconds", recipe.TimeoutSeconds); err != nil {
		return authoredContractError(append(append([]string{}, configPath...), "timeout_seconds"), err.Error())
	}
	if err := validateOutputPath(fieldPath+".output_path", recipe.OutputPath); err != nil {
		return authoredContractError(append(append([]string{}, configPath...), "output_path"), err.Error())
	}
	if err := validateCachesAtPath(append(append([]string{}, configPath...), "caches"), recipe.Caches); err != nil {
		return err
	}
	return nil
}

func validateDatabaseSeconds(fieldPath string, value *int) error {
	if value != nil && (*value <= 0 || *value > maxDatabaseInteger) {
		return fmt.Errorf("%s must be between 1 and %d", fieldPath, maxDatabaseInteger)
	}
	return nil
}

func validateProfileName(fieldPath, value string) error {
	if value == "" || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > maxProfileNameRunes {
		return fmt.Errorf("%s must be trimmed and contain 1-128 characters", fieldPath)
	}
	return nil
}

func validateReviewPathFilter(value string) error {
	if value == "" || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > maxReviewPathRunes {
		return fmt.Errorf("review path filters must contain 1-%d trimmed characters", maxReviewPathRunes)
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, "//") || strings.ContainsAny(value, "\\\x00") {
		return fmt.Errorf("review path filters must be valid repository-relative globs")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("review path filters must be valid repository-relative globs")
		}
	}
	return nil
}

func validateOutputPath(fieldPath string, value *string) error {
	if value == nil {
		return nil
	}
	if *value == "" || strings.TrimSpace(*value) != *value {
		return fmt.Errorf("%s must be non-empty and trimmed", fieldPath)
	}
	if utf8.RuneCountInString(*value) > maxBuildOutputPathRunes {
		return fmt.Errorf("%s must be at most 1200 characters", fieldPath)
	}
	if strings.HasPrefix(*value, "/") {
		return fmt.Errorf("%s must stay relative to the project root", fieldPath)
	}
	if strings.Contains(*value, "//") {
		return fmt.Errorf("%s must stay relative to the project root", fieldPath)
	}
	if strings.ContainsAny(*value, "\\\x00") {
		return fmt.Errorf("%s must stay relative to the project root", fieldPath)
	}
	for _, segment := range strings.Split(*value, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%s must stay relative to the project root", fieldPath)
		}
	}
	return nil
}

func validateCaches(fieldPath string, caches []BuildCache) error {
	return validateCachesAtPath(strings.Split(fieldPath, "."), caches)
}

func validateCachesAtPath(fieldPath []string, caches []BuildCache) error {
	for index, cache := range caches {
		cachePath := append(append([]string{}, fieldPath...), fmt.Sprintf("%d", index))
		cachePathText := strings.Join(cachePath, ".")
		if strings.TrimSpace(cache.Key) != cache.Key || !safeCacheKeyPattern.MatchString(cache.Key) {
			return authoredContractError(append(cachePath, "key"), fmt.Sprintf("%s.key must contain only letters, numbers, dots, hyphens, or underscores", cachePathText))
		}
		if len(cache.Paths) == 0 {
			return authoredContractError(append(cachePath, "paths"), fmt.Sprintf("%s.paths must not be empty", cachePathText))
		}
		for _, cacheEntry := range cache.Paths {
			if cacheEntry == "" || strings.TrimSpace(cacheEntry) != cacheEntry || utf8.RuneCountInString(cacheEntry) > 1024 ||
				strings.HasPrefix(cacheEntry, "/") || strings.HasPrefix(cacheEntry, "~") || strings.Contains(cacheEntry, "%") ||
				strings.ContainsRune(cacheEntry, '\x00') || path.Clean(cacheEntry) != cacheEntry || cacheEntry == "." || cacheEntry == ".." ||
				strings.HasPrefix(cacheEntry, "../") {
				return authoredContractError(append(cachePath, "paths"), fmt.Sprintf("%s.paths must contain valid project-relative paths", cachePathText))
			}
		}
	}
	return nil
}

func authoredContractError(path []string, message string) error {
	return newConfigError("contract", "invalid_contract", path, message)
}

func validProjectFramework(framework string) bool {
	switch framework {
	case "ios", "android", "react_native", "expo", "flutter":
		return true
	default:
		return false
	}
}
