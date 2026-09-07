package main

import (
	"io"
	"os"
)

func readDevStatusFile(statusPath privateRuntimePath) ([]byte, error) {
	statusFile, err := openDevStatusFile(statusPath)
	if err != nil {
		return nil, err
	}
	defer statusFile.Close()

	data, err := io.ReadAll(statusFile)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: statusPath.String(), Err: err}
	}
	return data, nil
}

// os.Root opens Windows files with FILE_SHARE_DELETE so writers can replace an active snapshot.
func openDevStatusFile(statusPath privateRuntimePath) (*os.File, error) {
	return openOrCreateManagedRuntimeFile(statusPath, os.O_RDONLY)
}
