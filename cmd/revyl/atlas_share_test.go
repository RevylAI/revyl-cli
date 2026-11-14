package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/toolcatalog"
	"github.com/spf13/cobra"
)

const (
	structureAppID    = "11111111-1111-4111-8111-111111111111"
	structureScreenID = "22222222-2222-4222-8222-222222222222"
	structureToken    = "55555555-5555-4555-8555-555555555555"
)

type structureRequest struct {
	method, path string
	body         map[string]interface{}
}

func structureBackend(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) *[]structureRequest {
	t.Helper()
	resetStructureScope := func() {
		atlasApp, atlasBuild, atlasFrom, atlasTo, atlasSince, atlasRange = "", "", "", "", "", ""
		atlasRecentBuildLimit, atlasJSON = 0, false
		atlasLaunch, atlasLaunchStdin = nil, false
		atlasDeviceModel, atlasRuntime, atlasTestID, atlasReportID, atlasWorkflowExecutionID, atlasSourceKind = "", "", "", "", "", ""
	}
	resetStructureScope()
	t.Cleanup(resetStructureScope)
	requests := []structureRequest{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/apps/"+structureAppID {
			_ = json.NewEncoder(w).Encode(api.App{ID: structureAppID})
			return
		}
		if r.URL.Path == "/api/v1/apps/"+structureAppID+"/builds" {
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
			return
		}
		request := structureRequest{method: r.Method, path: r.URL.Path}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &request.body)
		}
		requests = append(requests, request)
		if !handle(w, r) {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(backend.Close)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	return &requests
}

func runStructureCommand(t *testing.T, command *cobra.Command, args ...string) (string, error) {
	t.Helper()
	command.SilenceUsage, command.SilenceErrors = true, true
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), err
}

func TestAtlasShareCatalogMarksWritesAndRefusesRunFilters(t *testing.T) {
	for _, name := range []string{"atlas.share.create", "atlas.share.revoke"} {
		tool, err := findInvestigationTool(rootCmd, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tool.ReadOnly {
			t.Errorf("%s must be marked as a write", name)
		}
	}
	status, err := findInvestigationTool(rootCmd, "atlas.share.status")
	if err != nil || !status.ReadOnly {
		t.Fatalf("atlas.share.status must be a read: %v", err)
	}
	for _, name := range []string{"atlas.share.create", "atlas.share.status", "atlas.share.revoke"} {
		tool, _ := findInvestigationTool(rootCmd, name)
		for _, refused := range []string{"launch", "device_model", "runtime", "test_id", "report_id", "workflow_execution_id"} {
			if _, ok := tool.InputSchema.Properties[refused]; ok {
				t.Errorf("%s exposes %s, which shares cannot carry", name, refused)
			}
		}
	}
}

func TestAtlasCatalogHasNoStructureOrAnnotationTriageWrites(t *testing.T) {
	for _, name := range []string{
		"atlas.merge", "atlas.split", "atlas.curate",
		"annotations.move", "annotations.edit", "annotations.severity", "annotations.delete",
		"annotations.resolve", "annotations.dismiss", "annotations.reopen",
	} {
		if _, err := findInvestigationTool(rootCmd, name); err == nil {
			t.Errorf("%s must not be in the tool catalog", name)
		}
	}
	for _, name := range []string{"triage-annotations", "curate-structure"} {
		for _, cookbook := range toolcatalog.Cookbooks() {
			if cookbook.Name == name {
				t.Errorf("cookbook %s must not ship", name)
			}
		}
	}
	for _, name := range []string{"merge", "split", "curate", "hide", "entry-root"} {
		if command, _, err := rootCmd.Find([]string{"atlas", name}); err == nil && command.Name() == name {
			t.Errorf("revyl atlas %s must not be registered", name)
		}
	}
}

func TestAtlasShareRefusesDeviceLaunchAndRunFilters(t *testing.T) {
	structureBackend(t, func(http.ResponseWriter, *http.Request) bool { return false })
	for _, filter := range [][]string{
		{"--launch", "REGION=EU"},
		{"--launch-stdin"},
		{"--device", "Pixel 7"},
		{"--os", "Android 14"},
		{"--test-id", structureScreenID},
		{"--report-id", structureScreenID},
		{"--workflow-execution-id", structureScreenID},
	} {
		for name, build := range map[string]func() *cobra.Command{
			"create": newAtlasShareCreateCommand,
			"status": newAtlasShareStatusCommand,
			"revoke": newAtlasShareRevokeCommand,
		} {
			_, err := runStructureCommand(t, build(), append([]string{"--app", structureAppID}, filter...)...)
			if err == nil || !strings.Contains(err.Error(), "does not carry device, launch or run filters") {
				t.Errorf("share %s %v: err = %v", name, filter, err)
			}
		}
	}
}

func TestAtlasShareCreatePrintsLinkOnlyFromCreate(t *testing.T) {
	const link = "https://app.example.test/atlas/share?token=" + structureToken
	requests := structureBackend(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/atlas/v2/apps/"+structureAppID+"/share" || (r.Method != http.MethodPost && r.Method != http.MethodGet) {
			return false
		}
		_, _ = w.Write([]byte(`{"enabled":true,"share_id":"share-1","token":"` + structureToken + `","shareable_link":"` + link + `","expires_at":"2026-12-11T00:00:00Z"}`))
		return true
	})

	output, err := runStructureCommand(t, newAtlasShareCreateCommand(), "--app", structureAppID, "--expiration-hours", "48", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]interface{}
	if err := json.Unmarshal([]byte(output), &created); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if created["contract"] != "atlas_share.v1" || created["shareable_link"] != link {
		t.Fatalf("create output = %s", output)
	}
	if _, leaked := created["token"]; leaked {
		t.Fatalf("create output repeats the token separately: %s", output)
	}
	if body := (*requests)[0].body; body["expiration_hours"] != float64(48) {
		t.Fatalf("create body = %v", body)
	}

	output, err = runStructureCommand(t, newAtlasShareStatusCommand(), "--app", structureAppID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, structureToken) || !strings.Contains(output, `"enabled": true`) {
		t.Fatalf("status output must report the link without printing it: %s", output)
	}
}

func TestAtlasShareRevokeResolvesTokenFromScope(t *testing.T) {
	requests := structureBackend(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/atlas/v2/apps/"+structureAppID+"/share":
			_, _ = w.Write([]byte(`{"enabled":true,"share_id":"share-1","token":"` + structureToken + `"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/atlas/v2/apps/"+structureAppID+"/share/"+structureToken:
			_, _ = w.Write([]byte(`{"enabled":false}`))
		default:
			return false
		}
		return true
	})
	output, err := runStructureCommand(t, newAtlasShareRevokeCommand(), "--app", structureAppID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*requests) != 2 || (*requests)[1].method != http.MethodDelete {
		t.Fatalf("requests = %+v", *requests)
	}
	if strings.Contains(output, structureToken) || !strings.Contains(output, `"enabled": false`) {
		t.Fatalf("revoke output = %s", output)
	}
}
