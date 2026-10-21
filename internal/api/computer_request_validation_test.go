package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestComputerRequestSpecificOutcomeValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		command   string
		execution string
		valid     bool
	}{
		{"write_exact", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"succeeded","result":{"kind":"file_write","byte_count":3}}`, true},
		{"write_urlsafe", `{"kind":"file_write","path":"file","data":"__8="}`, `{"outcome":"succeeded","result":{"kind":"file_write","byte_count":2}}`, true},
		{"write_standard_base64", `{"kind":"file_write","path":"file","data":"//8="}`, `{"outcome":"succeeded","result":{"kind":"file_write","byte_count":2}}`, true},
		{"write_empty", `{"kind":"file_write","path":"file","data":""}`, `{"outcome":"succeeded","result":{"kind":"file_write","byte_count":0}}`, true},
		{"write_short", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"succeeded","result":{"kind":"file_write","byte_count":0}}`, false},
		{"write_encoded_length", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"succeeded","result":{"kind":"file_write","byte_count":4}}`, false},
		{"write_timeout_exit", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":7}`, false},
		{"write_timeout_no_exit", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"timed_out","termination":"unknown"}`, true},
		{"write_timeout_null_exit", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":null}`, true},
		{"read_timeout_exit", `{"kind":"file_read","path":"file"}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":0}`, false},
		{"stat_timeout_exit", `{"kind":"file_stat","path":"file"}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":0}`, false},
		{"list_timeout_exit", `{"kind":"process_list"}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":0}`, false},
		{"inspect_timeout_exit", `{"kind":"process_inspect","handle":{"computer_session_id":"11111111-1111-4111-8111-111111111111","handle_id":"33333333-3333-4333-8333-333333333333"}}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":0}`, false},
		{"terminate_timeout_exit", `{"kind":"process_terminate","handle":{"computer_session_id":"11111111-1111-4111-8111-111111111111","handle_id":"33333333-3333-4333-8333-333333333333"}}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":0}`, false},
		{"exec_timeout_exit", `{"kind":"exec","argv":["tool"]}`, `{"outcome":"timed_out","termination":"confirmed","observed_exit_code":7}`, true},
		{"exec_timeout_default", `{"kind":"exec","argv":["tool"]}`, `{"outcome":"timed_out","termination":"unknown"}`, true},
		{"exec_nonzero_default_kind", `{"kind":"exec","argv":["tool"]}`, `{"outcome":"failed","error_code":"exec_nonzero","result":{"exit_code":7}}`, true},
		{"write_nonzero_exec", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"failed","error_code":"exec_nonzero","result":{"exit_code":7}}`, false},
		{"write_access_denied", `{"kind":"file_write","path":"file","data":"YWJj"}`, `{"outcome":"failed","error_code":"access_denied"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := ComputerCommandRequest{ComputerSessionId: computerValidationID, RequestId: uuid.New()}
			if err := json.Unmarshal([]byte(tc.command), &request.Command); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"computer_session_id": request.ComputerSessionId, "operation_id": ComputerOperationID(request.ComputerSessionId, request.RequestId), "evidence": "recorded", "execution": json.RawMessage(tc.execution)})
			}))
			defer server.Close()
			_, err := NewClientWithBaseURL("fixture-key", server.URL).SubmitComputerOperation(context.Background(), request)
			if (err == nil) != tc.valid {
				t.Fatalf("response accepted=%v, want %v", err == nil, tc.valid)
			}
		})
	}
}

func TestComputerStandaloneLookupDoesNotInferRequestContext(t *testing.T) {
	for _, execution := range []string{
		`{"outcome":"succeeded","result":{"kind":"file_write","byte_count":0}}`,
		`{"outcome":"timed_out","termination":"confirmed","observed_exit_code":7}`,
	} {
		operationID := uuid.New()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"computer_session_id": computerValidationID, "operation_id": operationID, "evidence": "recorded", "execution": json.RawMessage(execution)})
		}))
		_, err := NewClientWithBaseURL("fixture-key", server.URL).GetComputerOperation(context.Background(), computerValidationID, operationID)
		server.Close()
		if err != nil {
			t.Fatal("lookup invented an originating command constraint", err)
		}
	}
}
