package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestAppBranchErrorsNameTheBranchAndApp(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const branchID = "22222222-2222-4222-8222-222222222222"
	const buildID = "33333333-3333-4333-8333-333333333333"
	for _, test := range []struct {
		name, action  string
		args          []string
		wantErr       string
		wantNotFound  bool
		branchLookups int
	}{
		{
			name:          "missing branch",
			action:        "pin",
			args:          []string{"does-not-exist", buildID},
			wantErr:       `branch "does-not-exist" doesn't exist on "My App"; list its branches with revyl app branch list --app ` + appID,
			wantNotFound:  true,
			branchLookups: 1,
		},
		{
			name:    "version label instead of a build ID",
			action:  "pin",
			args:    []string{"zzqa-feature", "zzqa-ab-feature-2"},
			wantErr: `"zzqa-ab-feature-2" is not a build ID; find the ID of the build to pin with revyl build list --app ` + appID + ` --branch zzqa-feature`,
		},
		{
			name:    "new name with surrounding whitespace",
			action:  "rename",
			args:    []string{"zzqa-empty", " bad "},
			wantErr: `branch name " bad " can't start or end with whitespace`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("REVYL_API_KEY", "test-key")
			t.Setenv("HOME", t.TempDir())
			branchLookups := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID:
					fmt.Fprintf(w, `{"id":%q,"name":"My App","platform":"ios","default_branch_id":%q}`, appID, branchID)
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/"+appID+"/branches":
					branchLookups++
					if test.wantNotFound {
						fmt.Fprint(w, `{"branches":[],"total":0}`)
						return
					}
					fmt.Fprintf(w, `{"branches":[{"id":%q,"app_id":%q,"name":%q}],"total":1}`, branchID, appID, r.URL.Query().Get("name"))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			registered, _, err := appCmd.Find([]string{"branch", test.action})
			if err != nil {
				t.Fatal(err)
			}
			cmd := newLeafCommand(test.action, registered.RunE)
			cmd.Flags().String("app", appID, "")
			err = registered.RunE(cmd, test.args)
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
			if errors.Is(err, api.ErrBranchNotFound) != test.wantNotFound {
				t.Fatalf("errors.Is(err, api.ErrBranchNotFound) = %t, want %t", !test.wantNotFound, test.wantNotFound)
			}
			if branchLookups != test.branchLookups {
				t.Fatalf("branch lookups = %d, want %d", branchLookups, test.branchLookups)
			}
		})
	}
}
