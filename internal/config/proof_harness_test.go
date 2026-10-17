package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProofHarnessKindsRoundTrip(t *testing.T) {
	for _, kind := range []string{"revyl", "cursor", "capy"} {
		t.Run(kind, func(t *testing.T) {
			data := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {ios: 22222222-2222-4222-8222-222222222222}}\n  proof_of_changes:\n    enabled: true\n    harness: {kind: " + kind + "}\n")
			authored, err := ParseAuthoredConfig(data)
			if err != nil {
				t.Fatal(err)
			}
			aggregate, err := CompileConfigBytes(data, CompilationContext{".", "."})
			if err != nil {
				t.Fatal(err)
			}
			if got := aggregate.ReviewPolicy.ProofOfChanges.Harness.Kind; got != kind {
				t.Fatalf("normalized harness = %q, want %q", got, kind)
			}
			canonical, err := MarshalCanonicalConfig(*authored)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip, err := ParseAuthoredConfig(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, authored) {
				t.Fatalf("canonical round trip changed the configuration: %#v", roundTrip)
			}
		})
	}
}

func TestProofHarnessRejectsUnknownKindAndInvalidModels(t *testing.T) {
	modelID := "test-model"
	capyProjectID := "capy-project"
	blank := " "
	for _, test := range []struct {
		name    string
		harness AuthoredProofHarness
		field   string
	}{
		{name: "unknown", harness: AuthoredProofHarness{Kind: "unknown"}, field: "kind"},
		{name: "revyl model", harness: AuthoredProofHarness{Kind: "revyl", ModelID: &modelID}, field: "model_id"},
		{name: "capy model", harness: AuthoredProofHarness{Kind: "capy", ModelID: &modelID}, field: "model_id"},
		{name: "revyl capy project", harness: AuthoredProofHarness{Kind: "revyl", CapyProjectID: &capyProjectID}, field: "capy_project_id"},
		{name: "cursor capy project", harness: AuthoredProofHarness{Kind: "cursor", CapyProjectID: &capyProjectID}, field: "capy_project_id"},
		{name: "blank capy project", harness: AuthoredProofHarness{Kind: "capy", CapyProjectID: &blank}, field: "capy_project_id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authored := authoredConfigWithReviewPaths("**")
			authored.PRReview.ProofOfChanges = &AuthoredProofOfChanges{Harness: &test.harness}
			err := authored.ValidateContract()
			var configErr *ConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("ValidateContract() error = %v, want ConfigError", err)
			}
			wantPath := []string{"pr_review", "proof_of_changes", "harness", test.field}
			if !reflect.DeepEqual(configErr.Path, wantPath) {
				t.Fatalf("error path = %v, want %v", configErr.Path, wantPath)
			}
		})
	}
}

func TestCapyHarnessAcceptsDefaultAndAllowlistedModels(t *testing.T) {
	for _, value := range []string{
		"null",
		"openai/gpt-6-luna",
		"openai/gpt-6.1-sol",
		"anthropic/claude-opus-5-5",
		"anthropic/claude-sonnet-5-5",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseAuthoredConfig([]byte("project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {ios: 22222222-2222-4222-8222-222222222222}}\n  proof_of_changes:\n    harness: {kind: capy, model_id: " + value + "}\n"))
			if err != nil {
				t.Fatalf("ParseAuthoredConfig() error = %v, want allowlisted model", err)
			}
		})
	}
}

func TestCapyHarnessCarriesAPinnedProjectIntoTheNormalizedPolicy(t *testing.T) {
	data := []byte("project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {ios: 22222222-2222-4222-8222-222222222222}}\n  proof_of_changes:\n    enabled: true\n    harness: {kind: capy, capy_project_id: capy-project}\n")
	aggregate, err := CompileConfigBytes(data, CompilationContext{".", "."})
	if err != nil {
		t.Fatal(err)
	}
	harness := aggregate.ReviewPolicy.ProofOfChanges.Harness
	if harness.CapyProjectID == nil || *harness.CapyProjectID != "capy-project" {
		t.Fatalf("normalized harness = %#v, want the pinned Capy project", harness)
	}
}

func TestCapyHarnessRejectsUnknownModel(t *testing.T) {
	_, err := ParseAuthoredConfig([]byte("project: {id: 11111111-1111-4111-8111-111111111111}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {ios: 22222222-2222-4222-8222-222222222222}}\n  proof_of_changes:\n    harness: {kind: capy, model_id: test-model}\n"))
	if err == nil || !strings.Contains(err.Error(), "model_id") {
		t.Fatalf("ParseAuthoredConfig() error = %v, want model_id rejection", err)
	}
}
