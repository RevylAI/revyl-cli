package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func writePrivateRuntimeFile(path string, data []byte) error {
	file, err := openPrivateRuntimeFile(
		path,
		os.O_CREATE|os.O_WRONLY,
	)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Truncate(0); err != nil {
		return errors.Join(err, file.Close())
	}
	if _, err := file.Write(data); err != nil {
		return errors.Join(err, file.Close())
	}
	return file.Close()
}

func readPrivateRuntimeFile(path string) ([]byte, error) {
	directoryRoot, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = directoryRoot.Close() }()
	return directoryRoot.ReadFile(filepath.Base(path))
}

func tightenExistingPrivateRuntimeFile(path string) (bool, error) {
	file, err := openPrivateRuntimeFile(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := file.Stat()
	if err != nil {
		return false, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() {
		return false, errors.Join(
			fmt.Errorf("private runtime path is not a regular file: %s", path),
			file.Close(),
		)
	}
	if err := file.Chmod(0o600); err != nil {
		return false, errors.Join(err, file.Close())
	}
	return true, file.Close()
}

func openPrivateRuntimeFile(path string, flags int) (*os.File, error) {
	directoryRoot, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	file, openErr := directoryRoot.OpenFile(filepath.Base(path), flags, 0o600)
	closeErr := directoryRoot.Close()
	if openErr != nil {
		return nil, errors.Join(openErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, file.Close())
	}
	return file, nil
}
