//go:build windows

package providers

import (
	"fmt"
	"os"
	"os/exec"
)

// setSysProcAttr is a no-op on Windows (no process groups via Setpgid).
func setSysProcAttr(cmd *exec.Cmd) {}

// killProcessGroup terminates the process tree on Windows using taskkill.
func killProcessGroup(pid int) {
	exec.Command("taskkill", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
}

// forceKillProcessGroup forcefully terminates the process tree on Windows.
func forceKillProcessGroup(pid int) {
	exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
}

// forceKillProcess forcefully terminates a single process on Windows.
func forceKillProcess(pid int) {
	p, err := os.FindProcess(pid)
	if err == nil {
		p.Kill()
	}
}

// killProcessOnPort kills any process listening on the given port.
func killProcessOnPort(port int) {
	// Use netstat + taskkill on Windows
	exec.Command("cmd", "/C", fmt.Sprintf("for /f \"tokens=5\" %%a in ('netstat -ano ^| findstr :%d') do taskkill /F /PID %%a", port)).Run()
}
