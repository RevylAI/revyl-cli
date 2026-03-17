// Package testutil provides a shared mock API server for sync integration tests.
package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/revyl/cli/internal/api"
)

// MockTest represents a test stored in the mock server's in-memory store.
type MockTest struct {
	ID       string
	Name     string
	Version  int
	Platform string
	Tasks    interface{}
	AppID    string
}

// APICall records a single API request received by the mock server.
type APICall struct {
	Method string
	Path   string
}

// MockServer is an in-memory HTTP test server that simulates the Revyl API.
//
// It supports pre-seeding test data, recording API calls for assertions,
// and injecting error responses for specific path prefixes.
type MockServer struct {
	Server *httptest.Server
	Client *api.Client

	tests  map[string]MockTest
	calls  []APICall
	errors map[string]int // path prefix -> forced HTTP status code
	nextID int
	mu     sync.Mutex
}

// NewMockServer creates and starts a new mock API server with all
// supported endpoints and returns a configured API client.
//
// Returns:
//   - *MockServer: A running mock server ready for use in tests
func NewMockServer() *MockServer {
	m := &MockServer{
		tests:  make(map[string]MockTest),
		errors: make(map[string]int),
		nextID: 1,
	}

	m.Server = httptest.NewServer(http.HandlerFunc(m.handler))
	m.Client = api.NewClientWithBaseURL("test-key", m.Server.URL)

	return m
}

// SeedTest adds a test to the mock server's in-memory store.
//
// Parameters:
//   - t: The test data to seed
func (m *MockServer) SeedTest(t MockTest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tests[t.ID] = t
}

// ForceError injects an error response for any request whose path starts
// with the given prefix. Persists until ClearErrors is called.
//
// Parameters:
//   - pathPrefix: URL path prefix to match
//   - statusCode: HTTP status code to return
func (m *MockServer) ForceError(pathPrefix string, statusCode int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[pathPrefix] = statusCode
}

// ClearErrors removes all injected error responses.
func (m *MockServer) ClearErrors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = make(map[string]int)
}

// Calls returns a snapshot of all recorded API calls in order.
//
// Returns:
//   - []APICall: Copy of recorded calls
func (m *MockServer) Calls() []APICall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]APICall, len(m.calls))
	copy(out, m.calls)
	return out
}

// HasCall returns true if a call matching the given method and path prefix exists.
//
// Parameters:
//   - method: HTTP method to match (e.g. "POST")
//   - pathPrefix: URL path prefix to match
//
// Returns:
//   - bool: True if a matching call was recorded
func (m *MockServer) HasCall(method, pathPrefix string) bool {
	for _, c := range m.Calls() {
		if c.Method == method && strings.HasPrefix(c.Path, pathPrefix) {
			return true
		}
	}
	return false
}

// TestByID returns the in-memory test for the given ID, if it exists.
//
// Parameters:
//   - id: The test ID to look up
//
// Returns:
//   - MockTest: The test data
//   - bool: True if found
func (m *MockServer) TestByID(id string) (MockTest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tests[id]
	return t, ok
}

// Close shuts down the mock server.
func (m *MockServer) Close() {
	m.Server.Close()
}

func (m *MockServer) handler(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, APICall{Method: r.Method, Path: r.URL.Path})

	for prefix, code := range m.errors {
		if strings.HasPrefix(r.URL.Path, prefix) {
			m.mu.Unlock()
			writeJSON(w, code, map[string]string{
				"detail": fmt.Sprintf("forced error %d", code),
			})
			return
		}
	}
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_test_by_id/"):
		m.handleGetTest(w, r)
	case r.Method == "POST" && r.URL.Path == "/api/v1/tests/create":
		m.handleCreateTest(w, r)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/v1/tests/update/"):
		m.handleUpdateTest(w, r)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tests/get_simple_tests"):
		m.handleListTests(w, r)
	case r.Method == "GET" && r.URL.Path == "/api/v1/tests/tags":
		m.handleListTags(w, r)
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/tests/tags/tests/") &&
		!strings.HasSuffix(r.URL.Path, "/sync"):
		m.handleGetTestTags(w, r)
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/v1/tests/tags/tests/") &&
		strings.HasSuffix(r.URL.Path, "/sync"):
		m.handleSyncTestTags(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "not found"})
	}
}

func (m *MockServer) handleGetTest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tests/get_test_by_id/")

	m.mu.Lock()
	t, ok := m.tests[id]
	m.mu.Unlock()

	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "test not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       t.ID,
		"name":     t.Name,
		"platform": t.Platform,
		"tasks":    t.Tasks,
		"version":  t.Version,
		"app_id":   t.AppID,
	})
}

func (m *MockServer) handleCreateTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string      `json:"name"`
		Platform string      `json:"platform"`
		Tasks    interface{} `json:"tasks"`
		AppID    string      `json:"app_id"`
		OrgID    string      `json:"org_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid request body"})
		return
	}

	m.mu.Lock()
	id := fmt.Sprintf("mock-%d", m.nextID)
	m.nextID++
	m.tests[id] = MockTest{
		ID:       id,
		Name:     req.Name,
		Version:  1,
		Platform: req.Platform,
		Tasks:    req.Tasks,
		AppID:    req.AppID,
	}
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      id,
		"version": 1,
		"name":    req.Name,
	})
}

func (m *MockServer) handleUpdateTest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tests/update/")

	var req struct {
		Name  string      `json:"name"`
		Tasks interface{} `json:"tasks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid request body"})
		return
	}

	m.mu.Lock()
	t, ok := m.tests[id]
	if !ok {
		m.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "test not found"})
		return
	}

	t.Version++
	if req.Name != "" {
		t.Name = req.Name
	}
	if req.Tasks != nil {
		t.Tasks = req.Tasks
	}
	m.tests[id] = t
	newVersion := t.Version
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      id,
		"version": newVersion,
	})
}

func (m *MockServer) handleListTests(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	tests := make([]map[string]interface{}, 0, len(m.tests))
	for _, t := range m.tests {
		tests = append(tests, map[string]interface{}{
			"id":       t.ID,
			"name":     t.Name,
			"platform": t.Platform,
		})
	}
	m.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tests": tests,
		"count": len(tests),
	})
}

func (m *MockServer) handleListTags(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tags": []interface{}{},
	})
}

func (m *MockServer) handleGetTestTags(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []interface{}{})
}

func (m *MockServer) handleSyncTestTags(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/tests/tags/tests/")
	testID := strings.TrimSuffix(path, "/sync")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"test_id": testID,
		"tags":    []interface{}{},
	})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
