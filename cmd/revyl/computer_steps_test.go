package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func computerStepServer(t *testing.T, status int, answer string, sent *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/steps") {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(sent); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(server.Close)
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	return server
}

func TestComputerInstructionSendsTheStepAndReportsTheEngine(t *testing.T) {
	var sent map[string]any
	computerStepServer(t, http.StatusOK, `{"step_type":"instruction","success":true,"status":"passed","status_reason":null,"validation_result":null,"extracted_data":null,"point":{"x":40,"y":50},"engine":"local","local_engine":"mtplx","attempts":2,"seconds":3.1}`, &sent)

	stdout, _, err := executeComputerScreen(t, "instruction", uuid.NewString(), "Open the File menu", "--engine", "local")

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"step_type": "instruction", "description": "Open the File menu", "engine": "local", "via": "cli"}
	for key, value := range want {
		if sent[key] != value {
			t.Fatalf("request body = %#v", sent)
		}
	}
	if !strings.Contains(stdout, "instruction passed engine=local attempts=2 seconds=3.10 local_engine=mtplx point=(40,50)") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestComputerValidationThatIsNotAPassExitsOneWithItsJSON(t *testing.T) {
	for _, status := range []string{"failed", "uncertain"} {
		t.Run(status, func(t *testing.T) {
			var sent map[string]any
			validation := "null"
			if status == "failed" {
				validation = "false"
			}
			computerStepServer(t, http.StatusOK, `{"step_type":"validation","success":false,"status":"`+status+`","status_reason":null,"validation_result":`+validation+`,"extracted_data":null,"point":null,"engine":"local","attempts":2,"seconds":3.5}`, &sent)

			stdout, _, err := executeComputerScreen(t, "validation", uuid.NewString(), "The cart shows 2 items", "--json")

			var exit *commandExitError
			if !errors.As(err, &exit) || exit.code != 1 {
				t.Fatalf("err = %v", err)
			}
			var printed map[string]any
			if json.Unmarshal([]byte(stdout), &printed) != nil || printed["status"] != status || printed["engine"] != "local" || printed["attempts"] != float64(2) {
				t.Fatalf("stdout = %q", stdout)
			}
			if sent["engine"] != "auto" {
				t.Fatalf("default engine = %v", sent["engine"])
			}
		})
	}
}

func TestComputerExtractPrintsTheValue(t *testing.T) {
	var sent map[string]any
	computerStepServer(t, http.StatusOK, `{"step_type":"extract","success":true,"status":"passed","status_reason":null,"validation_result":null,"extracted_data":{"information":"A-1042"},"point":null,"engine":"local","attempts":1,"seconds":1.2}`, &sent)

	stdout, _, err := executeComputerScreen(t, "extract", uuid.NewString(), "the order number")

	if err != nil || !strings.HasSuffix(stdout, "A-1042\n") {
		t.Fatalf("stdout = %q err = %v", stdout, err)
	}
}

func TestComputerStepNoEngineCanAnswerNamesWhy(t *testing.T) {
	var sent map[string]any
	computerStepServer(t, http.StatusConflict, `{"detail":"step_engine_unavailable"}`, &sent)

	_, _, err := executeComputerScreen(t, "validation", uuid.NewString(), "The cart shows 2 items")

	if err == nil || !strings.Contains(err.Error(), "own vision model is not answering") || !strings.Contains(err.Error(), "--inference") {
		t.Fatalf("err = %v", err)
	}
}

func TestComputerStepRefusesAnUnknownEngineBeforeSending(t *testing.T) {
	t.Setenv("REVYL_BACKEND_URL", "http://127.0.0.1:1")
	t.Setenv("REVYL_API_KEY", "fixture-key")

	_, _, err := executeComputerScreen(t, "instruction", uuid.NewString(), "Open the File menu", "--engine", "fast")

	if err == nil || !strings.Contains(err.Error(), "--engine must be auto, local or hosted") {
		t.Fatalf("err = %v", err)
	}
}
