package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
)

func TestAtlasAnnotationsCommandExposesCompleteLifecycle(t *testing.T) {
	command := newAtlasAnnotationsCommand()
	want := map[string]bool{
		"list": true, "get": true, "create": true, "move": true,
		"reply": true, "edit": true, "delete": true, "resolve": true,
		"dismiss": true, "reopen": true,
	}
	for _, child := range command.Commands() {
		delete(want, child.Name())
		if child.Flags().Lookup("app") == nil {
			t.Fatalf("%s does not expose --app", child.Name())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing annotation commands: %v", want)
	}
}

func TestReadAnnotationBodyRequiresExactlyOneSourceAndSupportsStdin(t *testing.T) {
	command := newAtlasAnnotationsReplyCommand()
	command.SetIn(strings.NewReader("  grounded feedback from stdin  \n"))
	if err := command.Flags().Set("body-file", "-"); err != nil {
		t.Fatal(err)
	}
	body, err := readAnnotationBody(command, annotationBodyOptions{bodyFile: "-"})
	if err != nil {
		t.Fatal(err)
	}
	if body != "grounded feedback from stdin" {
		t.Fatalf("body = %q", body)
	}

	both := newAtlasAnnotationsReplyCommand()
	_ = both.Flags().Set("body", "one")
	_ = both.Flags().Set("body-file", "two")
	if _, err := readAnnotationBody(both, annotationBodyOptions{body: "one", bodyFile: "two"}); err == nil {
		t.Fatal("expected mutually exclusive body source error")
	}
}

func TestReadAnnotationBodyRejectsOversizedFile(t *testing.T) {
	path := t.TempDir() + "/body.txt"
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), (64<<10)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newAtlasAnnotationsReplyCommand()
	if err := command.Flags().Set("body-file", path); err != nil {
		t.Fatal(err)
	}
	if _, err := readAnnotationBody(command, annotationBodyOptions{bodyFile: path}); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("error = %v, want body size rejection", err)
	}
}

func TestCreateDryRunRejectsBodyWithoutCallingBackend(t *testing.T) {
	command := newAtlasAnnotationsCreateCommand()
	command.SilenceUsage = true
	command.SetArgs([]string{
		"--app", "00000000-0000-0000-0000-000000000020",
		"--observation", "00000000-0000-0000-0000-000000000030",
		"--target", "Continue button", "--body", "wrong", "--dry-run",
	})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "prohibits") {
		t.Fatalf("error = %v, want dry-run body rejection", err)
	}
}

func TestAnnotationPreviewRejectsOversizedScreenshot(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/img") {
			response.Header().Set("Content-Type", "image/png")
			_, _ = response.Write(bytes.Repeat([]byte{0}, annotationPreviewImageLimit+1))
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"observation":{"screenshot_url":"` + server.URL + `/img"}}`))
	}))
	defer server.Close()

	command := newAtlasAnnotationsCommand()
	command.SetContext(context.Background())
	client := api.NewClientWithBaseURL("test-key", server.URL)
	err := writeAnnotationPreview(command, client, "app-id", &api.AtlasAnnotationAnchorPreviewResponse{
		ObservationId: "observation-id", PixelX: 1, PixelY: 1,
	}, t.TempDir()+"/preview.png")
	if err == nil || !strings.Contains(err.Error(), "exceeds 20 MiB") {
		t.Fatalf("error = %v, want oversized screenshot rejection", err)
	}
}
