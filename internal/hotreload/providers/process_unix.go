//go:build !windows

package providers

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(pid int) {
	syscall.Kill(-pid, syscall.SIGTERM)
}

func forceKillProcessGroup(pid int) {
	syscall.Kill(-pid, syscall.SIGKILL)
}

func forceKillProcess(pid int) {
	syscall.Kill(pid, syscall.SIGKILL)
}

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
