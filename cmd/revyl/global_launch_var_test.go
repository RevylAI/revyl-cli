package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
)

func newLaunchVarMockServer(t *testing.T, vars []api.OrgLaunchVariable) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/variables/org_launch_env", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.OrgLaunchVariablesResponse{
				Message: "ok",
				Result:  vars,
			})
		case http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			key, _ := body["key"].(string)
			value, _ := body["value"].(string)
			description, _ := body["description"].(string)
			_ = json.NewEncoder(w).Encode(api.OrgLaunchVariableResponse{
				Message: "created",
				Result: api.OrgLaunchVariable{
					ID:          "33333333-3333-3333-3333-333333333333",
					OrgID:       "org-1",
					Key:         key,
					Value:       value,
					Description: description,
				},
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/v1/variables/org_launch_env/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodPut:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			key, _ := body["key"].(string)
			value, _ := body["value"].(string)
			description, _ := body["description"].(string)
			_ = json.NewEncoder(w).Encode(api.OrgLaunchVariableResponse{
				Message: "updated",
				Result: api.OrgLaunchVariable{
					ID:                "11111111-1111-1111-1111-111111111111",
					OrgID:             "org-1",
					Key:               key,
					Value:             value,
					Description:       description,
					AttachedTestCount: 2,
				},
			})
		case http.MethodDelete:
			_ = json.NewEncoder(w).Encode(api.OrgLaunchVariableDeleteResponse{
				Message: "deleted",
				Result: api.OrgLaunchVariable{
					ID:    "11111111-1111-1111-1111-111111111111",
					OrgID: "org-1",
					Key:   "API_URL",
					Value: "https://example.com",
				},
				DetachedTestCount: 2,
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	return httptest.NewServer(mux)
}

func TestResolveLaunchVarKeyOrID(t *testing.T) {
	server := newLaunchVarMockServer(t, []api.OrgLaunchVariable{
		{
			ID:                "11111111-1111-1111-1111-111111111111",
			OrgID:             "org-1",
			Key:               "API_URL",
			Value:             "https://example.com",
			AttachedTestCount: 2,
		},
	})
	t.Cleanup(server.Close)

	client := api.NewClientWithBaseURL("test-key", server.URL)
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	byKey, err := resolveLaunchVarKeyOrID(cmd, client, "API_URL")
	if err != nil {
		t.Fatalf("resolve by key error = %v", err)
	}
	if byKey.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("resolve by key ID = %q", byKey.ID)
	}

	byID, err := resolveLaunchVarKeyOrID(cmd, client, "11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("resolve by id error = %v", err)
	}
	if byID.Key != "API_URL" {
		t.Fatalf("resolve by id key = %q", byID.Key)
	}
}

func TestRunGlobalLaunchVarListJSON(t *testing.T) {
	server := newLaunchVarMockServer(t, []api.OrgLaunchVariable{
		{
			ID:                "11111111-1111-1111-1111-111111111111",
			OrgID:             "org-1",
			Key:               "API_URL",
			Value:             "https://example.com",
			Description:       "shared endpoint",
			AttachedTestCount: 2,
		},
	})
	t.Cleanup(server.Close)

	origSetup := launchVarSetupClient
	launchVarSetupClient = func(cmd *cobra.Command) (*api.Client, error) {
		return api.NewClientWithBaseURL("test-key", server.URL), nil
	}
	t.Cleanup(func() { launchVarSetupClient = origSetup })

	root := &cobra.Command{Use: "revyl"}
	root.PersistentFlags().Bool("json", true, "")

	leaf := &cobra.Command{Use: "list"}
	leaf.RunE = runGlobalLaunchVarList
	root.AddCommand(leaf)
	leaf.SetContext(context.Background())

	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, nil); err != nil {
			t.Fatalf("runGlobalLaunchVarList() error = %v", err)
		}
	})

	if !strings.Contains(output, `"key": "API_URL"`) {
		t.Fatalf("expected JSON output to include API_URL, got %s", output)
	}
}
