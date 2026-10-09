package runinspect

import (
	"context"
	"github.com/revyl/cli/internal/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionReportDiagnosticsWithoutExecution(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.Contains(r.URL.Path, "/reports/by-session/") {
			t.Errorf("session became an execution: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"report-fixture","session_id":"session-fixture","hardware_metrics_url":"https://example.invalid/perf","hardware_metrics_partial":true,"steps":[]}`))
	}))
	defer server.Close()
	client := api.NewClientWithBaseURL("fixture", server.URL)
	report, err := FetchReportReference(context.Background(), client, "session-fixture", "session")
	if err != nil {
		t.Fatal(err)
	}
	if report.SessionID != "session-fixture" || report.ReportID != "report-fixture" || report.HardwareMetricsURL == "" || !report.HardwareMetricsPartial {
		t.Fatalf("lost session evidence: %+v", report)
	}
	if _, err := FetchReportReference(context.Background(), client, "session-fixture", "arbitrary"); err == nil {
		t.Fatal("accepted unknown reference kind")
	}
	if requests != 1 {
		t.Fatalf("unexpected fallback to another identity: %d requests", requests)
	}
}
