package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func writeExpoMetadataProjectFile(t *testing.T, projectRoot, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(projectRoot, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDeriveExpoBuildMetadataSkipsNonExpoFramework(t *testing.T) {
	metadata, err := deriveExpoBuildMetadataWithResolver(
		context.Background(),
		"",
		"react_native",
		nil,
		func(context.Context, string, map[string]string) ([]byte, error) {
			t.Fatal("dynamic Expo resolver must not run for React Native")
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("deriveExpoBuildMetadataWithResolver() error = %v", err)
	}
	if metadata != nil {
		t.Fatalf("metadata = %#v, want nil", metadata)
	}
}

func TestDeriveExpoBuildMetadataUsesStaticAppJSONScheme(t *testing.T) {
	projectRoot := t.TempDir()
	writeExpoMetadataProjectFile(t, projectRoot, "app.json", `{"expo":{"scheme":"demo-dev","slug":"ignored"}}`)

	metadata, err := deriveExpoBuildMetadataWithResolver(
		context.Background(),
		projectRoot,
		"expo",
		nil,
		func(context.Context, string, map[string]string) ([]byte, error) {
			t.Fatal("dynamic Expo resolver must not run for static app.json")
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("deriveExpoBuildMetadataWithResolver() error = %v", err)
	}
	if metadata.Scheme != "demo-dev" || metadata.UseExpPrefix {
		t.Fatalf("metadata = %#v, want scheme demo-dev without exp+ prefix", metadata)
	}
}

func TestDeriveExpoBuildMetadataUsesStaticSlugFallback(t *testing.T) {
	projectRoot := t.TempDir()
	writeExpoMetadataProjectFile(t, projectRoot, "app.json", `{"expo":{"slug":"demo-mobile"}}`)

	metadata, err := deriveExpoBuildMetadata(context.Background(), projectRoot, "expo", nil)
	if err != nil {
		t.Fatalf("deriveExpoBuildMetadata() error = %v", err)
	}
	if metadata.Scheme != "demo-mobile" || !metadata.UseExpPrefix {
		t.Fatalf("metadata = %#v, want generated exp+demo-mobile scheme", metadata)
	}
}

func TestDeriveExpoBuildMetadataResolvesDynamicConfig(t *testing.T) {
	projectRoot := t.TempDir()
	writeExpoMetadataProjectFile(t, projectRoot, "app.config.ts", `export default { scheme: "demo-dev" }`)

	metadata, err := deriveExpoBuildMetadataWithResolver(
		context.Background(),
		projectRoot,
		"expo",
		map[string]string{"EXPO_PUBLIC_API": "recipe-value"},
		func(ctx context.Context, gotRoot string, environment map[string]string) ([]byte, error) {
			if gotRoot != projectRoot {
				t.Fatalf("project root = %q, want %q", gotRoot, projectRoot)
			}
			if _, hasDeadline := ctx.Deadline(); !hasDeadline {
				t.Fatal("dynamic Expo resolution context has no deadline")
			}
			if environment["EXPO_PUBLIC_API"] != "recipe-value" {
				t.Fatalf("environment = %#v, want resolved recipe environment", environment)
			}
			return []byte(`{"scheme":["", "demo-dev"],"slug":"demo-mobile"}`), nil
		},
	)
	if err != nil {
		t.Fatalf("deriveExpoBuildMetadataWithResolver() error = %v", err)
	}
	if metadata.Scheme != "demo-dev" || metadata.UseExpPrefix {
		t.Fatalf("metadata = %#v, want resolved demo-dev scheme without exp+ prefix", metadata)
	}
}

func TestProjectRecipeProcessEnvironmentForcesToolingSafetyValues(t *testing.T) {
	t.Setenv("CI", "customer-value")
	t.Setenv("EXPO_NO_TELEMETRY", "customer-value")
	t.Setenv("EXPO_INHERITED_VALUE", "inherited")
	environment := recipeProcessEnvironment(map[string]string{
		"EXPO_RECIPE_VALUE": "recipe",
		"CI":                "recipe-value",
	})
	values := make(map[string]string)
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if ok {
			values[name] = value
		}
	}
	if values["EXPO_RECIPE_VALUE"] != "recipe" || values["EXPO_INHERITED_VALUE"] != "inherited" {
		t.Fatalf("environment = %#v, want recipe and inherited values", values)
	}
	if values["CI"] != "1" || values["EXPO_NO_TELEMETRY"] != "1" {
		t.Fatalf("environment = %#v, want forced Expo tooling values", values)
	}
}

func TestDeriveExpoBuildMetadataUsesResolvedDynamicSlugFallback(t *testing.T) {
	projectRoot := t.TempDir()
	writeExpoMetadataProjectFile(t, projectRoot, "app.config.js", `module.exports = { slug: "demo-mobile" }`)

	metadata, err := deriveExpoBuildMetadataWithResolver(
		context.Background(),
		projectRoot,
		"expo",
		nil,
		func(context.Context, string, map[string]string) ([]byte, error) {
			return []byte(`{"slug":"dynamic-demo"}`), nil
		},
	)
	if err != nil {
		t.Fatalf("deriveExpoBuildMetadataWithResolver() error = %v", err)
	}
	if metadata.Scheme != "dynamic-demo" || !metadata.UseExpPrefix {
		t.Fatalf("metadata = %#v, want generated exp+dynamic-demo scheme", metadata)
	}
}

func TestDeriveExpoBuildMetadataRequiresResolvedScheme(t *testing.T) {
	projectRoot := t.TempDir()
	writeExpoMetadataProjectFile(t, projectRoot, "app.config.js", `module.exports = {}`)

	_, err := deriveExpoBuildMetadataWithResolver(
		context.Background(),
		projectRoot,
		"expo",
		nil,
		func(context.Context, string, map[string]string) ([]byte, error) { return []byte(`{}`), nil },
	)
	if err == nil || !strings.Contains(err.Error(), "expo.scheme or expo.slug") {
		t.Fatalf("error = %v, want missing Expo scheme guidance", err)
	}
}

func TestDeriveExpoBuildMetadataReportsToolingFailure(t *testing.T) {
	projectRoot := t.TempDir()
	writeExpoMetadataProjectFile(t, projectRoot, "app.config.js", `module.exports = {}`)
	wantErr := errors.New("tool failed")

	_, err := deriveExpoBuildMetadataWithResolver(
		context.Background(),
		projectRoot,
		"expo",
		nil,
		func(context.Context, string, map[string]string) ([]byte, error) { return nil, wantErr },
	)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "npx expo config --json") {
		t.Fatalf("error = %v, want wrapped actionable tooling failure", err)
	}
}

func TestExpoDevClientBuildMetadataAttachToUsesBoundedContract(t *testing.T) {
	metadata := map[string]interface{}{
		"platform": "ios",
		artifactBuildMetadataKey: map[string]interface{}{
			"package_id": "com.example.demo",
		},
	}
	expoMetadata := expoDevClientBuildMetadata{Scheme: "demo-dev", UseExpPrefix: true}
	if err := expoMetadata.attachTo(metadata); err != nil {
		t.Fatalf("attachTo() error = %v", err)
	}

	artifactMetadata, ok := metadata[artifactBuildMetadataKey].(map[string]interface{})
	if !ok {
		t.Fatalf("artifact metadata = %#v, want object", metadata[artifactBuildMetadataKey])
	}
	got, ok := artifactMetadata[expoDevClientBuildMetadataKey].(map[string]interface{})
	if !ok {
		t.Fatalf("Expo metadata = %#v, want object", artifactMetadata[expoDevClientBuildMetadataKey])
	}
	if len(got) != 2 || got[expoDevClientSchemeMetadataKey] != "demo-dev" || got[expoDevClientUseExpPrefixMetadataKey] != true {
		t.Fatalf("Expo metadata = %#v, want exact scheme/prefix contract", got)
	}
	if metadata["platform"] != "ios" {
		t.Fatalf("existing metadata changed: %#v", metadata)
	}
	if artifactMetadata["package_id"] != "com.example.demo" {
		t.Fatalf("existing artifact metadata changed: %#v", artifactMetadata)
	}
}

func TestExpoDevClientBuildMetadataEnforcesBackendSchemeLimit(t *testing.T) {
	if _, err := newExpoDevClientBuildMetadata(strings.Repeat("a", 128), false); err != nil {
		t.Fatalf("128-byte scheme rejected: %v", err)
	}
	if _, err := newExpoDevClientBuildMetadata(strings.Repeat("a", 129), false); err == nil {
		t.Fatal("129-byte scheme accepted, want backend-aligned rejection")
	}
	if _, err := newExpoDevClientBuildMetadata(strings.Repeat("a", 125), true); err == nil {
		t.Fatal("129-byte effective exp+ scheme accepted, want backend-aligned rejection")
	}
}

func TestExpoProviderConfigFromBuildVersionReadsMetadata(t *testing.T) {
	version := &api.BuildVersion{Metadata: map[string]interface{}{
		artifactBuildMetadataKey: map[string]interface{}{
			expoDevClientBuildMetadataKey: map[string]interface{}{
				expoDevClientSchemeMetadataKey:       "demo-dev",
				expoDevClientUseExpPrefixMetadataKey: true,
			},
		},
	}}

	providerConfig, err := expoProviderConfigFromBuildVersion(version)
	if err != nil {
		t.Fatalf("expoProviderConfigFromBuildVersion() error = %v", err)
	}
	if providerConfig == nil || providerConfig.AppScheme != "demo-dev" || !providerConfig.UseExpPrefix {
		t.Fatalf("provider config = %#v, want metadata-derived scheme/prefix", providerConfig)
	}
	if providerConfig.Port != 0 || len(providerConfig.PlatformKeys) != 0 {
		t.Fatalf("provider config includes unrelated runtime wiring: %#v", providerConfig)
	}

	detailConfig, err := expoProviderConfigFromBuildVersionDetail(&api.BuildVersionDetail{Metadata: version.Metadata})
	if err != nil || detailConfig == nil || detailConfig.AppScheme != "demo-dev" || !detailConfig.UseExpPrefix {
		t.Fatalf("detail provider config = %#v err=%v, want metadata-derived scheme/prefix", detailConfig, err)
	}
	rawConfig, err := expoProviderConfigFromBuildMetadata(version.Metadata)
	if err != nil || rawConfig == nil || rawConfig.AppScheme != "demo-dev" || !rawConfig.UseExpPrefix {
		t.Fatalf("raw provider config = %#v err=%v, want metadata-derived scheme/prefix", rawConfig, err)
	}
}

func TestExpoProviderConfigFromBuildVersionDistinguishesLegacyAndMalformedMetadata(t *testing.T) {
	legacyConfig, err := expoProviderConfigFromBuildVersion(&api.BuildVersion{Metadata: map[string]interface{}{"platform": "ios"}})
	if err != nil || legacyConfig != nil {
		t.Fatalf("legacy metadata returned config=%#v err=%v, want nil, nil", legacyConfig, err)
	}

	malformed := []map[string]interface{}{
		{expoDevClientSchemeMetadataKey: "demo-dev"},
		{expoDevClientSchemeMetadataKey: "demo-dev", expoDevClientUseExpPrefixMetadataKey: "true"},
		{expoDevClientSchemeMetadataKey: "not a scheme", expoDevClientUseExpPrefixMetadataKey: false},
	}
	for _, value := range malformed {
		version := &api.BuildVersion{Metadata: map[string]interface{}{
			artifactBuildMetadataKey: map[string]interface{}{expoDevClientBuildMetadataKey: value},
		}}
		if got, malformedErr := expoProviderConfigFromBuildVersion(version); malformedErr == nil || got != nil {
			t.Fatalf("malformed metadata %#v returned config=%#v err=%v, want nil and error", value, got, malformedErr)
		}
	}
}
