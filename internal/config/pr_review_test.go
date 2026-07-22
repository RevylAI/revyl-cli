package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPRReviewActionsStrictBuildChecks verifies the SCM check policy parses.
func TestPRReviewActionsStrictBuildChecks(t *testing.T) {
	var cfg ProjectConfig
	err := yaml.Unmarshal([]byte(`
project:
  name: demo
pr_review:
  enabled: true
  actions:
    strict_build_checks: false
`), &cfg)
	if err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if cfg.PRReview == nil {
		t.Fatal("PRReview = nil, want configured PR review")
	}
	if cfg.PRReview.Actions.StrictBuildChecks == nil {
		t.Fatal("StrictBuildChecks = nil, want explicit false")
	}
	if *cfg.PRReview.Actions.StrictBuildChecks {
		t.Fatal("StrictBuildChecks = true, want false")
	}
	normalizedYAML, err := yaml.Marshal(cfg.PRReview.Actions)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	if !strings.Contains(string(normalizedYAML), "strict_build_checks: false") {
		t.Fatalf("normalized YAML lost explicit false:\n%s", normalizedYAML)
	}
}

// TestPRReviewBuildEntryCanonicalEnvironment verifies the shared env/secrets schema.
func TestPRReviewBuildEntryCanonicalEnvironment(t *testing.T) {
	var cfg ProjectConfig
	err := yaml.Unmarshal([]byte(`
project:
  name: demo
pr_review:
  enabled: true
  builds:
    ios:
      enabled: true
      image: ios-xcode
      env:
        NODE_ENV: production
      secrets:
        - API_TOKEN
      caches:
        - key: derived-data
          paths:
            - build/DerivedData
`), &cfg)
	if err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	entry := cfg.PRReview.Builds.IOS
	if entry == nil {
		t.Fatal("ios build entry is nil")
	}
	if got := entry.Env["NODE_ENV"]; got != "production" {
		t.Fatalf("Env[NODE_ENV] = %q, want production", got)
	}
	if len(entry.Secrets) != 1 || entry.Secrets[0] != "API_TOKEN" {
		t.Fatalf("Secrets = %#v, want [API_TOKEN]", entry.Secrets)
	}
	if entry.Image != "ios-xcode" {
		t.Fatalf("Image = %q, want ios-xcode", entry.Image)
	}
	if len(entry.Caches) != 1 || entry.Caches[0].Key != "derived-data" {
		t.Fatalf("Caches = %#v, want derived-data cache", entry.Caches)
	}
}

// TestPRReviewBuildEntryLegacyEnvList verifies compatibility with secret-name lists.
func TestPRReviewBuildEntryLegacyEnvList(t *testing.T) {
	var cfg ProjectConfig
	err := yaml.Unmarshal([]byte(`
project:
  name: demo
pr_review:
  enabled: true
  builds:
    ios:
      env:
        - API_TOKEN
        - API_TOKEN
      secrets:
        - SIGNING_TOKEN
`), &cfg)
	if err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	entry := cfg.PRReview.Builds.IOS
	if entry == nil {
		t.Fatal("ios build entry is nil")
	}
	if !entry.Enabled {
		t.Fatal("Enabled = false, want default true")
	}
	if len(entry.Env) != 0 {
		t.Fatalf("Env = %#v, want empty canonical env", entry.Env)
	}
	want := []string{"API_TOKEN", "SIGNING_TOKEN"}
	if len(entry.Secrets) != len(want) {
		t.Fatalf("Secrets = %#v, want %#v", entry.Secrets, want)
	}
	for index := range want {
		if entry.Secrets[index] != want[index] {
			t.Fatalf("Secrets = %#v, want %#v", entry.Secrets, want)
		}
	}

	normalizedYAML, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatalf("yaml.Marshal() error = %v", err)
	}
	normalized := string(normalizedYAML)
	if strings.Contains(normalized, "env:") {
		t.Fatalf("normalized YAML retained legacy env list:\n%s", normalized)
	}
	if !strings.Contains(normalized, "secrets:") {
		t.Fatalf("normalized YAML omitted canonical secrets:\n%s", normalized)
	}
}

// TestPRReviewBuildEntryRejectsEnvSecretCollision verifies ambiguous names fail.
func TestPRReviewBuildEntryRejectsEnvSecretCollision(t *testing.T) {
	var cfg ProjectConfig
	err := yaml.Unmarshal([]byte(`
project:
  name: demo
pr_review:
  enabled: true
  builds:
    ios:
      enabled: true
      env:
        API_TOKEN: public
      secrets:
        - API_TOKEN
`), &cfg)
	if err == nil || !strings.Contains(err.Error(), "cannot also be a secret reference") {
		t.Fatalf("yaml.Unmarshal() error = %v, want collision error", err)
	}
}
