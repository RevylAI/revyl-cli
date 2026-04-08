// Package tui — Files tab of the Library hub.
//
// Files are browsed/inspected/deleted here; description metadata can be
// edited inline. Upload, download, and content replacement stay in the
// `revyl file` CLI.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

// --- Commands ---

// fetchFilesCmd loads the org's files.
func fetchFilesCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListOrgFiles(ctx, 100, 0)
		if err != nil {
			return FileListMsg{Err: err}
		}
		items := make([]FileItem, 0, len(resp.Files))
		for _, f := range resp.Files {
			items = append(items, FileItem{
				ID:          f.ID,
				Filename:    f.Filename,
				FileSize:    f.FileSize,
				ContentType: f.ContentType,
				Description: f.Description,
			})
		}
		return FileListMsg{Files: items}
	}
}

// updateFileDescCmd updates a file's description metadata.
func updateFileDescCmd(client *api.Client, fileID, description string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		desc := description
		_, err := client.UpdateOrgFile(ctx, fileID, &api.CLIOrgFileUpdateRequest{Description: &desc})
		return FileUpdatedMsg{Err: err}
	}
}

// deleteFileCmd deletes a file by ID.
func deleteFileCmd(client *api.Client, fileID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := client.DeleteOrgFile(ctx, fileID)
		return FileDeletedMsg{Err: err}
	}
}

// --- Key handling ---

// handleLibraryFilesKey handles key events for the Files tab.
func handleLibraryFilesKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.libraryMode == libModeConfirmDelete {
		switch msg.String() {
		case "y":
			m.libraryMode = libModeList
			if m.selectedFile != nil && m.client != nil {
				m.filesLoading = true
				return m, deleteFileCmd(m.client, m.selectedFile.ID)
			}
		case "n", "esc":
			m.libraryMode = libModeList
		}
		return m, nil
	}

	if m.libraryMode == libModeEditing {
		switch msg.String() {
		case "esc":
			m.libraryMode = libModeDetail
			m.fileEditDescInput.Blur()
			return m, nil
		case "enter":
			if m.selectedFile == nil || m.client == nil {
				return m, nil
			}
			desc := m.fileEditDescInput.Value()
			m.filesLoading = true
			m.libraryMode = libModeDetail
			m.fileEditDescInput.Blur()
			return m, updateFileDescCmd(m.client, m.selectedFile.ID, desc)
		default:
			var cmd tea.Cmd
			m.fileEditDescInput, cmd = m.fileEditDescInput.Update(msg)
			return m, cmd
		}
	}

	if m.libraryMode == libModeDetail {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.libraryMode = libModeList
			return m, nil
		case "d":
			m.libraryMode = libModeConfirmDelete
		case "e":
			if m.selectedFile != nil {
				m.libraryMode = libModeEditing
				m.fileEditDescInput.SetValue(m.selectedFile.Description)
				m.fileEditDescInput.Focus()
				return m, textinput.Blink
			}
		}
		return m, nil
	}

	// list mode
	if mm, cmd, handled := handleLibraryTabNav(m, msg); handled {
		return mm, cmd
	}
	switch msg.String() {
	case "up", "k":
		if m.fileCursor > 0 {
			m.fileCursor--
		}
	case "down", "j":
		if m.fileCursor < len(m.fileItems)-1 {
			m.fileCursor++
		}
	case "enter":
		if m.fileCursor < len(m.fileItems) {
			f := m.fileItems[m.fileCursor]
			m.selectedFile = &f
			m.libraryMode = libModeDetail
		}
	case "e":
		if m.fileCursor < len(m.fileItems) {
			f := m.fileItems[m.fileCursor]
			m.selectedFile = &f
			m.libraryMode = libModeEditing
			m.fileEditDescInput.SetValue(f.Description)
			m.fileEditDescInput.Focus()
			return m, textinput.Blink
		}
	case "d":
		if m.fileCursor < len(m.fileItems) {
			f := m.fileItems[m.fileCursor]
			m.selectedFile = &f
			m.libraryMode = libModeConfirmDelete
		}
	}
	return m, nil
}

// --- Rendering ---

// formatLibraryFileSize renders a byte count as a human-readable string.
func formatLibraryFileSize(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(size)/(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%d B", size)
	}
}

// renderLibraryFilesBody renders the Files tab body.
func renderLibraryFilesBody(m hubModel, innerW int) string {
	_ = innerW
	var b strings.Builder
	if m.filesLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.libraryMode == libModeEditing && m.selectedFile != nil {
		b.WriteString("  " + sectionStyle.Render(fmt.Sprintf("Edit description for %s", m.selectedFile.Filename)) + "\n\n")
		b.WriteString("  Description: " + m.fileEditDescInput.View() + "\n\n")
		b.WriteString("  " + dimStyle.Render("enter: save  esc: cancel") + "\n")
		return b.String()
	}

	if m.libraryMode == libModeConfirmDelete && m.selectedFile != nil {
		b.WriteString("  " + errorStyle.Render(fmt.Sprintf("Delete file \"%s\"? (y/n)", m.selectedFile.Filename)) + "\n")
		return b.String()
	}

	if m.libraryMode == libModeDetail && m.selectedFile != nil {
		f := m.selectedFile
		b.WriteString("  " + normalStyle.Render(f.Filename) + "\n")
		b.WriteString("  " + dimStyle.Render(fmt.Sprintf("%s  ·  %s", formatLibraryFileSize(f.FileSize), f.ContentType)) + "\n")
		if f.Description != "" {
			b.WriteString("\n  " + dimStyle.Render("Description:") + "\n  " + normalStyle.Render(f.Description) + "\n")
		} else {
			b.WriteString("\n  " + dimStyle.Render("(no description)") + "\n")
		}
		b.WriteString("\n  " + renderLibraryCLIHint(libTabFiles) + "\n")
		return b.String()
	}

	// list mode
	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Files  %d", len(m.fileItems))) + "\n")
	if len(m.fileItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No files found") + "\n")
		b.WriteString("  " + renderLibraryCLIHint(libTabFiles) + "\n")
		return b.String()
	}
	start, end := scrollWindow(m.fileCursor, len(m.fileItems), 12)
	for i := start; i < end; i++ {
		f := m.fileItems[i]
		cursor := "  "
		if i == m.fileCursor {
			cursor = selectedStyle.Render("▸ ")
		}
		name := normalStyle.Render(fmt.Sprintf("%-26s", truncate(f.Filename, 26)))
		size := dimStyle.Render(fmt.Sprintf("%-10s", formatLibraryFileSize(f.FileSize)))
		desc := ""
		if f.Description != "" {
			desc = "  " + dimStyle.Render(truncate(f.Description, 20))
		}
		b.WriteString(fmt.Sprintf("  %s%s  %s%s\n", cursor, name, size, desc))
	}
	b.WriteString("\n  " + renderLibraryCLIHint(libTabFiles) + "\n")
	return b.String()
}
