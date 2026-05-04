package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
)

func writeExpoPreflightFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func expoPreflightConfig() *config.ProjectConfig {
	return &config.ProjectConfig{
		Build: config.BuildConfig{System: "Expo"},
	}
}

func TestEnsureExpoDevClientSchemeForBuildDetectsAppJSONScheme(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"name":"Demo","scheme":"demo-dev"}}`)
	cfg := expoPreflightConfig()

	changed, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err != nil {
		t.Fatalf("ensureExpoDevClientSchemeForBuild() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true when saving detected scheme")
	}
	expoCfg := cfg.HotReload.GetProviderConfig("expo")
	if expoCfg == nil || expoCfg.AppScheme != "demo-dev" {
		t.Fatalf("saved app_scheme = %#v, want demo-dev", expoCfg)
	}
	if cfg.HotReload.Default != "expo" {
		t.Fatalf("hotreload.default = %q, want expo", cfg.HotReload.Default)
	}
}

func TestEnsureExpoDevClientSchemeForBuildDetectsGeneratedSlugScheme(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"name":"Demo","slug":"brex-mobile"}}`)
	cfg := expoPreflightConfig()

	changed, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err != nil {
		t.Fatalf("ensureExpoDevClientSchemeForBuild() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true when saving generated slug scheme")
	}
	expoCfg := cfg.HotReload.GetProviderConfig("expo")
	if expoCfg == nil {
		t.Fatal("expected expo hotreload provider config")
	}
	if expoCfg.AppScheme != "brex-mobile" {
		t.Fatalf("saved app_scheme = %q, want brex-mobile", expoCfg.AppScheme)
	}
	if !expoCfg.UseExpPrefix {
		t.Fatal("use_exp_prefix = false, want true for generated exp+slug dev-client scheme")
	}
}

func TestEnsureExpoDevClientSchemeForBuildEnablesExpPrefixForExistingSlugConfig(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"name":"Demo","slug":"brex-mobile"}}`)
	cfg := expoPreflightConfig()
	cfg.HotReload.Providers = map[string]*config.ProviderConfig{
		"expo": {AppScheme: "brex-mobile"},
	}

	changed, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err != nil {
		t.Fatalf("ensureExpoDevClientSchemeForBuild() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true when enabling exp+ prefix")
	}
	expoCfg := cfg.HotReload.GetProviderConfig("expo")
	if expoCfg == nil || !expoCfg.UseExpPrefix {
		t.Fatalf("expo config = %#v, want use_exp_prefix=true", expoCfg)
	}
}

func TestEnsureExpoDevClientSchemeForBuildBlocksMissingStaticScheme(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"name":"Demo"}}`)
	cfg := expoPreflightConfig()

	_, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err == nil {
		t.Fatal("expected missing scheme error")
	}
	var schemeErr *expoSchemePreflightError
	if !errors.As(err, &schemeErr) {
		t.Fatalf("error type = %T, want *expoSchemePreflightError", err)
	}
	got := strings.Join(schemeErr.details, "\n")
	if !strings.Contains(got, "custom URL prefix") || !strings.Contains(got, "app.json") {
		t.Fatalf("details did not describe scheme/app.json clearly:\n%s", got)
	}
}

func TestEnsureExpoDevClientSchemeForBuildBlocksDynamicConfigWithoutRevylScheme(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.config.js", `module.exports = { expo: { name: "Demo" } };`)
	cfg := expoPreflightConfig()

	_, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err == nil {
		t.Fatal("expected missing scheme error")
	}
	var schemeErr *expoSchemePreflightError
	if !errors.As(err, &schemeErr) {
		t.Fatalf("error type = %T, want *expoSchemePreflightError", err)
	}
	got := strings.Join(schemeErr.details, "\n")
	if !strings.Contains(got, "could not auto-detect") || !strings.Contains(got, "app.config.js") {
		t.Fatalf("details did not describe dynamic config autodetect failure:\n%s", got)
	}
}

func TestEnsureExpoDevClientSchemeForBuildAllowsDynamicConfigWithRevylScheme(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.config.ts", `export default { expo: { name: "Demo", scheme: "demo-dev" } };`)
	cfg := expoPreflightConfig()
	cfg.HotReload.Providers = map[string]*config.ProviderConfig{
		"expo": {AppScheme: "demo-dev"},
	}

	changed, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err != nil {
		t.Fatalf("ensureExpoDevClientSchemeForBuild() error = %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false when Revyl scheme is already set")
	}
}

func TestEnsureExpoDevClientSchemeForBuildBlocksMismatch(t *testing.T) {
	dir := t.TempDir()
	writeExpoPreflightFile(t, dir, "app.json", `{"expo":{"scheme":"native-dev"}}`)
	cfg := expoPreflightConfig()
	cfg.HotReload.Providers = map[string]*config.ProviderConfig{
		"expo": {AppScheme: "revyl-dev"},
	}

	_, err := ensureExpoDevClientSchemeForBuild(dir, cfg)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("error = %q, want mismatch", err.Error())
	}
}
