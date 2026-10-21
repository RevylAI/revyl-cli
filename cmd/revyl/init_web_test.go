package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/config"
	"github.com/revyl/cli/internal/ui"
	"github.com/spf13/cobra"
)

func runNonInteractiveInitForTest(t *testing.T, workDir string) (string, string) {
	t.Helper()
	resetInitGlobals(t)
	initNonInteractive = true
	stubInitHasLocalCredentials(t, true)
	quiet := ui.IsQuietMode()
	ui.SetQuietMode(false)
	t.Cleanup(func() { ui.SetQuietMode(quiet) })
	gitInitForInitTest(t, workDir)
	withWorkingDir(t, workDir)
	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().Bool("dev", false, "")
	var runErr error
	stdout, stderr := captureStdoutAndStderrSeparate(t, func() { runErr = runInit(cmd, nil) })
	if runErr != nil {
		t.Fatalf("runInit() error = %v\n%s", runErr, stderr)
	}
	return stdout, stderr
}

func TestRunInitNonInteractiveWebProjectRequiresManualSetup(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "web-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExpoPreflightFile(t, workDir, "package.json", `{"name":"web-app","dependencies":{"next":"15"},"devDependencies":{"@opennextjs/cloudflare":"1"}}`)
	writeExpoPreflightFile(t, workDir, "package-lock.json", `{}`)
	writeExpoPreflightFile(t, workDir, "wrangler.jsonc", `{"main": ".open-next/worker.js"}`)

	stdout, stderr := runNonInteractiveInitForTest(t, workDir)

	if stdout != "" {
		t.Fatalf("init -y stdout = %q, want human output on stderr only", stdout)
	}
	if !strings.Contains(stderr, "https://docs.revyl.com/remote-builds/configuration#web-recipes") {
		t.Fatalf("init output lacks manual setup link:\n%s", stderr)
	}
	for _, unwanted := range []string{"Next.js", "Preview host", "versions upload", "netlify", "deployment_status"} {
		if strings.Contains(stderr, unwanted) {
			t.Fatalf("init guessed Web setup %q:\n%s", unwanted, stderr)
		}
	}
	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	authored, err := config.ParseAuthoredConfig(data)
	if err != nil {
		t.Fatalf("ParseAuthoredConfig() error = %v\n%s", err, data)
	}
	if authored.Build != nil {
		t.Fatalf("init guessed build configuration: %+v\n%s", authored.Build, data)
	}
}

func TestManualWebProfileSurvivesDetectMerge(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	draft := &initConfigDraft{
		Project:            initProjectDraft{ID: "33333333-3333-4333-8333-333333333333"},
		IdleTimeoutSeconds: 600,
	}
	authored, err := config.ParseAuthoredConfig([]byte(`project:
  id: 33333333-3333-4333-8333-333333333333
build:
  profiles:
    preview:
      web:
        app_id: ` + appID + `
        build_commands: [npm run build]
        output_path: dist
`))
	if err != nil {
		t.Fatal(err)
	}
	detected, err := draft.canonicalAuthoredConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeExistingCanonicalInitConfig(detected, authored, draft); err != nil {
		t.Fatal(err)
	}
	if got := detected.Build.Profiles["preview"].Web; !reflect.DeepEqual(got, authored.Build.Profiles["preview"].Web) {
		t.Fatalf("re-detect dropped the linked Web app: %+v", got)
	}
}

func TestRunInitNonInteractiveExpoWithWebDependenciesStaysPhoneOnly(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "expo-app")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExpoPreflightFile(t, workDir, "app.json", `{"expo":{"name":"Expo","slug":"expo","ios":{"bundleIdentifier":"com.example.expo"},"android":{"package":"com.example.expo"}}}`)
	writeExpoPreflightFile(t, workDir, "package.json", `{"name":"expo-app","dependencies":{"expo":"~50.0.0","react-native":"0.73.0","next":"15"}}`)
	writeExpoPreflightFile(t, workDir, "eas.json", `{"build":{"preview":{"android":{"buildType":"apk"}}}}`)

	_, stderr := runNonInteractiveInitForTest(t, workDir)

	if strings.Contains(stderr, "web app") || strings.Contains(stderr, "Preview host") {
		t.Fatalf("Expo init mentioned Web:\n%s", stderr)
	}
	data, err := os.ReadFile(filepath.Join(workDir, ".revyl", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "web:") {
		t.Fatalf("Expo config gained a web recipe:\n%s", data)
	}
}
