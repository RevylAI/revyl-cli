package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// resolveFileNameOrID resolves a file name or UUID to a file ID, name, and size.
// It first checks if the input looks like a UUID, then searches by name in the file list.
func resolveFileNameOrID(cmd *cobra.Command, client *api.Client, nameOrID string) (fileID, fileName string, fileSize int64, err error) {
	if looksLikeUUID(nameOrID) {
		dlResp, dlErr := client.GetOrgFileDownloadURL(cmd.Context(), nameOrID)
		if dlErr == nil {
			return nameOrID, dlResp.Filename, 0, nil
		}
	}

	listResp, listErr := client.ListOrgFiles(cmd.Context(), 1000, 0)
	if listErr != nil {
		return "", "", 0, fmt.Errorf("failed to list files: %w", listErr)
	}

	var matches []api.CLIOrgFile
	for _, f := range listResp.Files {
		if strings.EqualFold(f.Filename, nameOrID) {
			matches = append(matches, f)
		}
	}

	if len(matches) == 0 {
		return "", "", 0, fmt.Errorf("file \"%s\" not found", nameOrID)
	}
	if len(matches) == 1 {
		return matches[0].ID, matches[0].Filename, matches[0].FileSize, nil
	}

	lines := make([]string, 0, len(matches))
	for _, f := range matches {
		desc := f.Description
		if desc == "" {
			desc = "no description"
		}
		lines = append(lines, fmt.Sprintf("  - %s (%s)", f.ID, desc))
	}
	return "", "", 0, fmt.Errorf("multiple files named %q found. Use a file ID:\n%s", nameOrID, strings.Join(lines, "\n"))
}

// Allowed file extensions for org file uploads.
var orgFileAllowedExtensions = map[string]bool{
	".pem": true, ".cer": true, ".crt": true, ".key": true,
	".p12": true, ".pfx": true, ".der": true,
	".json": true, ".xml": true, ".yaml": true, ".yml": true,
	".toml": true, ".csv": true, ".txt": true, ".conf": true,
	".cfg": true, ".ini": true, ".properties": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".pdf": true,
	".mp4": true, ".mp3": true,
}

// Size limits in bytes by extension category.
var orgFileImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".pdf": true,
}
var orgFileMediaExts = map[string]bool{
	".mp4": true, ".mp3": true,
}

const (
	orgFileCertConfigMaxBytes = 50 * 1024 * 1024  // 50 MB
	orgFileImageMaxBytes      = 250 * 1024 * 1024 // 250 MB
	orgFileMediaMaxBytes      = 500 * 1024 * 1024 // 500 MB
)

// validateFileForUpload checks extension and size limits.
func validateFileForUpload(filePath string) (os.FileInfo, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s", filePath)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file: %s", filePath)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if !orgFileAllowedExtensions[ext] {
		return nil, fmt.Errorf("unsupported file extension %q; allowed: .pem .cer .crt .key .p12 .pfx .der .json .xml .yaml .yml .toml .csv .txt .conf .cfg .ini .properties .png .jpg .jpeg .gif .pdf .mp4 .mp3", ext)
	}

	var maxBytes int64
	var categoryName string
	switch {
	case orgFileMediaExts[ext]:
		maxBytes = orgFileMediaMaxBytes
		categoryName = "media"
	case orgFileImageExts[ext]:
		maxBytes = orgFileImageMaxBytes
		categoryName = "image"
	default:
		maxBytes = orgFileCertConfigMaxBytes
		categoryName = "cert/config"
	}

	if info.Size() > maxBytes {
		return nil, fmt.Errorf("file %s (%s) exceeds %d MB limit for %s files",
			filepath.Base(filePath), formatOrgFileSize(info.Size()),
			maxBytes/(1024*1024), categoryName)
	}

	return info, nil
}

// formatOrgFileSize returns a human-readable file size string.
func formatOrgFileSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// fileTypeLabel returns an uppercase extension label for display.
func fileTypeLabel(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return "FILE"
	}
	return strings.ToUpper(strings.TrimPrefix(ext, "."))
}

func runFileList(cmd *cobra.Command, _ []string) error {
	jsonOutput := fileListJSON
	if globalJSON, _ := cmd.Root().PersistentFlags().GetBool("json"); globalJSON {
		jsonOutput = true
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	if !jsonOutput {
		ui.StartSpinner("Fetching files...")
	}
	resp, err := client.ListOrgFiles(cmd.Context(), fileListLimit, fileListOffset)
	if !jsonOutput {
		ui.StopSpinner()
	}

	if err != nil {
		ui.PrintError("Failed to list files: %v", err)
		return err
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(resp.Files) == 0 {
		ui.Println()
		ui.PrintInfo("No files found")
		ui.PrintNextSteps([]ui.NextStep{
			{Label: "Upload a file:", Command: "revyl file upload <path>"},
		})
		return nil
	}

	ui.Println()
	if resp.Count > len(resp.Files) {
		ui.PrintInfo("Files (showing %d of %d)", len(resp.Files), resp.Count)
	} else {
		ui.PrintInfo("Files (%d)", resp.Count)
	}

	table := ui.NewTable("FILENAME", "TYPE", "SIZE", "ID", "DESCRIPTION", "UPLOADED")
	table.SetMinWidth(0, 20)
	table.SetMaxWidth(4, 40)

	for _, f := range resp.Files {
		desc := f.Description
		if desc == "" {
			desc = "\u2014"
		}
		uploaded := f.CreatedAt
		if t, parseErr := time.Parse(time.RFC3339Nano, f.CreatedAt); parseErr == nil {
			uploaded = t.Format("2006-01-02")
		}
		table.AddRow(
			f.Filename,
			fileTypeLabel(f.Filename),
			formatOrgFileSize(f.FileSize),
			f.ID,
			desc,
			uploaded,
		)
	}
	table.Render()

	ui.PrintNextSteps([]ui.NextStep{
		{Label: "Upload a file:", Command: "revyl file upload <path>"},
		{Label: "Download a file:", Command: "revyl file download <name|id>"},
	})

	return nil
}

func runFileUpload(cmd *cobra.Command, args []string) error {
	filePath := args[0]

	if _, err := validateFileForUpload(filePath); err != nil {
		ui.PrintError("%v", err)
		return err
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Ensure display name keeps the correct extension (matches frontend behavior).
	name := fileUploadName
	if name != "" {
		requiredExt := strings.ToLower(filepath.Ext(filePath))
		if requiredExt != "" && !strings.HasSuffix(strings.ToLower(name), requiredExt) {
			name = strings.TrimSuffix(name, filepath.Ext(name)) + requiredExt
			ui.PrintDim("  Name adjusted to \"%s\" (extension must match source file)", name)
		}
	}

	ui.StartSpinner("Uploading file...")
	result, err := client.UploadOrgFile(cmd.Context(), filePath, name, fileUploadDescription)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Upload failed: %v", err)
		return err
	}

	ui.PrintSuccess("Uploaded \"%s\" (%s)", result.Filename, formatOrgFileSize(result.FileSize))
	ui.PrintDim("  ID: %s", result.ID)
	ui.Println()
	ui.PrintNextSteps([]ui.NextStep{
		{Label: "List files:", Command: "revyl file list"},
		{Label: "Download:", Command: fmt.Sprintf("revyl file download %s", result.ID)},
	})

	return nil
}

func runFileDownload(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve name or ID.
	fileID, _, _, resolveErr := resolveFileNameOrID(cmd, client, args[0])
	if resolveErr != nil {
		ui.PrintError("%v", resolveErr)
		return resolveErr
	}

	// Get download URL to learn the filename.
	ui.StartSpinner("Fetching file info...")
	dlResp, err := client.GetOrgFileDownloadURL(cmd.Context(), fileID)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to get download URL: %v", err)
		return err
	}

	// Determine destination path.
	var destPath string
	if len(args) > 1 {
		dest := args[1]
		info, statErr := os.Stat(dest)
		if statErr == nil && info.IsDir() {
			destPath = filepath.Join(dest, dlResp.Filename)
		} else {
			destPath = dest
		}
	} else {
		destPath = dlResp.Filename
	}

	// Ensure parent directory exists.
	destDir := filepath.Dir(destPath)
	if destDir != "." {
		if _, statErr := os.Stat(destDir); statErr != nil {
			ui.PrintError("Directory does not exist: %s", destDir)
			return fmt.Errorf("directory does not exist: %s", destDir)
		}
	}

	// Warn if overwriting.
	if _, statErr := os.Stat(destPath); statErr == nil {
		ui.PrintWarning("Overwriting %s", destPath)
	}

	ui.StartSpinner("Downloading file...")
	downloadErr := client.DownloadFileFromURL(cmd.Context(), dlResp.URL, destPath)
	ui.StopSpinner()

	if downloadErr != nil {
		ui.PrintError("Download failed: %v", downloadErr)
		return downloadErr
	}

	info, _ := os.Stat(destPath)
	var sizeStr string
	if info != nil {
		sizeStr = formatOrgFileSize(info.Size())
	}

	ui.PrintSuccess("Downloaded \"%s\" (%s)", dlResp.Filename, sizeStr)
	ui.PrintDim("  Saved to: %s", destPath)

	return nil
}

func runFileEdit(cmd *cobra.Command, args []string) error {
	hasName := cmd.Flags().Changed("name")
	hasDesc := cmd.Flags().Changed("description")
	hasFile := fileEditFile != ""

	if !hasName && !hasDesc && !hasFile {
		ui.PrintError("Specify at least --name, --description, or --file")
		return fmt.Errorf("no updates specified")
	}

	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve name or ID.
	fileID, origFilename, _, resolveErr := resolveFileNameOrID(cmd, client, args[0])
	if resolveErr != nil {
		ui.PrintError("%v", resolveErr)
		return resolveErr
	}

	// Content replacement path.
	if hasFile {
		if _, err := validateFileForUpload(fileEditFile); err != nil {
			ui.PrintError("%v", err)
			return err
		}

		// Warn if extension mismatch.
		origExt := strings.ToLower(filepath.Ext(origFilename))
		newExt := strings.ToLower(filepath.Ext(fileEditFile))
		if origExt != "" && newExt != "" && origExt != newExt {
			ui.PrintWarning("Original file was %s, replacing with %s", origExt, newExt)
		}

		displayName := fileEditName
		description := fileEditDescription

		ui.StartSpinner("Replacing file...")
		result, replaceErr := client.ReplaceOrgFileContent(cmd.Context(), fileID, fileEditFile, displayName, description)
		ui.StopSpinner()

		if replaceErr != nil {
			ui.PrintError("Replace failed: %v", replaceErr)
			return replaceErr
		}

		ui.PrintSuccess("Replaced \"%s\" (%s)", result.Filename, formatOrgFileSize(result.FileSize))
		ui.PrintDim("  ID: %s (preserved)", result.ID)
		return nil
	}

	// Metadata-only update path.
	req := &api.CLIOrgFileUpdateRequest{}
	if hasName {
		req.Filename = &fileEditName
	}
	if hasDesc {
		req.Description = &fileEditDescription
	}

	ui.StartSpinner("Updating file...")
	result, err := client.UpdateOrgFile(cmd.Context(), fileID, req)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Update failed: %v", err)
		return err
	}

	ui.PrintSuccess("Updated \"%s\"", result.Filename)
	return nil
}

func runFileDelete(cmd *cobra.Command, args []string) error {
	apiKey, err := getAPIKey()
	if err != nil {
		return err
	}

	devMode, _ := cmd.Flags().GetBool("dev")
	client := api.NewClientWithDevMode(apiKey, devMode)

	// Resolve name or ID.
	fileID, fileName, _, resolveErr := resolveFileNameOrID(cmd, client, args[0])
	if resolveErr != nil {
		ui.PrintError("%v", resolveErr)
		return resolveErr
	}

	if !fileDeleteForce {
		ui.PrintWarning("Delete \"%s\"? Tests referencing this file will fail.", fileName)
		confirmed, promptErr := ui.PromptConfirm(fmt.Sprintf("Delete \"%s\"?", fileName), false)
		if promptErr != nil || !confirmed {
			ui.PrintInfo("Cancelled.")
			return nil
		}
	}

	ui.StartSpinner("Deleting file...")
	err = client.DeleteOrgFile(cmd.Context(), fileID)
	ui.StopSpinner()

	if err != nil {
		ui.PrintError("Failed to delete file: %v", err)
		return err
	}

	ui.PrintSuccess("File deleted")
	return nil
}
