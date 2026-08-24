//go:build windows

package config

import "golang.org/x/sys/windows"

func replaceConfigFile(sourcePath, destinationPath string) error {
	sourcePathPointer, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return err
	}
	destinationPathPointer, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		sourcePathPointer,
		destinationPathPointer,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
