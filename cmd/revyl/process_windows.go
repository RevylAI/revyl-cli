//go:build windows

package main

import (
	"fmt"
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
	return isProcessAlive(pid)
}

// isProcessAlive checks whether a single process is running on Windows
// by opening a handle with PROCESS_QUERY_LIMITED_INFORMATION and checking
// whether the process has exited.
func isProcessAlive(pid int) bool {
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	const STILL_ACTIVE = 259

	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)

	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == STILL_ACTIVE
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
