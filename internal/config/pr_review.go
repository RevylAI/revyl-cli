package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file defines the `pr_review` section of .revyl/config.yaml: the
// config-as-code schema for GitHub PR automation. It mirrors the backend
// schema in cognisim_backend/app/services/scm_config_file.py and the
// "generate with AI" prompt so the three producers stay in sync.

// PRReviewConfig is the `pr_review` block of .revyl/config.yaml.
//
// When committed to the default branch, Revyl reconciles this into the repo's
// PR automation (preview builds, proof checks, curated workflows, filters).
type PRReviewConfig struct {
	// Enabled turns PR automation on for this repository.
	Enabled bool `yaml:"enabled"`

	// Preset is an optional automation preset
	// (preview_only, proof_on_demand, smoke_every_pr, adaptive_report).
	Preset string `yaml:"preset,omitempty"`

	// SkipDrafts waits until draft PRs are marked ready for review.
	SkipDrafts bool `yaml:"skip_drafts"`

	// PathFilters limits automation to PRs touching matching glob paths.
	PathFilters []string `yaml:"path_filters,omitempty"`

	// LabelFilters includes matching labels; entries prefixed with ! exclude them.
	LabelFilters []string `yaml:"label_filters,omitempty"`

	// Actions controls what Revyl posts on the PR.
	Actions PRReviewActions `yaml:"actions,omitempty"`

	// Builds declares the per-platform preview builds.
	Builds PRReviewBuilds `yaml:"builds,omitempty"`
}

// PRReviewActions controls the PR response actions.
type PRReviewActions struct {
	// PreviewLink posts a link to a running preview session.
	PreviewLink bool `yaml:"preview_link"`

	// ProofOfChanges has the agent verify the change and attach a screenshot.
	ProofOfChanges bool `yaml:"proof_of_changes"`

	// Checks are natural-language assertions verified on the preview build.
	Checks []string `yaml:"checks,omitempty"`

	// SystemPrompt is extra guidance prepended to proof-of-changes runs.
	SystemPrompt string `yaml:"system_prompt,omitempty"`

	// Workflows are saved Revyl workflow IDs to run against the preview build.
	Workflows []string `yaml:"workflows,omitempty"`
}

// PRReviewBuilds declares per-platform preview builds.
type PRReviewBuilds struct {
	// IOS is the iOS preview build entry.
	IOS *PRReviewBuildEntry `yaml:"ios,omitempty"`

	// Android is the Android preview build entry.
	Android *PRReviewBuildEntry `yaml:"android,omitempty"`
}

// PRReviewBuildEntry is a single platform's preview build declaration.
type PRReviewBuildEntry struct {
	// Enabled turns this platform's preview build on.
	Enabled bool `yaml:"enabled"`

	// Framework is the build framework
	// (expo_ios, expo_android, react_native_ios, react_native_android,
	// native_ios, native_android).
	Framework string `yaml:"framework,omitempty"`

	// Image is the optional sandbox build toolchain image key.
	Image string `yaml:"image,omitempty"`

	// App is the Revyl app name or app id this build targets.
	App string `yaml:"app,omitempty"`

	// RootDir is the source subdirectory the build runs in (e.g. ./apps/mobile).
	RootDir string `yaml:"root_dir,omitempty"`

	// BuildCommand is the build command(s); newline-separated for multiple steps.
	BuildCommand string `yaml:"build_command,omitempty"`

	// ArtifactPath is the glob path to the built artifact.
	ArtifactPath string `yaml:"artifact_path,omitempty"`

	// UseExistingCI skips Revyl-managed builds; the customer CI uploads instead.
	UseExistingCI bool `yaml:"use_existing_ci,omitempty"`

	// Env contains non-secret environment variables passed to build steps.
	Env map[string]string `yaml:"env,omitempty"`

	// Secrets lists encrypted org build-secret names, never their values.
	Secrets []string `yaml:"secrets,omitempty"`

	// Caches contains remote-build cache disks for this platform's preview
	// builds. Omitted or empty means no caching; cache disks are only used when
	// explicitly configured.
	Caches []BuildCache `yaml:"caches,omitempty"`
}

// UnmarshalYAML decodes canonical build environment fields and legacy secret lists.
//
// Parameters:
//   - node: YAML node containing one PR-review platform build.
//
// Returns:
//   - An error when env is neither a mapping nor the deprecated string list,
//     or when a name is declared as both plaintext env and an encrypted secret.
func (e *PRReviewBuildEntry) UnmarshalYAML(node *yaml.Node) error {
	type buildEntryWire struct {
		Enabled       *bool        `yaml:"enabled"`
		Framework     string       `yaml:"framework,omitempty"`
		Image         string       `yaml:"image,omitempty"`
		App           string       `yaml:"app,omitempty"`
		RootDir       string       `yaml:"root_dir,omitempty"`
		BuildCommand  string       `yaml:"build_command,omitempty"`
		ArtifactPath  string       `yaml:"artifact_path,omitempty"`
		UseExistingCI bool         `yaml:"use_existing_ci,omitempty"`
		Env           yaml.Node    `yaml:"env,omitempty"`
		Secrets       []string     `yaml:"secrets,omitempty"`
		Caches        []BuildCache `yaml:"caches,omitempty"`
	}

	var wire buildEntryWire
	if err := node.Decode(&wire); err != nil {
		return err
	}

	env, legacySecrets, err := decodePRReviewBuildEnv(&wire.Env)
	if err != nil {
		return err
	}
	secrets := normalizePRReviewSecretRefs(append(legacySecrets, wire.Secrets...))
	for name := range env {
		for _, secret := range secrets {
			if name == secret {
				return fmt.Errorf("build environment key %q cannot also be a secret reference", name)
			}
		}
	}

	enabled := true
	if wire.Enabled != nil {
		enabled = *wire.Enabled
	}
	*e = PRReviewBuildEntry{
		Enabled:       enabled,
		Framework:     wire.Framework,
		Image:         wire.Image,
		App:           wire.App,
		RootDir:       wire.RootDir,
		BuildCommand:  wire.BuildCommand,
		ArtifactPath:  wire.ArtifactPath,
		UseExistingCI: wire.UseExistingCI,
		Env:           env,
		Secrets:       secrets,
		Caches:        wire.Caches,
	}
	return nil
}

// decodePRReviewBuildEnv normalizes canonical env maps and legacy secret lists.
//
// Parameters:
//   - node: YAML node stored under a PR-review build's env key.
//
// Returns:
//   - Plaintext environment values.
//   - Legacy secret references.
//   - An error for unsupported YAML shapes.
func decodePRReviewBuildEnv(node *yaml.Node) (map[string]string, []string, error) {
	if node == nil || node.Kind == 0 {
		return nil, nil, nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		var env map[string]string
		if err := node.Decode(&env); err != nil {
			return nil, nil, fmt.Errorf("decode pr_review build env mapping: %w", err)
		}
		return env, nil, nil
	case yaml.SequenceNode:
		var refs []string
		if err := node.Decode(&refs); err != nil {
			return nil, nil, fmt.Errorf("decode legacy pr_review build env list: %w", err)
		}
		return nil, refs, nil
	default:
		return nil, nil, fmt.Errorf("pr_review build env must be a mapping")
	}
}

// normalizePRReviewSecretRefs trims and de-duplicates secret names.
//
// Parameters:
//   - refs: Secret names from canonical and legacy YAML fields.
//
// Returns:
//   - Unique non-empty names preserving declaration order.
func normalizePRReviewSecretRefs(refs []string) []string {
	normalized := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		name := strings.TrimSpace(ref)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized
}
