package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testScreenPNG(t *testing.T, width, height int) string {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func TestComputerScreenActionUsesExactContract(t *testing.T) {
	id := uuid.New()
	observed := time.Now().UTC().Truncate(time.Second)
	pngData := testScreenPNG(t, 2, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/computer-runs/"+id.String()+"/screen/actions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		var action ComputerScreenAction
		if err := json.Unmarshal(body["action"], &action); err != nil || action.Action != "click" || action.X == nil || *action.X != 4 || action.Button != "right" {
			t.Fatalf("action = %#v", action)
		}
		var screenshot bool
		var via string
		_ = json.Unmarshal(body["screenshot"], &screenshot)
		_ = json.Unmarshal(body["via"], &via)
		if !screenshot || via != "cli" {
			t.Fatalf("screenshot=%t via=%q", screenshot, via)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"screenshot":{"width":2,"height":1,"observed_at":"` + observed.Format(time.RFC3339) + `","png":"` + pngData + `","cursor":{"x":4,"y":5}},"cursor":{"x":4,"y":5}}`))
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	x, y, count := 4, 5, 2
	result, err := client.PerformComputerScreenAction(context.Background(), id, ComputerScreenActionRequest{Action: ComputerScreenAction{Action: "click", X: &x, Y: &y, Button: "right", Count: &count}, Screenshot: true, Via: "cli"})
	if err != nil || result.Screenshot == nil {
		t.Fatalf("result = %#v, err=%v", result, err)
	}
}

func TestComputerScreenshotUsesRegionAndVia(t *testing.T) {
	id := uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("region") != "1,2,100,200" || r.URL.Query().Get("via") != "mcp" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"width":2,"height":1,"observed_at":"2026-10-19T00:00:00Z","png":"` + testScreenPNG(t, 2, 1) + `","cursor":null}`))
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	if _, err := client.GetComputerScreenshot(context.Background(), id, []int{1, 2, 100, 200}, "mcp"); err != nil {
		t.Fatal(err)
	}
}

func TestComputerScreenRejectsInvalidInput(t *testing.T) {
	for _, action := range []ComputerScreenAction{
		{Action: "click"},
		{Action: "type"},
		{Action: "key"},
		{Action: "scroll", Direction: "up", Amount: func() *int { value := 21; return &value }()},
	} {
		if err := ValidateComputerScreenAction(action); err == nil {
			t.Fatalf("action %#v unexpectedly validated", action)
		}
	}
}

func TestComputerScreenLeavesTextAndKeyRulesToBackend(t *testing.T) {
	for _, tc := range []struct {
		action ComputerScreenAction
		code   string
	}{
		{ComputerScreenAction{Action: "type", Text: "é\x01"}, "screen_text_unsupported"},
		{ComputerScreenAction{Action: "key", Keys: "alt+UnknownKey"}, "screen_key_unsupported"},
		{ComputerScreenAction{Action: "key", Keys: "cmd+space Return", HoldSeconds: 1}, "screen_key_unsupported"},
	} {
		t.Run(tc.action.Action+"/"+tc.code, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request ComputerScreenActionRequest
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.Action.Text != tc.action.Text || request.Action.Keys != tc.action.Keys {
					t.Error("client altered the text or key payload")
				}
				w.WriteHeader(http.StatusUnprocessableEntity)
				_ = json.NewEncoder(w).Encode(map[string]string{"detail": tc.code})
			}))
			defer server.Close()
			client := NewClientWithBaseURL("fixture-key", server.URL)
			_, err := client.PerformComputerScreenAction(context.Background(), uuid.New(), ComputerScreenActionRequest{
				Action: tc.action, Via: "cli",
			})
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.StatusCode != http.StatusUnprocessableEntity || calls != 1 {
				t.Fatalf("domain error = %v, requests = %d", err, calls)
			}
		})
	}
}
