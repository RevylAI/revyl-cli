// Package main provides the file command for organization file management.
package main

import (
	"github.com/spf13/cobra"
)

var (
	fileListJSON   bool
	fileListLimit  int
	fileListOffset int

	fileUploadName        string
	fileUploadDescription string

	fileEditName        string
	fileEditDescription string
	fileEditFile        string

	fileDeleteForce bool
)

var fileCmd = &cobra.Command{
	Use:   "file",
	Short: "Manage organization files",
	Long: `Manage files uploaded to your organization's library.

Files can be certificates, configs, images, or media used in tests via
revyl-file:// references.

COMMANDS:
  list      - List all files
  upload    - Upload a file
  download  - Download a file
  edit      - Edit metadata and/or replace content
  delete    - Delete a file

EXAMPLES:
  revyl file list
  revyl file upload ./certs/staging.pem --description "Staging TLS cert"
  revyl file download staging.pem
  revyl file edit staging.pem --name "prod-cert.pem" --description "Production cert"
  revyl file edit staging.pem --file ./new-cert.pem
  revyl file delete staging.pem`,
}

var fileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all files",
	Long: `List all files in your organization's library.

EXAMPLES:
  revyl file list
  revyl file list --json
  revyl file list --limit 10 --offset 20`,
	RunE: runFileList,
}

var fileUploadCmd = &cobra.Command{
	Use:   "upload <path>",
	Short: "Upload a file",
	Long: `Upload a file to your organization's library.

Supported file types:
  Certificates:  .pem .cer .crt .key .p12 .pfx .der     (max 50 MB)
  Config:        .json .xml .yaml .yml .toml .csv .txt    (max 50 MB)
                 .conf .cfg .ini .properties
  Images:        .png .jpg .jpeg .gif .pdf                (max 250 MB)
  Media:         .mp4 .mp3                                (max 500 MB)

EXAMPLES:
  revyl file upload ./certs/staging.pem
  revyl file upload ./config.json --name "App Config" --description "Feature flags"`,
	Args: cobra.ExactArgs(1),
	RunE: runFileUpload,
}

var fileDownloadCmd = &cobra.Command{
	Use:   "download <name|id> [dest]",
	Short: "Download a file",
	Long: `Download a file from your organization's library.

If no destination is specified, the file is saved to the current directory
using its original filename.

DESTINATION BEHAVIOR:
  No dest given          → saves as ./original-filename in current directory
  Existing directory     → saves as <dir>/original-filename  (e.g. ./certs/)
  Non-existing path      → saves with that exact name  (e.g. ./my-cert.pem)

EXAMPLES:
  revyl file download staging.pem                  # saves as ./staging.pem
  revyl file download staging.pem ./certs/         # saves as ./certs/staging.pem
  revyl file download staging.pem ./renamed.pem    # saves as ./renamed.pem`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runFileDownload,
}

var fileEditCmd = &cobra.Command{
	Use:   "edit <name|id>",
	Short: "Edit file metadata or replace content",
	Long: `Update a file's name or description, and optionally replace its content.

When --file is provided, the file content is replaced but the file ID is
preserved, so any revyl-file:// references in tests continue to work.

EXAMPLES:
  revyl file edit staging.pem --name "prod-cert.pem"
  revyl file edit abc-123 --description "Updated description"
  revyl file edit staging.pem --file ./new-cert.pem
  revyl file edit abc-123 --file ./new-cert.pem --name "rotated-cert.pem"`,
	Args: cobra.ExactArgs(1),
	RunE: runFileEdit,
}

var fileDeleteCmd = &cobra.Command{
	Use:   "delete <name|id>",
	Short: "Delete a file",
	Long: `Delete a file from your organization's library.

Warning: Tests referencing this file via revyl-file:// will fail on their
next run.

EXAMPLES:
  revyl file delete staging.pem
  revyl file delete abc-123 --force`,
	Args: cobra.ExactArgs(1),
	RunE: runFileDelete,
}

func init() {
	fileCmd.AddCommand(fileListCmd)
	fileCmd.AddCommand(fileUploadCmd)
	fileCmd.AddCommand(fileDownloadCmd)
	fileCmd.AddCommand(fileEditCmd)
	fileCmd.AddCommand(fileDeleteCmd)

	fileListCmd.Flags().BoolVar(&fileListJSON, "json", false, "Output as JSON")
	fileListCmd.Flags().IntVar(&fileListLimit, "limit", 100, "Max results (1-1000)")
	fileListCmd.Flags().IntVar(&fileListOffset, "offset", 0, "Pagination offset")

	fileUploadCmd.Flags().StringVar(&fileUploadName, "name", "", "Display name (defaults to filename)")
	fileUploadCmd.Flags().StringVar(&fileUploadDescription, "description", "", "File description")

	fileEditCmd.Flags().StringVar(&fileEditName, "name", "", "New filename")
	fileEditCmd.Flags().StringVar(&fileEditDescription, "description", "", "New description")
	fileEditCmd.Flags().StringVar(&fileEditFile, "file", "", "Path to replacement file")

	fileDeleteCmd.Flags().BoolVarP(&fileDeleteForce, "force", "f", false, "Skip confirmation prompt")
}
