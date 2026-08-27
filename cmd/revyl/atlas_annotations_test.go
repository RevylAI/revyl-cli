package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/revyl/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestAtlasAnnotationsCommandExposesCompleteLifecycle(t *testing.T) {
	command := newAtlasAnnotationsCommand()
	want := map[string]bool{
		"list": true, "members": true, "get": true, "create": true, "move": true,
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

func TestResolveAnnotationMentionsCanonicalizesMemberAndUTF16Offsets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/entity/orgs/members" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("q") != "user-1" {
			t.Fatalf("member query = %q", request.URL.Query().Get("q"))
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"members":[{"user_id":"user-1","display_name":"Hayden White"}]}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	body, mentions, err := resolveAnnotationMentions(
		context.Background(), client, "😀 @{hayden} ping", []string{"hayden=user-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if body != "😀 @Hayden White ping" {
		t.Fatalf("body = %q", body)
	}
	if len(mentions) != 1 || mentions[0].StartUtf16 != 3 || mentions[0].EndUtf16 != 16 {
		t.Fatalf("mentions = %#v", mentions)
	}
}

func TestResolveAnnotationMentionsRejectsAmbiguousBindings(t *testing.T) {
	client := api.NewClientWithBaseURL("test-key", "http://127.0.0.1:1")
	for name, values := range map[string][]string{
		"duplicate alias": {"hayden=user-1", "hayden=user-2"},
		"duplicate user":  {"hayden=user-1", "hw=user-1"},
		"missing token":   {"hayden=user-1"},
		"malformed":       {"hayden"},
	} {
		t.Run(name, func(t *testing.T) {
			body := "@{hayden} @{hw}"
			if name == "missing token" {
				body = "plain text"
			}
			if _, _, err := resolveAnnotationMentions(context.Background(), client, body, values); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	body, mentions, err := resolveAnnotationMentions(context.Background(), client, "literal @{unbound}", nil)
	if err != nil || body != "literal @{unbound}" || mentions != nil {
		t.Fatalf("unbound text changed: body=%q mentions=%#v err=%v", body, mentions, err)
	}
}

func TestAnnotationDeleteWarningDistinguishesRootAndReplyBehavior(t *testing.T) {
	if !strings.Contains(annotationDeleteWarning, "root comment deletion removes the entire thread") {
		t.Fatalf("delete warning does not explain root deletion: %q", annotationDeleteWarning)
	}
	if !strings.Contains(annotationDeleteWarning, "reply deletion removes only that reply") {
		t.Fatalf("delete warning does not explain reply deletion: %q", annotationDeleteWarning)
	}
}

func TestAnnotationAttachmentFlagsAndDeterministicUploadIDs(t *testing.T) {
	for _, command := range []*cobra.Command{
		newAtlasAnnotationsCreateCommand(),
		newAtlasAnnotationsReplyCommand(),
		newAtlasAnnotationsEditCommand(),
	} {
		if command.Flags().Lookup("attach") == nil {
			t.Fatalf("%s does not expose --attach", command.Name())
		}
	}
	edit := newAtlasAnnotationsEditCommand()
	if edit.Flags().Lookup("remove-attachment") == nil || edit.Flags().Lookup("clear-attachments") == nil {
		t.Fatal("edit does not expose attachment removal flags")
	}

	requestID := "00000000-0000-0000-0000-000000000060"
	first, err := annotationClientUploadID(requestID, 0)
	if err != nil {
		t.Fatal(err)
	}
	repeated, _ := annotationClientUploadID(requestID, 0)
	second, _ := annotationClientUploadID(requestID, 1)
	if first != repeated || first == second {
		t.Fatalf("upload IDs are not deterministic and ordinal-scoped: %s %s %s", first, repeated, second)
	}
}

func TestAnnotationAttachmentLimits(t *testing.T) {
	if got := annotationAttachmentLimit("video/mp4"); got != 64<<20 {
		t.Fatalf("video attachment limit = %d, want %d", got, int64(64<<20))
	}
	if got := annotationAttachmentLimit("image/png"); got != 10<<20 {
		t.Fatalf("image attachment limit = %d, want %d", got, int64(10<<20))
	}
	if got := annotationAttachmentLimit("application/pdf"); got != 25<<20 {
		t.Fatalf("PDF attachment limit = %d, want %d", got, int64(25<<20))
	}
	if got := annotationAttachmentTier("application/octet-stream"); got != "download" {
		t.Fatalf("generic attachment tier = %q, want download", got)
	}
	if got := annotationAttachmentDeclaredSizeBucket((64 << 20) + 1); got != "up_to_128_mib" {
		t.Fatalf("declared size bucket = %q, want up_to_128_mib", got)
	}
}

func TestAnnotationUploadResignUsesTypedHTTPStatus(t *testing.T) {
	err := fmt.Errorf("upload attempt: %w", &api.UploadHTTPError{StatusCode: http.StatusForbidden})
	if !annotationUploadNeedsResign(err) {
		t.Fatal("403 upload error should request a new signature")
	}
	if annotationUploadNeedsResign(errors.New("expired status 403")) {
		t.Fatal("plain error text must not control upload retry behavior")
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
