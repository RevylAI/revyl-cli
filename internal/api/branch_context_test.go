package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestContextBranchLookupPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, id, branch, hint, expected string
		missing, upload, fail            bool
	}{
		{name: "context", hint: "Feature/日本語", expected: "Feature/日本語"},
		{name: "explicit name", branch: "main", hint: " invalid ", expected: "main"},
		{name: "explicit ID", id: branchTestID, hint: " invalid "},
		{name: "missing read", hint: "feature", expected: "feature", missing: true, fail: true},
		{name: "missing upload", hint: "feature", expected: "feature", missing: true, upload: true},
		{name: "unsupported", hint: " invalid ", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, creates := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodPost {
					creates++
					var body map[string]string
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["name"] != tc.expected {
						t.Errorf("wrong create name %q", body["name"])
					}
					_ = json.NewEncoder(w).Encode(BuildBranch{ID: branchTestID, Name: tc.expected})
					return
				}
				if tc.id != "" {
					if r.URL.Path != "/api/v1/apps/app/branches/"+tc.id {
						t.Errorf("wrong ID lookup %s", r.URL.Path)
					}
					_ = json.NewEncoder(w).Encode(BuildBranch{ID: tc.id})
					return
				}
				if r.URL.Query().Get("name") != tc.expected {
					t.Errorf("wrong name query %s", r.URL.RawQuery)
				}
				branches := []BuildBranch{}
				if !tc.missing {
					branches = append(branches, BuildBranch{ID: branchTestID, Name: tc.expected})
				}
				_ = json.NewEncoder(w).Encode(BuildBranchesPage{Branches: branches})
			}))
			defer server.Close()
			client := NewClientWithBaseURL("test", server.URL)
			var err error
			if tc.upload {
				_, err = client.ResolveUploadBranchWithContext(context.Background(), "app", tc.id, tc.branch, tc.hint)
			} else {
				_, err = client.ResolveBranchWithContext(context.Background(), "app", tc.id, tc.branch, tc.hint)
			}
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v", err)
			}
			if tc.fail && !strings.Contains(err.Error(), "--branch") {
				t.Errorf("missing remedy %v", err)
			}
			if tc.missing && !tc.upload && !errors.Is(err, ErrBranchNotFound) {
				t.Errorf("wrong missing error %v", err)
			}
			if tc.name == "unsupported" && calls != 0 {
				t.Error("unsupported context reached API")
			}
			if (creates > 0) != (tc.missing && tc.upload) {
				t.Errorf("unexpected creates %d", creates)
			}
		})
	}
}

func TestBuildResolutionPreservesRawContextAndExactIntent(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic latest", true: "exact"}[exact], func(t *testing.T) {
			hint := strings.Repeat("界", 300)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/apps/resolve" {
					var body map[string]interface{}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["context_branch_name"] != hint {
						t.Error("raw context changed")
					}
					if _, ok := body["branch_name"]; ok {
						t.Error("context promoted to explicit branch")
					}
					if exact {
						if body["pinned_build_id"] != buildTestID || body["build_selection"] != "pinned" {
							t.Error("exact choice changed")
						}
					} else if body["build_selection"] != "latest" {
						t.Error("latest lost")
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"version_id": buildTestID, "branch_id": branchTestID, "download_url": "https://example.com/artifact"})
					return
				}
				_ = json.NewEncoder(w).Encode(BuildVersionDetail{ID: buildTestID})
			}))
			defer server.Close()
			selection := BuildSelection{ContextBranchName: hint, Mode: BuildSelectionModeLatest}
			if exact {
				selection.Mode = BuildSelectionModePinned
				selection.BuildID = buildTestID
			}
			if _, err := NewClientWithBaseURL("test", server.URL).ResolveBuildSelection(context.Background(), "app", selection); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeContextTransportRemainsSeparateAndOptional(t *testing.T) {
	for _, hint := range []string{"", "feature/日本語", " invalid "} {
		for _, endpoint := range []string{"test", "workflow", "device", "url-upload"} {
			t.Run(endpoint+"/"+hint, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body map[string]interface{}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if value, present := body["context_branch_name"]; present != (hint != "") || present && value != hint {
						t.Errorf("wrong hint %#v", body)
					}
					for _, key := range []string{"branch_id", "branch_name", "build_selection", "pinned_build_id"} {
						if _, present := body[key]; present {
							t.Errorf("hint changed saved intent: %s", key)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{}`))
				}))
				defer server.Close()
				client := NewClientWithBaseURL("test", server.URL)
				ctx := WithTraceHandoff(context.Background(), &TraceHandoff{Traceparent: "00-1234567890abcdef1234567890abcdef-1111111111111111-01", HandoffToken: "test", RequestID: "request"})
				var err error
				switch endpoint {
				case "test":
					_, err = client.ExecuteTest(ctx, &ExecuteTestRequest{TestID: "test", ContextBranchName: hint})
				case "workflow":
					_, err = client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{WorkflowID: "workflow", ContextBranchName: hint})
				case "device":
					_, err = client.StartDevice(ctx, &StartDeviceRequest{AppID: "app", ContextBranchName: hint})
				case "url-upload":
					_, err = client.CreateBuildFromURL(ctx, &CreateBuildFromURLRequest{AppID: "app", ContextBranchName: hint})
				}
				if err != nil || calls != 1 {
					t.Fatalf("calls=%d error=%v", calls, err)
				}
			})
		}
	}
}

func TestContextBoundsAndResumeIdentity(t *testing.T) {
	if err := (BuildSelection{ContextBranchName: strings.Repeat("界", MaxContextBranchNameLength)}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (BuildSelection{ContextBranchName: strings.Repeat("界", MaxContextBranchNameLength+1)}).Validate(); err == nil {
		t.Fatal("oversize accepted")
	}
	key := func(id, name, hint string) string {
		return uploadDestinationStateKeyWithContext("app", "v1", "app.apk", 10, id, name, hint)
	}
	legacy := uploadStateKey("app", "v1", "app.apk", 10)
	if key("", "", "") != legacy {
		t.Fatal("legacy identity changed")
	}
	if key("", "", "main") == legacy || key("", "", "main") == key("", "", "feature") || key("", "", "main") == key("", "main", "") {
		t.Fatal("destinations share state")
	}
	if key(branchTestID, "", "before") != key(branchTestID, "", "after") || key("", "main", "before") != key("", "main", "after") {
		t.Fatal("irrelevant hint changed frozen identity")
	}
}

func TestContextUploadFinalizationUsesOnlyFrozenUpload(t *testing.T) {
	artifact := writeTestArtifact(t)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/app/builds/upload-session":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["context_branch_name"] != "feature" {
				t.Error("missing upload context")
			}
			if _, ok := body["branch_name"]; ok {
				t.Error("context promoted to explicit")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"upload_id": "frozen", "upload_url": server.URL + "/artifact"})
		case "/artifact":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/apps/app/builds":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, key := range []string{"context_branch_name", "branch_name", "branch_id"} {
				if _, ok := body[key]; ok {
					t.Errorf("finalization reselects: %s", key)
				}
			}
			if body["upload_id"] != "frozen" {
				t.Error("lost frozen upload")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": buildTestID, "branch_id": branchTestID, "branch_name": "renamed"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	result, err := NewClientWithBaseURL("test", server.URL).UploadBuild(context.Background(), &UploadBuildRequest{AppID: "app", Version: "v1", FilePath: artifact, ContextBranchName: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if result.BranchName != "renamed" {
		t.Errorf("result=%+v", result)
	}
}

func TestContextUploadDoesNotFallBackToLegacyBackend(t *testing.T) {
	for _, multipart := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "multipart"}[multipart], func(t *testing.T) {
			if multipart {
				withMultipartThreshold(t, 1)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				expected := "/api/v1/apps/app/builds/upload-session"
				if multipart {
					expected = "/api/v1/apps/app/builds/multipart-upload/start"
				}
				if r.URL.Path != expected {
					t.Errorf("context silently fell back: %s", r.URL.Path)
				}
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["context_branch_name"] != "feature" {
					t.Error("context omitted from start")
				}
				http.Error(w, "not supported", http.StatusMethodNotAllowed)
			}))
			defer server.Close()
			_, err := NewClientWithBaseURL("test", server.URL).UploadBuild(context.Background(), &UploadBuildRequest{AppID: "app", Version: "v1", FilePath: writeTestArtifact(t), ContextBranchName: "feature"})
			if err == nil || !strings.Contains(err.Error(), "does not support branch upload destinations") || calls != 1 {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestContextMultipartResumeRetainsFrozenDestination(t *testing.T) {
	withMultipartThreshold(t, 1)
	artifact := writeTestArtifact(t)
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := fileSHA256(artifact)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/apps/app/builds" {
			t.Errorf("resume looked up mutable branch/default: %s", r.URL.Path)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, key := range []string{"branch_id", "branch_name", "context_branch_name"} {
			if _, ok := body[key]; ok {
				t.Errorf("resume must use upload identity: %s", key)
			}
		}
		if body["upload_id"] != "frozen-upload" {
			t.Error("lost frozen upload")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"build","branch_name":"renamed"}`))
	}))
	defer server.Close()
	client := NewClientWithBaseURL("test", server.URL)
	client.uploadStateDir = t.TempDir()
	key := uploadDestinationStateKeyWithContext("app", "v1", artifact, info.Size(), branchTestID, "", "old-git-branch")
	if err := client.writeResumableUploadState(key, &resumableUploadState{UploadID: "frozen-upload", S3UploadID: "s3", FileSize: info.Size(), FileHash: hash, Assembled: true}); err != nil {
		t.Fatal(err)
	}
	result, err := client.UploadBuild(context.Background(), &UploadBuildRequest{AppID: "app", Version: "v1", FilePath: artifact, BranchID: branchTestID, ContextBranchName: "changed-git-branch"})
	if err != nil {
		t.Fatal(err)
	}
	if result.BranchName != "renamed" || calls != 1 {
		t.Fatalf("calls=%d result=%+v", calls, result)
	}
	if client.loadResumableUploadState(key) != nil {
		t.Error("completed resume state not cleared")
	}
}

func TestOversizeContextFailsBeforeAnyRuntimeOrUploadRequest(t *testing.T) {
	hint := strings.Repeat("x", MaxContextBranchNameLength+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("oversize context reached backend: %s", r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client := NewClientWithBaseURL("test", server.URL)
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"test": func() error {
			_, err := client.ExecuteTest(ctx, &ExecuteTestRequest{ContextBranchName: hint})
			return err
		},
		"workflow": func() error {
			_, err := client.ExecuteWorkflow(ctx, &ExecuteWorkflowRequest{ContextBranchName: hint})
			return err
		},
		"device": func() error {
			_, err := client.StartDevice(ctx, &StartDeviceRequest{ContextBranchName: hint})
			return err
		},
		"upload": func() error {
			_, err := client.UploadBuild(ctx, &UploadBuildRequest{ContextBranchName: hint})
			return err
		},
		"url upload": func() error {
			_, err := client.CreateBuildFromURL(ctx, &CreateBuildFromURLRequest{ContextBranchName: hint})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "4096") {
				t.Fatalf("wrong error %v", err)
			}
		})
	}
	if _, err := client.ExecuteTest(ctx, nil); err == nil {
		t.Error("nil test request accepted")
	}
	if _, err := client.ExecuteWorkflow(ctx, nil); err == nil {
		t.Error("nil workflow request accepted")
	}
}
