package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const (
	reviewWebProjectID = "11111111-1111-4111-8111-111111111111"
	reviewIOSAppID     = "22222222-2222-4222-8222-222222222222"
	reviewAndroidAppID = "33333333-3333-4333-8333-333333333333"
	reviewWebAppID     = "55555555-5555-4555-8555-555555555555"
)

func reviewAppIDsConfig(appIDs string) []byte {
	return []byte("project: {id: " + reviewWebProjectID + "}\npr_review:\n  build: {kind: ci_upload_to_revyl, app_ids: {" + appIDs + "}}\n")
}

func activeApps(appIDs ...string) PublicationValidationContext {
	active := make(map[string]struct{}, len(appIDs))
	for _, appID := range appIDs {
		active[appID] = struct{}{}
	}
	return PublicationValidationContext{ActiveAppIDs: active}
}

func assertConfigErrorPath(t *testing.T, err error, stage, code string, path []string) {
	t.Helper()
	assertConfigError(t, err, stage, code)
	if got := err.(*ConfigError).Path; !reflect.DeepEqual(got, path) {
		t.Fatalf("error path = %#v, want %#v", got, path)
	}
}

func TestReviewWebAppIDCompilesToCanonicalNormalizedPolicy(t *testing.T) {
	aggregate := mustCompileConfig(t, reviewAppIDsConfig("web: "+strings.ToUpper(reviewWebAppID)), CompilationContext{".", "."})

	appIDs := aggregate.ReviewPolicy.Build.AppIDs
	if appIDs == nil || appIDs.Web == nil || *appIDs.Web != reviewWebAppID {
		t.Fatalf("normalized web app id = %#v, want %s", appIDs, reviewWebAppID)
	}
	if appIDs.IOS != nil || appIDs.Android != nil {
		t.Fatalf("web-only mapping gained mobile app ids: %#v", appIDs)
	}
	encoded, err := json.Marshal(aggregate.ReviewPolicy.Build)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"ci_upload_to_revyl","app_ids":{"web":"` + reviewWebAppID + `"}}`; string(encoded) != want {
		t.Fatalf("normalized review build JSON = %s, want %s", encoded, want)
	}
}

func TestReviewWebAppIDSurvivesCanonicalYAMLAndJSONRoundTrips(t *testing.T) {
	authored, err := ParseAuthoredConfig(reviewAppIDsConfig("ios: " + reviewIOSAppID + ", web: " + reviewWebAppID))
	if err != nil {
		t.Fatal(err)
	}

	canonical, err := MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), "web: "+reviewWebAppID) {
		t.Fatalf("canonical YAML dropped the web app id:\n%s", canonical)
	}
	reparsed, err := ParseAuthoredConfig(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reparsed, authored) {
		t.Fatalf("canonical YAML round trip changed config\nwant: %#v\n got: %#v", authored, reparsed)
	}

	encoded, err := json.Marshal(authored)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AuthoredConfig
	decodeStrictJSON(t, encoded, &decoded)
	if !reflect.DeepEqual(&decoded, authored) {
		t.Fatalf("JSON round trip changed config\nwant: %#v\n got: %#v", authored, &decoded)
	}
}

func TestNormalizedReviewWebAppIDIsClonedFromAuthoredConfig(t *testing.T) {
	authored, err := ParseAuthoredConfig(reviewAppIDsConfig("web: " + reviewWebAppID))
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := NormalizeAuthoredConfig(*authored, CompilationContext{".", "."})
	if err != nil {
		t.Fatal(err)
	}

	*authored.PRReview.Build.AppIDs.Web = reviewIOSAppID

	if got := *aggregate.ReviewPolicy.Build.AppIDs.Web; got != reviewWebAppID {
		t.Fatalf("normalized web app id aliased authored config: %s", got)
	}
}

func TestReviewWebAppIDParticipatesInReviewPolicyHash(t *testing.T) {
	context := CompilationContext{".", "."}
	iosOnly := mustCompileConfig(t, reviewAppIDsConfig("ios: "+reviewIOSAppID), context)
	withWeb := mustCompileConfig(t, reviewAppIDsConfig("ios: "+reviewIOSAppID+", web: "+reviewWebAppID), context)
	otherWeb := mustCompileConfig(t, reviewAppIDsConfig("ios: "+reviewIOSAppID+", web: "+reviewAndroidAppID), context)

	hashes := map[string]struct{}{
		iosOnly.ReviewPolicy.ReviewPolicyHash:  {},
		withWeb.ReviewPolicy.ReviewPolicyHash:  {},
		otherWeb.ReviewPolicy.ReviewPolicyHash: {},
	}
	if len(hashes) != 3 {
		t.Fatalf("web app mapping does not distinguish review policy hashes: %#v", hashes)
	}
}

func TestReviewWebAppIDMatchesPythonCompilerHashes(t *testing.T) {
	aggregate := mustCompileConfig(t, reviewAppIDsConfig("android: "+reviewAndroidAppID+", web: "+reviewWebAppID), CompilationContext{".", "."})

	if got, want := aggregate.ReviewPolicy.ReviewPolicyHash, "eddfa0ffe1a233d5413b30853294d1ff51633e24badaeee3a8134af6a0e8003a"; got != want {
		t.Fatalf("review policy hash = %s, want shared value %s", got, want)
	}
	if got, want := aggregate.ProjectConfigurationHash, "0fae92121d73d41e3f216c573672302f326e41071fbb376ac3b216794b02eece"; got != want {
		t.Fatalf("project configuration hash = %s, want shared value %s", got, want)
	}
}

func TestReviewWebAppIDContractRejections(t *testing.T) {
	cases := []struct {
		name   string
		appIDs string
		code   string
		path   []string
	}{
		{name: "empty mapping", appIDs: "", code: "invalid_contract", path: []string{"pr_review", "build", "app_ids"}},
		{name: "null web app id", appIDs: "web: null", code: "invalid_contract", path: []string{"pr_review", "build", "app_ids"}},
		{name: "invalid web uuid", appIDs: "web: not-a-uuid", code: "invalid_contract", path: []string{"pr_review", "build", "ci_upload_to_revyl", "app_ids", "web"}},
		{name: "unknown platform", appIDs: "desktop: " + reviewWebAppID, code: "unknown_field", path: []string{"pr_review", "build", "ci_upload_to_revyl", "app_ids", "desktop"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := CompileConfigBytes(reviewAppIDsConfig(testCase.appIDs), CompilationContext{".", "."})
			assertConfigErrorPath(t, err, "contract", testCase.code, testCase.path)
		})
	}
}

func TestReviewWebAppIDIsAuthoredContractPlatform(t *testing.T) {
	webAppID := reviewWebAppID
	invalid := "invalid-web"
	contract := AuthoredConfig{
		Project: AuthoredProject{ID: reviewWebProjectID},
		PRReview: &AuthoredPRReview{
			Build: AuthoredReviewBuild{Kind: "ci_upload_to_revyl", AppIDs: &AuthoredExternalCIAppIDs{Web: &webAppID}},
		},
	}
	if err := contract.ValidateContract(); err != nil {
		t.Fatalf("web-only mapping rejected: %v", err)
	}

	contract.PRReview.Build.AppIDs.Web = &invalid
	assertConfigErrorPath(t, contract.ValidateContract(), "contract", "invalid_contract", []string{"pr_review", "build", "app_ids", "web"})
}

func TestReviewWebAppIDPublicationRequiresActiveOrganizationApp(t *testing.T) {
	context := CompilationContext{".", "."}
	webPath := []string{"pr_review", "build", "app_ids", "web"}
	webOnly := mustCompileConfig(t, reviewAppIDsConfig("web: "+reviewWebAppID), context)
	if err := ValidatePublication(*webOnly, activeApps(reviewWebAppID)); err != nil {
		t.Fatalf("active web app rejected: %v", err)
	}
	assertConfigErrorPath(t, ValidatePublication(*webOnly, activeApps()), "validation", "app_not_active_for_organization", webPath)
	assertConfigErrorPath(t, ValidatePublication(*webOnly, activeApps(reviewIOSAppID)), "validation", "app_not_active_for_organization", webPath)

	mixed := mustCompileConfig(t, reviewAppIDsConfig("ios: "+reviewIOSAppID+", web: "+reviewWebAppID), context)
	if err := ValidatePublication(*mixed, activeApps(reviewIOSAppID, reviewWebAppID)); err != nil {
		t.Fatalf("active mobile and web apps rejected: %v", err)
	}
	assertConfigErrorPath(t, ValidatePublication(*mixed, activeApps(reviewIOSAppID)), "validation", "app_not_active_for_organization", webPath)
	assertConfigErrorPath(t, ValidatePublication(*mixed, activeApps(reviewWebAppID)), "validation", "app_not_active_for_organization", []string{"pr_review", "build", "app_ids", "ios"})
}

func TestMobileReviewAppIDsAreUnchangedByWebSupport(t *testing.T) {
	mobile := reviewAppIDsConfig("ios: " + reviewIOSAppID + ", android: " + reviewAndroidAppID)
	aggregate := mustCompileConfig(t, mobile, CompilationContext{".", "."})

	if aggregate.ReviewPolicy.Build.AppIDs.Web != nil {
		t.Fatalf("mobile mapping gained a web app id: %#v", aggregate.ReviewPolicy.Build.AppIDs)
	}
	if got, want := aggregate.ReviewPolicy.ReviewPolicyHash, "1c085640c711b78d9cf9fbfb576c796c8d1d1feafa459896a94c56cf45bb74ba"; got != want {
		t.Fatalf("mobile review policy hash = %s, want pre-web value %s", got, want)
	}
	if got, want := aggregate.ProjectConfigurationHash, "70303bf8cbcad5ed06ed93046239e3110bf21cca47244fc93bf45672b3580681"; got != want {
		t.Fatalf("mobile project configuration hash = %s, want pre-web value %s", got, want)
	}
	encoded, err := json.Marshal(aggregate.ReviewPolicy.Build)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"kind":"ci_upload_to_revyl","app_ids":{"ios":"` + reviewIOSAppID + `","android":"` + reviewAndroidAppID + `"}}`; string(encoded) != want {
		t.Fatalf("mobile review build JSON = %s, want %s", encoded, want)
	}
	authored, err := ParseAuthoredConfig(mobile)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalCanonicalConfig(*authored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonical), "web:") {
		t.Fatalf("mobile canonical YAML gained a web key:\n%s", canonical)
	}
	if err := ValidatePublication(*aggregate, activeApps(reviewIOSAppID, reviewAndroidAppID)); err != nil {
		t.Fatalf("active mobile apps rejected: %v", err)
	}
	assertConfigErrorPath(t, ValidatePublication(*aggregate, activeApps(reviewIOSAppID)), "validation", "app_not_active_for_organization", []string{"pr_review", "build", "app_ids", "android"})
}
