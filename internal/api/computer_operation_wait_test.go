package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type computerOperationReply struct {
	status int
	body   string
}

func serveComputerOperation(t *testing.T, request ComputerCommandRequest, submit computerOperationReply, reads ...computerOperationReply) (*httptest.Server, func() (int, int)) {
	t.Helper()
	previous := computerOperationPollInterval
	computerOperationPollInterval = time.Millisecond
	t.Cleanup(func() { computerOperationPollInterval = previous })
	operationPath := fmt.Sprintf("/api/v1/computer-runs/%s/operations", request.ComputerSessionId)
	readPath := fmt.Sprintf("%s/%s", operationPath, ComputerOperationID(request.ComputerSessionId, request.RequestId))
	var mu sync.Mutex
	submits, readCount := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reply := computerOperationReply{status: http.StatusAccepted, body: "null"}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == operationPath:
			submits++
			reply = submit
			if r.Header.Get("Prefer") != "respond-async" {
				t.Error("operation submit did not opt in to an async answer")
			}
		case r.Method == http.MethodGet && r.URL.Path == readPath:
			if readCount < len(reads) {
				reply = reads[readCount]
			}
			readCount++
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			reply = computerOperationReply{status: http.StatusNotFound, body: `{"detail":"operation_not_found"}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(server.Close)
	return server, func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return submits, readCount
	}
}

func longExecRequest(t *testing.T) ComputerCommandRequest {
	t.Helper()
	request := ComputerCommandRequest{ComputerSessionId: uuid.New(), RequestId: uuid.New()}
	if err := json.Unmarshal([]byte(`{"kind":"exec","argv":["sleep","135"],"timeout_ms":180000}`), &request.Command); err != nil {
		t.Fatal(err)
	}
	return request
}

func execResult(request ComputerCommandRequest, evidence string) computerOperationReply {
	return computerOperationReply{status: http.StatusOK, body: fmt.Sprintf(
		`{"computer_session_id":%q,"operation_id":%q,"evidence":%q,"execution":{"outcome":"succeeded","result":{"kind":"exec","exit_code":0,"stdout":"","stderr":""}}}`,
		request.ComputerSessionId, ComputerOperationID(request.ComputerSessionId, request.RequestId), evidence,
	)}
}

var computerStillRunning = computerOperationReply{status: http.StatusAccepted, body: "null"}

func TestComputerSubmitWaitsForAStillRunningOperationByReadingIt(t *testing.T) {
	request := longExecRequest(t)
	server, counts := serveComputerOperation(t, request, computerStillRunning,
		computerStillRunning,
		computerOperationReply{status: http.StatusServiceUnavailable, body: `{"detail":"evidence_unavailable"}`},
		computerOperationReply{status: http.StatusBadGateway, body: "<html>edge page</html>"},
		execResult(request, "pending"),
		execResult(request, "recorded"),
	)

	result, err := NewClientWithBaseURL("fixture-key", server.URL).SubmitComputerOperation(context.Background(), request)

	if err != nil || result == nil || result.Evidence != "recorded" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if submits, reads := counts(); submits != 1 || reads != 5 {
		t.Fatalf("submits=%d reads=%d, want 1 submit and 5 reads", submits, reads)
	}
}

func TestComputerSubmitWaitEndsOnARefusalWithoutResubmitting(t *testing.T) {
	for code, status := range map[string]int{"owner_unavailable": http.StatusConflict, "operation_not_found": http.StatusNotFound} {
		t.Run(code, func(t *testing.T) {
			request := longExecRequest(t)
			server, counts := serveComputerOperation(t, request, computerStillRunning,
				computerOperationReply{status: status, body: fmt.Sprintf(`{"detail":%q}`, code)},
			)

			result, err := NewClientWithBaseURL("fixture-key", server.URL).SubmitComputerOperation(context.Background(), request)

			var apiErr *APIError
			if result != nil || !errors.As(err, &apiErr) || apiErr.Code != code {
				t.Fatalf("result = %+v, %v", result, err)
			}
			if errors.Is(err, ErrComputerOutcomeUnconfirmed) != (code == "operation_not_found") {
				t.Fatalf("unconfirmed = %v for %s", errors.Is(err, ErrComputerOutcomeUnconfirmed), code)
			}
			if submits, reads := counts(); submits != 1 || reads != 1 {
				t.Fatalf("submits=%d reads=%d, want 1 of each", submits, reads)
			}
		})
	}
}

func TestComputerSubmitWaitIsBoundedAndLeavesTheOutcomeUnconfirmed(t *testing.T) {
	request := longExecRequest(t)
	server, counts := serveComputerOperation(t, request, computerStillRunning)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := NewClientWithBaseURL("fixture-key", server.URL).SubmitComputerOperation(ctx, request)

	if result != nil || !errors.Is(err, ErrComputerOutcomeUnconfirmed) {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if submits, reads := counts(); submits != 1 || reads == 0 {
		t.Fatalf("submits=%d reads=%d, want 1 submit and some reads", submits, reads)
	}
}

func TestComputerSubmitWaitReturnsUnrecordedEvidenceAtItsBound(t *testing.T) {
	request := longExecRequest(t)
	unrecorded := execResult(request, "pending")
	reads := make([]computerOperationReply, 10_000)
	for index := range reads {
		reads[index] = unrecorded
	}
	server, _ := serveComputerOperation(t, request, computerStillRunning, reads...)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := NewClientWithBaseURL("fixture-key", server.URL).SubmitComputerOperation(ctx, request)

	if err != nil || result == nil || result.Evidence != "pending" {
		t.Fatalf("result = %+v, %v", result, err)
	}
}
