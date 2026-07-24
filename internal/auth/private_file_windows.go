//go:build windows

package auth

import "golang.org/x/sys/windows"

// replacePrivateFile atomically replaces a destination with write-through semantics on Windows.
//
// Parameters:
//   - sourcePath: Closed temporary file in the destination directory.
//   - destinationPath: Existing or new private-file path.
//
// Returns:
//   - error: Path conversion or MoveFileEx failure.
func replacePrivateFile(sourcePath string, destinationPath string) error {
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
