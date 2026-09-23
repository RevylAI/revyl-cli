package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The --private share variants must never call the public share-link
// endpoints: the organization-scoped link is built locally, so the backend
// never mints a token that could be handed to someone outside the org.

func newPrivateShareMockServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	publicShareCalls := 0
	inner := newMockAPIServer(t)
	t.Cleanup(inner.Close)
	guarded := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "generate_shareable") {
			publicShareCalls++
		}
		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, inner.URL+r.URL.RequestURI(), r.Body)
		if err != nil {
			t.Fatalf("proxy request: %v", err)
		}
		proxyReq.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(proxyReq)
		if err != nil {
			t.Fatalf("proxy call: %v", err)
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 64*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
	}))
	t.Cleanup(guarded.Close)
	return guarded, &publicShareCalls
}

func TestTestSharePrivateJSONBuildsOrganizationLinkWithoutMinting(t *testing.T) {
	server, publicShareCalls := newPrivateShareMockServer(t)
	withMockClient(t, server)

	oldJSON, oldPrivate := shareOutputJSON, sharePrivate
	shareOutputJSON, sharePrivate = true, true
	defer func() { shareOutputJSON, sharePrivate = oldJSON, oldPrivate }()

	leaf := newLeafCommand("share", runTestShare)
	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestShare --private: %v", err)
		}
	})

	result := parseJSON(t, output)
	if got := result["access"]; got != shareAccessOrganization {
		t.Fatalf("expected access=%q, got %v", shareAccessOrganization, got)
	}
	link, _ := result["shareable_link"].(string)
	if !strings.HasSuffix(link, "/tests/report?taskId=task-001") {
		t.Fatalf("expected organization report link, got %q", link)
	}
	if strings.Contains(link, "token=") {
		t.Fatalf("private link must not carry a public token: %q", link)
	}
	if *publicShareCalls != 0 {
		t.Fatalf("expected no public share-link request, got %d", *publicShareCalls)
	}
}

func TestWorkflowSharePrivateJSONBuildsOrganizationLinkWithoutMinting(t *testing.T) {
	server, publicShareCalls := newPrivateShareMockServer(t)
	withMockClient(t, server)

	oldJSON, oldPrivate := wfShareOutputJSON, wfSharePrivate
	wfShareOutputJSON, wfSharePrivate = true, true
	defer func() { wfShareOutputJSON, wfSharePrivate = oldJSON, oldPrivate }()

	leaf := newLeafCommand("share", runWorkflowShare)
	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"smoke-tests"}); err != nil {
			t.Fatalf("runWorkflowShare --private: %v", err)
		}
	})

	result := parseJSON(t, output)
	if got := result["access"]; got != shareAccessOrganization {
		t.Fatalf("expected access=%q, got %v", shareAccessOrganization, got)
	}
	link, _ := result["shareable_link"].(string)
	if !strings.Contains(link, "/workflows/report?taskId=") || strings.Contains(link, "token=") {
		t.Fatalf("expected organization workflow report link, got %q", link)
	}
	if *publicShareCalls != 0 {
		t.Fatalf("expected no public share-link request, got %d", *publicShareCalls)
	}
}

// withSessionClient points the session commands (which build their own client
// from the stored API key rather than the project config) at the mock server.
func withSessionClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	t.Setenv("REVYL_API_KEY", "test-api-key")
	t.Setenv("REVYL_BACKEND_URL", server.URL)
}

func TestSessionSharePrivateJSONBuildsOrganizationLinkWithoutMinting(t *testing.T) {
	server, publicShareCalls := newPrivateShareMockServer(t)
	withSessionClient(t, server)

	oldJSON, oldPrivate := sessionShareOutputJSON, sessionSharePrivate
	sessionShareOutputJSON, sessionSharePrivate = true, true
	defer func() { sessionShareOutputJSON, sessionSharePrivate = oldJSON, oldPrivate }()

	leaf := newLeafCommand("share", runSessionShare)
	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{knownSessionID}); err != nil {
			t.Fatalf("runSessionShare --private: %v", err)
		}
	})

	result := parseJSON(t, output)
	if got := result["access"]; got != shareAccessOrganization {
		t.Fatalf("expected access=%q, got %v", shareAccessOrganization, got)
	}
	if got := result["session_id"]; got != knownSessionID {
		t.Fatalf("expected session_id=%q, got %v", knownSessionID, got)
	}
	link, _ := result["shareable_link"].(string)
	if !strings.HasSuffix(link, "/sessions/"+knownSessionID) {
		t.Fatalf("expected organization session link, got %q", link)
	}
	if *publicShareCalls != 0 {
		t.Fatalf("expected no public share-link request, got %d", *publicShareCalls)
	}
}

func TestSessionSharePrivateRejectsUnknownSession(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withSessionClient(t, server)

	oldJSON, oldPrivate := sessionShareOutputJSON, sessionSharePrivate
	sessionShareOutputJSON, sessionSharePrivate = true, true
	defer func() { sessionShareOutputJSON, sessionSharePrivate = oldJSON, oldPrivate }()

	leaf := newLeafCommand("share", runSessionShare)
	output := captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{unknownSessionID}); err == nil {
			t.Fatalf("expected --private to fail for a session the caller cannot see")
		}
	})
	if strings.Contains(output, "/sessions/"+unknownSessionID) {
		t.Fatalf("must not print an organization link for an unknown session:\n%s", output)
	}
}

func TestTestSharePrivateRejectsUnknownExecution(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON, oldPrivate := shareOutputJSON, sharePrivate
	shareOutputJSON, sharePrivate = true, true
	defer func() { shareOutputJSON, sharePrivate = oldJSON, oldPrivate }()

	leaf := newLeafCommand("share", runTestShare)
	output := captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{routeMissingExecution}); err == nil {
			t.Fatalf("expected --private to fail for an execution the caller cannot see")
		}
	})
	if strings.Contains(output, "taskId="+routeMissingExecution) {
		t.Fatalf("must not print an organization link for an unknown execution:\n%s", output)
	}
}

func TestPublicShareJSONReportsPublicAccess(t *testing.T) {
	server := newMockAPIServer(t)
	defer server.Close()
	withMockClient(t, server)

	oldJSON, oldPrivate := shareOutputJSON, sharePrivate
	shareOutputJSON, sharePrivate = true, false
	defer func() { shareOutputJSON, sharePrivate = oldJSON, oldPrivate }()

	leaf := newLeafCommand("share", runTestShare)
	output := captureStdout(t, func() {
		if err := leaf.RunE(leaf, []string{"login-flow"}); err != nil {
			t.Fatalf("runTestShare: %v", err)
		}
	})

	result := parseJSON(t, output)
	if got := result["access"]; got != shareAccessPublic {
		t.Fatalf("expected access=%q, got %v", shareAccessPublic, got)
	}
}

func TestValidatePrivateShareFlags(t *testing.T) {
	cases := []struct {
		name    string
		private bool
		expires string
		wantErr bool
	}{
		{"public without expiry", false, "", false},
		{"public with expiry", false, "30d", false},
		{"private without expiry", true, "", false},
		{"private with blank expiry", true, "   ", false},
		{"private with expiry", true, "30d", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePrivateShareFlags(tc.private, tc.expires)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePrivateShareFlags(%v, %q) err=%v, wantErr=%v", tc.private, tc.expires, err, tc.wantErr)
			}
		})
	}
}

func TestSessionSharePrivateRejectsExpires(t *testing.T) {
	oldPrivate, oldExpires := sessionSharePrivate, sessionShareExpires
	sessionSharePrivate, sessionShareExpires = true, "30d"
	defer func() { sessionSharePrivate, sessionShareExpires = oldPrivate, oldExpires }()

	leaf := newLeafCommand("share", runSessionShare)
	_ = captureStdoutAndStderr(t, func() {
		if err := leaf.RunE(leaf, []string{knownSessionID}); err == nil {
			t.Fatalf("expected --private with --expires to be rejected")
		}
	})
}
