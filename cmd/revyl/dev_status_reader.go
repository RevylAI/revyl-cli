package main

import (
	"io"
	"os"
)

// readDevStatusFile reads one status snapshot through the platform-specific
// opener so atomic replacements remain safe while readers are active.
func readDevStatusFile(statusPath string) ([]byte, error) {
	statusFile, err := openDevStatusFile(statusPath)
	if err != nil {
		return nil, err
	}
	defer statusFile.Close()

	data, err := io.ReadAll(statusFile)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: statusPath, Err: err}
	}
	return data, nil
}
