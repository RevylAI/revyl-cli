package providers

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/revyl/cli/internal/hotreload"
)

const (
	processStderrTailLines     = 20
	processStderrTailLineRunes = 300
	processStderrMaxLineBytes  = 64 * 1024
	processOutputWaitDelay     = 2 * time.Second
)

func processExitError(prefix string, err error) error {
	if err == nil {
		return fmt.Errorf("%s", prefix)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func processExitDetail(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	return fmt.Sprintf("%s: %v", prefix, err)
}

func unexpectedProcessExitFailure(provider string, port int, prefix string, err error, stderrTail []string) hotreload.RuntimeFailure {
	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	detail := processExitDetail(prefix, err)
	if len(stderrTail) > 0 {
		detail += "; last stderr line: " + stderrTail[len(stderrTail)-1]
	}
	return hotreload.RuntimeFailure{
		Kind:          hotreload.RuntimeFailureLocalDevServerDown,
		Provider:      provider,
		Port:          port,
		Detail:        detail,
		Fatal:         true,
		Err:           err,
		ProcessExited: true,
		ExitCode:      exitCode,
		StderrTail:    stderrTail,
	}
}

// processStderr is a child process's stderr writer. It hands each complete
// line to onLine and keeps the last lines so an unexpected exit can report
// what the process printed before it died. Assigning it to exec.Cmd.Stderr
// makes Wait drain the pipe before returning, so the final lines are kept.
type processStderr struct {
	onLine  func(string)
	mu      sync.Mutex
	partial []byte
	tail    []string
}

func newProcessStderr(onLine func(string)) *processStderr {
	return &processStderr{onLine: onLine}
}

func (s *processStderr) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.partial = append(s.partial, p...)
	var lines []string
	for {
		end := bytes.IndexByte(s.partial, '\n')
		if end < 0 {
			break
		}
		lines = append(lines, strings.TrimRight(string(s.partial[:end]), "\r"))
		s.partial = s.partial[end+1:]
	}
	if len(s.partial) > processStderrMaxLineBytes {
		lines = append(lines, string(s.partial))
		s.partial = nil
	}
	for _, line := range lines {
		s.recordLocked(line)
	}
	s.mu.Unlock()
	for _, line := range lines {
		s.onLine(line)
	}
	return len(p), nil
}

func (s *processStderr) recordLocked(line string) {
	line = capStderrTailLine(line)
	if line == "" {
		return
	}
	s.tail = append(s.tail, line)
	if len(s.tail) > processStderrTailLines {
		s.tail = s.tail[len(s.tail)-processStderrTailLines:]
	}
}

// Tail returns the last stderr lines, including an unterminated final line.
func (s *processStderr) Tail() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tail := append([]string(nil), s.tail...)
	if pending := capStderrTailLine(string(s.partial)); pending != "" {
		tail = append(tail, pending)
		if len(tail) > processStderrTailLines {
			tail = tail[len(tail)-processStderrTailLines:]
		}
	}
	return tail
}

func capStderrTailLine(line string) string {
	line = strings.TrimSpace(line)
	if runes := []rune(line); len(runes) > processStderrTailLineRunes {
		return string(runes[:processStderrTailLineRunes]) + "..."
	}
	return line
}
