package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// captureStdoutAndStderr redirects os.Stdout and os.Stderr to pipes, runs fn,
// then returns whatever was written to each.
func captureStdoutAndStderr(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut := os.Stdout
	origErr := os.Stderr
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
	}()

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating stderr pipe: %v", err)
	}

	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	wOut.Close()
	wErr.Close()

	var bufOut, bufErr bytes.Buffer
	bufOut.ReadFrom(rOut)
	bufErr.ReadFrom(rErr)

	return bufOut.String(), bufErr.String()
}

func TestPrintInfoWritesToStderr(t *testing.T) {
	SetQuietMode(false)
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintInfo("hello %s", "world")
	})

	if stdout != "" {
		t.Errorf("PrintInfo wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "hello world") {
		t.Errorf("PrintInfo did not write to stderr; got %q", stderr)
	}
}

func TestPrintWarningWritesToStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintWarning("danger %d", 42)
	})

	if stdout != "" {
		t.Errorf("PrintWarning wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "danger 42") {
		t.Errorf("PrintWarning did not write to stderr; got %q", stderr)
	}
}

func TestPrintSuccessWritesToStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintSuccess("done")
	})

	if stdout != "" {
		t.Errorf("PrintSuccess wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "done") {
		t.Errorf("PrintSuccess did not write to stderr; got %q", stderr)
	}
}

func TestPrintErrorWritesToStderr(t *testing.T) {
	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintError("fail")
	})

	if stdout != "" {
		t.Errorf("PrintError wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "fail") {
		t.Errorf("PrintError did not write to stderr; got %q", stderr)
	}
}

func TestPrintInfoSuppressedInQuietMode(t *testing.T) {
	SetQuietMode(true)
	defer SetQuietMode(false)

	stdout, stderr := captureStdoutAndStderr(t, func() {
		PrintInfo("should not appear")
	})

	if stdout != "" {
		t.Errorf("PrintInfo wrote to stdout in quiet mode: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("PrintInfo wrote to stderr in quiet mode: %q", stderr)
	}
}
