package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const urlOnlyBuildDefinitionTestHash = "00556d4b78abbf61d3ee4f979d6b933f98c8a1cb617d686eb253344293736f19"

func compileURLOnlyTestConfig(t *testing.T, build string) *NormalizedProjectAggregate {
	t.Helper()
	aggregate, err := CompileConfigBytes([]byte("project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n"+build), CompilationContext{".", "."})
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}

func assertURLOnlySelectionError(t *testing.T, err error, profile string) {
	t.Helper()
	var configError *ConfigError
	if !errors.As(err, &configError) || configError.Code != "profile_url_only" || !reflect.DeepEqual(configError.Path, []string{"build", "profiles", profile, "web"}) || configError.Message != URLOnlyProfileMessage(profile) {
		t.Fatalf("URL-only selection error = %#v", err)
	}
}

func TestURLOnlyWebProfileNormalizesToRecipelessConfiguration(t *testing.T) {
	aggregate := compileURLOnlyTestConfig(t, "  profiles:\n    preview:\n      web: {app_id: "+webRecipeTestAppID+"}\n")
	if aggregate.Framework == nil || *aggregate.Framework != "web" || len(aggregate.Profiles) != 1 || len(aggregate.Profiles[0].Configurations) != 1 {
		t.Fatalf("URL-only aggregate = %#v", aggregate)
	}
	configuration := aggregate.Profiles[0].Configurations[0]
	if configuration.Platform != "web" || configuration.Recipe != nil || configuration.AppID == nil || *configuration.AppID != webRecipeTestAppID || configuration.BuildDefinitionHash != urlOnlyBuildDefinitionTestHash {
		t.Fatalf("URL-only configuration = %#v", configuration)
	}
	encoded, err := json.Marshal(aggregate)
	if err != nil || !strings.Contains(string(encoded), `"recipe":null`) {
		t.Fatalf("URL-only JSON = %s, %v", encoded, err)
	}
	var roundTrip NormalizedProjectAggregate
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(&roundTrip, aggregate) {
		t.Fatalf("URL-only roundtrip = %#v, %v", roundTrip, err)
	}
	for _, explicit := range [][2]string{{"", ""}, {"preview", ""}, {"preview", "web"}, {"", "web"}} {
		_, err := ResolveProfilePlatform(roundTrip, explicit[0], explicit[1], false)
		assertURLOnlySelectionError(t, err, "preview")
	}
}

func TestSelectionSkipsURLOnlyWebBesidePhoneRecipe(t *testing.T) {
	aggregate := compileURLOnlyTestConfig(t, "  framework: react_native\n  profiles:\n    development:\n      android: {build_commands: [./gradlew assembleDebug], output_path: app.apk}\n      web: {app_id: "+webRecipeTestAppID+"}\n")
	selection, err := ResolveProfilePlatform(*aggregate, "", "", false)
	if err != nil || !reflect.DeepEqual(selection.Resolved, &ResolvedProfilePlatform{Profile: "development", Platform: "android"}) {
		t.Fatalf("selection = %#v, %v", selection, err)
	}
	_, err = ResolveProfilePlatform(*aggregate, "development", "web", false)
	assertURLOnlySelectionError(t, err, "development")
}

func TestURLOnlyWebAppIDAffectsSemanticComparison(t *testing.T) {
	prefix := "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  profiles:\n    preview:\n      web: "
	comparison, err := CompareConfigSemantics(
		[]byte(prefix+"{app_id: "+webRecipeTestAppID+"}\n"),
		[]byte(prefix+"{app_id: 66666666-6666-4666-8666-666666666666}\n"),
		CompilationContext{".", "."},
	)
	if err != nil || comparison.Equal || comparison.LeftHash == comparison.RightHash {
		t.Fatalf("URL-only app edit was invisible to config pull: %#v, %v", comparison, err)
	}
}

func TestURLOnlyWebAppsValidateOrganizationOwnership(t *testing.T) {
	aggregate := compileURLOnlyTestConfig(t, "  profiles:\n    preview:\n      web: {app_id: "+webRecipeTestAppID+"}\n")
	err := ValidatePublication(*aggregate, PublicationValidationContext{})
	var configError *ConfigError
	if !errors.As(err, &configError) || configError.Code != "app_not_active_for_organization" || !reflect.DeepEqual(configError.Path, []string{"build", "profiles", "preview", "web", "app_id"}) {
		t.Fatalf("unknown app accepted: %v", err)
	}
	if err := ValidatePublication(*aggregate, PublicationValidationContext{ActiveAppIDs: map[string]struct{}{webRecipeTestAppID: {}}}); err != nil {
		t.Fatal(err)
	}
}
