package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestComputerMaxBytesMatchEmbeddedSchema(t *testing.T) {
	if ComputerMaxOutputBytes != 1_048_576 {
		t.Fatalf("ComputerMaxOutputBytes = %d, want 1048576", ComputerMaxOutputBytes)
	}
	if ComputerMaxWriteBytes != 65_536 {
		t.Fatalf("ComputerMaxWriteBytes = %d, want 65536", ComputerMaxWriteBytes)
	}
}

func TestComputerSchemaByteCountMaximumRefusesAnInvalidSchema(t *testing.T) {
	for name, schema := range map[string]string{
		"missing_definition": `{"$defs":{}}`,
		"missing_maximum":    `{"$defs":{"Limit":{"properties":{"byte_count":{}}}}}`,
		"fraction":           `{"$defs":{"Limit":{"properties":{"byte_count":{"maximum":1.5}}}}}`,
		"string":             `{"$defs":{"Limit":{"properties":{"byte_count":{"maximum":"1"}}}}}`,
		"zero":               `{"$defs":{"Limit":{"properties":{"byte_count":{"maximum":0}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if message := fmt.Sprint(recover()); !strings.Contains(message, "Limit.byte_count.maximum") {
					t.Fatalf("expected a panic naming the schema field, got %q", message)
				}
			}()
			computerSchemaByteCountMaximum([]byte(schema), "Limit")
		})
	}
	if value := computerSchemaByteCountMaximum([]byte(`{"$defs":{"Limit":{"properties":{"byte_count":{"maximum":12.0}}}}}`), "Limit"); value != 12 {
		t.Fatalf("maximum 12.0 became %d", value)
	}
}

func TestComputerOperationIDMatchesPython(t *testing.T) {
	got := ComputerOperationID(uuid.MustParse("11111111-1111-4111-8111-111111111111"), uuid.MustParse("22222222-2222-4222-8222-222222222222"))
	if got.String() != "61396d92-7faa-5629-b99c-b073e1e76122" {
		t.Fatalf("operation ID = %s", got)
	}
}

func TestComputerBinaryBoundsAndAlphabets(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 16_383, 16_384, ComputerMaxOutputBytes, ComputerMaxOutputBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			data := bytes.Repeat([]byte{255}, size)
			for _, encoded := range []string{base64.StdEncoding.EncodeToString(data), EncodeComputerBytes(data)} {
				decoded, err := DecodeComputerBytes(encoded, ComputerMaxOutputBytes)
				if size > ComputerMaxOutputBytes {
					if err == nil {
						t.Fatal("oversize decoded data accepted")
					}
					continue
				}
				if err != nil || !bytes.Equal(decoded, data) {
					t.Fatal("binary round trip failed")
				}
			}
		})
	}
	for _, encoded := range []string{"_w", "_w=", "!w==", "_w==\n", "_x=="} {
		if _, err := DecodeComputerBytes(encoded, 64); err == nil {
			t.Fatal("invalid or noncanonical server base64 accepted")
		}
	}
	for _, size := range []int{16_383, 16_384} {
		request := ComputerCommandRequest{ComputerSessionId: uuid.New(), RequestId: uuid.New()}
		encoded := EncodeComputerBytes(bytes.Repeat([]byte{255}, size))
		_ = request.Command.FromComputerExec(ComputerExec{Argv: []string{"x"}, Stdin: &encoded})
		if err := ValidateComputerCommand(request); (err == nil) != (size == 16_383) {
			t.Fatal("combined argv/stdin bound changed")
		}
		body, _ := json.Marshal(request)
		if !bytes.Contains(body, []byte(`"kind":"exec"`)) {
			t.Fatal("generated request omitted its explicit discriminator")
		}
	}
}

func TestComputerFileWriteUsesItsSchemaLimit(t *testing.T) {
	for _, size := range []int{ComputerMaxWriteBytes, ComputerMaxWriteBytes + 1} {
		request := ComputerCommandRequest{ComputerSessionId: uuid.New(), RequestId: uuid.New()}
		data := EncodeComputerBytes(bytes.Repeat([]byte{'x'}, size))
		if err := request.Command.FromComputerFileWrite(ComputerFileWrite{Path: "file", Data: data}); err != nil {
			t.Fatal(err)
		}
		if err := ValidateComputerCommand(request); (err == nil) != (size == ComputerMaxWriteBytes) {
			t.Fatalf("write size %d returned %v", size, err)
		}
	}
}

func TestComputerFileWriteResultUsesItsSchemaLimit(t *testing.T) {
	body := fmt.Sprintf(`{"kind":"file_write","byte_count":%d}`, ComputerMaxWriteBytes+1)
	if _, err := inspectComputerResult([]byte(body), uuid.New()); err == nil {
		t.Fatal("file write result larger than the schema limit was accepted")
	}
}

func TestComputerFileLimitErrorsNameTheirSchemaCaps(t *testing.T) {
	request := ComputerCommandRequest{ComputerSessionId: uuid.New(), RequestId: uuid.New()}
	limit := ComputerMaxOutputBytes + 1
	if err := request.Command.FromComputerFileRead(ComputerFileRead{Path: "file", MaxBytes: &limit}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateComputerCommand(request); err == nil || err.Error() != "computer read limit must be 1 to 1048576 bytes" {
		t.Fatalf("read error does not name its limit: %v", err)
	}
	if err := request.Command.FromComputerFileWrite(ComputerFileWrite{Path: "file", Data: EncodeComputerBytes(bytes.Repeat([]byte{'x'}, ComputerMaxWriteBytes+1))}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateComputerCommand(request); err == nil || err.Error() != "computer file data is invalid or exceeds 65536 bytes" {
		t.Fatalf("write error does not name its limit: %v", err)
	}
}

func TestComputerPendingIsStrictly202Null(t *testing.T) {
	id, operationID := uuid.New(), uuid.New()
	for _, tc := range []struct {
		status  int
		body    string
		pending bool
	}{
		{202, "null", true}, {202, " \t\r\nnull\n\r\t ", true}, {202, `{}`, false}, {202, `null {}`, false}, {200, "null", false}, {200, `{}`, false},
		{202, "\u00a0null", false}, {202, "null\u00a0", false},
		{202, "\u2003null", false}, {202, "\ufeffnull", false},
		{202, "\vnull", false}, {202, "null\f", false},
		{202, "\xffnull", false}, {202, "null null", false}, {202, `"null"`, false},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.body), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := NewClientWithBaseURL("fixture-key", server.URL).GetComputerOperation(context.Background(), id, operationID)
			if tc.pending {
				if result != nil || err != nil {
					t.Fatal("202/null not preserved")
				}
			} else if err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestComputerRejectsMissingNullAndUnknownTags(t *testing.T) {
	id, operationID := uuid.New(), uuid.New()
	for _, execution := range []string{
		`{}`, `{"outcome":null}`, `{"outcome":"future"}`, `{"outcome":"succeeded","result":{}}`,
		`{"outcome":"succeeded","result":{"kind":null}}`, `{"outcome":"succeeded","result":{"kind":"future"}}`,
		`{"outcome":"succeeded","result":{"kind":"exec","exit_code":1,"stdout":"","stderr":""}}`,
		`{"outcome":"unknown","error_code":"private-canary","termination":"unknown"}`,
	} {
		body := fmt.Sprintf(`{"computer_session_id":%q,"operation_id":%q,"evidence":"recorded","execution":%s}`, id, operationID, execution)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := NewClientWithBaseURL("fixture-key", server.URL).GetComputerOperation(context.Background(), id, operationID)
		server.Close()
		if err == nil || strings.Contains(err.Error(), "private-canary") {
			t.Fatal("invalid discriminator accepted or exposed")
		}
	}
}

func TestComputerErrorBodiesStayPrivate(t *testing.T) {
	for _, body := range []string{`{"detail":"private-canary"}`, `{"detail":{"message":"private-canary"}}`, `<html>private-canary</html>`, `{"detail":"runtime_unavailable"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); _, _ = w.Write([]byte(body)) }))
		_, err := NewClientWithBaseURL("fixture-key", server.URL).GetComputerRun(context.Background(), uuid.New())
		server.Close()
		if err == nil || strings.Contains(err.Error(), "private-canary") {
			t.Fatal("HTTP error body escaped into diagnostics")
		}
	}
}

func TestComputerRefusalCodesSurface(t *testing.T) {
	for status, code := range map[int]string{503: "start_failed", 429: "computer_limit", 403: "computer_not_enabled"} {
		body := fmt.Sprintf(`{"detail":%q}`, code)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }))
		_, err := NewClientWithBaseURL("fixture-key", server.URL).GetComputerRun(context.Background(), uuid.New())
		server.Close()
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status || apiErr.Message != "computer request refused: "+code {
			t.Fatalf("%s was not surfaced: %v", code, err)
		}
	}
}

func TestComputerGuestPathValidation(t *testing.T) {
	for _, path := range []string{"", "/absolute", "../escape", "a/../b", "a//b", "a/./b", "a\\b", "a:b", "a\n", strings.Repeat("a", 1025)} {
		if ValidateComputerPath(path) == nil {
			t.Fatal("invalid guest path accepted")
		}
	}
	if err := ValidateComputerPath("workspace/a file.txt"); err != nil {
		t.Fatal(err)
	}
}
