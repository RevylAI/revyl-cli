package analytics

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/auth"
	"github.com/revyl/cli/internal/testutil"
)

const killedCommandChildEnv = "REVYL_ANALYTICS_KILLED_COMMAND_CHILD"

func TestMain(m *testing.M) {
	if IsTelemetryHelper() {
		RunTelemetryHelper(os.Stdin)
		return
	}
	if os.Getenv(killedCommandChildEnv) == "1" {
		runLongCommandUntilKilled()
		return
	}
	os.Exit(m.Run())
}

// runLongCommandUntilKilled stands in for a long `revyl build upload` that an
// agent's tool timeout kills: the command body never returns, so the terminal
// event and its flush never happen.
func runLongCommandUntilKilled() {
	recorder := NewFromEnv(Config{Version: "test", BackendURL: os.Getenv("REVYL_BACKEND_URL")})
	recorder.StartCommand(&cobra.Command{Use: "upload"}, nil)
	_, _ = os.Stdout.WriteString("command body running\n")
	time.Sleep(time.Hour)
}

func TestKilledCommandStillDeliversStartedEvent(t *testing.T) {
	home := t.TempDir()
	testutil.SetHomeDir(t, home)
	t.Setenv("REVYL_API_KEY", "")
	if err := auth.NewManager().SaveCredentials(&auth.Credentials{APIKey: "rk_placeholder_for_test"}); err != nil {
		t.Fatal(err)
	}

	received := make(chan TelemetryEvent, 4)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != defaultBackendAnalyticsPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload TelemetryPayload
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err == nil {
			for _, event := range payload.Events {
				received <- event
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	child := exec.Command(os.Args[0], "-test.run=^$")
	child.Env = append(environmentWithout("REVYL_API_KEY", "REVYL_TELEMETRY_DISABLED", "DO_NOT_TRACK"),
		killedCommandChildEnv+"=1",
		"REVYL_ANALYTICS_TEST=1",
		"REVYL_BACKEND_URL="+backend.URL,
	)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	ready := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(stdout, make([]byte, len("command body running\n")))
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("child never reached the command body: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child did not reach the command body within 10s")
	}
	// An agent's tool timeout fires minutes into a build, long after the
	// started event's millisecond handoff to the helper.
	time.Sleep(time.Second)
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()

	select {
	case event := <-received:
		if event.Event != CliCommandStartedEvent {
			t.Fatalf("event = %q, want %q", event.Event, CliCommandStartedEvent)
		}
		if event.Properties["sent_at_start"] != true {
			t.Fatalf("sent_at_start = %v, want true", event.Properties["sent_at_start"])
		}
		if commandID, _ := event.Properties["command_id"].(string); commandID == "" {
			t.Fatal("started event has no command_id")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("killed command never delivered its started event")
	}
	select {
	case event := <-received:
		t.Fatalf("killed command delivered an unexpected %q", event.Event)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestStartedEventIsHandedOffBeforeCommandBodyRuns(t *testing.T) {
	flushes := make(chan TelemetryPayload, 4)
	recorder := authenticatedTestRecorder(func(payload TelemetryPayload) { flushes <- payload })

	run := recorder.StartCommand(&cobra.Command{Use: "upload"}, nil)

	started := receivePayload(t, flushes)
	if len(started.Events) != 1 || started.Events[0].Event != CliCommandStartedEvent {
		t.Fatalf("payload before the command body = %+v, want only the started event", started.Events)
	}
	if started.Events[0].Properties["sent_at_start"] != true {
		t.Fatalf("sent_at_start = %v, want true", started.Events[0].Properties["sent_at_start"])
	}

	run.Complete(nil)
	run.Flush()

	terminal := receivePayload(t, flushes)
	if len(terminal.Events) != 1 || terminal.Events[0].Event != CliCommandCompletedEvent {
		t.Fatalf("terminal payload = %+v, want only the completed event", terminal.Events)
	}
	if _, exists := terminal.Events[0].Properties["sent_at_start"]; exists {
		t.Fatal("sent_at_start leaked onto the terminal event")
	}
	if started.Events[0].Properties["command_id"] != terminal.Events[0].Properties["command_id"] {
		t.Fatal("started and terminal events do not share one command_id")
	}
}

func TestStartedEventWaitsForTerminalFlushWithoutCredentials(t *testing.T) {
	var payloads []TelemetryPayload
	recorder := testRecorder()
	recorder.flush = func(payload TelemetryPayload) { payloads = append(payloads, payload) }

	run := recorder.StartCommand(&cobra.Command{Use: "login"}, nil)
	if len(payloads) != 0 {
		t.Fatalf("flushed %d payloads before the command finished, want 0", len(payloads))
	}
	run.Complete(nil)
	run.Flush()

	if len(payloads) != 1 || len(payloads[0].Events) != 2 {
		t.Fatalf("payloads = %+v, want one payload with started and completed", payloads)
	}
	if _, exists := payloads[0].Events[0].Properties["sent_at_start"]; exists {
		t.Fatal("a started event sent at exit must not claim sent_at_start")
	}
}

func TestSlowStartedFlushNeverDelaysTheCommand(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var mu sync.Mutex
	var flushed []TelemetryPayload
	first := true
	recorder := authenticatedTestRecorder(func(payload TelemetryPayload) {
		mu.Lock()
		blocking := first
		first = false
		mu.Unlock()
		if blocking {
			<-release
		}
		mu.Lock()
		flushed = append(flushed, payload)
		mu.Unlock()
	})

	begin := time.Now()
	run := recorder.StartCommand(&cobra.Command{Use: "build"}, nil)
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("StartCommand blocked for %v behind a stuck flusher", elapsed)
	}

	commandErr := errors.New("build failed")
	run.Complete(commandErr)
	begin = time.Now()
	run.Flush()
	if elapsed := time.Since(begin); elapsed > backgroundFlushWait+time.Second {
		t.Fatalf("terminal flush waited %v for a stuck started flush", elapsed)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 1 || flushed[0].Events[0].Event != CliCommandFailedEvent {
		t.Fatalf("flushed = %+v, want the failed event despite the stuck started flush", flushed)
	}
	if got := flushed[0].Events[0].Properties["exit_code"]; got != 1 {
		t.Fatalf("exit_code = %v, want 1", got)
	}
}

func authenticatedTestRecorder(flush func(TelemetryPayload)) *Recorder {
	recorder := testRecorder()
	recorder.flush = flush
	recorder.sendStartedAtStart = true
	return recorder
}

func receivePayload(t *testing.T, payloads <-chan TelemetryPayload) TelemetryPayload {
	t.Helper()
	select {
	case payload := <-payloads:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("no payload flushed")
		return TelemetryPayload{}
	}
}

func environmentWithout(names ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		keep := true
		for _, name := range names {
			if strings.HasPrefix(entry, name+"=") {
				keep = false
				break
			}
		}
		if keep {
			env = append(env, entry)
		}
	}
	return env
}
