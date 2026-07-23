//go:build !windows

package main

import "os"

func openDevStatusFile(statusPath string) (*os.File, error) {
	return os.Open(statusPath)
}
