package build

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/revyl/cli/internal/ui"
)

func stageValidatedTar(compressed io.Reader, tempDir string, budget *archiveWorkspaceBudget) (staged *os.File, layout *archiveLayout, resultErr error) {
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open gzip stream: %w", err)
	}
	defer gzipReader.Close()

	if err := budget.reserve(archiveFilesystemEntryBytes); err != nil {
		return nil, nil, err
	}
	staged, err = os.CreateTemp(tempDir, "revyl-*.tar")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temporary tar; check temporary disk space and permissions: %w", err)
	}
	stagedFile := staged
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, stagedFile.Close(), os.Remove(stagedFile.Name()))
			staged = nil
		}
	}()

	boundedWriter := &archiveBudgetWriter{target: staged, workspace: budget, limitBytes: budget.limitBytes}
	stagedWriter := bufio.NewWriterSize(boundedWriter, 128*1024)
	decompressed := io.TeeReader(gzipReader, stagedWriter)
	layout, err = validateTarArchive(tar.NewReader(decompressed))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to stage and validate tar archive: %w", err)
	}
	// Tar EOF can precede the gzip trailer containing its checksum.
	if _, err := copyArchiveContent(io.Discard, decompressed, budget.remainingBytes()); err != nil {
		return nil, nil, fmt.Errorf("failed to finish staging gzip archive: %w", err)
	}
	if err := stagedWriter.Flush(); err != nil {
		return nil, nil, fmt.Errorf("failed to flush temporary tar; check temporary disk space: %w", err)
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("failed to rewind temporary tar: %w", err)
	}
	return staged, layout, nil
}

func cleanupArchiveExtraction(tempDir string, resultPath *string, resultErr *error, removeDirectory func(string) error) {
	if err := removeDirectory(tempDir); err != nil {
		if *resultErr == nil {
			ui.PrintWarning("Archive conversion succeeded, but temporary directory %s could not be removed; remove it manually to reclaim disk space: %v", tempDir, err)
		} else {
			*resultErr = errors.Join(*resultErr, fmt.Errorf("failed to remove archive extraction directory: %w", err))
		}
	}
	if *resultErr != nil && *resultPath != "" {
		if err := os.Remove(*resultPath); err != nil {
			*resultErr = errors.Join(*resultErr, fmt.Errorf("failed to remove incomplete archive: %w", err))
		}
		*resultPath = ""
	}
}
