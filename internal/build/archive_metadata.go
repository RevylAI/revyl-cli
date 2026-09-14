package build

import (
	"fmt"
	"os"
	"path/filepath"
)

type archiveFileMetadata struct {
	mode       os.FileMode
	linkTarget string
}

type extractedArchiveMetadata map[string]archiveFileMetadata

func archiveMetadataPath(filePath string) (string, error) {
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(filePath))
	if err != nil {
		return "", fmt.Errorf("failed to resolve archive metadata path: %w", err)
	}
	return filepath.Join(parentPath, filepath.Base(filePath)), nil
}

func (metadata extractedArchiveMetadata) record(filePath string, entry archiveFileMetadata) error {
	key, err := archiveMetadataPath(filePath)
	if err != nil {
		return err
	}
	metadata[key] = entry
	return nil
}

func (metadata extractedArchiveMetadata) lookup(filePath string) (archiveFileMetadata, bool, error) {
	key, err := archiveMetadataPath(filePath)
	if err != nil {
		return archiveFileMetadata{}, false, err
	}
	entry, found := metadata[key]
	return entry, found, nil
}
