//go:build !windows

package auth

import "os"

// replacePrivateFile atomically replaces a destination on POSIX filesystems.
//
// Parameters:
//   - sourcePath: Closed temporary file in the destination directory.
//   - destinationPath: Existing or new private-file path.
//
// Returns:
//   - error: Rename failure, if any.
func replacePrivateFile(sourcePath string, destinationPath string) error {
	return os.Rename(sourcePath, destinationPath)
}
