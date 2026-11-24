package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Legacy pr_review structs retained for compatibility and migration; canonical
// review policy lives in AuthoredPRReview (contracts.go) and cognisim_schemas.

// PRReviewConfig is the legacy pr_review block decoded from pre-canonical config.yaml.
type PRReviewConfig struct {
	Enabled bool `yaml:"enabled"`

	Preset string `yaml:"preset,omitempty"`

	SkipDrafts bool `yaml:"skip_drafts"`

	PathFilters []string `yaml:"path_filters,omitempty"`

	LabelFilters []string `yaml:"label_filters,omitempty"`

	Actions PRReviewActions `yaml:"actions,omitempty"`

	Builds PRReviewBuilds `yaml:"builds,omitempty"`
}

type PRReviewActions struct {
	PreviewLink bool `yaml:"preview_link"`

	// StrictBuildChecks requests strict SCM build-check failure policy when set.
	StrictBuildChecks *bool `yaml:"strict_build_checks,omitempty"`

	ProofOfChanges bool `yaml:"proof_of_changes"`

	Checks []string `yaml:"checks,omitempty"`

	SystemPrompt string `yaml:"system_prompt,omitempty"`

	// ProjectRoot is a legacy checkout-relative config directory validated against
	// the selected project root during migration.
	ProjectRoot string `yaml:"project_root,omitempty"`

	Workflows []string `yaml:"workflows,omitempty"`
}

type PRReviewBuilds struct {
	IOS *PRReviewBuildEntry `yaml:"ios,omitempty"`

	Android *PRReviewBuildEntry `yaml:"android,omitempty"`
}

type PRReviewBuildEntry struct {
	Enabled bool `yaml:"enabled"`

	Framework string `yaml:"framework,omitempty"`

	Image string `yaml:"image,omitempty"`

	App string `yaml:"app,omitempty"`

	RootDir string `yaml:"root_dir,omitempty"`

	BuildCommand string `yaml:"build_command,omitempty"`

	ArtifactPath string `yaml:"artifact_path,omitempty"`

	UseExistingCI bool `yaml:"use_existing_ci,omitempty"`

	Env map[string]string `yaml:"env,omitempty"`

	Secrets []string `yaml:"secrets,omitempty"`

	Caches []BuildCache `yaml:"caches,omitempty"`
}

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
