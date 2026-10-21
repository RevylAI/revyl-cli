package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDeviceTapActionTimingJSON(t *testing.T) {
	const precisionFields = `,"grounding_bundle_duration_ms":114.690123456789012345,"input_duration_ms":8.937e+1,"settle_duration_ms":0`
	const coreSuccess = `"success":true,"x":56,"y":78,"latency_ms":52.500`
	const coreFailure = `"success":false,"error":"tap not applied","x":56,"y":78,"latency_ms":52.500`
	const phoneJSON = "{\n  \"action\": \"tap\",\n  \"x\": 56,\n  \"y\": 78,\n  \"success\": true,\n  \"latency_ms\": 52.500\n}\n"
	for _, tc := range []struct {
		name, platform, body, wantJSON, wantError string
		grounded                                  bool
	}{
		{name: "root precision", platform: "web", body: "{" + coreSuccess + precisionFields + "}"},
		{name: "grounded root precision", platform: "web", grounded: true, body: "{" + coreSuccess + precisionFields + "}"},
		{name: "phone byte compatibility", platform: "ios", body: "{" + coreSuccess + "}", wantJSON: phoneJSON},
		{name: "android byte compatibility", platform: "android", body: "{" + coreSuccess + "}", wantJSON: phoneJSON},
		{name: "null byte compatibility", platform: "ios", body: "{" + coreSuccess + `,"grounding_bundle_duration_ms":null,"input_duration_ms":null,"settle_duration_ms":null}`, wantJSON: phoneJSON},
		{name: "invalid metadata retains failure", platform: "web", body: "{" + coreFailure + `,"grounding_bundle_duration_ms":1e309,"input_duration_ms":"12.5","settle_duration_ms":true}`},
		{name: "grounded invalid metadata retains failure", platform: "web", grounded: true, body: "{" + coreFailure + `,"grounding_bundle_duration_ms":1e309,"input_duration_ms":"12.5","settle_duration_ms":true}`, wantError: "tap not applied"},
		{name: "grounded invalid metadata retains success", platform: "web", grounded: true, body: "{" + coreSuccess + `,"grounding_bundle_duration_ms":1e309,"input_duration_ms":"12.5","settle_duration_ms":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withWorkingDirectory(t, t.TempDir())
			t.Setenv("REVYL_API_KEY", "fixture-key")
			t.Setenv(sessionIDEnvVar, "")
			var sessionCalls, resolveCalls, tapCalls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/execution/device-sessions/" + flowSessionID:
					sessionCalls.Add(1)
					fmt.Fprintf(w, `{"id":%q,"org_id":"fixture-org","platform":%q,"viewer_kind":"whep","status":"running","workflow_run_id":%q}`, flowSessionID, tc.platform, flowWorkflowRunID)
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/health":
					_, _ = w.Write([]byte(`{"status":"ok","device_connected":true,"screen_width":402,"screen_height":874}`))
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/resolve_target":
					resolveCalls.Add(1)
					_, _ = w.Write([]byte(`{"found":true,"x":12,"y":34,"screen_token":"fixture-observation"}`))
				case "/api/v1/execution/device-proxy/" + flowWorkflowRunID + "/tap":
					tapCalls.Add(1)
					if r.Method != http.MethodPost {
						t.Errorf("tap method = %s, want POST", r.Method)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("tap request JSON: %v", err)
					}
					if body["x"] != float64(12) || body["y"] != float64(34) {
						t.Errorf("tap request coordinates = %v", body)
					}
					if tc.grounded && (body["target"] != "fixture target" || body["screen_token"] != "fixture-observation") {
						t.Error("grounded request changed its target or observation token")
					}
					_, _ = w.Write([]byte(tc.body))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(backend.Close)
			t.Setenv("REVYL_BACKEND_URL", backend.URL)
			cmd := newStopTestCommand(t, "--session-id", flowSessionID, "--json")
			cmd.Flags().String("target", "", "")
			cmd.Flags().Int("x", 0, "")
			cmd.Flags().Int("y", 0, "")
			args := []string{"--x", "12", "--y", "34"}
			if tc.grounded {
				args = []string{"--target", "fixture target"}
			}
			if err := cmd.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			var runErr error
			stdout, stderr := captureStdoutAndStderrSeparate(t, func() {
				runErr = deviceTapCmd.RunE(cmd, nil)
			})
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			wantResolveCalls := int32(0)
			if tc.grounded {
				wantResolveCalls = 1
			}
			if sessionCalls.Load() != 1 || resolveCalls.Load() != wantResolveCalls || tapCalls.Load() != 1 {
				t.Fatalf("requests: session=%d resolve=%d tap=%d error=%v", sessionCalls.Load(), resolveCalls.Load(), tapCalls.Load(), runErr)
			}
			if tc.wantError != "" {
				if runErr == nil || runErr.Error() != tc.wantError || stdout != "" {
					t.Fatalf("failure output: error=%v stdout=%q", runErr, stdout)
				}
				return
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			if tc.wantJSON != "" && stdout != tc.wantJSON {
				t.Fatalf("stdout = %q, want byte-equal %q", stdout, tc.wantJSON)
			}
			result := assertActionTimingResult(t, []byte(stdout), tc.body, tc.grounded)
			if !strings.Contains(tc.name, "precision") && (result.GroundingBundleDurationMs != "" || result.InputDurationMs != "" || result.SettleDurationMs != "") {
				t.Fatalf("absent or invalid timing reached stdout: %s", stdout)
			}
			if tc.grounded && (result.X != 12 || result.Y != 34 || result.Target != "fixture target") {
				t.Fatalf("grounded result = %+v", result)
			}
		})
	}
}

func TestActionTimingInvalidFieldsAreIndependent(t *testing.T) {
	for _, invalid := range []string{`"12.5"`, `"NaN"`, `true`, `false`, `null`, `-1`, `-1e-999`, `1e309`, `{}`, `[]`, "1." + strings.Repeat("2", 64)} {
		for _, field := range []string{"grounding_bundle_duration_ms", "input_duration_ms", "settle_duration_ms"} {
			t.Run(field+"/"+invalid, func(t *testing.T) {
				values := map[string]string{
					"grounding_bundle_duration_ms": "114.690123456789012345",
					"input_duration_ms":            "8.937e+1",
					"settle_duration_ms":           "0",
				}
				values[field] = invalid
				body := fmt.Sprintf(`{"success":false,"error":"tap not applied","x":56,"y":78,"latency_ms":52.500,"grounding_bundle_duration_ms":%s,"input_duration_ms":%s,"settle_duration_ms":%s}`, values["grounding_bundle_duration_ms"], values["input_duration_ms"], values["settle_duration_ms"])
				result := buildActionResult("tap", 12, 34, "", []byte(body))
				output, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				assertActionTimingResult(t, output, body, false)
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(output, &fields); err != nil {
					t.Fatal(err)
				}
				if _, exists := fields[field]; exists {
					t.Fatalf("invalid %s retained: %s", field, output)
				}
				for sibling, value := range values {
					if sibling != field && string(fields[sibling]) != value {
						t.Fatalf("valid sibling %s = %s, want %s", sibling, fields[sibling], value)
					}
				}
				grounded, err := buildGroundedTapActionResult(12, 34, "fixture target", []byte(body))
				if err == nil || err.Error() != "tap not applied" || grounded.Success || grounded.LatencyMs != "52.500" || grounded.X != 12 || grounded.Y != 34 {
					t.Fatalf("grounded failure corrupted: result=%+v error=%v", grounded, err)
				}
			})
		}
	}
}

func TestActionTimingPreservesNonnegativeNumericLexemes(t *testing.T) {
	for _, number := range []string{"0", "-0", "-0.000E-2", "1e-999", "1.2345678901234567e-6", "1." + strings.Repeat("2", 62)} {
		t.Run(number, func(t *testing.T) {
			result := buildActionResult("tap", 12, 34, "", []byte(`{"success":true,"input_duration_ms":`+number+`}`))
			if result.InputDurationMs.String() != number {
				t.Fatalf("input timing = %q, want %q", result.InputDurationMs, number)
			}
			output, err := json.Marshal(result)
			if err != nil || !strings.Contains(string(output), `"input_duration_ms":`+number) {
				t.Fatalf("numeric lexeme changed: output=%s error=%v", output, err)
			}
		})
	}
}

func assertActionTimingResult(t *testing.T, output []byte, body string, grounded bool) ActionResult {
	t.Helper()
	var result ActionResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid stdout JSON: %v", err)
	}
	wantSuccess := !strings.Contains(body, `"success":false`)
	if result.Success != wantSuccess || result.LatencyMs != "52.500" {
		t.Fatalf("core response corrupted: %+v", result)
	}
	if !wantSuccess && result.Error != "tap not applied" {
		t.Fatalf("worker error corrupted: %+v", result)
	}
	if !grounded && (result.X != 56 || result.Y != 78) {
		t.Fatalf("worker coordinates corrupted: %+v", result)
	}
	if strings.Contains(body, `"grounding_bundle_duration_ms":114.690123456789012345`) && strings.Contains(body, `"input_duration_ms":8.937e+1`) && strings.Contains(body, `"settle_duration_ms":0`) {
		if result.GroundingBundleDurationMs != "114.690123456789012345" || result.InputDurationMs != "8.937e+1" || result.SettleDurationMs != "0" {
			t.Fatalf("timing precision changed: %+v", result)
		}
	}
	return result
}
