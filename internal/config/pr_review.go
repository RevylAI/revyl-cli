package config

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

	// LabelFilters limits automation to PRs carrying matching labels.
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

	// Env lists org launch env var names (refs only, never values) the build needs.
	Env []string `yaml:"env,omitempty"`
}
