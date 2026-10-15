package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/api"
)

func TestWorkflowConfigSetRejectsOutOfRangeRetries(t *testing.T) {
	for _, input := range []int{0, -3, 6} {
		t.Run(strconv.Itoa(input), func(t *testing.T) {
			server := newWorkflowConfigServer(t, storedWorkflowRunConfigJSON)
			cmd := newWorkflowConfigSetCommand(t, "--retries", strconv.Itoa(input), "--json")
			var notices strings.Builder
			cmd.SetErr(&notices)
			stdout := captureStdout(t, func() {
				want := fmt.Sprintf("--retries must be between %d and %d (got %d)", minRetries, maxRetries, input)
				err := cmd.RunE(cmd, []string{"smoke-tests"})
				if err == nil || err.Error() != want {
					t.Fatalf("error=%v want=%q", err, want)
				}
				if got := reportedFailureClass(t, err); got != string(analytics.FailureClassUsage) {
					t.Fatalf("failure_class=%s want=usage", got)
				}
			})
			if stdout != "" || notices.String() != "" {
				t.Fatalf("unexpected new output: stdout=%q stderr=%q", stdout, notices.String())
			}
			if server.requestCount != 0 {
				t.Fatal("invalid explicit retries must not contact the backend")
			}
		})
	}
	cmd := newWorkflowConfigSetCommand(t)
	if err := cmd.ParseFlags([]string{"--retries", "two"}); err == nil {
		t.Fatal("non-number must remain a parse error")
	}
}

func TestWorkflowConfigTransientReadAndWriteRetries(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			getCount, putCount := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					getCount++
					if getCount == 1 {
						w.WriteHeader(status)
						return
					}
					_, _ = w.Write([]byte(storedWorkflowRunConfigJSON))
					return
				}
				putCount++
				if putCount == 1 {
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(`{"message":"saved"}`))
			}))
			defer server.Close()
			withWfSettingsMockClient(t, server, workflowConfigTestID)
			cmd := newWorkflowConfigSetCommand(t, "--retries", "3")
			if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
				t.Fatal(err)
			}
			if getCount != 2 || putCount != 2 {
				t.Fatalf("gets=%d puts=%d", getCount, putCount)
			}
		})
	}
}

func TestWorkflowConfigActionErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{{403, "permission"}, {404, "revyl workflow list"}, {422, "execution_mode.retries"}} {
		err := workflowConfigActionError("write", &api.APIError{StatusCode: tc.status, Detail: "raw payload must not be printed", ValidationIssues: []api.APIValidationIssue{{Field: "execution_mode.retries"}}})
		if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "raw payload") {
			t.Fatalf("error=%s", err)
		}
		wantClass := analytics.FailureClassUsage
		if tc.status == http.StatusForbidden {
			wantClass = analytics.FailureClassAuth
		}
		if got := reportedFailureClass(t, err); got != string(wantClass) {
			t.Fatalf("status=%d failure_class=%s want=%s", tc.status, got, wantClass)
		}
	}
}

func TestWorkflowConfigShowDefaults(t *testing.T) {
	newWorkflowConfigServer(t, `{"id":"wf-uuid-001","name":"smoke-tests","run_config":null,"override_run_config":false}`)
	cmd := newLeafCommand("show", runWorkflowConfigShow)
	if err := cmd.ParseFlags([]string{"--json"}); err != nil {
		t.Fatal(err)
	}
	stdout := captureStdout(t, func() {
		if err := cmd.RunE(cmd, []string{"smoke-tests"}); err != nil {
			t.Fatal(err)
		}
	})
	if output := parseJSON(t, stdout); output["retries"] != float64(1) || !strings.Contains(stdout, "workflow_retries_default") {
		t.Fatalf("output=%s", stdout)
	}
}
