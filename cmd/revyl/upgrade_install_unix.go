//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// executableReplaceBlocker returns why this user cannot rename execPath to its
// backup and write the new binary beside it, or nil when it can. A sticky
// directory also requires owning the file or the directory.
func executableReplaceBlocker(execPath string) error {
	installDir := filepath.Dir(execPath)
	if err := unix.Access(installDir, unix.W_OK|unix.X_OK); err != nil {
		return &os.PathError{Op: "access", Path: installDir, Err: err}
	}
	dirInfo, err := os.Stat(installDir)
	if err != nil || dirInfo.Mode()&os.ModeSticky == 0 {
		return nil
	}
	fileInfo, err := os.Lstat(execPath)
	if err != nil {
		return nil
	}
	euid := os.Geteuid()
	dirStat, dirOK := dirInfo.Sys().(*syscall.Stat_t)
	fileStat, fileOK := fileInfo.Sys().(*syscall.Stat_t)
	if euid == 0 || !dirOK || !fileOK || int(fileStat.Uid) == euid || int(dirStat.Uid) == euid {
		return nil
	}
	return &os.PathError{Op: "replace", Path: execPath, Err: syscall.EPERM}
}
