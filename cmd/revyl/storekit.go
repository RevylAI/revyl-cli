// Package main provides scope-generic helpers behind the `revyl app storekit`
// and `revyl test storekit` subcommands.
package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/ui"
)

// storeKitShowJSON is the JSON shape emitted by `storekit show --json`;
// Configured is false when the scope inherits the default.
type storeKitShowJSON struct {
	ScopeType  string  `json:"scope_type"`
	ScopeID    string  `json:"scope_id"`
	Configured bool    `json:"configured"`
	Mode       *string `json:"mode"`
	FileID     *string `json:"file_id"`
	Filename   *string `json:"filename"`
}

// buildStoreKitShowJSON maps a ref (nil = inherits the default) to the output shape.
func buildStoreKitShowJSON(scopeType, scopeID string, ref *api.StoreKitConfigRef) storeKitShowJSON {
	out := storeKitShowJSON{ScopeType: scopeType, ScopeID: scopeID}
	if ref != nil {
		out.Configured = true
		out.Mode = &ref.Mode
		out.FileID = ref.FileID
		out.Filename = ref.Filename
	}
	return out
}

// storeKitSet uploads a .storekit file (unless fileID is supplied) and attaches
// it to the given scope in mode=file.
func storeKitSet(cmd *cobra.Command, client *api.Client, scopeType, scopeID, scopeLabel, filePath, fileRef string) error {
	// A file path and --file-id are mutually exclusive.
	if filePath != "" && fileRef != "" {
		err := fmt.Errorf("provide either a .storekit file path or --file-id, not both")
		ui.PrintError("%v", err)
		return err
	}

	var resolvedFileID string

	if fileRef != "" {
		// Attach an already-uploaded org file, given by name or ID.
		fileID, filename, _, err := resolveFileNameOrID(cmd, client, fileRef)
		if err != nil {
			ui.PrintError("%v", err)
			return err
		}
		if ext := strings.ToLower(filepath.Ext(filename)); ext != ".storekit" {
			err := fmt.Errorf("file %q is not a .storekit file", filename)
			ui.PrintError("%v", err)
			return err
		}
		resolvedFileID = fileID
	} else {
		// Upload a local .storekit file, then attach it.
		if ext := strings.ToLower(filepath.Ext(filePath)); ext != ".storekit" {
			err := fmt.Errorf("StoreKit config must be a .storekit file, got %q", filepath.Base(filePath))
			ui.PrintError("%v", err)
			return err
		}
		if _, err := validateFileForUpload(filePath); err != nil {
			ui.PrintError("%v", err)
			return err
		}

		ui.StartSpinner("Uploading StoreKit config...")
		uploaded, err := client.UploadOrgFile(cmd.Context(), filePath, "", "")
		ui.StopSpinner()
		if err != nil {
			ui.PrintError("Upload failed: %v", err)
			return err
		}
		resolvedFileID = uploaded.ID
		ui.PrintSuccess("Uploaded \"%s\" (%s)", uploaded.Filename, formatOrgFileSize(uploaded.FileSize))
	}

	ui.StartSpinner("Attaching StoreKit config...")
	resp, err := client.UpsertStoreKitConfigRef(cmd.Context(), &api.StoreKitConfigRefUpsertRequest{
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Mode:      "file",
		FileID:    &resolvedFileID,
	})
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to attach StoreKit config: %v", err)
		if fileRef == "" {
			ui.PrintDim("  File was uploaded (ID: %s); reuse it with --file-id or remove it with `revyl file delete %s`.", resolvedFileID, resolvedFileID)
		}
		return err
	}

	name := resolvedFileID
	if resp.Result != nil && resp.Result.Filename != nil {
		name = *resp.Result.Filename
	}
	ui.PrintSuccess("StoreKit config \"%s\" attached to %s", name, scopeLabel)
	return nil
}

// storeKitShow prints the explicit StoreKit config attached to the scope.
func storeKitShow(cmd *cobra.Command, client *api.Client, scopeType, scopeID, scopeLabel string) error {
	jsonOutput, _ := cmd.Root().PersistentFlags().GetBool("json")

	resp, err := client.GetStoreKitConfigRef(cmd.Context(), scopeType, scopeID)
	if err != nil {
		ui.PrintError("Failed to read StoreKit config: %v", err)
		return err
	}

	if jsonOutput {
		out := buildStoreKitShowJSON(scopeType, scopeID, resp.Result)
		data, marshalErr := json.MarshalIndent(out, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Println(string(data))
		return nil
	}

	if resp.Result == nil {
		ui.PrintInfo("No explicit StoreKit config on %s (inherits default).", scopeLabel)
		return nil
	}

	switch resp.Result.Mode {
	case "disabled":
		ui.PrintInfo("StoreKit is explicitly disabled on %s.", scopeLabel)
	case "file":
		name := "(unknown file)"
		if resp.Result.Filename != nil {
			name = *resp.Result.Filename
		}
		ui.PrintInfo("StoreKit config on %s: %s", scopeLabel, name)
		if resp.Result.FileID != nil {
			ui.PrintDim("  File ID: %s", *resp.Result.FileID)
		}
	default:
		ui.PrintInfo("StoreKit config on %s: mode=%s", scopeLabel, resp.Result.Mode)
	}
	return nil
}

// storeKitDisable explicitly turns StoreKit off for the scope (overriding any
// config inherited from a parent scope).
func storeKitDisable(cmd *cobra.Command, client *api.Client, scopeType, scopeID, scopeLabel string) error {
	ui.StartSpinner("Disabling StoreKit config...")
	_, err := client.UpsertStoreKitConfigRef(cmd.Context(), &api.StoreKitConfigRefUpsertRequest{
		ScopeType: scopeType,
		ScopeID:   scopeID,
		Mode:      "disabled",
		FileID:    nil,
	})
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to disable StoreKit config: %v", err)
		return err
	}
	ui.PrintSuccess("StoreKit explicitly disabled on %s", scopeLabel)
	return nil
}

// storeKitClear removes the explicit StoreKit config so the scope inherits the
// default again.
func storeKitClear(cmd *cobra.Command, client *api.Client, scopeType, scopeID, scopeLabel string) error {
	ui.StartSpinner("Clearing StoreKit config...")
	err := client.DeleteStoreKitConfigRef(cmd.Context(), scopeType, scopeID)
	ui.StopSpinner()
	if err != nil {
		ui.PrintError("Failed to clear StoreKit config: %v", err)
		return err
	}
	ui.PrintSuccess("Cleared StoreKit config on %s (inherits default)", scopeLabel)
	return nil
}
