//go:build !windows

package sessionprofile

import (
	"os/exec"
	"syscall"
)

// startInProcessGroup starts command as the leader of a new process group and
// returns a function that kills every process left in that group.
func startInProcessGroup(command *exec.Cmd) (func(), error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return nil, err
	}
	groupID := command.Process.Pid
	return func() { _ = syscall.Kill(-groupID, syscall.SIGKILL) }, nil
}
