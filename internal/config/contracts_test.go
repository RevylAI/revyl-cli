package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type namedContractFixture struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}

type invalidContractFixture struct {
	Name          string                     `json:"name"`
	Contract      string                     `json:"contract"`
	Value         json.RawMessage            `json:"value"`
	ExpectedError ConfigurationContractError `json:"expected_error"`
}

type foundationContractFixtures struct {
	AuthoredConfigs        []namedContractFixture   `json:"authored_configs"`
	EffectiveRecipes       []namedContractFixture   `json:"effective_recipes"`
	NormalizedProjects     json.RawMessage          `json:"normalized_projects"`
	ConfigurationSnapshots json.RawMessage          `json:"configuration_snapshots"`
	ReviewTargetIntents    json.RawMessage          `json:"review_target_intents"`
	Provenance             json.RawMessage          `json:"provenance"`
	InvalidContracts       []invalidContractFixture `json:"invalid_contracts"`
}

func TestAuthoredContractFixturesRoundTrip(t *testing.T) {
	fixtures := loadFoundationContractFixtures(t)
	for _, fixture := range fixtures.AuthoredConfigs {
		t.Run(fixture.Name, func(t *testing.T) {
			var contract AuthoredConfig
			decodeStrictJSON(t, fixture.Value, &contract)
			if err := contract.ValidateContract(); err != nil {
				t.Fatalf("ValidateContract() error = %v", err)
			}

			encoded, err := json.Marshal(contract)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var roundTrip AuthoredConfig
			decodeStrictJSON(t, encoded, &roundTrip)
			if !reflect.DeepEqual(roundTrip, contract) {
				t.Fatalf("round trip changed contract\nwant: %#v\n got: %#v", contract, roundTrip)
			}
		})
	}
}

func TestAuthoredReviewPathFiltersAreCanonicalRepositoryGlobs(t *testing.T) {
	valid := []string{"apps/mobile/**", "packages/shared/**", "README.md", "**"}
	invalid := []string{"", " apps/mobile/**", "apps/mobile/** ", "/apps/mobile/**", `apps\mobile\**`, "apps//mobile/**", "./apps/mobile/**", "apps/../mobile/**", "apps/mobile/", "bad\x00path"}

	for _, pathFilter := range valid {
		if err := authoredConfigWithReviewPaths(pathFilter).ValidateContract(); err != nil {
			t.Fatalf("valid path filter %q rejected: %v", pathFilter, err)
		}
	}
	for _, pathFilter := range invalid {
		err := authoredConfigWithReviewPaths(pathFilter).ValidateContract()
		if err == nil {
			t.Fatalf("invalid path filter %q accepted", pathFilter)
		}
		var contractErr *ConfigError
		if !errors.As(err, &contractErr) {
			t.Fatalf("invalid path filter %q error = %T, want ConfigurationContractError", pathFilter, err)
		}
		wantPath := []string{"pr_review", "review_triggers", "paths", "0"}
		if !reflect.DeepEqual(contractErr.Path, wantPath) {
			t.Fatalf("invalid path filter %q path = %#v, want %#v", pathFilter, contractErr.Path, wantPath)
		}
	}
}

func authoredConfigWithReviewPaths(pathFilter string) AuthoredConfig {
	profile := "review"
	commands := []string{"build"}
	return AuthoredConfig{
		Project: AuthoredProject{ID: "11111111-1111-4111-8111-111111111111"},
		Build: &AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]AuthoredBuildProfile{
				profile: {IOS: &AuthoredBuildRecipe{BuildCommands: &commands}},
			},
		},
		PRReview: &AuthoredPRReview{
			ReviewTriggers: &AuthoredReviewTriggers{Paths: []string{pathFilter}},
			Build:          AuthoredReviewBuild{Kind: "revyl", Profile: &profile},
		},
	}
}

func TestAuthoredReviewLabelFiltersAreCanonicalAndUnique(t *testing.T) {
	valid := [][]string{{"mobile"}, {"mobile app", "!skip-mobile"}}
	invalid := [][]string{{""}, {" mobile"}, {"mobile "}, {"!"}, {"mobile", "mobile"}}

	for _, labels := range valid {
		config := authoredConfigWithReviewPaths("**")
		config.PRReview.ReviewTriggers.Labels = labels
		if err := config.ValidateContract(); err != nil {
			t.Fatalf("valid label filters %#v rejected: %v", labels, err)
		}
	}
	for _, labels := range invalid {
		config := authoredConfigWithReviewPaths("**")
		config.PRReview.ReviewTriggers.Labels = labels
		err := config.ValidateContract()
		if err == nil {
			t.Fatalf("invalid label filters %#v accepted", labels)
		}
		var contractErr *ConfigError
		if !errors.As(err, &contractErr) {
			t.Fatalf("invalid label filters %#v error = %T, want ConfigError", labels, err)
		}
		if !reflect.DeepEqual(contractErr.Path[:3], []string{"pr_review", "review_triggers", "labels"}) {
			t.Fatalf("invalid label filters %#v path = %#v", labels, contractErr.Path)
		}
	}
}

func TestAuthoredStrictCICheckRequiresBuildPresence(t *testing.T) {
	build := false
	config := authoredConfigWithReviewPaths("**")
	config.PRReview.StrictCICheck = &AuthoredStrictCICheck{Build: &build}
	if err := config.ValidateContract(); err != nil {
		t.Fatalf("explicit false strict CI build check should be valid: %v", err)
	}

	config.PRReview.StrictCICheck = &AuthoredStrictCICheck{}
	if err := config.ValidateContract(); err == nil {
		t.Fatal("omitted strict CI build check should fail structural validation")
	}
}

func TestEffectiveRecipeFixturesRoundTrip(t *testing.T) {
	fixtures := loadFoundationContractFixtures(t)
	for _, fixture := range fixtures.EffectiveRecipes {
		t.Run(fixture.Name, func(t *testing.T) {
			var recipe EffectiveBuildRecipe
			decodeStrictJSON(t, fixture.Value, &recipe)
			if err := recipe.ValidateContract(); err != nil {
				t.Fatalf("ValidateContract() error = %v", err)
			}

			encoded, err := json.Marshal(recipe)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			var roundTrip EffectiveBuildRecipe
			decodeStrictJSON(t, encoded, &roundTrip)
			if !reflect.DeepEqual(roundTrip, recipe) {
				t.Fatalf("round trip changed recipe\nwant: %#v\n got: %#v", recipe, roundTrip)
			}
		})
	}
}

func TestSharedInvalidContractFixturesAreRejected(t *testing.T) {
	fixtures := loadFoundationContractFixtures(t)
	for _, fixture := range fixtures.InvalidContracts {
		t.Run(fixture.Name, func(t *testing.T) {
			var err error
			switch fixture.Contract {
			case "authored_config":
				var contract AuthoredConfig
				decodeStrictJSON(t, fixture.Value, &contract)
				err = contract.ValidateContract()
			case "effective_recipe":
				var contract EffectiveBuildRecipe
				decodeStrictJSON(t, fixture.Value, &contract)
				err = contract.ValidateContract()
			case "normalized_project", "review_target_intent":
				t.Skip("contract is currently Python-owned")
			default:
				t.Fatalf("unknown contract fixture tag %q", fixture.Contract)
			}
			if err == nil {
				t.Fatal("ValidateContract() error = nil, want contract rejection")
			}
			if fixture.ExpectedError.Stage == "" || fixture.ExpectedError.Code == "" {
				t.Fatalf("expected error identity is incomplete: %#v", fixture.ExpectedError)
			}
		})
	}
}

func TestAuthoredContractDistinguishesOmittedAndEmptyBuildCommands(t *testing.T) {
	emptyCommands := []string{}
	base := AuthoredConfig{
		Project: AuthoredProject{ID: "10000000-0000-4000-8000-000000000001"},
		Build: &AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]AuthoredBuildProfile{
				"development": {
					IOS: &AuthoredBuildRecipe{BuildCommands: &emptyCommands},
				},
			},
		},
	}
	if err := base.ValidateContract(); err != nil {
		t.Fatalf("explicit empty build_commands should be valid authoring: %v", err)
	}

	base.Build.Profiles["development"] = AuthoredBuildProfile{
		IOS: &AuthoredBuildRecipe{},
	}
	if err := base.ValidateContract(); err == nil {
		t.Fatal("omitted build_commands should fail structural validation")
	}
}

func TestAuthoredContractValidationOrderIsDeterministic(t *testing.T) {
	contract := AuthoredConfig{
		Project: AuthoredProject{ID: "10000000-0000-4000-8000-000000000001"},
		Build: &AuthoredBuild{
			Framework: "ios",
			Profiles: map[string]AuthoredBuildProfile{
				" invalid ": {IOS: &AuthoredBuildRecipe{}},
				"valid":     {IOS: &AuthoredBuildRecipe{}},
			},
		},
	}
	for range 100 {
		err := contract.ValidateContract()
		if err == nil || !strings.Contains(err.Error(), "build.profiles must be trimmed") {
			t.Fatalf("ValidateContract() error = %v, want the sorted profile-name failure", err)
		}
	}
}

func TestExternalCIAppIDValidationOrderIsDeterministic(t *testing.T) {
	iosAppID := "invalid-ios"
	androidAppID := "invalid-android"
	contract := AuthoredConfig{
		Project: AuthoredProject{ID: "10000000-0000-4000-8000-000000000001"},
		PRReview: &AuthoredPRReview{
			Build: AuthoredReviewBuild{
				Kind: "ci_upload_to_revyl",
				AppIDs: &AuthoredExternalCIAppIDs{
					IOS:     &iosAppID,
					Android: &androidAppID,
				},
			},
		},
	}
	for range 100 {
		err := contract.ValidateContract()
		var configErr *ConfigError
		if !errors.As(err, &configErr) {
			t.Fatalf("ValidateContract() error = %v, want ConfigError", err)
		}
		wantPath := []string{"pr_review", "build", "app_ids", "ios"}
		if !reflect.DeepEqual(configErr.Path, wantPath) {
			t.Fatalf("ValidateContract() path = %#v, want %#v", configErr.Path, wantPath)
		}
	}
}

func TestEffectiveRecipeRequiresRunnableCommands(t *testing.T) {
	recipe := EffectiveBuildRecipe{
		Framework:           "ios",
		SelectedProjectRoot: ".",
		ExecutionDirectory:  ".",
	}
	if err := recipe.ValidateContract(); err == nil {
		t.Fatal("ValidateContract() error = nil, want empty command rejection")
	}
}

func loadFoundationContractFixtures(t *testing.T) foundationContractFixtures {
	t.Helper()
	data, err := os.ReadFile("testdata/foundation_contract_cases.json")
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	var fixtures foundationContractFixtures
	decodeStrictJSON(t, data, &fixtures)
	return fixtures
}

func decodeStrictJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("strict JSON decode failed: %v", err)
	}
}
