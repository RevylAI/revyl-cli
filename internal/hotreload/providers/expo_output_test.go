package providers

import (
	"testing"

	"github.com/revyl/cli/internal/hotreload"
)

func TestExpoDevServerEmitProcessOutput(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	var got hotreload.DevServerOutput
	server.emitProcessOutput(func(output hotreload.DevServerOutput) {
		got = output
	}, hotreload.DevServerOutputStdout, "Metro waiting on exp://...")

	if got.Stream != hotreload.DevServerOutputStdout {
		t.Fatalf("stream = %q, want %q", got.Stream, hotreload.DevServerOutputStdout)
	}
	if got.Line != "Metro waiting on exp://..." {
		t.Fatalf("line = %q, want %q", got.Line, "Metro waiting on exp://...")
	}
}

func TestExpoDevServerSetOutputCallback(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)

	var got hotreload.DevServerOutput
	server.SetOutputCallback(func(output hotreload.DevServerOutput) {
		got = output
	})

	server.emitProcessOutput(server.outputCallback, hotreload.DevServerOutputStderr, "JavaScript runtime error")

	if got.Stream != hotreload.DevServerOutputStderr {
		t.Fatalf("stream = %q, want %q", got.Stream, hotreload.DevServerOutputStderr)
	}
	if got.Line != "JavaScript runtime error" {
		t.Fatalf("line = %q, want %q", got.Line, "JavaScript runtime error")
	}
}

func TestExpoDevServerEmitProcessOutput_NilCallback(t *testing.T) {
	server := NewExpoDevServer(".", "demo", 8081, false)
	server.emitProcessOutput(nil, hotreload.DevServerOutputStdout, "ignored")
}
