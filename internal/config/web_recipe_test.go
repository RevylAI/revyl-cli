package config

import (
	"reflect"
	"strings"
	"testing"
)

const webRecipeTestAppID = "55555555-5555-4555-8555-555555555555"

func TestURLOnlyWebBesidePhoneRecipeKeepsPhoneDefinition(t *testing.T) {
	phoneOnly := "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: react_native\n  profiles:\n    development:\n      android:\n        app_id: 33333333-3333-4333-8333-333333333333\n        build_commands: [./gradlew assembleDebug]\n        output_path: app.apk\n"
	withWeb := phoneOnly + "      web:\n        app_id: " + webRecipeTestAppID + "\n    preview:\n      web: {app_id: 66666666-6666-4666-8666-666666666666}\n"
	context := CompilationContext{".", "."}
	want, err := CompileConfigBytes([]byte(phoneOnly), context)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CompileConfigBytes([]byte(withWeb), context)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Framework, want.Framework) || !reflect.DeepEqual(got.Session, want.Session) || len(got.Profiles) != 2 {
		t.Fatalf("URL-only Web changed project meaning: got %#v, want %#v", got, want)
	}
	development := got.Profiles[0].Configurations
	if len(development) != 2 || !reflect.DeepEqual(development[0], want.Profiles[0].Configurations[0]) || development[1].Recipe != nil {
		t.Fatalf("URL-only Web changed the phone definition: %#v", development)
	}
	if got.ProjectConfigurationHash == want.ProjectConfigurationHash {
		t.Fatal("URL-only Web targets must affect the project hash so config pull can detect them")
	}
}

func TestWebOnlyConfigRoundTripsThroughCanonicalMarshal(t *testing.T) {
	appID := webRecipeTestAppID
	authored := AuthoredConfig{
		Project: AuthoredProject{ID: "11111111-1111-4111-8111-111111111111"},
		Build: &AuthoredBuild{Profiles: map[string]AuthoredBuildProfile{
			"development": {Web: &AuthoredWebRecipe{AppID: &appID}},
		}},
	}
	encoded, err := MarshalCanonicalConfig(authored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "framework") {
		t.Fatalf("web-only config wrote a framework:\n%s", encoded)
	}
	parsed, err := ParseAuthoredConfig(encoded)
	if err != nil {
		t.Fatalf("parse %s: %v", encoded, err)
	}
	if got := parsed.Build.Profiles["development"].Web; got == nil || got.AppID == nil || *got.AppID != appID {
		t.Fatalf("web recipe = %#v, want app %s", got, appID)
	}
}

func TestWebRecipeContractErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		yaml string
		code string
		path []string
	}{
		{
			name: "explicit null framework for web-only build",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  framework: null\n  profiles:\n    development:\n      web: {app_id: " + webRecipeTestAppID + "}\n",
			code: "invalid_contract",
			path: []string{"build", "framework"},
		},
		{
			name: "managed review of a URL-only profile",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild:\n  profiles:\n    development:\n      web: {app_id: " + webRecipeTestAppID + "}\npr_review:\n  build: {kind: revyl, profile: development}\n",
			code: "profile_url_only",
			path: []string{"pr_review", "build", "profile"},
		},
		{
			name: "build without profiles still requires a framework",
			yaml: "project: {id: 11111111-1111-4111-8111-111111111111}\nbuild: {}\n",
			code: "missing_field",
			path: []string{"build", "framework"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileConfigBytes([]byte(test.yaml), CompilationContext{".", "."})
			var configError *ConfigError
			if !errorsAs(err, &configError) {
				t.Fatalf("error = %T %v", err, err)
			}
			if configError.Stage != "contract" || configError.Code != test.code || !reflect.DeepEqual(configError.Path, test.path) {
				t.Fatalf("error = %#v, want %s at %#v", configError, test.code, test.path)
			}
		})
	}
}

func TestConfiguredWebAppID(t *testing.T) {
	otherAppID := "66666666-6666-4666-8666-666666666666"
	upper := strings.ToUpper(webRecipeTestAppID)
	profiles := func(apps ...*string) *AuthoredBuild {
		build := &AuthoredBuild{Profiles: map[string]AuthoredBuildProfile{
			"development": {Android: &AuthoredBuildRecipe{}},
		}}
		for index, app := range apps {
			build.Profiles[string(rune('a'+index))] = AuthoredBuildProfile{Web: &AuthoredWebRecipe{AppID: app}}
		}
		return build
	}
	for _, test := range []struct {
		name    string
		build   *AuthoredBuild
		want    string
		wantErr string
	}{
		{name: "no build", build: nil},
		{name: "phone only", build: profiles()},
		{name: "web recipe without app", build: profiles(nil)},
		{name: "one web app", build: profiles(&upper), want: webRecipeTestAppID},
		{name: "same web app twice", build: profiles(&upper, &[]string{webRecipeTestAppID}[0]), want: webRecipeTestAppID},
		{name: "different web apps", build: profiles(&upper, &otherAppID), wantErr: "build.profiles.a.web.app_id and build.profiles.b.web.app_id name different Web apps"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.build.ConfiguredWebAppID()
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ConfiguredWebAppID() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
