package devloop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCommandRunnerDelegatesCanonicalCommands(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	workDir := t.TempDir()
	binary := writeFakeRevyl(t, `#!/bin/sh
case "$*" in
  *"dev --detach"*)
    printf '%s\n' '{"context":"default","state":"preparing","pid":42,"platform":"ios","session_id":"session-1","session_index":0,"viewer_url":"https://viewer","build":{"status":"running"}}'
    ;;
  *"dev status"*)
    printf '%s\n' '{"running":true,"context":"default","state":"building","platform":"ios","build_mode":"remote","session_id":"session-1","session_owned":true,"viewer_url":"https://viewer","last_rebuild_status":"running","last_rebuild":{"started_at":"2026-07-19T10:00:00Z","seq":2,"status":"running","push_mode":"pending","remote_job_id":"job-1","logs":[{"at":"2026-07-19T10:00:01Z","kind":"info","message":"Remote build queued"}]},"build":{"state":"queued","phase":"remote_queue","remote_job_id":"job-1"}}'
    ;;
  *"dev rebuild"*)
    if [ -n "$REVYL_INTERNAL_REBUILD_PROGRESS" ] || [ -n "$REVYL_INTERNAL_REBUILD_CONTROL" ] || [ -n "$REVYL_INTERNAL_REBUILD_HANDLE" ]; then
      printf '%s\n' 'unexpected internal rebuild environment' >&2
      exit 3
    fi
    printf '%s\n' '{"status":"success","duration_ms":1200,"data_preserved":true,"remote_build_version_id":"version-1"}'
    ;;
  *"dev stop"*)
    printf '%s\n' '{}'
    ;;
  *)
    printf 'unexpected args: %s\n' "$*" >&2
    exit 2
    ;;
esac
`)
	runner := &CommandRunner{BinaryPath: binary, DevMode: true}

	start, err := runner.Start(context.Background(), workDir, StartRequest{
		Platform: "ios",
		Remote:   true,
	})
	if err != nil || start.ViewerURL != "https://viewer" {
		t.Fatalf("Start() = %+v, %v", start, err)
	}
	if start.Build.State != BuildStatePreparing {
		t.Fatalf("Start() build state = %q, want preparing", start.Build.State)
	}
	status, err := runner.Status(context.Background(), workDir, "default")
	if err != nil || !status.Running {
		t.Fatalf("Status() = %+v, %v", status, err)
	}
	if status.BuildMode != "remote" || status.Build.State != BuildStateQueued || status.Build.Phase != "remote_queue" {
		t.Fatalf("Status() remote build = %+v", status)
	}
	if status.LastRebuild == nil || status.LastRebuild.Sequence != 2 || len(status.LastRebuild.Logs) != 1 {
		t.Fatalf("Status() rebuild progress = %+v", status.LastRebuild)
	}
	rebuild, err := runner.Rebuild(context.Background(), workDir, RebuildRequest{TimeoutSeconds: 60})
	if err != nil || rebuild.Status != "success" {
		t.Fatalf("Rebuild() = %+v, %v", rebuild, err)
	}
	if rebuild.Build.State != BuildStateSuccess || !rebuild.Build.FreshBuildApplied {
		t.Fatalf("Rebuild() semantic build = %+v", rebuild.Build)
	}
	if rebuild.BuiltVersionID != "version-1" || rebuild.Build.BuiltVersion != "version-1" {
		t.Fatalf("Rebuild() version metadata = %+v", rebuild)
	}
	stop, err := runner.Stop(context.Background(), workDir, "default")
	if err != nil || !stop.Stopped {
		t.Fatalf("Stop() = %+v, %v", stop, err)
	}
}

func TestCommandRunnerPreservesStructuredFailure(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	binary := writeFakeRevyl(t, `#!/bin/sh
printf '%s\n' '{"status":"build_failed","error":"compiler failed"}'
printf '%s\n' 'rebuild failed' >&2
exit 1
`)
	runner := &CommandRunner{BinaryPath: binary}

	result, err := runner.Rebuild(context.Background(), t.TempDir(), RebuildRequest{})
	if err == nil {
		t.Fatal("Rebuild() error = nil")
	}
	if result.Status != "build_failed" || result.Error != "compiler failed" {
		t.Fatalf("Rebuild() result = %+v", result)
	}
}

func TestCommandRunnerStreamsRebuildProgressFromSingleChild(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	binary := writeFakeRevyl(t, `#!/bin/sh
if [ "$REVYL_INTERNAL_REBUILD_PROGRESS" != "jsonl-v1" ]; then
  printf '%s\n' 'missing progress environment' >&2
  exit 2
fi
case "$*" in
  *progress*)
    printf '%s\n' 'progress mode leaked into CLI arguments' >&2
    exit 2
    ;;
esac
printf '%s\n' 'REVYL_REBUILD_PROGRESS {"sequence":2,"status":"running","state":"queued","phase":"remote_queue","remote_job_id":"job-1"}' >&2
printf '%s\n' 'REVYL_REBUILD_PROGRESS malformed-json' >&2
printf '%s\n' 'ordinary diagnostic' >&2
printf '%s\n' 'REVYL_REBUILD_PROGRESS {"sequence":2,"status":"running","state":"building","phase":"compile","message":"Compiling app","remote_job_id":"job-1"}' >&2
printf '%s\n' '{"status":"success","duration_ms":1200,"remote_job_id":"job-1","remote_build_version_id":"version-1"}'
`)
	runner := &CommandRunner{BinaryPath: binary}
	var progress []RebuildProgressEvent

	result, err := runner.Rebuild(context.Background(), t.TempDir(), RebuildRequest{
		TimeoutSeconds: 60,
		OnProgress: func(event RebuildProgressEvent) {
			progress = append(progress, event)
		},
	})

	if err != nil {
		t.Fatalf("Rebuild(): %v", err)
	}
	if result.Status != "success" || result.RemoteJobID != "job-1" || result.BuiltVersionID != "version-1" {
		t.Fatalf("Rebuild() result = %+v", result)
	}
	if len(progress) != 2 {
		t.Fatalf("progress = %#v, want two valid events", progress)
	}
	if progress[0].State != BuildStateQueued || progress[1].Message != "Compiling app" {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestCommandRunnerTriggersThenWaitsWithoutSecondSignal(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	binary := writeFakeRevyl(t, `#!/bin/sh
case "$REVYL_INTERNAL_REBUILD_CONTROL" in
  trigger-json-v1)
    case "$*" in
      *"--wait"*)
        printf '%s\n' 'trigger unexpectedly waited' >&2
        exit 2
        ;;
    esac
    printf '%s\n' '{"project_dir":"/tmp/project","context":"default","baseline_sequence":4,"expected_sequence":5,"process_id":42,"process_started_at_nano":99,"requested_at":"2026-07-19T10:00:00Z"}'
    ;;
  wait-json-v1)
    case "$*" in
      *"--wait --json"*) ;;
      *)
        printf '%s\n' 'wait flags missing' >&2
        exit 2
        ;;
    esac
    case "$REVYL_INTERNAL_REBUILD_HANDLE" in
      *'"expected_sequence":5'*) ;;
      *)
        printf '%s\n' 'wait handle missing' >&2
        exit 2
        ;;
    esac
    printf '%s\n' 'REVYL_REBUILD_PROGRESS {"sequence":5,"status":"running","state":"building"}' >&2
    printf '%s\n' '{"status":"success","duration_ms":1200,"remote_build_version_id":"version-5"}'
    ;;
  *)
    printf '%s\n' 'missing rebuild control mode' >&2
    exit 2
    ;;
esac
`)
	runner := &CommandRunner{BinaryPath: binary}

	handle, err := runner.TriggerRebuild(
		context.Background(),
		t.TempDir(),
		TriggerRebuildRequest{Context: "default"},
	)
	if err != nil {
		t.Fatalf("TriggerRebuild(): %v", err)
	}
	if handle.ExpectedSequence != 5 || handle.ProcessID != 42 {
		t.Fatalf("TriggerRebuild() handle = %+v", handle)
	}

	var progress []RebuildProgressEvent
	result, err := runner.WaitForRebuild(
		context.Background(),
		t.TempDir(),
		WaitForRebuildRequest{
			Handle:         handle,
			TimeoutSeconds: 60,
			OnProgress: func(event RebuildProgressEvent) {
				progress = append(progress, event)
			},
		},
	)
	if err != nil {
		t.Fatalf("WaitForRebuild(): %v", err)
	}
	if result.Status != "success" || result.BuiltVersionID != "version-5" {
		t.Fatalf("WaitForRebuild() result = %+v", result)
	}
	if len(progress) != 1 || progress[0].Sequence != 5 {
		t.Fatalf("WaitForRebuild() progress = %#v", progress)
	}
}

func TestCommandRunnerProgressFailurePreservesNonProgressStderr(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	binary := writeFakeRevyl(t, `#!/bin/sh
printf '%s\n' 'REVYL_REBUILD_PROGRESS {"sequence":3,"status":"running","state":"building"}' >&2
printf '%s\n' 'compiler process failed' >&2
printf '%s\n' '{"status":"build_failed","error":"compiler failed"}'
exit 1
`)
	runner := &CommandRunner{BinaryPath: binary}
	var progress []RebuildProgressEvent

	result, err := runner.Rebuild(context.Background(), t.TempDir(), RebuildRequest{
		OnProgress: func(event RebuildProgressEvent) {
			progress = append(progress, event)
		},
	})

	if err == nil {
		t.Fatal("Rebuild() error = nil")
	}
	if result.Status != "build_failed" || len(progress) != 1 {
		t.Fatalf("Rebuild() result=%+v progress=%#v", result, progress)
	}
	if !strings.Contains(err.Error(), "compiler process failed") {
		t.Fatalf("Rebuild() error = %q, missing non-progress stderr", err)
	}
	if strings.Contains(err.Error(), RebuildProgressPrefix) {
		t.Fatalf("Rebuild() error leaked progress protocol: %q", err)
	}
}

func TestBoundedTailBufferRetainsLatestStderr(t *testing.T) {
	buffer := newBoundedTailBuffer(8)
	_, _ = buffer.Write([]byte("first-"))
	_, _ = buffer.Write([]byte("second"))

	got := buffer.String()

	if got != "[earlier stderr truncated]\nt-second" {
		t.Fatalf("bounded stderr = %q", got)
	}
}

func TestCommandRunnerProgressStreamStopsOnCancellation(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	binary := writeFakeRevyl(t, `#!/bin/sh
printf '%s\n' 'REVYL_REBUILD_PROGRESS {"sequence":5,"status":"running","state":"building"}' >&2
while :; do :; done
`)
	runner := &CommandRunner{BinaryPath: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := runner.Rebuild(ctx, t.TempDir(), RebuildRequest{
		OnProgress: func(event RebuildProgressEvent) {},
	})

	if err == nil {
		t.Fatal("Rebuild() cancellation error = nil")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
	}
}

func TestCommandRunnerClassifiesCapacityBlock(t *testing.T) {
	t.Setenv("REVYL_BINARY", "")
	binary := writeFakeRevyl(t, `#!/bin/sh
printf '%s\n' '{"running":true,"last_rebuild_status":"capacity_blocked","last_rebuild_error":"build capacity unavailable","seeded_version":"seed-1"}'
`)
	runner := &CommandRunner{BinaryPath: binary}

	result, err := runner.Status(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("Status(): %v", err)
	}
	if result.Build.State != BuildStateCapacityBlocked || !result.Build.Retryable {
		t.Fatalf("capacity build = %+v", result.Build)
	}
	if result.Build.SeededVersion != "seed-1" {
		t.Fatalf("seeded version = %q", result.Build.SeededVersion)
	}
}

func TestCommandRunnerResolvesEnvironmentBinaryPerInvocation(t *testing.T) {
	binary := writeFakeRevyl(t, `#!/bin/sh
printf '%s\n' '{"context":"default","viewer_url":"https://viewer"}'
`)
	t.Setenv("REVYL_BINARY", binary)
	runner := &CommandRunner{BinaryPath: filepath.Join(t.TempDir(), "deleted-revyl")}

	result, err := runner.Start(context.Background(), t.TempDir(), StartRequest{})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if result.ViewerURL != "https://viewer" {
		t.Fatalf("Start() viewer URL = %q", result.ViewerURL)
	}
}

func TestCommandRunnerRejectsInvalidEnvironmentBinary(t *testing.T) {
	fallback := writeFakeRevyl(t, "#!/bin/sh\nprintf '{}\\n'\n")
	requested := filepath.Join(t.TempDir(), "missing-revyl")
	t.Setenv("REVYL_BINARY", requested)
	runner := &CommandRunner{BinaryPath: fallback}

	if _, err := runner.Status(context.Background(), t.TempDir(), ""); err == nil {
		t.Fatal("Status() error = nil")
	}
}

// writeFakeRevyl creates a deterministic executable fixture.
func writeFakeRevyl(t *testing.T, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture is not executable on Windows")
	}
	path := filepath.Join(t.TempDir(), "revyl")
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write fake revyl: %v", err)
	}
	return path
}
