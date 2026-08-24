//go:build !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris && !windows

package build

import "os/exec"

func configureCommandProcessGroup(_ *exec.Cmd) bool { return false }

func terminateCommand(cmd *exec.Cmd, _ bool) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
