package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// resumableUploadState is the on-disk record that lets a multipart upload
// resume after an interruption (a dropped connection, a crash, or simply a
// re-run of the same command). It is written before any part is uploaded and
// deleted once the build is finalized. S3 itself holds the uploaded parts, so
// this file only needs to identify the in-progress upload and the local
// artifact it belongs to.
type resumableUploadState struct {
	UploadID   string `json:"upload_id"`
	S3UploadID string `json:"s3_upload_id"`
	AppID      string `json:"app_id"`
	Version    string `json:"version"`
	FileName   string `json:"file_name"`
	FilePath   string `json:"file_path"`
	FileSize   int64  `json:"file_size"`
	// FileHash is the SHA-256 of the artifact's bytes. It is the authoritative
	// identity for resuming: size+mtime would wrongly reuse stale S3 parts when
	// a rebuild keeps the same size and mtime (reproducible builds, mtime-
	// preserving copies), silently finalizing a corrupt artifact.
	FileHash string `json:"file_sha256"`
	PartSize int64  `json:"part_size"`
	// Assembled is set once CompleteMultipartUpload has succeeded. After that the
	// multipart session is gone (ListParts would 410) but the staged object
	// remains, so a re-run skips straight to the create/finalize call instead of
	// re-uploading the whole artifact.
	Assembled bool `json:"assembled,omitempty"`
}

// matches reports whether the saved state still describes the current artifact.
// A content (hash) change means it was rebuilt, so the parts already in S3
// belong to a stale build and the state must be discarded.
func (s *resumableUploadState) matches(fileSize int64, fileHash string) bool {
	return s.FileSize == fileSize && s.FileHash == fileHash
}

// fileSHA256 returns the hex SHA-256 of the file's contents. Resuming reads the
// whole artifact once to fingerprint it; that cost is negligible next to
// transferring a multi-hundred-megabyte build and is the price of never
// reusing parts that belong to a different build.
func fileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolveUploadStateDir returns the directory resume state is stored in,
// defaulting to ~/.revyl/uploads (alongside the credential store). Tests set
// Client.uploadStateDir to redirect it to a temp directory.
func (c *Client) resolveUploadStateDir() (string, error) {
	if c.uploadStateDir != "" {
		return c.uploadStateDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".revyl", "uploads"), nil
}

// uploadStateKey derives a stable filename for the resume record. Keying on
// app, version, absolute path and size means re-running the same upload command
// finds the same record, while uploading a different artifact (or the same one
// to a different version) gets its own.
func uploadStateKey(appID, version, filePath string, fileSize int64) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", appID, version, abs, fileSize)))
	return hex.EncodeToString(sum[:])
}

// loadResumableUploadState reads the resume record for key, returning nil when
// none exists or the file is unreadable/corrupt — a missing or unusable record
// simply means "start fresh", never a hard failure.
func (c *Client) loadResumableUploadState(key string) *resumableUploadState {
	dir, err := c.resolveUploadStateDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, key+".json"))
	if err != nil {
		return nil
	}
	var state resumableUploadState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	if state.UploadID == "" || state.S3UploadID == "" {
		return nil
	}
	return &state
}

// saveResumableUploadState writes the resume record atomically (unique temp
// file then rename) so a crash mid-write never leaves a half-written record and
// concurrent writers never collide on a shared temp path. Persistence failure
// is non-fatal — the upload still proceeds — but it is surfaced once, because a
// silently non-resumable upload means a later interruption restarts from zero.
func (c *Client) saveResumableUploadState(key string, state *resumableUploadState) {
	if err := c.writeResumableUploadState(key, state); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: could not save upload resume state (%v); this upload won't be resumable if interrupted\n",
			err,
		)
	}
}

func (c *Client) writeResumableUploadState(key string, state *resumableUploadState) error {
	dir, err := c.resolveUploadStateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, key+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, key+".json")); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// deleteResumableUploadState removes the resume record once the upload is fully
// finalized (or proven unrecoverable). Best effort: a leftover record is
// harmless because the next run validates it against S3 before trusting it.
func (c *Client) deleteResumableUploadState(key string) {
	dir, err := c.resolveUploadStateDir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, key+".json"))
}
