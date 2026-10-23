package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/config"
)

func TestConfigPushRoundTripsProofHarnessKinds(t *testing.T) {
	for _, kind := range []string{"revyl", "cursor", "capy", "devin"} {
		t.Run(kind, func(t *testing.T) {
			local := prReviewConfigContext(t, true)
			local.Authored.PRReview.ProofOfChanges.Harness.Kind = kind
			if kind == "cursor" {
				modelID := "test-model"
				local.Authored.PRReview.ProofOfChanges.Harness.ModelID = &modelID
			}
			if kind == "capy" {
				capyProjectID := "capy-project"
				local.Authored.PRReview.ProofOfChanges.Harness.CapyProjectID = &capyProjectID
			}
			client := &fakeProjectConfigurationClient{
				readResult: &api.ProjectConfigurationReadResponse{State: api.ProjectConfigurationReadResponseStateAbsent},
				replaceResult: &api.ProjectConfigurationReplaceResponse{
					Outcome: api.ProjectConfigurationReplaceResponseOutcomeApplied,
				},
			}
			withProjectConfigurationDependencies(t, local, "token", client)
			if err := runConfigPush(testConfigCommand(), nil); err != nil {
				t.Fatal(err)
			}
			if client.replaceRequest == nil {
				t.Fatal("config push did not publish the configuration")
			}
			payload, err := json.Marshal(client.replaceRequest.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			var remote api.AuthoredRevylConfig
			if err := json.Unmarshal(payload, &remote); err != nil {
				t.Fatal(err)
			}
			value, err := remote.PrReview.ProofOfChanges.Harness.ValueByDiscriminator()
			if err != nil {
				t.Fatal(err)
			}
			if kind == "capy" {
				if _, ok := value.(api.CapyProofHarness); !ok {
					t.Fatalf("remote harness type = %T, want CapyProofHarness", value)
				}
			}
			if kind == "devin" {
				if _, ok := value.(api.DevinProofHarness); !ok {
					t.Fatalf("remote harness type = %T, want DevinProofHarness", value)
				}
			}
			roundTrip, err := authoredConfigFromAPI(remote)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, *local.Authored) {
				t.Fatalf("remote round trip changed the configuration: %#v", roundTrip)
			}
			if _, err := config.MarshalCanonicalConfig(roundTrip); err != nil {
				t.Fatalf("pulled configuration is invalid: %v", err)
			}
		})
	}
}

func TestRemoteProofHarnessRejectsUnknownKindAndInvalidCapyModel(t *testing.T) {
	for _, harness := range []string{`{"kind":"unknown"}`, `{"kind":"capy","model_id":"test-model"}`} {
		t.Run(harness, func(t *testing.T) {
			var remote api.AuthoredRevylConfig
			data := `{"project":{"id":"11111111-1111-4111-8111-111111111111"},"pr_review":{"build":{"kind":"ci_upload_to_revyl","app_ids":{"ios":"22222222-2222-4222-8222-222222222222"}},"proof_of_changes":{"harness":` + harness + `}}}`
			if err := json.Unmarshal([]byte(data), &remote); err != nil {
				t.Fatal(err)
			}
			if _, err := authoredConfigFromAPI(remote); err == nil {
				t.Fatal("remote configuration accepted an invalid proof harness")
			}
		})
	}
}

func TestRemoteProofHarnessAllowsCapyDefaultModel(t *testing.T) {
	var remote api.AuthoredRevylConfig
	data := `{"project":{"id":"11111111-1111-4111-8111-111111111111"},"pr_review":{"build":{"kind":"ci_upload_to_revyl","app_ids":{"ios":"22222222-2222-4222-8222-222222222222"}},"proof_of_changes":{"harness":{"kind":"capy","model_id":null}}}}`
	if err := json.Unmarshal([]byte(data), &remote); err != nil {
		t.Fatal(err)
	}
	if _, err := authoredConfigFromAPI(remote); err != nil {
		t.Fatalf("remote configuration rejected Capy's default model: %v", err)
	}
}

func TestInitAndGithubSetupPreserveCapyProofHarness(t *testing.T) {
	local := prReviewConfigContext(t, true)
	local.Authored.PRReview.ProofOfChanges.Harness.Kind = "capy"
	appID := githubSetupIOSAppID
	local.Authored.Build.Profiles["preview"].IOS.AppID = &appID
	authored := config.AuthoredConfig{Project: local.Authored.Project}
	if err := mergeExistingCanonicalInitConfig(&authored, local.Authored, &initConfigDraft{}); err != nil {
		t.Fatal(err)
	}
	local = githubSetupContext(t, authored)
	candidate, changed, err := completeReviewSetupConfiguration(testConfigCommand(), nil, local)
	if err != nil {
		t.Fatal(err)
	}
	if changed || !reflect.DeepEqual(candidate.PRReview, local.Authored.PRReview) {
		t.Fatalf("setup changed the existing Capy policy: %#v", candidate.PRReview)
	}
	data, err := config.MarshalCanonicalConfig(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "kind: capy") {
		t.Fatalf("setup dropped the Capy harness: %s", data)
	}
}
