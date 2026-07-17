package ui

import (
	"os"
	"strings"
	"testing"
)

func TestCanRunInteractiveSelect(t *testing.T) {
	origInputTTY := isInputTTY
	origOutputTTY := isOutputTTY
	origStderrTTY := isStderrTTY
	defer func() {
		isInputTTY = origInputTTY
		isOutputTTY = origOutputTTY
		isStderrTTY = origStderrTTY
	}()

	tests := []struct {
		name       string
		inputTTY   bool
		outputTTY  bool
		stderrTTY  bool
		expectable bool
	}{
		{
			name:       "stdin and stdout are tty",
			inputTTY:   true,
			outputTTY:  true,
			stderrTTY:  false,
			expectable: true,
		},
		{
			name:       "stdin and stderr are tty",
			inputTTY:   true,
			outputTTY:  false,
			stderrTTY:  true,
			expectable: true,
		},
		{
			name:       "stdin tty but no tty output streams",
			inputTTY:   true,
			outputTTY:  false,
			stderrTTY:  false,
			expectable: false,
		},
		{
			name:       "no stdin tty",
			inputTTY:   false,
			outputTTY:  true,
			stderrTTY:  true,
			expectable: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isInputTTY = tc.inputTTY
			isOutputTTY = tc.outputTTY
			isStderrTTY = tc.stderrTTY

			if got := canRunInteractiveSelect(); got != tc.expectable {
				t.Fatalf("canRunInteractiveSelect() = %t, want %t", got, tc.expectable)
			}
		})
	}
}

func TestPromptSecretRejectsNonTerminalStdin(t *testing.T) {
	originalStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = reader.Close()
		_ = writer.Close()
	})

	_, err = PromptSecret("Secret:")
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("PromptSecret() error = %v, want non-terminal error", err)
	}
}
