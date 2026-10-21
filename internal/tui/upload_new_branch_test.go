package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestUploadNamesANewBranchInsteadOfCreatingIt(t *testing.T) {
	const renamedStarterID = "33333333-3333-4333-8333-333333333333"
	for _, scenario := range []struct {
		name      string
		selection api.BuildSelection
		lookup    string
		want      api.BuildUploadSessionRequest
	}{
		{name: "detected Git branch", selection: api.BuildSelection{GitBranchName: "main"}, lookup: "main", want: api.BuildUploadSessionRequest{GitBranchName: stringPointer("main")}},
		{name: "named in the picker", selection: api.BuildSelection{BranchName: "release"}, lookup: "release", want: api.BuildUploadSessionRequest{BranchName: stringPointer("release")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var sessions []api.BuildUploadSessionRequest
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if handleTestCLITraceFallback(w, r) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/api/v1/apps/"+pickerAppID:
					fmt.Fprintf(w, `{"id":%q,"default_branch_id":%q}`, pickerAppID, pickerBranchID)
				case r.URL.Path == "/api/v1/apps/"+pickerAppID+"/branches" && r.Method == http.MethodGet:
					if r.URL.Query().Get("name") != scenario.lookup {
						t.Errorf("branch lookup = %s", r.URL.RawQuery)
					}
					fmt.Fprint(w, `{"branches":[],"total_pages":1}`)
				case r.URL.Path == "/api/v1/apps/"+pickerAppID+"/builds/upload-session":
					var session api.BuildUploadSessionRequest
					if err := json.NewDecoder(r.Body).Decode(&session); err != nil {
						t.Error(err)
					}
					sessions = append(sessions, session)
					fmt.Fprintf(w, `{"upload_id":%q,"upload_url":%q,"content_type":"application/octet-stream","branch_id":%q}`, pickerBuildID, server.URL+"/artifact", renamedStarterID)
				case r.URL.Path == "/artifact":
					w.WriteHeader(http.StatusOK)
				case r.URL.Path == "/api/v1/apps/"+pickerAppID+"/builds" && r.Method == http.MethodPost:
					fmt.Fprintf(w, `{"id":%q,"version":"v1","branch_id":%q,"branch_name":%q}`, pickerBuildID, renamedStarterID, scenario.lookup)
				case r.URL.Path == "/api/v1/apps/resolve":
					fmt.Fprintf(w, `{"version_id":%q,"branch_id":%q,"branch_name":%q,"resolution_status":"resolved"}`, pickerBuildID, renamedStarterID, scenario.lookup)
				case r.URL.Path == "/api/v1/apps/builds/"+pickerBuildID:
					fmt.Fprintf(w, `{"id":%q,"app_id":%q,"version":"v1"}`, pickerBuildID, pickerAppID)
				case r.URL.Path == "/api/v1/apps/"+pickerAppID+"/branches/"+renamedStarterID:
					fmt.Fprintf(w, `{"id":%q,"name":%q}`, renamedStarterID, scenario.lookup)
				default:
					t.Errorf("unexpected request, such as creating the branch before its upload: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			file := filepath.Join(t.TempDir(), "app.apk")
			if err := os.WriteFile(file, []byte("artifact"), 0o600); err != nil {
				t.Fatal(err)
			}

			result := uploadBuildCmd(api.NewClientWithBaseURL("test", server.URL), pickerAppID, file, "v1", scenario.selection)().(BuildUploadedMsg)

			if result.Err != nil {
				t.Fatal(result.Err)
			}
			if len(sessions) != 1 || sessions[0].BranchId != nil ||
				stringOrEmpty(sessions[0].BranchName) != stringOrEmpty(scenario.want.BranchName) ||
				stringOrEmpty(sessions[0].GitBranchName) != stringOrEmpty(scenario.want.GitBranchName) {
				t.Fatalf("upload destination = %+v, want %+v", sessions, scenario.want)
			}
			if result.Branch == nil || result.Branch.ID != renamedStarterID || result.Branch.Name != scenario.lookup {
				t.Fatalf("upload reported %+v, want the branch the backend chose", result.Branch)
			}
		})
	}
}

func stringPointer(value string) *string { return &value }

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
