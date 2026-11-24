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

type AuthoredConfig struct {
	Project                   AuthoredProject   `json:"project" yaml:"project"`
	RunTestDefinitionsFromGit bool              `json:"run_test_definitions_from_git,omitempty" yaml:"run_test_definitions_from_git,omitempty"`
	Session                   *AuthoredSession  `json:"session,omitempty" yaml:"session,omitempty"`
	Build                     *AuthoredBuild    `json:"build,omitempty" yaml:"build,omitempty"`
	PRReview                  *AuthoredPRReview `json:"pr_review,omitempty" yaml:"pr_review,omitempty"`
}

type AuthoredProject struct {
	ID string `json:"id" yaml:"id"`
}

type AuthoredSession struct {
	IdleTimeoutSeconds *int                              `json:"idle_timeout_seconds,omitempty" yaml:"idle_timeout_seconds,omitempty"`
	BeforeScript       *AuthoredBeforeScript             `json:"before_script,omitempty" yaml:"before_script,omitempty"`
	AuthBypass         *AuthoredAuthBypass               `json:"auth_bypass,omitempty" yaml:"auth_bypass,omitempty"`
	DefaultProfile     *string                           `json:"default_profile,omitempty" yaml:"default_profile,omitempty"`
	Profiles           map[string]AuthoredSessionProfile `json:"profiles,omitempty" yaml:"profiles,omitempty"`
}

type AuthoredSessionProfile struct {
	Script                     string `json:"script" yaml:"script"`
	RequiresRepositoryCheckout bool   `json:"requires_repository_checkout,omitempty" yaml:"requires_repository_checkout,omitempty"`
}

type AuthoredBeforeScript struct {
	ScriptPath     *string `json:"script_path,omitempty" yaml:"script_path,omitempty"`
	TimeoutSeconds *int    `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
}

type AuthoredAuthBypass struct {
	LaunchVars []string        `json:"launch_vars,omitempty" yaml:"launch_vars,omitempty"`
	DeepLink   *string         `json:"deep_link,omitempty" yaml:"deep_link,omitempty"`
	SignIn     *AuthoredSignIn `json:"sign_in,omitempty" yaml:"sign_in,omitempty"`
}

type AuthoredSignIn struct {
	Path   *string `json:"path,omitempty" yaml:"path,omitempty"`
	Secret *string `json:"secret,omitempty" yaml:"secret,omitempty"`
}

const (
	maxSignInPathRunes       = 512
	launchVariableNameSyntax = `^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`
)

var launchVariableNameRegexp = regexp.MustCompile(launchVariableNameSyntax)

func ValidateSignInAppPath(value string) bool {
	return strings.HasPrefix(value, "/") &&
		!strings.HasPrefix(value, "//") &&
		utf8.RuneCountInString(value) <= maxSignInPathRunes &&
		!strings.ContainsAny(value, "\\#?") &&
		strings.IndexFunc(value, func(r rune) bool { return r <= 32 || r == 127 }) < 0
}

func ValidateLaunchVariableName(value string) bool {
	return launchVariableNameRegexp.MatchString(value)
}

func validateAuthoredSignIn(signIn *AuthoredSignIn) error {
	if signIn == nil {
		return nil
	}
	path := []string{"session", "auth_bypass", "sign_in"}
	field := func(name string) []string { return append(append([]string{}, path...), name) }
	if signIn.Path == nil {
		return newConfigError("contract", "missing_field", field("path"), "")
	}
	if !ValidateSignInAppPath(*signIn.Path) {
		return authoredContractError(field("path"), "session.auth_bypass.sign_in.path must be a relative app path without a query")
	}
	if signIn.Secret == nil {
		return newConfigError("contract", "missing_field", field("secret"), "")
	}
	if !ValidateLaunchVariableName(*signIn.Secret) {
		return authoredContractError(field("secret"), "session.auth_bypass.sign_in.secret must name an org launch variable")
	}
	return nil
}

type AuthoredBuild struct {
	Framework string                          `json:"framework,omitempty" yaml:"framework,omitempty"`
	Env       map[string]string               `json:"env,omitempty" yaml:"env,omitempty"`
	Secrets   []string                        `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Caches    []BuildCache                    `json:"caches,omitempty" yaml:"caches,omitempty"`
	Profiles  map[string]AuthoredBuildProfile `json:"profiles,omitempty" yaml:"profiles,omitempty"`
}

type AuthoredBuildProfile struct {
	IOS     *AuthoredBuildRecipe `json:"ios,omitempty" yaml:"ios,omitempty"`
	Android *AuthoredBuildRecipe `json:"android,omitempty" yaml:"android,omitempty"`
	Web     *AuthoredWebRecipe   `json:"web,omitempty" yaml:"web,omitempty"`
}

type AuthoredWebRecipe AuthoredBuildRecipe

type AuthoredWebRun struct {
	Start               *string  `json:"start,omitempty" yaml:"start,omitempty"`
	Port                int      `json:"port" yaml:"port"`
	Healthcheck         string   `json:"healthcheck" yaml:"healthcheck"`
	StartTimeoutSeconds int      `json:"start_timeout_seconds" yaml:"start_timeout_seconds"`
	EnvKeys             []string `json:"env_keys" yaml:"env_keys"`
	SecretNames         []string `json:"secret_names" yaml:"secret_names"`
}

func (r *AuthoredWebRecipe) HasBuildRecipe() bool {
	return r != nil && (r.BuildCommands != nil || len(r.SetupCommands) > 0 || r.OutputPath != nil || r.Image != nil || r.TimeoutSeconds != nil || len(r.Env) > 0 || len(r.Secrets) > 0 || len(r.Caches) > 0 || r.Run != nil)
}

func (p AuthoredBuildProfile) hasPhoneRecipe() bool {
	return p.IOS != nil || p.Android != nil
}

// FrameworkOptional reports whether Profiles is non-empty and contains no phone
// (iOS/Android) recipe; full authored validation checks Web presence separately.
func (b AuthoredBuild) FrameworkOptional() bool {
	if len(b.Profiles) == 0 {
		return false
	}
	for _, profile := range b.Profiles {
		if profile.hasPhoneRecipe() {
			return false
		}
	}
	return true
}

type AuthoredBuildRecipe struct {
	AppID          *string           `json:"app_id,omitempty" yaml:"app_id,omitempty"`
	SetupCommands  []BuildStepItem   `json:"setup_commands,omitempty" yaml:"setup_commands,omitempty"`
	BuildCommands  *[]BuildStepItem  `json:"build_commands" yaml:"build_commands"`
	OutputPath     *string           `json:"output_path,omitempty" yaml:"output_path,omitempty"`
	Image          *string           `json:"image,omitempty" yaml:"image,omitempty"`
	TimeoutSeconds *int              `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	Env            map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Secrets        []string          `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Caches         []BuildCache      `json:"caches,omitempty" yaml:"caches,omitempty"`
	Run            *AuthoredWebRun   `json:"run,omitempty" yaml:"run,omitempty"`
}

type AuthoredPRReview struct {
	Enabled        *bool                   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Triggers       *AuthoredReviewTriggers `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	ReviewTriggers *AuthoredReviewTriggers `json:"review_triggers,omitempty" yaml:"review_triggers,omitempty"`
	Build          *AuthoredReviewBuild    `json:"build,omitempty" yaml:"build,omitempty"`
	ProofOfChanges *AuthoredProofOfChanges `json:"proof_of_changes,omitempty" yaml:"proof_of_changes,omitempty"`
	Workflows      *AuthoredWorkflows      `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	WorkflowIDs    []string                `json:"workflow_ids,omitempty" yaml:"workflow_ids,omitempty"`
	StrictCICheck  *AuthoredStrictCICheck  `json:"strict_ci_check,omitempty" yaml:"strict_ci_check,omitempty"`
}

type AuthoredWorkflows struct {
	Enabled     *bool                   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	WorkflowIDs []string                `json:"workflow_ids,omitempty" yaml:"workflow_ids,omitempty"`
	Triggers    *AuthoredReviewTriggers `json:"triggers,omitempty" yaml:"triggers,omitempty"`
}

type AuthoredReviewTriggers struct {
	Paths  []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Labels []string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Drafts *bool    `json:"drafts,omitempty" yaml:"drafts,omitempty"`
}

type AuthoredReviewBuild struct {
	Kind    string                    `json:"kind" yaml:"kind"`
	Profile *string                   `json:"profile,omitempty" yaml:"profile,omitempty"`
	AppIDs  *AuthoredExternalCIAppIDs `json:"app_ids,omitempty" yaml:"app_ids,omitempty"`
}

// AuthoredExternalCIAppIDs maps upload platforms to Revyl app IDs.
type AuthoredExternalCIAppIDs struct {
	IOS     *string `json:"ios,omitempty" yaml:"ios,omitempty"`
	Android *string `json:"android,omitempty" yaml:"android,omitempty"`
	Web     *string `json:"web,omitempty" yaml:"web,omitempty"`
}

type AuthoredProofOfChanges struct {
	Triggers     *AuthoredReviewTriggers `json:"triggers,omitempty" yaml:"triggers,omitempty"`
	Enabled      *bool                   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Harness      *AuthoredProofHarness   `json:"harness,omitempty" yaml:"harness,omitempty"`
	SystemPrompt *string                 `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	AlwaysVerify []string                `json:"always_verify,omitempty" yaml:"always_verify,omitempty"`
	Macos        *AuthoredProofMacos     `json:"macos,omitempty" yaml:"macos,omitempty"`
}

// AuthoredProofMacos proves the pull request head on a Mac computer that a
// session profile prepares. AppID is the Web app the proof's sessions, reports
// and history belong to, and Targets name up to ten starting points Revyl
// checks on the Mac first; with none, the profile alone prepares it.
type AuthoredProofMacos struct {
	AppID          string              `json:"app_id" yaml:"app_id"`
	SessionProfile *string             `json:"session_profile,omitempty" yaml:"session_profile,omitempty"`
	Targets        []AuthoredMacTarget `json:"targets" yaml:"targets,omitempty"`
}

type AuthoredMacTarget struct {
	Name    string          `json:"name" yaml:"name"`
	Web     *string         `json:"web,omitempty" yaml:"web,omitempty"`
	App     *AuthoredMacApp `json:"app,omitempty" yaml:"app,omitempty"`
	Command *string         `json:"command,omitempty" yaml:"command,omitempty"`
	Preview *string         `json:"preview,omitempty" yaml:"preview,omitempty"`
	// ReadyTimeoutSeconds bounds how long a web target's check waits for its
	// URL to answer; it is web-only and defaults to 300 seconds.
	ReadyTimeoutSeconds *int `json:"ready_timeout_seconds,omitempty" yaml:"ready_timeout_seconds,omitempty"`
}

// AuthoredMacApp launches an installed app by BundleID, or a built .app at
// Path that the optional Build command produces in the checkout.
type AuthoredMacApp struct {
	BundleID *string `json:"bundle_id,omitempty" yaml:"bundle_id,omitempty"`
	Path     *string `json:"path,omitempty" yaml:"path,omitempty"`
	Build    *string `json:"build,omitempty" yaml:"build,omitempty"`
}

type AuthoredProofHarness struct {
	Kind          string  `json:"kind" yaml:"kind"`
	ModelID       *string `json:"model_id,omitempty" yaml:"model_id,omitempty"`
	CapyProjectID *string `json:"capy_project_id,omitempty" yaml:"capy_project_id,omitempty"`
}

type AuthoredStrictCICheck struct {
	Build *bool `json:"build" yaml:"build"`
}

// EffectiveBuildRecipe is the normalized recipe after pure compilation (before
// execution runnability checks); profile, app, and platform routing stay outside.
type EffectiveBuildRecipe struct {
	Framework           string            `json:"framework"`
	SetupCommands       []BuildStepItem   `json:"setup_commands"`
	BuildCommands       []BuildStepItem   `json:"build_commands"`
	SelectedProjectRoot string            `json:"selected_project_root"`
	ExecutionDirectory  string            `json:"execution_directory"`
	OutputPath          *string           `json:"output_path,omitempty"`
	Image               *string           `json:"image,omitempty"`
	TimeoutSeconds      *int              `json:"timeout_seconds,omitempty"`
	Env                 map[string]string `json:"env"`
	SecretRefs          []string          `json:"secret_refs"`
	Caches              []BuildCache      `json:"caches"`
	Run                 *AuthoredWebRun   `json:"run,omitempty"`
}

type ConfigurationContractError struct {
	Stage   string   `json:"stage"`
	Code    string   `json:"code"`
	Path    []string `json:"path"`
	Message *string  `json:"message,omitempty"`
}

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
		if c.Session.AuthBypass != nil {
			if err := validateAuthoredSignIn(c.Session.AuthBypass.SignIn); err != nil {
				return err
			}
		}
		if err := validateAuthoredSessionProfiles(*c.Session); err != nil {
			return err
		}
	}

	if c.Build != nil {
		if !validProjectFramework(c.Build.Framework) && (c.Build.Framework != "" || !c.Build.FrameworkOptional()) {
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
			if !profile.hasPhoneRecipe() && profile.Web == nil {
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
			if profile.Web.HasBuildRecipe() {
				if err := validateAuthoredRecipe(profileName, "web", (*AuthoredBuildRecipe)(profile.Web)); err != nil {
					return err
				}
			} else if profile.Web != nil && profile.Web.AppID == nil {
				return authoredContractError(
					[]string{"build", "profiles", profileName, "web"},
					fmt.Sprintf("build.profiles.%s.web needs app_id for a URL-only target, or build_commands and output_path for a bundle", profileName),
				)
			}
			if profile.Web != nil && profile.Web.AppID != nil {
				if _, err := uuid.Parse(*profile.Web.AppID); err != nil {
					return authoredContractError(
						[]string{"build", "profiles", profileName, "web", "app_id"},
						fmt.Sprintf("build.profiles.%s.web.app_id must be a UUID", profileName),
					)
				}
			}
		}
	}

	if c.PRReview == nil {
		return nil
	}
	if err := validateReviewTriggerBlocks(c.PRReview); err != nil {
		return err
	}
	for _, workflowID := range c.PRReview.WorkflowIDs {
		if _, err := uuid.Parse(workflowID); err != nil {
			return authoredContractError([]string{"pr_review", "workflow_ids"}, "pr_review.workflow_ids must contain UUIDs")
		}
	}
	if c.PRReview.Workflows != nil {
		for _, workflowID := range c.PRReview.Workflows.WorkflowIDs {
			if _, err := uuid.Parse(workflowID); err != nil {
				return authoredContractError([]string{"pr_review", "workflows", "workflow_ids"}, "pr_review.workflows.workflow_ids must contain UUIDs")
			}
		}
	}
	if c.PRReview.StrictCICheck != nil && c.PRReview.StrictCICheck.Build == nil {
		return fmt.Errorf("pr_review.strict_ci_check.build is required")
	}
	if c.PRReview.ProofOfChanges != nil && c.PRReview.ProofOfChanges.Harness != nil {
		harness := c.PRReview.ProofOfChanges.Harness
		switch harness.Kind {
		case "revyl":
			if harness.ModelID != nil && *harness.ModelID != "morph-dsv41flash-abliterated" && *harness.ModelID != "openai/gpt-6.1-terra" {
				return authoredContractError(
					[]string{"pr_review", "proof_of_changes", "harness", "model_id"},
					"pr_review.proof_of_changes.harness.model_id is not an allowlisted Revyl Proof model",
				)
			}
		case "devin":
			if harness.ModelID != nil {
				return authoredContractError(
					[]string{"pr_review", "proof_of_changes", "harness", "model_id"},
					"pr_review.proof_of_changes.harness.model_id is not supported by Devin",
				)
			}
		case "capy":
			if harness.ModelID != nil && !validCapyProofModelID(*harness.ModelID) {
				return authoredContractError(
					[]string{"pr_review", "proof_of_changes", "harness", "model_id"},
					"pr_review.proof_of_changes.harness.model_id is not an allowlisted Capy model",
				)
			}
			if harness.CapyProjectID != nil && (strings.TrimSpace(*harness.CapyProjectID) == "" || utf8.RuneCountInString(*harness.CapyProjectID) > 255) {
				return authoredContractError(
					[]string{"pr_review", "proof_of_changes", "harness", "capy_project_id"},
					"pr_review.proof_of_changes.harness.capy_project_id must contain 1-255 nonblank characters",
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
		if harness.CapyProjectID != nil && harness.Kind != "capy" {
			return authoredContractError(
				[]string{"pr_review", "proof_of_changes", "harness", "capy_project_id"},
				"pr_review.proof_of_changes.harness.capy_project_id is capy-only",
			)
		}
	}

	build := c.PRReview.Build
	if build == nil {
		if !c.PRReview.HasMacProof() {
			return newConfigError("contract", "missing_field", []string{"pr_review", "build"}, "")
		}
	} else {
		switch build.Kind {
		case "revyl":
			if build.Profile == nil {
				return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile is required for revyl")
			}
			if err := validateProfileName("pr_review.build.profile", *build.Profile); err != nil {
				return authoredContractError([]string{"pr_review", "build", "profile"}, err.Error())
			}
			if build.AppIDs != nil {
				return authoredContractError([]string{"pr_review", "build", "app_ids"}, "pr_review.build.app_ids is external-CI-only")
			}
		case "ci_upload_to_revyl":
			if build.Profile != nil {
				return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile is managed-build-only")
			}
			if build.AppIDs == nil || (build.AppIDs.IOS == nil && build.AppIDs.Android == nil && build.AppIDs.Web == nil) {
				return authoredContractError([]string{"pr_review", "build", "app_ids"}, "pr_review.build.app_ids must contain at least one platform")
			}
			for _, candidate := range []struct {
				platform string
				appID    *string
			}{
				{platform: "ios", appID: build.AppIDs.IOS},
				{platform: "android", appID: build.AppIDs.Android},
				{platform: "web", appID: build.AppIDs.Web},
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
	}
	if err := validateDeprecatedSpellings(c.PRReview); err != nil {
		return err
	}
	if build != nil && build.Kind == "revyl" {
		if c.Build == nil {
			return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile requires build profiles")
		}
		reviewProfile, ok := c.Build.Profiles[*build.Profile]
		if !ok {
			return authoredContractError([]string{"pr_review", "build", "profile"}, "pr_review.build.profile must name a declared profile")
		}
		if !reviewProfile.hasPhoneRecipe() && !reviewProfile.Web.HasBuildRecipe() {
			return newConfigError("contract", "profile_url_only", []string{"pr_review", "build", "profile"}, URLOnlyProfileMessage(*build.Profile))
		}
	}
	return c.validateMacProof()
}

func validCapyProofModelID(modelID string) bool {
	switch modelID {
	case "openai/gpt-6-luna", "openai/gpt-6.1-sol", "anthropic/claude-opus-5-5", "anthropic/claude-sonnet-5-5":
		return true
	default:
		return false
	}
}

func (r EffectiveBuildRecipe) ValidateContract() error {
	if !validProjectFramework(r.Framework) {
		return fmt.Errorf("framework is unsupported")
	}
	if len(r.BuildCommands) == 0 {
		return fmt.Errorf("build_commands must contain at least one command")
	}
	if err := validateBuildStepItems(r.SetupCommands, []string{"setup_commands"}, ""); err != nil {
		return err
	}
	if err := validateBuildStepItems(r.BuildCommands, []string{"build_commands"}, ""); err != nil {
		return err
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
	if r.Framework == "web" {
		if r.OutputPath == nil {
			return fmt.Errorf("web output_path must name an output directory")
		}
		if err := validateWebOutputPath(*r.OutputPath); err != nil {
			return err
		}
	} else if r.Run != nil {
		return fmt.Errorf("run is only supported for web recipes")
	}
	if r.Run != nil {
		return validateWebRun(r.Run, []string{"run"})
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
	if err := validateBuildStepItems(recipe.SetupCommands, append(append([]string{}, configPath...), "setup_commands"), platform); err != nil {
		return err
	}
	if err := validateBuildStepItems(*recipe.BuildCommands, append(append([]string{}, configPath...), "build_commands"), platform); err != nil {
		return err
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
	if platform == "web" {
		if recipe.OutputPath == nil || strings.TrimSpace(*recipe.OutputPath) == "" {
			return authoredContractError(append(configPath, "output_path"), fieldPath+".output_path must name the output directory")
		}
		if err := validateWebOutputPath(*recipe.OutputPath); err != nil {
			return authoredContractError(append(configPath, "output_path"), err.Error())
		}
		if recipe.Run != nil {
			return validateWebRun(recipe.Run, append(configPath, "run"))
		}
	} else if recipe.Run != nil {
		return authoredContractError(append(configPath, "run"), "run is only supported for web recipes")
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
	value = strings.TrimPrefix(value, "!")
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("review path filters must contain a non-empty, trimmed glob after !")
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
		if !canonicalCachePaths(cache.Paths) {
			return authoredContractError(append(cachePath, "paths"), fmt.Sprintf("%s.paths must contain project-relative paths or ~/ paths", cachePathText))
		}
		if !canonicalCachePaths(cache.Hash) {
			return authoredContractError(append(cachePath, "hash"), fmt.Sprintf("%s.hash must contain project-relative paths or ~/ paths", cachePathText))
		}
	}
	return nil
}

func canonicalCachePaths(entries []string) bool {
	for _, cacheEntry := range entries {
		relativeEntry := strings.TrimPrefix(cacheEntry, "~/")
		if relativeEntry == "" || strings.TrimSpace(cacheEntry) != cacheEntry || utf8.RuneCountInString(cacheEntry) > 1024 ||
			strings.HasPrefix(relativeEntry, "/") || strings.HasPrefix(relativeEntry, "~") || strings.Contains(cacheEntry, "%") ||
			strings.ContainsRune(cacheEntry, '\x00') || path.Clean(relativeEntry) != relativeEntry || relativeEntry == "." || relativeEntry == ".." ||
			strings.HasPrefix(relativeEntry, "../") {
			return false
		}
	}
	return true
}

func authoredContractError(path []string, message string) error {
	return newConfigError("contract", "invalid_contract", path, message)
}

func validProjectFramework(framework string) bool {
	switch framework {
	case "ios", "android", "react_native", "expo", "flutter", "web":
		return true
	default:
		return false
	}
}

func validateAuthoredReviewTriggers(triggers *AuthoredReviewTriggers, location []string) error {
	if triggers != nil {
		for index, pathFilter := range triggers.Paths {
			if err := validateReviewPathFilter(pathFilter); err != nil {
				return authoredContractError(
					append(append([]string{}, location...), "paths", fmt.Sprintf("%d", index)),
					err.Error(),
				)
			}
		}
		seenLabels := make(map[string]struct{}, len(triggers.Labels))
		for index, labelFilter := range triggers.Labels {
			if labelFilter == "" || labelFilter == "!" || labelFilter != strings.TrimSpace(labelFilter) {
				return authoredContractError(
					append(append([]string{}, location...), "labels", fmt.Sprintf("%d", index)),
					"must be a trimmed non-empty label name",
				)
			}
			if _, exists := seenLabels[labelFilter]; exists {
				return authoredContractError(
					append(append([]string{}, location...), "labels", fmt.Sprintf("%d", index)),
					"must not repeat an earlier label",
				)
			}
			seenLabels[labelFilter] = struct{}{}
		}
	}
	return nil
}
