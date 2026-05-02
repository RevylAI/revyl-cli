//go:build !windows

package main

import (
	"os"
	"syscall"
)

// isProcessAlive checks whether a single process is running.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
