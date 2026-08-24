package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

// CompilationContext is pure path context established before hooks run.
type CompilationContext struct {
	RepositoryRelativeProjectRoot string `json:"repository_relative_project_root"`
	ExecutionDirectory            string `json:"execution_directory"`
}

// NormalizedProjectAggregate is the deterministic semantic projection of one
// canonical config.
type NormalizedProjectAggregate struct {
	ProjectID                string                   `json:"project_id"`
	Session                  AuthoredSession          `json:"session"`
	Framework                *string                  `json:"framework,omitempty"`
	Profiles                 []NormalizedBuildProfile `json:"profiles"`
	ReviewPolicy             *NormalizedReviewPolicy  `json:"review_policy,omitempty"`
	ProjectConfigurationHash string                   `json:"project_configuration_hash"`
}

type NormalizedBuildProfile struct {
	Name             string                            `json:"name"`
	BuildProfileHash string                            `json:"build_profile_hash"`
	Configurations   []NormalizedPlatformConfiguration `json:"configurations"`
}

type NormalizedPlatformConfiguration struct {
	Platform            string               `json:"platform"`
	AppID               *string              `json:"app_id,omitempty"`
	Recipe              EffectiveBuildRecipe `json:"recipe"`
	BuildDefinitionHash string               `json:"build_definition_hash"`
}

type NormalizedReviewPolicy struct {
	Enabled          bool                     `json:"enabled"`
	ReviewTriggers   NormalizedReviewTriggers `json:"review_triggers"`
	Build            NormalizedReviewBuild    `json:"build"`
	ProofOfChanges   *NormalizedProof         `json:"proof_of_changes,omitempty"`
	WorkflowIDs      []string                 `json:"workflow_ids"`
	StrictBuildCheck bool                     `json:"strict_build_check"`
	ReviewPolicyHash string                   `json:"review_policy_hash"`
}

type NormalizedReviewTriggers struct {
	Paths  []string `json:"paths"`
	Labels []string `json:"labels"`
	Drafts bool     `json:"drafts"`
}

type NormalizedReviewBuild struct {
	Kind    string                    `json:"kind"`
	Profile *string                   `json:"profile,omitempty"`
	AppIDs  *AuthoredExternalCIAppIDs `json:"app_ids,omitempty"`
}

type NormalizedProof struct {
	Enabled      bool                    `json:"enabled"`
	Harness      *NormalizedProofHarness `json:"harness,omitempty"`
	SystemPrompt *string                 `json:"system_prompt,omitempty"`
	AlwaysVerify []string                `json:"always_verify"`
}

type NormalizedProofHarness struct {
	Kind    string  `json:"kind"`
	ModelID *string `json:"model_id,omitempty"`
}

func CanonicalRevylJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical configuration: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, fmt.Errorf("decode canonical configuration: %w", err)
	}
	encoded, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("encode canonical configuration: %w", err)
	}
	return encoded, nil
}

func HashRevylProjection(domain string, value any) (string, error) {
	encoded, err := CanonicalRevylJSON(value)
	if err != nil {
		return "", err
	}
	return hashCanonicalRevylJSON(domain, encoded), nil
}

func hashCanonicalRevylJSON(domain string, encoded []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("revyl:" + domain + ":v1\x00"))
	_, _ = hash.Write(encoded)
	return hex.EncodeToString(hash.Sum(nil))
}

// NormalizeAuthoredConfig resolves defaults and inheritance without I/O.
func NormalizeAuthoredConfig(authored AuthoredConfig, context CompilationContext) (*NormalizedProjectAggregate, error) {
	if err := authored.ValidateContract(); err != nil {
		return nil, newConfigError("contract", "invalid_contract", nil, err.Error())
	}
	if err := validateCanonicalAuthoredConfigSize(authored); err != nil {
		return nil, err
	}
	if authored.Build != nil && len(authored.Build.Profiles) > MaxConfigProfiles {
		return nil, newConfigError("normalization", "too_many_build_profiles", []string{"build", "profiles"}, "")
	}
	if configCollectionItemCount(authored) > MaxConfigCollectionItems {
		return nil, newConfigError("normalization", "too_many_collection_items", nil, "")
	}
	root, err := normalizeRelativeDirectory(context.RepositoryRelativeProjectRoot, "repository_relative_project_root")
	if err != nil {
		return nil, err
	}
	executionDirectory, err := normalizeRelativeDirectory(context.ExecutionDirectory, "execution_directory")
	if err != nil {
		return nil, err
	}
	if root != "." && executionDirectory != root && !strings.HasPrefix(executionDirectory, root+"/") {
		return nil, newConfigError("normalization", "execution_directory_outside_project", []string{"execution_directory"}, "")
	}

	session := AuthoredSession{}
	if authored.Session != nil {
		session = cloneAuthoredSession(*authored.Session)
	}
	if session.BeforeScript != nil && session.BeforeScript.ScriptPath != nil {
		authoredScript := *session.BeforeScript.ScriptPath
		if authoredScript == "" || strings.TrimSpace(authoredScript) != authoredScript || strings.HasPrefix(authoredScript, "/") || strings.ContainsAny(authoredScript, "\\\x00") {
			return nil, newConfigError("normalization", "invalid_before_script_path", []string{"session", "before_script", "script_path"}, "")
		}
		script := path.Clean(authoredScript)
		if script == "." {
			return nil, newConfigError("normalization", "invalid_before_script_path", []string{"session", "before_script", "script_path"}, "")
		}
		repositoryScript := path.Clean(path.Join(root, script))
		if repositoryScript == ".." || strings.HasPrefix(repositoryScript, "../") {
			return nil, newConfigError("normalization", "path_escapes_repository", []string{"session", "before_script", "script_path"}, "")
		}
		session.BeforeScript.ScriptPath = &script
	}

	aggregate := &NormalizedProjectAggregate{
		ProjectID: authored.Project.ID,
		Session:   session,
		Profiles:  []NormalizedBuildProfile{},
	}
	normalizedRecipeBytes := 0
	if authored.Build != nil {
		framework := authored.Build.Framework
		aggregate.Framework = &framework
		profileNames := make([]string, 0, len(authored.Build.Profiles))
		for name := range authored.Build.Profiles {
			profileNames = append(profileNames, name)
		}
		sort.Strings(profileNames)
		for _, name := range profileNames {
			profile := authored.Build.Profiles[name]
			normalizedProfile := NormalizedBuildProfile{Name: name, Configurations: []NormalizedPlatformConfiguration{}}
			for _, platform := range []string{"ios", "android"} {
				var recipe *AuthoredBuildRecipe
				if platform == "ios" {
					recipe = profile.IOS
				} else {
					recipe = profile.Android
				}
				if recipe == nil {
					continue
				}
				recipePath := []string{"build", "profiles", name, platform}
				effective, normalizeErr := normalizeBuildRecipe(*authored.Build, *recipe, root, executionDirectory, recipePath)
				if normalizeErr != nil {
					return nil, normalizeErr
				}
				encodedRecipe, encodeErr := CanonicalRevylJSON(effective)
				if encodeErr != nil {
					return nil, encodeErr
				}
				normalizedRecipeBytes += len(encodedRecipe)
				if normalizedRecipeBytes > MaxNormalizedConfigBytes {
					return nil, newConfigError("normalization", "normalized_config_too_large", nil, "")
				}
				definitionHash := hashCanonicalRevylJSON("build-definition", encodedRecipe)
				normalizedProfile.Configurations = append(normalizedProfile.Configurations, NormalizedPlatformConfiguration{
					Platform: platform, AppID: cloneStringPointer(recipe.AppID), Recipe: effective, BuildDefinitionHash: definitionHash,
				})
			}
			profileProjection := make([]any, 0, len(normalizedProfile.Configurations))
			for _, configuration := range normalizedProfile.Configurations {
				profileProjection = append(profileProjection, map[string]any{
					"platform": configuration.Platform, "app_id": nullableString(configuration.AppID), "recipe": configuration.Recipe,
				})
			}
			normalizedProfile.BuildProfileHash, err = HashRevylProjection("build-profile", profileProjection)
			if err != nil {
				return nil, err
			}
			aggregate.Profiles = append(aggregate.Profiles, normalizedProfile)
		}
	}

	if authored.PRReview != nil {
		aggregate.ReviewPolicy, _, err = normalizeReviewPolicy(*authored.PRReview)
		if err != nil {
			return nil, err
		}
	}
	projectProjection := ProjectHashProjection(*aggregate)
	encodedProject, encodeErr := CanonicalRevylJSON(projectProjection)
	if encodeErr != nil {
		return nil, encodeErr
	}
	if len(encodedProject) > MaxNormalizedConfigBytes {
		return nil, newConfigError("normalization", "normalized_config_too_large", nil, "")
	}
	aggregate.ProjectConfigurationHash = hashCanonicalRevylJSON("project-configuration", encodedProject)
	return aggregate, nil
}

func validateCanonicalAuthoredConfigSize(authored AuthoredConfig) error {
	encoded, err := encodeCanonicalAuthoredConfig(authored)
	if err != nil {
		return newConfigError("normalization", "unsupported_canonical_value", nil, "")
	}
	if len(encoded) > MaxConfigBytes {
		return newConfigError("normalization", "canonical_config_too_large", nil, "")
	}
	return nil
}

func encodeCanonicalAuthoredConfig(authored AuthoredConfig) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(authoredConfigFileProjection(authored)); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func authoredConfigFileProjection(authored AuthoredConfig) map[string]any {
	projection := map[string]any{
		"project": map[string]any{"id": authored.Project.ID},
	}
	if authored.Session != nil {
		session := map[string]any{}
		if authored.Session.IdleTimeoutSeconds != nil {
			session["idle_timeout_seconds"] = *authored.Session.IdleTimeoutSeconds
		}
		if authored.Session.BeforeScript != nil {
			beforeScript := map[string]any{}
			if authored.Session.BeforeScript.ScriptPath != nil {
				beforeScript["script_path"] = *authored.Session.BeforeScript.ScriptPath
			}
			if authored.Session.BeforeScript.TimeoutSeconds != nil {
				beforeScript["timeout_seconds"] = *authored.Session.BeforeScript.TimeoutSeconds
			}
			session["before_script"] = beforeScript
		}
		if authored.Session.AuthBypass != nil {
			authBypass := map[string]any{
				"launch_vars": nonNilSlice(authored.Session.AuthBypass.LaunchVars),
			}
			if authored.Session.AuthBypass.DeepLink != nil {
				authBypass["deep_link"] = *authored.Session.AuthBypass.DeepLink
			}
			session["auth_bypass"] = authBypass
		}
		projection["session"] = session
	}
	if authored.Build != nil {
		profiles := make(map[string]any, len(authored.Build.Profiles))
		for name, profile := range authored.Build.Profiles {
			profileProjection := map[string]any{}
			if profile.IOS != nil {
				profileProjection["ios"] = authoredBuildRecipeFileProjection(*profile.IOS)
			}
			if profile.Android != nil {
				profileProjection["android"] = authoredBuildRecipeFileProjection(*profile.Android)
			}
			profiles[name] = profileProjection
		}
		projection["build"] = map[string]any{
			"framework": authored.Build.Framework,
			"env":       nonNilMap(authored.Build.Env),
			"secrets":   nonNilSlice(authored.Build.Secrets),
			"caches":    nonNilSlice(authored.Build.Caches),
			"profiles":  profiles,
		}
	}
	if authored.PRReview != nil {
		enabled := true
		if authored.PRReview.Enabled != nil {
			enabled = *authored.PRReview.Enabled
		}
		review := map[string]any{
			"enabled":      enabled,
			"build":        authoredReviewBuildFileProjection(authored.PRReview.Build),
			"workflow_ids": nonNilSlice(authored.PRReview.WorkflowIDs),
		}
		if authored.PRReview.ReviewTriggers != nil {
			drafts := false
			if authored.PRReview.ReviewTriggers.Drafts != nil {
				drafts = *authored.PRReview.ReviewTriggers.Drafts
			}
			review["review_triggers"] = map[string]any{
				"paths":  nonNilSlice(authored.PRReview.ReviewTriggers.Paths),
				"labels": nonNilSlice(authored.PRReview.ReviewTriggers.Labels),
				"drafts": drafts,
			}
		}
		if authored.PRReview.ProofOfChanges != nil {
			proofEnabled := false
			if authored.PRReview.ProofOfChanges.Enabled != nil {
				proofEnabled = *authored.PRReview.ProofOfChanges.Enabled
			}
			proof := map[string]any{
				"enabled":       proofEnabled,
				"always_verify": nonNilSlice(authored.PRReview.ProofOfChanges.AlwaysVerify),
			}
			if authored.PRReview.ProofOfChanges.Harness != nil {
				harness := map[string]any{
					"kind": authored.PRReview.ProofOfChanges.Harness.Kind,
				}
				if authored.PRReview.ProofOfChanges.Harness.ModelID != nil {
					harness["model_id"] = *authored.PRReview.ProofOfChanges.Harness.ModelID
				}
				proof["harness"] = harness
			}
			if authored.PRReview.ProofOfChanges.SystemPrompt != nil {
				proof["system_prompt"] = *authored.PRReview.ProofOfChanges.SystemPrompt
			}
			review["proof_of_changes"] = proof
		}
		if authored.PRReview.StrictCICheck != nil {
			review["strict_ci_check"] = map[string]any{
				"build": *authored.PRReview.StrictCICheck.Build,
			}
		}
		projection["pr_review"] = review
	}
	return projection
}

func authoredBuildRecipeFileProjection(recipe AuthoredBuildRecipe) map[string]any {
	buildCommands := []string{}
	if recipe.BuildCommands != nil {
		buildCommands = nonNilSlice(*recipe.BuildCommands)
	}
	projection := map[string]any{
		"setup_commands": nonNilSlice(recipe.SetupCommands),
		"build_commands": buildCommands,
		"env":            nonNilMap(recipe.Env),
		"secrets":        nonNilSlice(recipe.Secrets),
		"caches":         nonNilSlice(recipe.Caches),
	}
	if recipe.AppID != nil {
		projection["app_id"] = *recipe.AppID
	}
	if recipe.OutputPath != nil {
		projection["output_path"] = *recipe.OutputPath
	}
	if recipe.Image != nil {
		projection["image"] = *recipe.Image
	}
	if recipe.TimeoutSeconds != nil {
		projection["timeout_seconds"] = *recipe.TimeoutSeconds
	}
	return projection
}

func authoredReviewBuildFileProjection(build AuthoredReviewBuild) map[string]any {
	projection := map[string]any{"kind": build.Kind}
	if build.Profile != nil {
		projection["profile"] = *build.Profile
	}
	if build.AppIDs != nil {
		appIDs := map[string]any{}
		if build.AppIDs.IOS != nil {
			appIDs["ios"] = *build.AppIDs.IOS
		}
		if build.AppIDs.Android != nil {
			appIDs["android"] = *build.AppIDs.Android
		}
		projection["app_ids"] = appIDs
	}
	return projection
}

func nonNilSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func configCollectionItemCount(authored AuthoredConfig) int {
	count := 0
	if authored.Session != nil && authored.Session.AuthBypass != nil {
		count += len(authored.Session.AuthBypass.LaunchVars)
	}
	if authored.Build != nil {
		build := authored.Build
		count += len(build.Profiles) + len(build.Env) + len(build.Secrets) + len(build.Caches)
		for _, cache := range build.Caches {
			count += len(cache.Paths)
		}
		for _, profile := range build.Profiles {
			for _, recipe := range []*AuthoredBuildRecipe{profile.IOS, profile.Android} {
				if recipe == nil {
					continue
				}
				count += len(recipe.SetupCommands) + len(recipe.Env) + len(recipe.Secrets) + len(recipe.Caches)
				if recipe.BuildCommands != nil {
					count += len(*recipe.BuildCommands)
				}
				for _, cache := range recipe.Caches {
					count += len(cache.Paths)
				}
			}
		}
	}
	if authored.PRReview != nil {
		review := authored.PRReview
		count += len(review.WorkflowIDs)
		if review.ReviewTriggers != nil {
			count += len(review.ReviewTriggers.Paths) + len(review.ReviewTriggers.Labels)
		}
		if review.ProofOfChanges != nil {
			count += len(review.ProofOfChanges.AlwaysVerify)
		}
	}
	return count
}

// ProjectHashProjection returns the canonical semantic projection shared with Python.
func ProjectHashProjection(aggregate NormalizedProjectAggregate) map[string]any {
	profileProjection := make([]any, 0, len(aggregate.Profiles))
	for _, profile := range aggregate.Profiles {
		configurations := make([]any, 0, len(profile.Configurations))
		for _, configuration := range profile.Configurations {
			configurations = append(configurations, map[string]any{
				"platform": configuration.Platform, "app_id": nullableString(configuration.AppID), "recipe": configuration.Recipe,
			})
		}
		profileProjection = append(profileProjection, map[string]any{"name": profile.Name, "configurations": configurations})
	}
	var policyProjection any
	if aggregate.ReviewPolicy != nil {
		policyProjection = map[string]any{
			"enabled":            aggregate.ReviewPolicy.Enabled,
			"review_triggers":    aggregate.ReviewPolicy.ReviewTriggers,
			"build":              aggregate.ReviewPolicy.Build,
			"proof_of_changes":   aggregate.ReviewPolicy.ProofOfChanges,
			"workflow_ids":       aggregate.ReviewPolicy.WorkflowIDs,
			"strict_build_check": aggregate.ReviewPolicy.StrictBuildCheck,
		}
	}
	return map[string]any{
		"session": normalizeSessionProjection(aggregate.Session), "framework": nullableString(aggregate.Framework),
		"profiles": profileProjection, "review_policy": policyProjection,
	}
}

func normalizeRelativeDirectory(value, field string) (string, error) {
	if value == "." {
		return value, nil
	}
	if value == "" || value != strings.TrimSpace(value) || utf8.RuneCountInString(value) > 1024 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") || path.Clean(value) != value {
		return "", newConfigError("normalization", "invalid_relative_directory", []string{field}, "")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", newConfigError("normalization", "invalid_relative_directory", []string{field}, "")
		}
	}
	return value, nil
}

func normalizeBuildRecipe(build AuthoredBuild, recipe AuthoredBuildRecipe, root, executionDirectory string, recipePath []string) (EffectiveBuildRecipe, error) {
	env := cloneStringMap(build.Env)
	for key, value := range recipe.Env {
		env[key] = value
	}
	secretRefs := orderedUniqueStrings(build.Secrets, recipe.Secrets)
	for _, name := range secretRefs {
		if _, exists := env[name]; exists {
			return EffectiveBuildRecipe{}, newConfigError(
				"normalization",
				"environment_secret_collision",
				append(append([]string{}, recipePath...), "env", name),
				fmt.Sprintf("build variable %q is declared as both plaintext env and an encrypted secret", name),
			)
		}
	}
	commands := []string{}
	if recipe.BuildCommands != nil {
		commands = cloneStrings(*recipe.BuildCommands)
	}
	return EffectiveBuildRecipe{
		Framework: build.Framework, SetupCommands: cloneStrings(recipe.SetupCommands), BuildCommands: commands,
		SelectedProjectRoot: root, ExecutionDirectory: root, OutputPath: cloneStringPointer(recipe.OutputPath),
		Image: cloneStringPointer(recipe.Image), TimeoutSeconds: cloneIntPointer(recipe.TimeoutSeconds), Env: env,
		SecretRefs: secretRefs, Caches: mergeCaches(build.Caches, recipe.Caches),
	}, nil
}

func mergeCaches(inherited, recipe []BuildCache) []BuildCache {
	keys := []string{}
	pathsByKey := map[string][]string{}
	for _, cache := range append(append([]BuildCache{}, inherited...), recipe...) {
		if _, exists := pathsByKey[cache.Key]; !exists {
			keys = append(keys, cache.Key)
			pathsByKey[cache.Key] = []string{}
		}
		pathsByKey[cache.Key] = orderedUniqueStrings(pathsByKey[cache.Key], cache.Paths)
	}
	result := make([]BuildCache, 0, len(keys))
	for _, key := range keys {
		result = append(result, BuildCache{Key: key, Paths: pathsByKey[key]})
	}
	return result
}

func normalizeReviewPolicy(review AuthoredPRReview) (*NormalizedReviewPolicy, map[string]any, error) {
	enabled := true
	if review.Enabled != nil {
		enabled = *review.Enabled
	}
	triggers := NormalizedReviewTriggers{Paths: []string{}, Labels: []string{}}
	if review.ReviewTriggers != nil {
		triggers.Paths = orderedUniqueStrings(review.ReviewTriggers.Paths)
		triggers.Labels = orderedUniqueStrings(review.ReviewTriggers.Labels)
		if review.ReviewTriggers.Drafts != nil {
			triggers.Drafts = *review.ReviewTriggers.Drafts
		}
	}
	build := NormalizedReviewBuild{Kind: review.Build.Kind, Profile: cloneStringPointer(review.Build.Profile)}
	if review.Build.AppIDs != nil {
		build.AppIDs = &AuthoredExternalCIAppIDs{IOS: cloneStringPointer(review.Build.AppIDs.IOS), Android: cloneStringPointer(review.Build.AppIDs.Android)}
	}
	var proof *NormalizedProof
	if review.ProofOfChanges != nil {
		proof = &NormalizedProof{AlwaysVerify: orderedUniqueStrings(review.ProofOfChanges.AlwaysVerify), SystemPrompt: cloneStringPointer(review.ProofOfChanges.SystemPrompt)}
		if review.ProofOfChanges.Enabled != nil {
			proof.Enabled = *review.ProofOfChanges.Enabled
		}
		if review.ProofOfChanges.Harness != nil {
			proof.Harness = &NormalizedProofHarness{Kind: review.ProofOfChanges.Harness.Kind, ModelID: cloneStringPointer(review.ProofOfChanges.Harness.ModelID)}
		}
	}
	strict := true
	if review.StrictCICheck != nil {
		strict = *review.StrictCICheck.Build
	}
	workflowIDs := orderedUniqueStrings(review.WorkflowIDs)
	projection := map[string]any{
		"enabled": enabled, "review_triggers": triggers, "build": build,
		"proof_of_changes": proof, "workflow_ids": workflowIDs, "strict_build_check": strict,
	}
	hash, err := HashRevylProjection("review-policy", projection)
	if err != nil {
		return nil, nil, err
	}
	return &NormalizedReviewPolicy{
		Enabled: enabled, ReviewTriggers: triggers, Build: build, ProofOfChanges: proof,
		WorkflowIDs: workflowIDs, StrictBuildCheck: strict, ReviewPolicyHash: hash,
	}, projection, nil
}

func normalizeSessionProjection(session AuthoredSession) map[string]any {
	result := map[string]any{}
	if session.IdleTimeoutSeconds != nil {
		result["idle_timeout_seconds"] = *session.IdleTimeoutSeconds
	}
	if session.BeforeScript != nil {
		before := map[string]any{}
		if session.BeforeScript.ScriptPath != nil {
			before["script_path"] = *session.BeforeScript.ScriptPath
		}
		if session.BeforeScript.TimeoutSeconds != nil {
			before["timeout_seconds"] = *session.BeforeScript.TimeoutSeconds
		}
		result["before_script"] = before
	}
	if session.AuthBypass != nil {
		auth := map[string]any{"launch_vars": nonNilStrings(session.AuthBypass.LaunchVars)}
		if session.AuthBypass.DeepLink != nil {
			auth["deep_link"] = *session.AuthBypass.DeepLink
		}
		result["auth_bypass"] = auth
	}
	return result
}

func cloneAuthoredSession(session AuthoredSession) AuthoredSession {
	result := AuthoredSession{IdleTimeoutSeconds: cloneIntPointer(session.IdleTimeoutSeconds)}
	if session.BeforeScript != nil {
		result.BeforeScript = &AuthoredBeforeScript{ScriptPath: cloneStringPointer(session.BeforeScript.ScriptPath), TimeoutSeconds: cloneIntPointer(session.BeforeScript.TimeoutSeconds)}
	}
	if session.AuthBypass != nil {
		result.AuthBypass = &AuthoredAuthBypass{LaunchVars: orderedUniqueStrings(session.AuthBypass.LaunchVars), DeepLink: cloneStringPointer(session.AuthBypass.DeepLink)}
	}
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func orderedUniqueStrings(groups ...[]string) []string {
	result := []string{}
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, value := range group {
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
