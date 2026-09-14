package build

import (
	"archive/zip"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func writeArchiveSymlinks(writer *zip.Writer, appPath string, metadata extractedArchiveMetadata, limitBytes int64) error {
	appRoot, err := filepath.EvalSymlinks(appPath)
	if err != nil {
		return fmt.Errorf("failed to resolve app bundle path: %w", err)
	}
	for _, filePath := range slices.Sorted(maps.Keys(metadata)) {
		entry := metadata[filePath]
		if entry.mode&os.ModeSymlink == 0 {
			continue
		}
		relativePath, err := filepath.Rel(appRoot, filePath)
		if err != nil {
			return err
		}
		if relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			continue
		}
		header := &zip.FileHeader{
			Name:   filepath.ToSlash(filepath.Join(filepath.Base(appPath), relativePath)),
			Method: zip.Deflate,
		}
		header.SetMode(entry.mode)
		content, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		written, err := copyArchiveContent(content, strings.NewReader(entry.linkTarget), limitBytes)
		if err != nil {
			return err
		}
		limitBytes -= written
	}
	return nil
}
