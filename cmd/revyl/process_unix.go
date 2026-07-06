//go:build !windows

package main

import (
	"os"
	"os/exec"
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

func configureDetachedDevCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
