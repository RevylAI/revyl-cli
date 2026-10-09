//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestInvestigationTools(t *testing.T) {
	const app = "00000000-0000-4000-8000-000000000001"
	const base = "00000000-0000-4000-8000-000000000002"
	const head = "00000000-0000-4000-8000-000000000003"
	const report = "00000000-0000-4000-8000-000000000004"
	const unmapped = "00000000-0000-4000-8000-000000000005"
	const execution = "00000000-0000-4000-8000-000000000006"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/atlas/v2/apps/"+app+"/graph":
			ids := []string{"shared", "base-only"}
			q := r.URL.Query()
			if q.Get("build_id") == head {
				ids = []string{"shared", "head-only"}
			}
			if q.Get("report_id") == unmapped {
				ids = nil
			}
			nodes := []any{}
			for _, id := range ids {
				nodes = append(nodes, map[string]any{"id": id, "semantic_name": id})
			}
			if q.Get("limit") == "99" {
				nodes[0].(map[string]any)["semantic_description"] = strings.Repeat("x", 130000)
			}
			json.NewEncoder(w).Encode(map[string]any{"app_id": app, "nodes": nodes, "edges": []any{}, "projection": map[string]any{"truncated": q.Get("limit") == "1", "data_source": "evidence"}})
		case r.URL.Path == "/api/v1/apps/builds/"+base+"/runs":
			json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"execution_id": execution, "status": "failed", "success": false}}, "total": 2, "page": 1, "page_size": 1, "total_pages": 2, "has_next": true, "has_previous": false})
		case r.URL.Path == "/api/v1/apps/"+app+"/builds":
			if r.URL.Query().Get("page") != "2" {
				t.Error("build pagination scope lost")
			}
			json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": base, "version": "fixture", "metadata": map[string]any{"branch": "main"}}}, "total": 3, "page": 2, "page_size": 1, "total_pages": 3, "has_next": true})
		case r.URL.Path == "/api/v1/tests/get_tests":
			if r.URL.Query().Get("search") == "onboarding & setup" {
				if r.URL.Query().Get("app_id") != app || r.URL.Query().Get("platform") != "ios" || r.URL.Query().Get("sort_by") != "name" {
					t.Error("test filters were not applied by the server")
				}
				json.NewEncoder(w).Encode(map[string]any{"tests": []any{map[string]any{"id": execution, "name": "onboarding & setup", "platform": "ios", "tags": []any{}}}, "count": 1, "total_count": 1})
				return
			}
			if r.URL.Query().Get("offset") != "5" || r.URL.Query().Get("limit") != "1" {
				t.Error("test pagination scope lost")
			}
			json.NewEncoder(w).Encode(map[string]any{"tests": []any{map[string]any{"id": execution, "name": "Fixture", "platform": "ios", "tags": []any{}}}, "count": 1, "total_count": 10})
		case r.URL.Path == "/api/v1/tests/get_test_by_id/"+execution:
			json.NewEncoder(w).Encode(map[string]any{"id": execution, "name": "Fixture", "platform": "ios", "version": 3, "tasks": []any{map[string]any{"type": "validation", "description": "Dashboard is visible"}}, "mobile_targets": []any{map[string]any{"device_model": "iPhone 16", "os_version": "iOS 18.5"}}, "run_config": map[string]any{"fail_fast": false}})
		case r.URL.Path == "/api/v1/reports-v3/reports/by-execution/"+execution+"/context":
			json.NewEncoder(w).Encode(map[string]any{"id": report, "success": false, "steps": []any{}, "device_state_url": nil})
		case r.URL.Path == "/api/v1/reports-v3/reports/"+report+"/device-logs":
			json.NewEncoder(w).Encode(map[string]any{"download_url": "http://" + r.Host + "/live/device_logs.txt", "filename": "device_logs.txt", "compressed": false, "partial": true})
		case r.URL.Path == "/live/device_logs.txt":
			w.Header().Set("Content-Type", "text/plain")
			for i := 0; i < 150; i++ {
				fmt.Fprintf(w, "09-22 12:00:00.000 123 456 E Fixture: failure %d\n", i)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	call := func(t *testing.T, stdin string, args ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, revylBin, args...)
		cmd.WaitDelay = time.Second
		cmd.Env = append(os.Environ(), "REVYL_BACKEND_URL="+server.URL, "REVYL_API_KEY=fixture-key", "REVYL_TELEMETRY_DISABLED=1", "NO_COLOR=1")
		cmd.Stdin = strings.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			return output, fmt.Errorf("%w: %s", err, stderr.String())
		}
		return output, nil
	}
	decode := func(t *testing.T, output []byte) map[string]json.RawMessage {
		t.Helper()
		var result map[string]json.RawMessage
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatalf("non-JSON stdout: %s", output)
		}
		return result
	}
	t.Run("discovery_and_schema", func(t *testing.T) {
		out, err := call(t, "", "tools", "search", "device logs")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), "reports.logs") {
			t.Fatal("device logs undiscoverable")
		}
		out, err = call(t, "", "tools", "describe", "reports.logs")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(decode(t, out)["input_schema"]), "execution_id") {
			t.Fatal("schema missing identity")
		}
	})
	t.Run("build_runs_to_exact_execution", func(t *testing.T) {
		out, err := call(t, `{"build_id":"`+base+`","limit":1}`, "tools", "call", "builds.runs", "-", "--read-only")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		if string(result["has_next"]) != "true" || !strings.Contains(string(result["items"]), execution) {
			t.Fatal("lost execution or pagination")
		}
	})
	t.Run("filtered_build_page_preserves_unread_pages", func(t *testing.T) {
		out, err := call(t, `{"app":"`+app+`","page":2,"limit":1,"branch":"other"}`, "tools", "call", "builds.list", "-")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		if string(result["count"]) != "0" || string(result["has_next"]) != "true" || string(result["scanned_count"]) != "1" {
			t.Fatalf("empty filtered page hid unread builds: %s", out)
		}
	})
	t.Run("filtered_test_page_preserves_unread_rows", func(t *testing.T) {
		out, err := call(t, `{"offset":5,"limit":1,"tag":"absent"}`, "tools", "call", "tests.list", "-")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		if string(result["has_more"]) != "true" || string(result["next_offset"]) != "6" || string(result["tests"]) != "[]" {
			t.Fatalf("empty filtered page hid unread tests: %s", out)
		}
	})
	t.Run("test_search_filters_before_pagination", func(t *testing.T) {
		out, err := call(t, `{"app":"`+app+`","search":"onboarding & setup","platform":"ios","limit":1}`, "tools", "call", "tests.list", "-")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		if string(result["filter_scope"]) != `"server"` || string(result["has_more"]) != "false" || !strings.Contains(string(result["tests"]), execution) {
			t.Fatalf("test search scope lost: %s", out)
		}
	})
	t.Run("device_logs_filtered_and_bounded", func(t *testing.T) {
		out, err := call(t, `{"execution_id":"`+execution+`","tail":5,"level":["E"]}`, "tools", "call", "reports.logs", "-")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		if string(result["matched"]) != "5" || string(result["matched_before_limit"]) != "150" || string(result["truncated"]) != "true" || string(result["partial"]) != "true" {
			t.Fatalf("lost diagnostic completeness: %s", out)
		}
	})
	t.Run("current_definition_and_configuration", func(t *testing.T) {
		out, err := call(t, `{"test_id":"`+execution+`"}`, "tools", "call", "tests.get", "-", "--read-only")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		if string(result["version"]) != "3" || !strings.Contains(string(result["tasks"]), "Dashboard is visible") || !strings.Contains(string(result["mobile_targets"]), "iPhone 16") {
			t.Fatalf("definition evidence lost: %s", out)
		}
		out, err = call(t, `{"test_id":"`+execution+`"}`, "tools", "call", "tests.configuration", "-", "--read-only")
		if err != nil {
			t.Fatal(err)
		}
		if string(decode(t, out)["fail_fast"]) != "false" {
			t.Fatalf("explicit false configuration lost: %s", out)
		}
	})
	t.Run("unmapped_reports_survive_projection", func(t *testing.T) {
		out, err := call(t, `{"app":"`+app+`","reports":["`+report+`","`+unmapped+`"]}`, "tools", "call", "atlas.from_reports", "-")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), unmapped) || !strings.Contains(string(out), "no_matching_atlas_evidence") {
			t.Fatalf("lost unmapped report: %s", out)
		}
	})
	t.Run("comparison_recipe_rehydrates", func(t *testing.T) {
		out, err := call(t, `{"app":"`+app+`","base_build":"`+base+`","head_build":"`+head+`"}`, "tools", "call", "atlas.compare_builds", "-", "--view")
		if err != nil {
			t.Fatal(err)
		}
		result := decode(t, out)
		view := result["view"]
		data := decode(t, result["data"])
		if !strings.Contains(string(data["screens"]), "head-only") || string(data["partial"]) != "false" {
			t.Fatal("comparison failed")
		}
		out, err = call(t, string(view), "tools", "hydrate", "-")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decode(t, out)["view"], view) {
			t.Fatal("hydration changed query")
		}
	})
	t.Run("truncated_comparison_is_partial", func(t *testing.T) {
		out, err := call(t, `{"app":"`+app+`","base_build":"`+base+`","head_build":"`+head+`","limit":1}`, "tools", "call", "atlas.compare_builds", "-")
		if err != nil {
			t.Fatal(err)
		}
		if string(decode(t, out)["partial"]) != "true" {
			t.Fatal("truncation hidden")
		}
	})
	for name, args := range map[string][]string{
		"oversized_graph":      {"tools", "call", "atlas.compare_builds", `{"app":"` + app + `","base_build":"` + base + `","head_build":"` + head + `","limit":99}`},
		"unknown_tool":         {"tools", "call", "shell.exec", `{}`},
		"unknown_parameter":    {"tools", "call", "reports.logs", `{"execution_id":"` + execution + `","download":true}`},
		"read_only_write":      {"tools", "call", "annotations.create", `{}`, "--read-only"},
		"hydrate_write":        {"tools", "hydrate", `{"version":1,"renderer":"atlas_graph","query":{"tool":"annotations.create","arguments":{}}}`},
		"unsupported_renderer": {"tools", "hydrate", `{"version":1,"renderer":"javascript","query":{"tool":"atlas.graph","arguments":{"app":"` + app + `"}}}`},
		"missing_resource":     {"tools", "call", "reports.logs", `{"execution_id":"` + unmapped + `"}`},
		"moving_build_view":    {"tools", "call", "atlas.graph", `{"app":"` + app + `","build":"latest"}`, "--view"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := call(t, "", args...)
			if err == nil || len(out) != 0 {
				t.Fatalf("invalid call produced a result: %s %v", out, err)
			}
		})
	}
}
