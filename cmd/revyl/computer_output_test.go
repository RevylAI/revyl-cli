package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/analytics"
	"github.com/revyl/cli/internal/testutil"
	"github.com/revyl/cli/internal/ui"
)

func TestComputerInputPreservesBytesAndRejectsOverflow(t *testing.T) {
	data := []byte{0, 255, 254, '\n', 'a'}
	for _, limit := range []int{len(data) - 1, len(data), len(data) + 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			got, err := readComputerInput(bytes.NewReader(data), limit)
			if limit < len(data) {
				if got != nil || err == nil {
					t.Fatal("oversize input was accepted or truncated")
				}
				return
			}
			if err != nil || !bytes.Equal(got, data) {
				t.Fatal("bounded binary input did not round trip")
			}
		})
	}
	for _, limit := range []int{0, -1} {
		if data, err := readComputerInput(strings.NewReader(""), limit); data != nil || err == nil {
			t.Fatal("nonpositive input limit accepted")
		}
	}
}

func TestComputerInputReadIsBounded(t *testing.T) {
	reader := &computerCountingReader{}
	data, err := readComputerInput(reader, 64)
	if data != nil || err == nil || reader.readBytes != 65 {
		t.Fatalf("input bound: bytes=%d error=%v", reader.readBytes, err)
	}
}

func TestComputerRawOutputPreservesExactBytes(t *testing.T) {
	data := []byte{0, 255, 254, '\r', '\n', 'a'}
	var output bytes.Buffer
	if err := writeComputerOutput(&output, data, len(data)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), data) {
		t.Fatal("raw output changed bytes or added a newline")
	}
	output.Reset()
	if err := writeComputerOutput(&output, data, len(data)-1); err == nil || output.Len() != 0 {
		t.Fatal("oversize output was written")
	}
}

func TestComputerJSONIsOneBoundedValue(t *testing.T) {
	value := map[string]any{"bytes": []byte{0, 255, 254}, "text": "output\ncanary"}
	expected, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeComputerJSON(&output, value, len(expected)+1); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil || decoded["text"] != value["text"] {
		t.Fatal("JSON output did not preserve the value")
	}
	if err := decoder.Decode(&decoded); err != io.EOF {
		t.Fatal("JSON output contains a banner or extra value")
	}
	output.Reset()
	if err := writeComputerJSON(&output, value, len(expected)); err == nil || output.Len() != 0 {
		t.Fatal("JSON byte bound did not include its newline")
	}
}

func TestComputerIOErrorsDoNotExposeUnderlyingDetails(t *testing.T) {
	const private = "private-file-path-and-content-canary"
	_, readErr := readComputerInput(computerFailingReader{err: errors.New(private)}, 64)
	writeErr := writeComputerOutput(computerFailingWriter{err: errors.New(private)}, []byte(private), 64)
	shortErr := writeComputerOutput(computerFailingWriter{}, []byte(private), 64)
	var output bytes.Buffer
	marshalErr := writeComputerJSON(&output, computerFailingJSON{err: errors.New(private)}, 64)
	for _, err := range []error{readErr, writeErr, shortErr, marshalErr} {
		if err == nil || strings.Contains(err.Error(), private) || strings.Contains(fmt.Sprintf("%+v", err), private) {
			t.Fatal("I/O failure did not produce a fixed safe error")
		}
	}
	if output.Len() != 0 {
		t.Fatal("failed JSON encoding wrote partial output")
	}
}

func TestComputerOutputDoesNotEnterLifecycleAnalytics(t *testing.T) {
	testutil.SetHomeDir(t, t.TempDir())
	t.Setenv("REVYL_API_KEY", "")
	const private = "computer-output-content-canary"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, private)
	}))
	defer server.Close()
	for _, mode := range []string{"raw", "json", "write_failure", "failure_after_output"} {
		t.Run(mode, func(t *testing.T) {
			var captured analytics.TelemetryPayload
			recorder := analytics.NewWithFlusher(analytics.Config{}, func(payload analytics.TelemetryPayload) {
				captured = payload
			})
			command := &cobra.Command{Use: "exec"}
			computer := &cobra.Command{Use: "computer"}
			root := &cobra.Command{Use: "revyl"}
			root.AddCommand(computer)
			computer.AddCommand(command)
			command.Flags().String("fixture-path", "", "")
			if err := command.Flags().Set("fixture-path", "private-path-canary"); err != nil {
				t.Fatal(err)
			}
			run := recorder.StartCommand(command, []string{"private-argv-canary"})
			ui.SetOutputObserver(run.ObserveOutput)
			defer ui.SetOutputObserver(nil)
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			data, err := readComputerInput(response.Body, 128)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			var commandErr error
			switch mode {
			case "raw", "failure_after_output":
				commandErr = writeComputerOutput(&output, data, 128)
				if mode == "failure_after_output" {
					commandErr = errors.New("computer operation outcome is unknown")
				}
			case "json":
				commandErr = writeComputerJSON(&output, map[string]string{"fixture": string(data)}, 128)
			case "write_failure":
				commandErr = writeComputerOutput(computerFailingWriter{err: errors.New(private)}, data, 128)
			}
			run.Complete(commandErr)
			run.Flush()
			if len(captured.Events) != 2 || captured.Events[0].Event != analytics.CliCommandStartedEvent {
				t.Fatalf("lifecycle event count=%d", len(captured.Events))
			}
			wantTerminal := analytics.CliCommandCompletedEvent
			if commandErr != nil {
				wantTerminal = analytics.CliCommandFailedEvent
			}
			if captured.Events[1].Event != wantTerminal {
				t.Fatal("output plumbing changed the lifecycle outcome")
			}
			encoded, err := json.Marshal(captured)
			if err != nil {
				t.Fatal(err)
			}
			for _, canary := range []string{private, "private-argv-canary", "private-path-canary"} {
				if bytes.Contains(encoded, []byte(canary)) {
					t.Fatal("content entered lifecycle analytics")
				}
			}
			if tail, present := captured.Events[1].Properties["output_tail"]; present && len(tail.([]map[string]any)) != 0 {
				t.Fatal("raw output entered the UI analytics observer")
			}
			if mode != "write_failure" && !strings.Contains(output.String(), private) {
				t.Fatal("authorized output was lost")
			}
		})
	}
}

type computerCountingReader struct{ readBytes int }

func (r *computerCountingReader) Read(data []byte) (int, error) {
	r.readBytes += len(data)
	return len(data), nil
}

type computerFailingReader struct{ err error }

func (r computerFailingReader) Read([]byte) (int, error) { return 0, r.err }

type computerFailingWriter struct{ err error }

func (w computerFailingWriter) Write([]byte) (int, error) { return 0, w.err }

type computerFailingJSON struct{ err error }

func (v computerFailingJSON) MarshalJSON() ([]byte, error) { return nil, v.err }

func TestComputerTablesEscapeTerminalControls(t *testing.T) {
	var output bytes.Buffer
	if err := writeComputerTable(&output, [][]string{{"1", "printf '\x1b]52;c;aGk=\x07' 'ok\rls -la' 'a\tb' 'caf\u00e9'"}}); err != nil {
		t.Fatal(err)
	}
	if want := `1  printf '\x1b]52;c;aGk=\a' 'ok\rls -la' 'a\tb' 'café'` + "\n"; output.String() != want {
		t.Fatalf("table printed %q, want %q", output.String(), want)
	}
	for _, b := range output.Bytes()[:output.Len()-1] {
		if b < 0x20 || b == 0x7f {
			t.Fatalf("table kept control byte %#x", b)
		}
	}
}
