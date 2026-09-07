package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/revyl/cli/internal/api"
)

func TestReportAnnotationsCommandExposesRunNativeLifecycle(t *testing.T) {
	group := newReportAnnotationsCommand()
	for _, flag := range []string{"session-id", "json"} {
		if group.PersistentFlags().Lookup(flag) == nil {
			t.Fatalf("annotations group does not expose persistent --%s", flag)
		}
	}
	want := map[string]bool{
		"create": true, "list": true, "get": true, "reply": true,
		"resolve": true, "dismiss": true, "reopen": true, "severity": true,
	}
	for _, child := range group.Commands() {
		delete(want, child.Name())
		if child.Name() == "create" {
			continue
		}
		if child.Flags().Lookup("app") == nil {
			t.Fatalf("%s does not expose --app", child.Name())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing report annotation commands: %v", want)
	}

	report := newReportCommand()
	if report.Name() != "report" {
		t.Fatalf("group name = %q, want report", report.Name())
	}
	var hasAnnotations bool
	for _, child := range report.Commands() {
		if child.Name() == "annotations" {
			hasAnnotations = true
		}
	}
	if !hasAnnotations {
		t.Fatal("report group does not expose annotations")
	}
}

func TestReportAnnotationsCreateValidatesFlagsBeforeAnyBackendCall(t *testing.T) {
	run := func(args ...string) error {
		command := newReportAnnotationsCreateCommand()
		command.SilenceUsage = true
		command.SetArgs(args)
		command.SetErr(new(bytes.Buffer))
		return command.Execute()
	}

	if err := run("   ", "--target", "the save button"); err == nil || !strings.Contains(err.Error(), "body cannot be empty") {
		t.Fatalf("error = %v, want empty body rejection", err)
	}
	if err := run("broken save button"); err == nil || !strings.Contains(err.Error(), "--target is required") {
		t.Fatalf("error = %v, want missing target rejection", err)
	}
	if err := run("broken", "--target", "save", "--role", "during"); err == nil || !strings.Contains(err.Error(), "--role must be before or after") {
		t.Fatalf("error = %v, want role rejection", err)
	}
	if err := run("broken", "--target", "save", "--action", "not-a-uuid"); err == nil || !strings.Contains(err.Error(), "--action must be a UUID") {
		t.Fatalf("error = %v, want action uuid rejection", err)
	}
	if err := run("broken", "--target", "save", "--severity", "catastrophic"); err == nil || !strings.Contains(err.Error(), "invalid --severity") {
		t.Fatalf("error = %v, want severity rejection", err)
	}
	if err := run("--body", "one", "two", "--target", "save"); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error = %v, want conflicting body source rejection", err)
	}
}

func TestReportAnnotationBodyFilePreservesShellMetacharacters(t *testing.T) {
	path := t.TempDir() + "/finding.md"
	body := "Checkout total changed from $12.00 to $2.00."
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newReportAnnotationsCreateCommand()
	if err := command.Flags().Set("body-file", path); err != nil {
		t.Fatal(err)
	}
	actual, err := readReportAnnotationBody(
		command,
		nil,
		annotationBodyOptions{bodyFile: path},
	)
	if err != nil {
		t.Fatal(err)
	}
	if actual != body {
		t.Fatalf("body = %q, want %q", actual, body)
	}
}

func TestReportAnnotationPreviewReceiptIsPrivateAndRoundTrips(t *testing.T) {
	path := t.TempDir() + "/receipt.json"
	want := reportAnnotationPreviewReceiptFile{
		Version: 1, SessionID: "session-1", Target: "save", ScreenshotRole: "after", PreviewReceipt: strings.Repeat("r", 43),
	}
	if err := writeReportAnnotationPreviewReceipt(path, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("receipt mode = %o, want 600", info.Mode().Perm())
	}
	got, err := readReportAnnotationPreviewReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("receipt = %#v, want %#v", got, want)
	}
	replacement := want
	replacement.Target = "updated save"
	if err := writeReportAnnotationPreviewReceipt(path, replacement); err != nil {
		t.Fatalf("replace receipt: %v", err)
	}
	got, err = readReportAnnotationPreviewReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != replacement {
		t.Fatalf("replacement receipt = %#v, want %#v", got, replacement)
	}
}

func TestReportAnnotationPreviewReceiptReplacesSymlinkWithoutFollowingIt(t *testing.T) {
	directory := t.TempDir()
	victimPath := directory + "/victim.txt"
	receiptPath := directory + "/receipt.json"
	if err := os.WriteFile(victimPath, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victimPath, receiptPath); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	want := reportAnnotationPreviewReceiptFile{
		Version: 1, SessionID: "session-1", Target: "save", ScreenshotRole: "after", PreviewReceipt: strings.Repeat("r", 43),
	}
	if err := writeReportAnnotationPreviewReceipt(receiptPath, want); err != nil {
		t.Fatal(err)
	}
	victim, err := os.ReadFile(victimPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(victim) != "keep me" {
		t.Fatalf("receipt write followed symlink and changed victim: %q", victim)
	}
	got, err := readReportAnnotationPreviewReceipt(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("receipt = %#v, want %#v", got, want)
	}
}

func TestReportAnnotationsListValidatesFilterFlags(t *testing.T) {
	run := func(args ...string) error {
		command := newReportAnnotationsListCommand()
		command.SilenceUsage = true
		command.SetArgs(args)
		command.SetErr(new(bytes.Buffer))
		return command.Execute()
	}

	if err := run("--status", "stale"); err == nil || !strings.Contains(err.Error(), "--status must be") {
		t.Fatalf("error = %v, want status rejection", err)
	}
	if err := run("--severity", "catastrophic"); err == nil || !strings.Contains(err.Error(), "--severity must be") {
		t.Fatalf("error = %v, want severity rejection", err)
	}
	if err := run("--limit", "0"); err == nil || !strings.Contains(err.Error(), "--limit must be between 1 and 100") {
		t.Fatalf("error = %v, want limit rejection", err)
	}
}

func TestReportAnnotationsReplyRequiresExactlyOneBodySource(t *testing.T) {
	command := newReportAnnotationsReplyCommand()
	command.SilenceUsage = true
	command.SetArgs([]string{"thread-1"})
	command.SetErr(new(bytes.Buffer))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "exactly one of --body or --body-file") {
		t.Fatalf("error = %v, want body source rejection", err)
	}
}

func TestReportAnnotationsSeverityRequiresOneChange(t *testing.T) {
	command := newReportAnnotationsSeverityCommand()
	command.SilenceUsage = true
	command.SetArgs([]string{"thread-1"})
	command.SetErr(new(bytes.Buffer))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "provide --severity") {
		t.Fatalf("error = %v, want missing severity rejection", err)
	}
}

func TestResolveReportAnnotationRoleAndActionID(t *testing.T) {
	role, err := resolveReportAnnotationRole(" after ")
	if err != nil || role == nil || *role != "after" {
		t.Fatalf("role = %v err = %v, want after", role, err)
	}
	if _, err := resolveReportAnnotationRole(""); err == nil {
		t.Fatal("expected empty role rejection")
	}

	actionID, err := resolveReportAnnotationActionID("  ")
	if err != nil || actionID != nil {
		t.Fatalf("blank action id should resolve to nil: value=%v err=%v", actionID, err)
	}
	actionID, err = resolveReportAnnotationActionID("00000000-0000-0000-0000-000000000042")
	if err != nil || actionID == nil || uuid.UUID(*actionID).String() != "00000000-0000-0000-0000-000000000042" {
		t.Fatalf("action id mis-resolved: value=%v err=%v", actionID, err)
	}
}

func TestReportAnnotationAppScopeDoesNotRequireDeviceSession(t *testing.T) {
	command := newReportAnnotationsListCommand()

	sessionID, err := resolveReportAnnotationScopeSessionID(command, " app-1 ")

	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "" {
		t.Fatalf("session id = %q, want empty app-wide scope", sessionID)
	}
}

func TestReportAnnotationFeedbackRowIsTabSeparatedAndFirstLineOnly(t *testing.T) {
	severity := api.AtlasAnnotationSeverityIssue
	preview := "Save button overlaps the keyboard\nsecond line detail"
	item := api.AtlasAnnotationFeedbackItem{
		ThreadId:    "thread-1",
		Severity:    &severity,
		Status:      api.AtlasAnnotationStatus("open"),
		CreatedAt:   time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		PreviewText: &preview,
	}
	row := reportAnnotationFeedbackRow(item)
	fields := strings.Split(row, "\t")
	if len(fields) != 5 {
		t.Fatalf("row has %d fields, want 5: %q", len(fields), row)
	}
	if fields[0] != "thread-1" || fields[1] != "issue" || fields[2] != "open" {
		t.Fatalf("row identity fields = %q", row)
	}
	if fields[3] != "2026-08-30T12:00:00Z" {
		t.Fatalf("created_at = %q", fields[3])
	}
	if fields[4] != "Save button overlaps the keyboard" {
		t.Fatalf("first line = %q", fields[4])
	}

	bare := reportAnnotationFeedbackRow(api.AtlasAnnotationFeedbackItem{ThreadId: "thread-2", Status: api.AtlasAnnotationStatus("open")})
	if !strings.HasPrefix(bare, "thread-2\tnone\topen\t") {
		t.Fatalf("severity-less row = %q", bare)
	}
}

func TestCreateSessionGroundedAnnotationThreadHitsSessionEndpoint(t *testing.T) {
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/atlas/v2/sessions/session-1/grounded-annotation-threads" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		buffer := new(bytes.Buffer)
		_, _ = buffer.ReadFrom(request.Body)
		requestBody = buffer.String()
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"thread":{"id":"thread-1","app_id":"app-1","anchor":{"observation_id":"obs-1","x":0.5,"y":0.5,"screenshot_width":100,"screenshot_height":100},"origin_surface":"revyl_cli","status":"open","severity":"issue","version":1,"created_by":{"user_id":"user-1"},"created_at":"2026-08-30T00:00:00Z","last_activity_at":"2026-08-30T00:00:00Z"},"grounding":{"observation_id":"obs-1","normalized_x":0.5,"normalized_y":0.5,"pixel_x":50,"pixel_y":50,"screenshot_width":100,"screenshot_height":100}}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	severity := api.AtlasAnnotationSeverityIssue
	role := api.AtlasSessionGroundedAnnotationThreadCreateRequestScreenshotRole("after")
	receipt := strings.Repeat("r", 43)
	actionID := openapi_types.UUID(uuid.MustParse("00000000-0000-0000-0000-000000000042"))
	result, err := client.CreateSessionGroundedAtlasAnnotationThread(context.Background(), "session-1", &api.AtlasSessionGroundedAnnotationThreadCreateRequest{
		ActionId:        &actionID,
		Body:            "Save button overlaps the keyboard",
		ClientRequestId: openapi_types.UUID(uuid.MustParse("00000000-0000-0000-0000-000000000060")),
		ScreenshotRole:  &role,
		PreviewReceipt:  &receipt,
		Severity:        &severity,
		Target:          "the save button",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"body":"Save button overlaps the keyboard"`,
		`"target":"the save button"`,
		`"severity":"issue"`,
		`"screenshot_role":"after"`,
		`"action_id":"00000000-0000-0000-0000-000000000042"`,
		`"client_request_id":"00000000-0000-0000-0000-000000000060"`,
		`"preview_receipt":"rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr"`,
	} {
		if !strings.Contains(requestBody, fragment) {
			t.Fatalf("request body missing %s: %s", fragment, requestBody)
		}
	}
	if result.Thread.Id != "thread-1" || result.Grounding.ObservationId != "obs-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestPreviewSessionAnnotationAnchorHitsSessionEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/atlas/v2/sessions/session-1/annotation-anchor-preview" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"app_id":"app-1","observation_id":"obs-1","normalized_x":0.5,"normalized_y":0.5,"pixel_x":50,"pixel_y":50,"screenshot_width":100,"screenshot_height":100,"screenshot_url":"https://example.test/screenshot","preview_receipt":"rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr"}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	result, err := client.PreviewSessionAtlasAnnotationAnchor(
		context.Background(),
		"session-1",
		&api.AtlasSessionAnnotationAnchorPreviewRequest{Target: "the Save button"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.AppId != "app-1" || result.PreviewReceipt == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestListAtlasAnnotationFeedbackSendsSessionScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/v1/atlas/v2/annotations/feedback" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if got := request.URL.Query().Get("session_id"); got != "session-1" {
			t.Fatalf("session_id = %q, want session-1", got)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"items":[],"open_count":0,"closed_count":0}`))
	}))
	defer server.Close()

	client := api.NewClientWithBaseURL("test-key", server.URL)
	if _, err := client.ListAtlasAnnotationFeedback(context.Background(), "app-1", "session-1", "", "open", "all", "", 25); err != nil {
		t.Fatal(err)
	}
}

func TestWriteReportAnnotationPreviewUsesThreadEvidenceBeforeProjection(t *testing.T) {
	var projectionCalled atomic.Bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/api/v1/atlas/v2/apps/app-1/annotation-threads/thread-1":
			if got := request.URL.Query().Get("session_id"); got != "session-1" {
				t.Fatalf("session_id = %q, want session-1", got)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"thread":{"screenshot_url":"` + server.URL + `/image.png"}}`))
		case request.URL.Path == "/image.png":
			response.Header().Set("Content-Type", "image/png")
			_ = png.Encode(response, image.NewRGBA(image.Rect(0, 0, 100, 100)))
		default:
			projectionCalled.Store(true)
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	command := newReportAnnotationsCommand()
	command.SetContext(context.Background())
	client := api.NewClientWithBaseURL("test-key", server.URL)
	outputPath := t.TempDir() + "/preview.png"
	err := writeThreadAnnotationPreview(command, client, "app-1", "thread-1", "session-1", &api.AtlasAnnotationAnchorPreviewResponse{
		ObservationId: "observation-1", PixelX: 50, PixelY: 50,
	}, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if projectionCalled.Load() {
		t.Fatal("projection lookup should not run when thread evidence is available")
	}
	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		t.Fatalf("preview file = %#v, err = %v", info, err)
	}
}
