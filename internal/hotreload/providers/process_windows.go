//go:build windows

package providers

import (
	"fmt"
	"os"
	"os/exec"
)

func setSysProcAttr(cmd *exec.Cmd) {}

func killProcessGroup(pid int) {
	exec.Command("taskkill", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
}

func forceKillProcessGroup(pid int) {
	exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
}

func forceKillProcess(pid int) {
	p, err := os.FindProcess(pid)
	if err == nil {
		p.Kill()
	}
}

func killProcessOnPort(port int) {
	exec.Command("cmd", "/C", fmt.Sprintf("for /f \"tokens=5\" %%a in ('netstat -ano ^| findstr :%d') do taskkill /F /PID %%a", port)).Run()
}
