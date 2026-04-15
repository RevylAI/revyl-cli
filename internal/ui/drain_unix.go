//go:build !windows

package ui

import (
	"os"
	"syscall"
)

// drainStdin discards any pending bytes in the stdin buffer.
// Prevents stale keypresses (e.g. from a prior Prompt call) from being
// consumed by the next bubbletea program as an immediate Enter/selection.
//
// Uses syscall.SetNonblock to temporarily make stdin non-blocking so that
// reads return immediately when the buffer is empty.
func drainStdin() {
	fd := int(os.Stdin.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		return
	}
	buf := make([]byte, 256)
	for {
		n, _ := os.Stdin.Read(buf)
		if n == 0 {
			break
		}
	}
	_ = syscall.SetNonblock(fd, false)
}
