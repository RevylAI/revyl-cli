//go:build !windows

package main

import (
	"errors"
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

func configureExpoConfigCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateExpoConfigCommand(cmd *exec.Cmd) {
	if cmd.Process != nil {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
			return
		}
		_ = cmd.Process.Kill()
	}
}
