package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type archiveExtractor struct {
	root        string
	layout      *archiveLayout
	budget      *archiveWorkspaceBudget
	directories map[string]bool
	metadata    extractedArchiveMetadata
}

func newArchiveExtractor(root string, layout *archiveLayout, budget *archiveWorkspaceBudget) (*archiveExtractor, error) {
	if err := budget.reserve(archiveFilesystemEntryBytes); err != nil {
		return nil, err
	}
	return &archiveExtractor{
		root: root, layout: layout, budget: budget,
		directories: map[string]bool{root: true}, metadata: make(extractedArchiveMetadata),
	}, nil
}

func (extractor *archiveExtractor) createDirectory(directory string) error {
	if extractor.directories[directory] {
		return nil
	}
	if err := extractor.createDirectory(filepath.Dir(directory)); err != nil {
		return err
	}
	if err := extractor.budget.reserve(archiveFilesystemEntryBytes); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("failed to create archive directory: %w", err)
	}
	extractor.directories[directory] = true
	return nil
}

func (extractor *archiveExtractor) extractMember(member archiveMember, source io.Reader) error {
	if member.isLink {
		return nil
	}
	resolved, err := resolveArchiveMember(member.name, extractor.layout.links)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(extractor.root, filepath.FromSlash(resolved))
	if member.isDir {
		if err := extractor.createDirectory(targetPath); err != nil {
			return err
		}
		return extractor.metadata.record(targetPath, archiveFileMetadata{mode: member.mode})
	}
	if err := extractor.createDirectory(filepath.Dir(targetPath)); err != nil {
		return err
	}
	if err := extractor.budget.reserve(archiveFilesystemEntryBytes); err != nil {
		return err
	}
	// Preflight resolves every destination inside the private root; symbolic links remain archive metadata.
	output, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("failed to create archive file: %w", err)
	}
	writer := &archiveBudgetWriter{target: output, workspace: extractor.budget, limitBytes: extractor.budget.limitBytes}
	_, copyErr := copyArchiveContent(writer, source, extractor.budget.remainingBytes())
	if err := errors.Join(copyErr, output.Close()); err != nil {
		return fmt.Errorf("failed to extract archive file: %w", err)
	}
	return extractor.metadata.record(targetPath, archiveFileMetadata{mode: member.mode &^ os.ModeType})
}

func (extractor *archiveExtractor) stageLinks() error {
	for _, member := range extractor.layout.members {
		if !member.isLink {
			continue
		}
		targetPath := filepath.Join(extractor.root, filepath.FromSlash(member.name))
		if err := extractor.createDirectory(filepath.Dir(targetPath)); err != nil {
			return err
		}
		if err := extractor.budget.reserve(archiveFilesystemEntryBytes); err != nil {
			return err
		}
		original := archiveFileMetadata{mode: member.mode, linkTarget: member.target}
		if member.hardLink {
			resolved, err := resolveArchiveMember(member.name, extractor.layout.links)
			if err != nil {
				return err
			}
			sourcePath := filepath.Join(extractor.root, filepath.FromSlash(resolved))
			sourceMetadata, found, err := extractor.metadata.lookup(sourcePath)
			if err != nil {
				return err
			}
			if !found || !sourceMetadata.mode.IsRegular() {
				return fmt.Errorf("archive hardlink %q must resolve to a regular file", member.name)
			}
			if err := os.Link(sourcePath, targetPath); err != nil {
				return fmt.Errorf("failed to create archive hardlink: %w", err)
			}
			original = sourceMetadata
		} else {
			if err := extractor.budget.reserve(int64(len(member.target))); err != nil {
				return err
			}
		}
		if err := extractor.metadata.record(targetPath, original); err != nil {
			return err
		}
	}
	return nil
}
