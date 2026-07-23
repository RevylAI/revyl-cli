//go:build windows

package main

import (
	"os"
	"syscall"
)

func openDevStatusFile(statusPath string) (*os.File, error) {
	pathPointer, err := syscall.UTF16PtrFromString(statusPath)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: statusPath, Err: err}
	}

	handle, err := syscall.CreateFile(
		pathPointer,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: statusPath, Err: err}
	}
	return os.NewFile(uintptr(handle), statusPath), nil
}
