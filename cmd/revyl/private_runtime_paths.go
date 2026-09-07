package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revyl/cli/internal/privatefs"
)

type privateRuntimePath struct {
	trustedRoot  string
	relativePath string
}

func (p privateRuntimePath) String() string {
	return filepath.Join(p.trustedRoot, p.relativePath)
}

func (p privateRuntimePath) openOrCreateDirectory(create bool) (*os.Root, error) {
	if !filepath.IsLocal(p.relativePath) || filepath.Base(p.relativePath) == "." {
		return nil, fmt.Errorf("open managed runtime file: invalid relative path %q", p.relativePath)
	}
	if create {
		return privatefs.CreateDirectory(p.trustedRoot, filepath.Dir(p.relativePath))
	}
	return privatefs.OpenDirectory(p.trustedRoot, filepath.Dir(p.relativePath))
}

func openOrCreateManagedRuntimeFile(path privateRuntimePath, flags int) (*os.File, error) {
	root, err := path.openOrCreateDirectory(flags&os.O_CREATE != 0)
	if err != nil {
		return nil, err
	}
	file, openErr := root.OpenFile(filepath.Base(path.relativePath), flags, 0o600)
	closeErr := root.Close()
	if openErr != nil {
		return nil, errors.Join(openErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, file.Close())
	}
	return file, nil
}

func readManagedRuntimeFile(path privateRuntimePath) ([]byte, error) {
	root, err := path.openOrCreateDirectory(false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path.relativePath))
}

func writeManagedRuntimeFile(path privateRuntimePath, data []byte) error {
	file, err := openOrCreateManagedRuntimeFile(path, os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return err
	}
	return writeAndClosePrivateFile(file, data)
}

func writeAndClosePrivateFile(file *os.File, data []byte) error {
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

func removeManagedRuntimeFile(path privateRuntimePath) error {
	root, err := path.openOrCreateDirectory(false)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Remove(filepath.Base(path.relativePath))
}
