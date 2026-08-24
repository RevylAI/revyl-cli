//go:build windows

package build

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureCommandProcessGroup(cmd *exec.Cmd) bool {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	return true
}

func terminateCommand(cmd *exec.Cmd, ownsProcessGroup bool) {
	if cmd.Process == nil {
		return
	}
	if ownsProcessGroup {
		killContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		killTree := exec.CommandContext(killContext, "taskkill.exe", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := killTree.Run(); err == nil {
			return
		}
	}
	_ = cmd.Process.Kill()
}
