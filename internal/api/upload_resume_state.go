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

type resumableUploadState struct {
	UploadID   string `json:"upload_id"`
	S3UploadID string `json:"s3_upload_id"`
	AppID      string `json:"app_id"`
	Version    string `json:"version"`
	FileName   string `json:"file_name"`
	FilePath   string `json:"file_path"`
	FileSize   int64  `json:"file_size"`
	FileHash   string `json:"file_sha256"`
	PartSize   int64  `json:"part_size"`
	Assembled  bool   `json:"assembled,omitempty"`
}

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

func uploadStateKey(appID, version, filePath string, fileSize int64) string {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		abs = filePath
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", appID, version, abs, fileSize)))
	return hex.EncodeToString(sum[:])
}

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

func (c *Client) deleteResumableUploadState(key string) {
	dir, err := c.resolveUploadStateDir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, key+".json"))
}

func uploadDestinationStateKey(appID, version, filePath string, fileSize int64, branchID, branchName string) string {
	base := uploadStateKey(appID, version, filePath, fileSize)
	if branchID == "" && branchName == "" {
		return base
	}
	sum := sha256.Sum256([]byte(base + "\x00" + branchID + "\x00" + branchName))
	return hex.EncodeToString(sum[:])
}

func uploadDestinationStateKeyWithContext(appID, version, filePath string, fileSize int64, branchID, branchName, gitBranchName string) string {
	base := uploadDestinationStateKey(appID, version, filePath, fileSize, branchID, branchName)
	if branchID != "" || branchName != "" || gitBranchName == "" {
		return base
	}
	sum := sha256.Sum256([]byte(base + "\x00context\x00" + gitBranchName))
	return hex.EncodeToString(sum[:])
}
