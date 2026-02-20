package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestCanonicalAppName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: " My App ", want: "my app"},
		{in: "My     App", want: "my app"},
		{in: "MY APP", want: "my app"},
		{in: "My-App", want: "my app"},
		{in: "My_App", want: "my app"},
		{in: "My.App", want: "my app"},
		{in: "  My***App  ", want: "my app"},
		{in: "  ", want: ""},
	}

	for _, tc := range tests {
		if got := canonicalAppName(tc.in); got != tc.want {
			t.Fatalf("canonicalAppName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindAppIDByNameMatchesCanonicalizedNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items":[{"id":"app-123","name":"My   Test App","platform":"ios"}],
			"total":1,
			"page":1,
			"page_size":100,
			"total_pages":1,
			"has_next":false,
			"has_previous":false
		}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-api-key", server.URL)
	got, err := findAppIDByName(context.Background(), client, "ios", "  my test app ")
	if err != nil {
		t.Fatalf("findAppIDByName(): %v", err)
	}
	if got != "app-123" {
		t.Fatalf("findAppIDByName() = %q, want %q", got, "app-123")
	}
}

func TestCreateOrLinkAppByNameLinksExistingOnConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{
				"items":[{"id":"app-123","name":"My-App","platform":"ios"}],
				"total":1,
				"page":1,
				"page_size":100,
				"total_pages":1,
				"has_next":false,
				"has_previous":false
			}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-api-key", server.URL)
	result, err := createOrLinkAppByName(context.Background(), client, "my app", "ios")
	if err != nil {
		t.Fatalf("createOrLinkAppByName(): %v", err)
	}
	if result == nil {
		t.Fatal("createOrLinkAppByName() returned nil result")
	}
	if !result.LinkedExisting {
		t.Fatal("expected linked existing app result")
	}
	if result.ID != "app-123" {
		t.Fatalf("createOrLinkAppByName() ID = %q, want %q", result.ID, "app-123")
	}
}

func TestCreateOrLinkAppByNameReturnsConflictWhenNoMatchFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/builds/vars" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"app already exists"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{
				"items":[{"id":"app-999","name":"Different App","platform":"ios"}],
				"total":1,
				"page":1,
				"page_size":100,
				"total_pages":1,
				"has_next":false,
				"has_previous":false
			}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-api-key", server.URL)
	result, err := createOrLinkAppByName(context.Background(), client, "my app", "ios")
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if result != nil {
		t.Fatalf("expected nil result on unresolved conflict, got %+v", result)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
		t.Fatalf("expected already-exists conflict error, got %v", err)
	}
}
