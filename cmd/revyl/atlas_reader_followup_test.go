package main

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestAtlasReaderCommandsDefaultToAllBranches(t *testing.T) {
	atlasGraphCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		value := flag.Value.String()
		t.Cleanup(func() { _ = flag.Value.Set(value) })
	})
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/apps/"+appID {
			_ = json.NewEncoder(w).Encode(api.App{ID: appID, DefaultBranchID: branchID})
			return
		}
		requests++
		query := r.URL.Query()
		if query.Has("branch_id") || query.Has("build_id") || query.Get("recent_build_limit") != "2" || query.Get("from_time") != "2026-01-01T00:00:00Z" {
			t.Errorf("Atlas request changed All scope or limits: %s", r.URL)
		}
		switch r.URL.Path {
		case "/api/v1/atlas/v2/apps/" + appID + "/graph":
			graph := atlasContractFixture()
			graph["app_id"] = appID
			_ = json.NewEncoder(w).Encode(graph)
		case "/api/v1/atlas/v2/apps/" + appID + "/entities/home/observations":
			_ = json.NewEncoder(w).Encode(api.AtlasResponse{"observations": []interface{}{}, "total": 0})
		default:
			t.Errorf("unexpected lookup: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	for _, test := range []struct {
		name string
		args []string
		run  func(*cobra.Command, []string) error
	}{
		{"brief", nil, runAtlasBrief},
		{"graph", nil, runAtlasGraph},
		{"map", nil, runAtlasMap},
		{"search", []string{"expense"}, runAtlasSearch},
		{"screen", []string{"home"}, runAtlasScreen},
		{"observations", []string{"home"}, runAtlasObservations},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := requests
			cmd := &cobra.Command{Use: test.name, RunE: test.run, SilenceUsage: true, SilenceErrors: true}
			addAtlasScopeFlags(cmd, true)
			addAtlasOutputFlags(cmd)
			cmd.SetArgs(append(test.args, "--app", appID, "--recent-build-limit", "2", "--from", "2026-01-01T00:00:00Z", "--json"))
			var runErr error
			output := captureStdout(t, func() { runErr = cmd.Execute() })
			if runErr != nil || requests != before+1 {
				t.Fatalf("All read failed: err=%v requests=%d", runErr, requests-before)
			}
			var result api.AtlasResponse
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if test.name == "graph" || test.name == "map" {
				if atlasInt(atlasMap(result["returned"])["nodes"]) != 2 {
					t.Fatalf("All read discarded graph nodes: %s", output)
				}
			}
		})
	}
}

func TestAtlasReaderCommandsPreserveBranchInFollowups(t *testing.T) {
	atlasGraphCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		value := flag.Value.String()
		t.Cleanup(func() { _ = flag.Value.Set(value) })
	})
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	wantBuildID := ""
	graph := atlasContractFixture()
	graph["app_id"] = appID
	graph["branch_id"] = branchID
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/" + appID:
			_ = json.NewEncoder(w).Encode(api.App{ID: appID})
		case "/api/v1/apps/" + appID + "/builds":
			_ = json.NewEncoder(w).Encode(api.AtlasResponse{"items": []interface{}{}, "total": 0})
		case "/api/v1/apps/" + appID + "/branches/" + branchID:
			_ = json.NewEncoder(w).Encode(map[string]string{"id": branchID, "app_id": appID, "name": "release"})
		default:
			requests++
			if r.URL.Query().Get("branch_id") != branchID || r.URL.Query().Get("build_id") != wantBuildID {
				t.Errorf("Atlas request lost branch scope: %s", r.URL)
			}
			switch r.URL.Path {
			case "/api/v1/atlas/v2/apps/" + appID + "/graph":
				_ = json.NewEncoder(w).Encode(graph)
			case "/api/v1/atlas/v2/apps/" + appID + "/entities/home/observations":
				_ = json.NewEncoder(w).Encode(api.AtlasResponse{
					"next_actions": []string{"revyl atlas brief --app " + appID},
				})
			default:
				t.Errorf("unexpected request: %s", r.URL)
				http.NotFound(w, r)
			}
		}
	}))
	defer backend.Close()
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("REVYL_BACKEND_URL", backend.URL)
	client := api.NewClientWithBaseURL("fixture-key", backend.URL)
	execute := func(name string, args []string, run func(*cobra.Command, []string) error) api.AtlasResponse {
		t.Helper()
		cmd := &cobra.Command{Use: name, RunE: run, SilenceUsage: true, SilenceErrors: true}
		addAtlasScopeFlags(cmd, true)
		addAtlasOutputFlags(cmd)
		cmd.SetArgs(append(args, "--json"))
		var runErr error
		output := captureStdout(t, func() { runErr = cmd.Execute() })
		if runErr != nil {
			t.Fatalf("%s %v: %v", name, args, runErr)
		}
		var result api.AtlasResponse
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("%s returned invalid JSON: %v", name, err)
		}
		return result
	}
	for _, build := range []string{"", buildID} {
		wantBuildID = build
		atlasMap(graph["projection"])["requested_build_id"] = build
		for _, initial := range []struct {
			name string
			args []string
			run  func(*cobra.Command, []string) error
		}{
			{"graph", nil, runAtlasGraph},
			{"brief", nil, runAtlasBrief},
			{"area", []string{"Home"}, runAtlasArea},
			{"observations", []string{"home"}, runAtlasObservations},
		} {
			t.Run(initial.name+"/build="+build, func(t *testing.T) {
				before := requests
				args := append(initial.args, "--app", appID, "--branch-id", branchID)
				if build != "" {
					args = append(args, "--build", build)
				}
				result := execute(initial.name, args, initial.run)
				if requests != before+1 {
					t.Fatalf("initial command made %d Atlas requests, want 1", requests-before)
				}
				actions := atlasStringSlice(result["next_actions"])
				if len(actions) == 0 {
					t.Fatal("initial command produced no followups")
				}
				for _, action := range actions {
					parser := csv.NewReader(strings.NewReader(action))
					parser.Comma = ' '
					words, err := parser.Read()
					if err != nil || len(words) < 3 {
						t.Fatalf("invalid followup %q: %v", action, err)
					}
					before = requests
					if words[2] == "brief" || words[2] == "graph" {
						run := runAtlasBrief
						if words[2] == "graph" {
							run = runAtlasGraph
						}
						execute(words[2], words[3:], run)
					} else {
						execute(words[2], words[3:], func(cmd *cobra.Command, _ []string) error {
							query, _, err := atlasQueryFor(cmd, client)
							if err != nil {
								return err
							}
							graph, err := client.GetAtlasGraph(cmd.Context(), query)
							if err != nil {
								return err
							}
							return printAtlasContract(cmd, graph, func(map[string]interface{}) {})
						})
					}
					if requests != before+1 {
						t.Fatalf("followup %q made %d Atlas requests, want 1", action, requests-before)
					}
				}
			})
		}
	}
}
