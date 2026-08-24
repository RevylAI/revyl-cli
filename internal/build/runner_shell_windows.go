//go:build windows

package build

import (
	"os/exec"
	"syscall"
)

func newShellCommand(command string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /D /S /C "` + command + `"`,
	}
	return cmd
}
