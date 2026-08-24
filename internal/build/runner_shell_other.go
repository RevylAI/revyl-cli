//go:build !windows

package build

import "os/exec"

func newShellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}
