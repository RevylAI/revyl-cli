//go:build !windows

package main

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcGroup configures the command to run in its own process group.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends a signal to the entire process group.
func killProcessGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

// isProcessGroupAlive checks whether any process in the group is still running.
func isProcessGroupAlive(pid int) bool {
	return syscall.Kill(-pid, syscall.Signal(0)) == nil
}

// isServiceProcess checks whether a PID belongs to a shell process spawned by
// revyl services start. This prevents killing unrelated processes if the PID
// file is stale and the OS has recycled the PID.
func isServiceProcess(pid int) bool {
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(out))
	return comm == "bash" || comm == "/bin/bash" || comm == "sh" || comm == "/bin/sh"
}
