package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func resetAtlasFilterGlobals(t *testing.T) {
	t.Cleanup(func() {
		atlasLaunch, atlasLaunchStdin, atlasRange = nil, false, ""
		atlasScreenDetails, atlasEdgeRuns, atlasFlows = false, false, false
	})
}

func newAtlasGraphFamilyCommand(name string, run func(*cobra.Command, []string) error) *cobra.Command {
	cmd := &cobra.Command{Use: name, RunE: run, SilenceUsage: true, SilenceErrors: true}
	addAtlasScopeFlags(cmd, true)
	addAtlasOutputFlags(cmd)
	addAtlasLaunchFlags(cmd)
	return cmd
}

func TestAtlasGraphLaunchStdinKeepsValuesOutOfURLAndOutput(t *testing.T) {
	resetAtlasFilterGlobals(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/apps/"+appID {
			_ = json.NewEncoder(w).Encode(api.App{ID: appID})
			return
		}
		if strings.Contains(r.URL.String(), "s3cr3t") || r.URL.Query().Has("launch") {
			t.Errorf("launch value reached the URL: %s", r.URL)
		}
		if r.URL.Path == "/api/v1/apps/"+appID+"/builds" {
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
			return
		}
		if r.URL.Path != "/api/v1/atlas/v2/apps/"+appID+"/graph" {
			t.Errorf("unexpected lookup: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if header := r.Header.Get(api.AtlasLaunchTermsHeader); header != "COUPON:s3cr3t%2Cvalue:with-delims,REGION:EU,API_TOKEN" {
			t.Errorf("launch header = %q", header)
		}
		graph := atlasContractFixture()
		graph["app_id"] = appID
		graph["launch_filter"] = map[string]interface{}{"term_count": 2, "unknown_run_count": 1, "matched_run_count": 3}
		_ = json.NewEncoder(w).Encode(graph)
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newAtlasGraphFamilyCommand("graph", runAtlasGraph)
	cmd.SetIn(strings.NewReader("COUPON=s3cr3t,value:with-delims\nREGION=EU\nAPI_TOKEN\n"))
	cmd.SetArgs([]string{"--app", appID, "--launch-stdin", "--json"})
	var runErr error
	output := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if strings.Contains(output, "s3cr3t") {
		t.Fatalf("output echoed a launch value: %s", output)
	}
	var result api.AtlasResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if atlasInt(atlasMap(atlasMap(result["projection"])["launch_filter"])["matched_run_count"]) != 3 {
		t.Fatalf("launch filter result missing from projection: %s", output)
	}
}

func TestAtlasLaunchFilterTermsRejectMalformedInput(t *testing.T) {
	resetAtlasFilterGlobals(t)
	for _, test := range []struct {
		name  string
		terms []string
	}{
		{"empty key", []string{"=EU"}},
		{"colon in a bare key", []string{"A:B"}},
		{"colon in key", []string{"A:B=EU"}},
		{"padded key", []string{" REGION=EU"}},
		{"control character", []string{"REGION=E\x01U"}},
		{"too many terms", []string{"A=1", "B=1", "C=1", "D=1", "E=1", "F=1", "G=1", "H=1", "I=1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			atlasLaunch, atlasLaunchStdin = test.terms, false
			if _, err := atlasLaunchFilterTerms(&cobra.Command{}); err == nil {
				t.Fatalf("accepted %q", test.terms)
			}
		})
	}
	atlasLaunch = []string{"REGION=EU", "URL=https://a.example/x?y=1", "API_TOKEN", "EMPTY="}
	terms, err := atlasLaunchFilterTerms(&cobra.Command{})
	if err != nil || strings.Join(terms, "|") != "REGION:EU|URL:https://a.example/x?y=1|API_TOKEN|EMPTY:" {
		t.Fatalf("terms = %q, err = %v", terms, err)
	}
}

func TestAtlasOpenRefusesAValueForASecretLaunchKey(t *testing.T) {
	resetAtlasFilterGlobals(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_ = json.NewEncoder(w).Encode(api.App{ID: appID})
		case "/api/v1/apps/" + appID + "/builds":
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case "/api/v1/atlas/v2/apps/" + appID + "/graph/filters":
			if key := r.URL.Query().Get("key"); key != "API_TOKEN" {
				t.Errorf("looked up the secret flag of %q; a bare key needs no lookup", key)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"level": "values", "group": "launch", "key": "API_TOKEN", "secret": true, "values": []interface{}{}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newAtlasGraphFamilyCommand("open", runAtlasOpen)
	cmd.SetIn(strings.NewReader("REGION\nAPI_TOKEN=s3cr3t\n"))
	cmd.SetArgs([]string{"--app", appID, "--launch-stdin"})
	var runErr error
	output := captureStdoutAndStderr(t, func() { runErr = cmd.Execute() })

	if runErr == nil || !strings.Contains(runErr.Error(), "filtered only by whether they were set") {
		t.Fatalf("err = %v", runErr)
	}
	if strings.Contains(runErr.Error()+output, "s3cr3t") {
		t.Fatalf("echoed a secret value: %v %s", runErr, output)
	}
}

func TestAtlasOpenLinksPresenceFiltersAsBareKeys(t *testing.T) {
	resetAtlasFilterGlobals(t)
	if runtime.GOOS == "windows" {
		t.Skip("the fake browser is a shell script")
	}
	const appID = "11111111-1111-4111-8111-111111111111"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_ = json.NewEncoder(w).Encode(api.App{ID: appID})
		case "/api/v1/apps/" + appID + "/builds":
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case "/api/v1/atlas/v2/apps/" + appID + "/graph/filters":
			if key := r.URL.Query().Get("key"); key != "REGION" {
				t.Errorf("looked up the secret flag of %q; a bare key needs no lookup", key)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"level": "values", "group": "launch", "key": "REGION", "secret": false, "values": []interface{}{}})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	browserDir := t.TempDir()
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(browserDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", browserDir)

	cmd := newAtlasGraphFamilyCommand("open", runAtlasOpen)
	cmd.SetIn(strings.NewReader("API_TOKEN\nREGION=EU\n"))
	cmd.SetArgs([]string{"--app", appID, "--launch-stdin"})
	var runErr error
	output := captureStdoutAndStderr(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}

	link, err := url.Parse(regexp.MustCompile(`https?://\S+`).FindString(output))
	if err != nil {
		t.Fatal(err)
	}
	if got := link.Query()["launch"]; strings.Join(got, "|") != "API_TOKEN|REGION:EU" {
		t.Fatalf("launch params = %q in %q", got, output)
	}
}

func TestApplyAtlasRangeMatchesMapTimePicker(t *testing.T) {
	resetAtlasFilterGlobals(t)
	now := time.Date(2026, 11, 11, 15, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name, value, build string
		wantFrom           string
		wantRecent         int
	}{
		{"recent", "recent", "", "", atlasRecentBuilds},
		{"recent with exact build", "recent", "build-1", "", 0},
		{"one day", "1d", "", "2026-11-10T00:00:00Z", 0},
		{"thirty days", "30d", "", "2026-10-12T00:00:00Z", 0},
		{"all", "all", "", "", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			atlasRange = test.value
			query := api.AtlasQuery{BuildID: test.build}
			if err := applyAtlasRange(&cobra.Command{}, &query, now); err != nil {
				t.Fatal(err)
			}
			if query.FromTime != test.wantFrom || query.RecentBuildLimit != test.wantRecent {
				t.Fatalf("query = %+v", query)
			}
		})
	}
	atlasRange = "90d"
	if err := applyAtlasRange(&cobra.Command{}, &api.AtlasQuery{}, now); err == nil {
		t.Fatal("accepted an unknown range")
	}
	cmd := newAtlasGraphFamilyCommand("graph", runAtlasGraph)
	if err := cmd.ParseFlags([]string{"--since", "7d", "--range", "7d"}); err != nil {
		t.Fatal(err)
	}
	if err := applyAtlasRange(cmd, &api.AtlasQuery{}, now); err == nil {
		t.Fatal("combined --range with --since")
	}
}

func TestAtlasEdgeRunsRefusesLaunchFiltersBeforeCallingTheBackend(t *testing.T) {
	resetAtlasFilterGlobals(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("refused read still called the backend: %s", r.URL)
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	cmd := newAtlasGraphFamilyCommand("edge", runAtlasEdge)
	cmd.Flags().BoolVar(&atlasEdgeRuns, "runs", false, "")
	cmd.SetArgs([]string{"home", "expenses", "--app", "11111111-1111-4111-8111-111111111111", "--launch", "REGION=EU", "--runs"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "atlas edge --runs does not apply launch-variable filters") {
		t.Fatalf("err = %v", err)
	}
}

func TestAtlasScreenDetailsSendsLaunchAndStartValueTermsInTheHeader(t *testing.T) {
	resetAtlasFilterGlobals(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	const wantHeader = "REGION:EU,API_TOKEN,@initial_locale:ja_JP,URL:https:%2F%2Fa.example%2Fx%3Fy=1"
	detailsHeader := ""
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.String(), "REGION") || strings.Contains(r.URL.String(), "initial_locale") || r.URL.Query().Has("launch") {
			t.Errorf("launch term reached the URL: %s", r.URL)
		}
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_ = json.NewEncoder(w).Encode(api.App{ID: appID})
		case "/api/v1/apps/" + appID + "/builds":
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case "/api/v1/atlas/v2/apps/" + appID + "/graph":
			graph := atlasContractFixture()
			graph["app_id"] = appID
			_ = json.NewEncoder(w).Encode(graph)
		case "/api/v1/atlas/v2/apps/" + appID + "/nodes/home/details":
			detailsHeader = r.Header.Get(api.AtlasLaunchTermsHeader)
			_, _ = w.Write([]byte(`{"nodes":[{"id":"home","state_stacks":[{"id":"stack-1"}]}]}`))
		default:
			t.Errorf("unexpected lookup: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newAtlasGraphFamilyCommand("screen", runAtlasScreen)
	cmd.Flags().BoolVar(&atlasScreenDetails, "details", false, "")
	cmd.SetArgs([]string{"home", "--app", appID, "--launch", "REGION=EU", "--launch", "API_TOKEN", "--launch", "@initial_locale=ja_JP", "--launch", "URL=https://a.example/x?y=1", "--details", "--json"})
	var runErr error
	output := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if detailsHeader != wantHeader {
		t.Fatalf("details launch header = %q, want %q", detailsHeader, wantHeader)
	}
	var result api.AtlasResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(atlasMaps(atlasMap(result["details"])["state_stacks"])) != 1 {
		t.Fatalf("details missing from output: %s", output)
	}
}

func TestAtlasFiltersGroupAloneListsThatGroupsKeys(t *testing.T) {
	resetAtlasFilterGlobals(t)
	t.Cleanup(func() { atlasFilterGroup, atlasFilterKey = "", "" })
	const appID = "11111111-1111-4111-8111-111111111111"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_ = json.NewEncoder(w).Encode(api.App{ID: appID})
		case "/api/v1/apps/" + appID + "/builds":
			_, _ = w.Write([]byte(`{"items":[],"total":0}`))
		case "/api/v1/atlas/v2/apps/" + appID + "/graph/filters":
			if r.URL.Query().Has("group") || r.URL.Query().Has("key") {
				t.Errorf("group-only read asked for values: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"level":"keys","run_count":9,"runs_without_launch_data":1,"groups":[{"id":"time","keys":[{"key":"build","run_count":9}]},{"id":"launch","keys":[{"key":"REGION","run_count":4}]}]}`))
		default:
			t.Errorf("unexpected lookup: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)

	cmd := newAtlasGraphFamilyCommand("filters", runAtlasFilters)
	cmd.Flags().StringVar(&atlasFilterGroup, "group", "", "")
	cmd.Flags().StringVar(&atlasFilterKey, "key", "", "")
	cmd.SetArgs([]string{"--app", appID, "--group", "launch", "--json"})
	var runErr error
	output := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var result api.AtlasResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, output)
	}
	groups := atlasMaps(result["groups"])
	if len(groups) != 1 || atlasString(groups[0], "id") != "launch" {
		t.Fatalf("groups = %v", result["groups"])
	}
}

func TestAtlasFiltersPrintsUnknownKeyCountsAsNotAvailable(t *testing.T) {
	var result api.AtlasResponse
	if err := json.Unmarshal([]byte(`{"level":"keys","run_count":9,"runs_without_launch_data":1,"groups":[{"id":"runs","keys":[{"key":"report","run_count":null},{"key":"test"}]},{"id":"launch","keys":[{"key":"REGION","run_count":4},{"key":"EMPTY","run_count":0}]}]}`), &result); err != nil {
		t.Fatal(err)
	}

	output := captureStdoutAndStderr(t, func() { printAtlasFilters(result) })

	for _, line := range []string{"report", "test"} {
		if !regexp.MustCompile(`(?m)^\s*` + line + `\s+runs=n/a\s*$`).MatchString(output) {
			t.Errorf("%s count is not marked n/a:\n%s", line, output)
		}
	}
	if !strings.Contains(output, "runs=4") || !strings.Contains(output, "runs=0") {
		t.Errorf("known counts are missing:\n%s", output)
	}
}

func TestAtlasAppsSaysWhenMoreAppsExist(t *testing.T) {
	previousJSON, previousPlatform := atlasJSON, appListPlatform
	atlasJSON, appListPlatform = false, ""
	t.Cleanup(func() {
		atlasAppsOffset, atlasAppsLimit, atlasAppsSearch, atlasAppsAll = 0, 250, "", false
		atlasJSON, appListPlatform = previousJSON, previousPlatform
	})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/atlas/v2/index" {
			t.Errorf("unexpected lookup: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		offset := r.URL.Query().Get("offset")
		apps := []map[string]any{{"app_id": "11111111-1111-4111-8111-111111111111", "app_name": "First", "platform": "ios", "olap": map[string]any{"ready": true}}}
		if offset == "1" {
			apps[0]["app_id"], apps[0]["app_name"] = "22222222-2222-4222-8222-222222222222", "Second"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"apps": apps, "total": 2})
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	firstPage := captureStdoutAndStderr(t, func() {
		atlasAppsOffset, atlasAppsLimit = 0, 1
		if err := runAtlasApps(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	lastPage := captureStdoutAndStderr(t, func() {
		atlasAppsOffset = 1
		if err := runAtlasApps(cmd, nil); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(firstPage, "Listed apps 1-1 of 2. More exist: rerun with --offset 1") {
		t.Errorf("first page does not say more apps exist:\n%s", firstPage)
	}
	if strings.Contains(lastPage, "More exist") || !strings.Contains(lastPage, "Second") {
		t.Errorf("last page:\n%s", lastPage)
	}
}

func TestAtlasUnknownSubcommandIsNamedEvenWithFlags(t *testing.T) {
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	for _, name := range []string{"grpah", "merge"} {
		rootCmd.SetArgs([]string{"atlas", name, "--app", "11111111-1111-4111-8111-111111111111"})

		var err error
		captureStdoutAndStderr(t, func() { err = rootCmd.Execute() })

		if err == nil || !strings.Contains(err.Error(), `unknown command "`+name+`" for "revyl atlas"`) {
			t.Errorf("atlas %s: got %v", name, err)
		}
	}
	rootCmd.SetArgs([]string{"atlas", "graph", "--bogus"})
	var err error
	captureStdoutAndStderr(t, func() { err = rootCmd.Execute() })
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --bogus") {
		t.Errorf("atlas graph --bogus: got %v", err)
	}
}

func TestAtlasProjectionContractKeepsTheReaderFallbackReason(t *testing.T) {
	fallback := atlasProjectionContract(api.AtlasResponse{"projection": map[string]interface{}{"data_source": "live", "reader_fallback_reason": "schema_not_ready"}})
	clickhouse := atlasProjectionContract(api.AtlasResponse{"projection": map[string]interface{}{"data_source": "clickhouse", "reader_fallback_reason": nil}})

	if fallback["reader_fallback_reason"] != "schema_not_ready" {
		t.Errorf("fallback reason dropped: %v", fallback)
	}
	if _, ok := clickhouse["reader_fallback_reason"]; ok {
		t.Errorf("a ClickHouse read reported a fallback: %v", clickhouse)
	}
}

func TestAtlasGuideListsFiltersAmongTheFirstCommands(t *testing.T) {
	output := captureStdoutAndStderr(t, func() {
		if err := runAtlasGuide(atlasCmd, nil); err != nil {
			t.Fatal(err)
		}
	})

	for _, help := range []string{atlasCmd.Long, output} {
		if !strings.Contains(help, "revyl atlas filters --app \"My App\"") {
			t.Errorf("atlas guide does not list atlas filters:\n%s", help)
		}
	}
}
