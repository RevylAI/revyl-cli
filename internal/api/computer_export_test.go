package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExportComputerHarborStreamsWithLongDeadline(t *testing.T) {
	id := uuid.New()
	payload := bytes.Repeat([]byte("archive-fixture"), 256*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/computer-runs/"+id.String()+"/export/harbor" {
			t.Error("export used an unexpected method or route")
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("export lost its authentication")
		}
		time.Sleep(10 * time.Millisecond)
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client := NewClientWithBaseURL("fixture-key", server.URL)
	original := &http.Client{Timeout: time.Millisecond}
	client.SetHTTPClient(original)
	var output bytes.Buffer
	if err := client.ExportComputerHarbor(context.Background(), id, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), payload) {
		t.Fatal("export did not preserve streamed bytes")
	}
	if client.httpClient != original || original.Timeout != time.Millisecond || original.CheckRedirect != nil {
		t.Fatal("export changed the shared HTTP client")
	}
}

func TestExportComputerHarborMapsSafeRefusals(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
	}{
		{401, "unauthenticated"}, {403, "forbidden"}, {404, "inaccessible"},
		{503, "evidence_unavailable"}, {500, "private-error-canary"},
	} {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, `{"detail":"`+test.code+`"}`)
			}))
			defer server.Close()
			var output bytes.Buffer
			err := NewClientWithBaseURL("fixture-key", server.URL).ExportComputerHarbor(context.Background(), uuid.New(), &output)
			var refusal *APIError
			if !errors.As(err, &refusal) || refusal.StatusCode != test.status || output.Len() != 0 {
				t.Fatalf("refusal = %v, written = %d", err, output.Len())
			}
			if test.status == 500 {
				if refusal.Code != "" || strings.Contains(err.Error(), test.code) {
					t.Fatal("unrecognized error contents escaped")
				}
			} else if refusal.Code != test.code {
				t.Fatalf("code = %q, want %q", refusal.Code, test.code)
			}
		})
	}
}

func TestExportComputerHarborHonorsCancellation(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[flushHeaders], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if flushHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := NewClientWithBaseURL("fixture-key", server.URL).ExportComputerHarbor(ctx, uuid.New(), io.Discard)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cancellation error = %v", err)
			}
		})
	}
}

func TestExportComputerHarborRefusesRedirects(t *testing.T) {
	var followed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private-location-canary" {
			followed = true
			return
		}
		w.Header().Set("Location", "/private-location-canary")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	err := NewClientWithBaseURL("fixture-key", server.URL).ExportComputerHarbor(context.Background(), uuid.New(), io.Discard)
	var refusal *APIError
	if !errors.As(err, &refusal) || refusal.StatusCode != http.StatusTemporaryRedirect || followed {
		t.Fatalf("redirect result = %v, followed = %t", err, followed)
	}
	if strings.Contains(err.Error(), "private-location-canary") {
		t.Fatal("redirect leaked its location")
	}
}

func TestExportComputerHarborHidesTransportErrors(t *testing.T) {
	client := NewClientWithBaseURL("fixture-key", "https://fixture.invalid")
	client.SetHTTPClient(&http.Client{Transport: computerRoundTripper(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 29*time.Minute {
			t.Error("export deadline cannot support a long download")
		}
		return nil, errors.New("private-transport-canary")
	})})
	err := client.ExportComputerHarbor(context.Background(), uuid.New(), io.Discard)
	if !errors.Is(err, errComputerReadFailed) || strings.Contains(err.Error(), "private-transport-canary") {
		t.Fatalf("transport error = %v", err)
	}
}
