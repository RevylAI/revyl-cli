//go:build !windows

package providers

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// setSysProcAttr configures the command to use a new process group,
// so we can kill all child processes together.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGTERM to the entire process group.
func killProcessGroup(pid int) {
	syscall.Kill(-pid, syscall.SIGTERM)
}

// forceKillProcessGroup sends SIGKILL to the entire process group.
func forceKillProcessGroup(pid int) {
	syscall.Kill(-pid, syscall.SIGKILL)
}

// forceKillProcess sends SIGKILL to a single process.
func forceKillProcess(pid int) {
	syscall.Kill(pid, syscall.SIGKILL)
}

// killProcessOnPort kills any process listening on the given port.
func killProcessOnPort(port int) {
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port))
	output, err := cmd.Output()
	if err != nil {
		return
	}

	pids := strings.Fields(strings.TrimSpace(string(output)))
	for _, pidStr := range pids {
		var pid int
		if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil {
			forceKillProcess(pid)
		}
	}
}
