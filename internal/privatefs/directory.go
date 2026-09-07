package privatefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func CreateDirectory(trustedRoot, relativePath string) (*os.Root, error) {
	return openOrCreateDirectory(trustedRoot, relativePath, true)
}

func OpenDirectory(trustedRoot, relativePath string) (*os.Root, error) {
	return openOrCreateDirectory(trustedRoot, relativePath, false)
}

func openOrCreateDirectory(trustedRoot, relativePath string, create bool) (*os.Root, error) {
	parent, err := os.OpenRoot(trustedRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	return openOrCreateDirectoryInRoot(parent, relativePath, create)
}

func CreateDirectoryInRoot(parent *os.Root, relativePath string) (*os.Root, error) {
	return openOrCreateDirectoryInRoot(parent, relativePath, true)
}

func OpenDirectoryInRoot(parent *os.Root, relativePath string) (*os.Root, error) {
	return openOrCreateDirectoryInRoot(parent, relativePath, false)
}

func openOrCreateDirectoryInRoot(parent *os.Root, relativePath string, create bool) (*os.Root, error) {
	if !filepath.IsLocal(relativePath) {
		return nil, fmt.Errorf("open managed directory %q: use a relative path within the trusted root", relativePath)
	}
	components := strings.Split(filepath.ToSlash(relativePath), "/")
	for _, component := range components {
		if component == ".." {
			return nil, fmt.Errorf("open managed directory %q: parent traversal is not allowed", relativePath)
		}
	}
	current, err := parent.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, component := range components {
		if component == "" || component == "." {
			continue
		}
		next, err := openOrCreateDirectoryComponent(current, component, create)
		closeErr := current.Close()
		if err != nil {
			return nil, errors.Join(err, closeErr)
		}
		if closeErr != nil {
			return nil, errors.Join(closeErr, next.Close())
		}
		current = next
	}
	if create && filepath.Clean(relativePath) != "." {
		info, err := current.Stat(".")
		if err != nil {
			return nil, errors.Join(err, current.Close())
		}
		return tightenDirectory(current, info)
	}
	return current, nil
}

func openOrCreateDirectoryComponent(parent *os.Root, name string, create bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("open managed directory %s: replace the symlink or non-directory component %q with a real directory", parent.Name(), name)
	}
	return openVerifiedDirectory(parent, name, info)
}

func openVerifiedDirectory(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	openedInfo, err := root.Stat(".")
	if err == nil && !os.SameFile(expected, openedInfo) {
		err = fmt.Errorf("open managed directory %s: component %q changed while opening; retry the operation", parent.Name(), name)
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func tightenDirectory(root *os.Root, expected os.FileInfo) (*os.Root, error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	info, err := directory.Stat()
	if err == nil && !os.SameFile(expected, info) {
		err = fmt.Errorf("secure private directory %s: directory changed while opening; retry the operation", root.Name())
	}
	if err == nil {
		err = directory.Chmod(0o700)
	}
	if err = errors.Join(err, directory.Close()); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}
