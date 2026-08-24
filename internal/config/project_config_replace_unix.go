//go:build !windows

package config

import "os"

func replaceConfigFile(sourcePath, destinationPath string) error {
	return os.Rename(sourcePath, destinationPath)
}
