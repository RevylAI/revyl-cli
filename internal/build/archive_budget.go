package build

import (
	"fmt"
	"io"
)

const archiveWorkspaceLimitBytes int64 = 8 << 30
const archiveUploadLimitBytes int64 = 5 << 30
const archiveFilesystemEntryBytes int64 = 4096

type archiveLimits struct {
	workspaceBytes int64
	artifactBytes  int64
}

func defaultArchiveLimits() archiveLimits {
	return archiveLimits{workspaceBytes: archiveWorkspaceLimitBytes, artifactBytes: archiveUploadLimitBytes}
}

type archiveWorkspaceBudget struct {
	limitBytes int64
	usedBytes  int64
}

func (budget *archiveWorkspaceBudget) remainingBytes() int64 {
	return budget.limitBytes - budget.usedBytes
}

func (budget *archiveWorkspaceBudget) reserve(bytes int64) error {
	if bytes > budget.remainingBytes() {
		return fmt.Errorf("archive conversion exceeds the %d-byte temporary workspace limit; use a smaller app artifact", budget.limitBytes)
	}
	budget.usedBytes += bytes
	return nil
}

type archiveBudgetWriter struct {
	target       io.Writer
	workspace    *archiveWorkspaceBudget
	limitBytes   int64
	writtenBytes int64
}

func (writer *archiveBudgetWriter) Write(data []byte) (int, error) {
	if err := writer.workspace.reserve(int64(len(data))); err != nil {
		return 0, err
	}
	if int64(len(data)) > writer.limitBytes-writer.writtenBytes {
		writer.workspace.usedBytes -= int64(len(data))
		return 0, fmt.Errorf("normalized archive exceeds the %d-byte artifact limit; use a smaller app artifact", writer.limitBytes)
	}
	written, err := writer.target.Write(data)
	writer.workspace.usedBytes -= int64(len(data) - written)
	writer.writtenBytes += int64(written)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	return written, err
}

func copyArchiveContent(target io.Writer, source io.Reader, limitBytes int64) (int64, error) {
	written, err := io.CopyN(target, source, limitBytes)
	if err == io.EOF {
		return written, nil
	}
	if err != nil {
		return written, err
	}
	var probe [1]byte
	count, err := io.ReadFull(source, probe[:])
	if count > 0 {
		return written, fmt.Errorf("archive content exceeds its remaining %d-byte processing budget; use a smaller app artifact", limitBytes)
	}
	if err == io.EOF {
		return written, nil
	}
	return written, err
}
