//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package build

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureCommandProcessGroup(cmd *exec.Cmd) bool {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return true
}

func terminateCommand(cmd *exec.Cmd, ownsProcessGroup bool) {
	if cmd.Process == nil {
		return
	}
	if ownsProcessGroup {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	_ = cmd.Process.Kill()
}
