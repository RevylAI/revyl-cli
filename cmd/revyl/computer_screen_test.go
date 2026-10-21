package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func cliScreenFixture(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func executeComputerScreen(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := &cobra.Command{Use: "revyl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newComputerCommand())
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"computer"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func TestComputerScreenClickSendsExactBodyAndWritesOut(t *testing.T) {
	data := cliScreenFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/screen/actions") {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Action     map[string]any `json:"action"`
			Screenshot bool           `json:"screenshot"`
			Via        string         `json:"via"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Action["action"] != "click" || body.Action["button"] != "right" || body.Action["count"] != float64(2) || body.Screenshot != true || body.Via != "cli" {
			t.Fatalf("request body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"screenshot":{"width":2,"height":1,"observed_at":"2026-10-19T00:00:00Z","png":"` + base64.StdEncoding.EncodeToString(data) + `","cursor":{"x":4,"y":5}},"cursor":{"x":4,"y":5}}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	outPath := filepath.Join(t.TempDir(), "screen.png")
	_, stderr, err := executeComputerScreen(t, "click", uuid.NewString(), "--x", "4", "--y", "5", "--button", "right", "--count", "2", "--out", outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(outPath); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("saved PNG = %d bytes, err=%v", len(got), err)
	}
	if !strings.Contains(stderr, "Saved ") || !strings.Contains(stderr, "Cursor (4, 5)") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestComputerScreenWithoutOutDisablesScreenshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Screenshot bool   `json:"screenshot"`
			Via        string `json:"via"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Screenshot || body.Via != "cli" {
			t.Fatalf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"screenshot":null,"cursor":{"x":4,"y":5}}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	_, _, err := executeComputerScreen(t, "move", uuid.NewString(), "--x", "4", "--y", "5")
	if err != nil {
		t.Fatal(err)
	}
}

func TestComputerTypeTextEnvNeverPrintsValue(t *testing.T) {
	const secret = "screen-text-private-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action struct {
				Text string `json:"text"`
			} `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Action.Text != secret {
			t.Fatalf("text was not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"screenshot":null,"cursor":null}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("COMPUTER_SCREEN_TEXT", secret)
	_, stderr, err := executeComputerScreen(t, "type", uuid.NewString(), "--text-env", "COMPUTER_SCREEN_TEXT")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, secret) {
		t.Fatalf("text env value leaked in output: %q", stderr)
	}
}

func TestComputerScreenActionBodies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  []string
		action string
	}{
		{"click", []string{"--x", "0", "--y", "1"}, `{"action":"click","x":0,"y":1,"button":"left","count":1}`},
		{"click", []string{"--x", "0", "--y", "1", "--button", "middle", "--count", "3", "--mods", "cmd,shift"}, `{"action":"click","x":0,"y":1,"button":"middle","count":3,"modifiers":["cmd","shift"]}`},
		{"move", []string{"--x", "4095", "--y", "0"}, `{"action":"move","x":4095,"y":0}`},
		{"drag", []string{"--start-x", "0", "--start-y", "1", "--end-x", "2", "--end-y", "3"}, `{"action":"drag","start_x":0,"start_y":1,"end_x":2,"end_y":3}`},
		{"scroll", []string{"--x", "0", "--y", "1", "--direction", "left"}, `{"action":"scroll","x":0,"y":1,"direction":"left","amount":3}`},
		{"scroll", []string{"--x", "0", "--y", "1", "--direction", "up", "--amount", "20", "--mods", "ctrl"}, `{"action":"scroll","x":0,"y":1,"direction":"up","amount":20,"modifiers":["ctrl"]}`},
		{"type", []string{"--text", "A\n\t!"}, `{"action":"type","text":"A\n\t!"}`},
		{"key", []string{"--key", "cmd+space Return", "--hold", "2.5"}, `{"action":"key","keys":"cmd+space Return","hold_seconds":2.5}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.NewString()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/computer-runs/"+id+"/screen/actions" {
					t.Error("unexpected request target")
				}
				var got, want map[string]any
				if json.NewDecoder(r.Body).Decode(&got) != nil || json.Unmarshal([]byte(`{"action":`+tc.action+`,"screenshot":false,"via":"cli"}`), &want) != nil {
					t.Fatal("request JSON could not be decoded")
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("body = %#v, want %#v", got, want)
				}
				_, _ = w.Write([]byte(`{"screenshot":null,"cursor":null}`))
			}))
			defer server.Close()
			t.Setenv("REVYL_BACKEND_URL", server.URL)
			t.Setenv("REVYL_API_KEY", "fixture-key")
			stdout, _, err := executeComputerScreen(t, append([]string{tc.name, id, "--json"}, tc.flags...)...)
			if err != nil || strings.TrimSpace(stdout) != `{"screenshot":null,"cursor":null}` {
				t.Fatalf("output=%q err=%v", stdout, err)
			}
		})
	}
}

func TestComputerScreenshotZoomAndJSON(t *testing.T) {
	data := cliScreenFixture(t)
	id := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/computer-runs/"+id+"/screen/screenshot" || r.URL.Query().Get("region") != "0,0,20,10" || r.URL.Query().Get("via") != "cli" {
			t.Error("unexpected screenshot request")
		}
		_, _ = w.Write([]byte(`{"width":2,"height":1,"observed_at":"2026-10-19T00:00:00Z","png":"` + base64.StdEncoding.EncodeToString(data) + `","cursor":null}`))
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	path := filepath.Join(t.TempDir(), "shot.png")
	stdout, stderr, err := executeComputerScreen(t, "screenshot", id, "--zoom", "0,0,20,10", "--out", path, "--json")
	if err != nil || stderr != "" {
		t.Fatalf("stderr=%q err=%v", stderr, err)
	}
	var output map[string]any
	if json.Unmarshal([]byte(stdout), &output) != nil || output["png"] != base64.URLEncoding.EncodeToString(data) {
		t.Fatal("JSON screenshot did not use URL-safe base64")
	}
	if saved, err := os.ReadFile(path); err != nil || !bytes.Equal(saved, data) {
		t.Fatal("screenshot PNG was not saved")
	}
}

func TestComputerScreenInvalidFlags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid flags reached the server")
	}))
	defer server.Close()
	t.Setenv("REVYL_BACKEND_URL", server.URL)
	t.Setenv("REVYL_API_KEY", "fixture-key")
	t.Setenv("COMPUTER_SCREEN_BAD_TEXT", "\x01private-canary")
	for _, flags := range [][]string{
		{"screenshot", "--zoom", "1,2,3"}, {"screenshot", "--zoom", "0,0,0,1"}, {"screenshot", "--zoom", "0,0,4096,1"},
		{"click", "--x", "0"}, {"click", "--x", "-1", "--y", "0"}, {"click", "--x", "0", "--y", "4096"},
		{"click", "--x", "0", "--y", "0", "--button", "extra"}, {"click", "--x", "0", "--y", "0", "--count", "4"},
		{"click", "--x", "0", "--y", "0", "--mods", "cmd,cmd"}, {"click", "--x", "0", "--y", "0", "--mods", "hyper"},
		{"move", "--y", "0"}, {"drag", "--start-x", "0", "--start-y", "0", "--end-x", "0"},
		{"scroll", "--x", "0", "--y", "0"}, {"scroll", "--x", "0", "--y", "0", "--direction", "diagonal"},
		{"scroll", "--x", "0", "--y", "0", "--direction", "up", "--amount", "21"},
		{"type"}, {"type", "--text", "one", "--text-env", "ENV"}, {"type", "--text", ""},
		{"key"}, {"key", "--key", "Return", "--hold", "5.1"},
		{"key", "--key", "Return", "--hold", "NaN"}, {"key", "--key", "Return", "--hold", "-1"},
		{"screenshot", "--out", ""},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			args := append([]string{flags[0], uuid.NewString(), "--json"}, flags[1:]...)
			stdout, stderr, err := executeComputerScreen(t, args...)
			if err == nil {
				t.Fatal("invalid flags succeeded")
			}
			if strings.Contains(stdout+stderr+err.Error(), "private-canary") {
				t.Fatal("invalid text leaked")
			}
		})
	}
}
