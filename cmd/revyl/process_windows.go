//go:build windows

package main

import (
	"syscall"
)

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
