//go:build !windows

package main

import (
	"os"
	"syscall"
)

func openComputerInputFile(path string) (*os.File, error) {
	// #nosec G304 -- the caller explicitly selects this local CLI path; it opens
	// read-only with O_NOFOLLOW, and the caller keeps only the regular file it stat'ed first.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
