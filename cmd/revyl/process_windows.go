//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// setProcGroup configures the command to run in its own process group.
// On Windows, CREATE_NEW_PROCESS_GROUP is the equivalent of Unix Setpgid.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessGroup terminates a process and its children on Windows.
// Uses taskkill /T (tree kill) to approximate Unix process group semantics.
func killProcessGroup(pid int, sig syscall.Signal) error {
	// taskkill /T /F /PID <pid> kills the process tree
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("taskkill failed for pid %d: %w", pid, err)
	}
	return nil
}

// isProcessGroupAlive checks whether a process is still running on Windows.
func isProcessGroupAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, FindProcess always succeeds. Signal(0) checks liveness.
	return p.Signal(syscall.Signal(0)) == nil
}

// isServiceProcess checks whether a PID is still a running process on Windows.
// Unlike Unix, we cannot inspect the comm name easily, so we just check if the
// process exists via tasklist.
func isServiceProcess(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}
