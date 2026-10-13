// Package tui provides the upload-build sub-model for inline build uploads.
package tui

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revyl/cli/internal/api"
	"github.com/revyl/cli/internal/buildselection"
)

// uploadStep tracks which stage of the upload flow the user is on.
type uploadStep int

const (
	uploadStepFilePath  uploadStep = iota // entering file path
	uploadStepVersion                     // entering version (optional)
	uploadStepConfirm                     // reviewing before submit
	uploadStepUploading                   // upload in flight
	uploadStepDone                        // upload complete
)

const uploadPinNotice = "This upload doesn't pin the branch: a branch on Latest build runs its newest upload"

// validBuildExts lists accepted file extensions for build uploads.
var validBuildExts = map[string]bool{
	".apk": true,
	".ipa": true,
	".zip": true,
	".app": true,
}

// uploadBuildModel manages the state of the inline "Upload a build" TUI flow.
//
// The flow is: file path -> version (optional) -> confirm -> uploading -> done.
// The user must provide a valid path to a .apk, .ipa, .zip, or .app file.
// The version string is optional; if left blank it is auto-generated.
type uploadBuildModel struct {
	step            uploadStep
	filePathInput   textinput.Model
	versionInput    textinput.Model
	buildPicker     *buildPickerModel
	branchSelection api.BuildSelection
	branchLabel     string
	pendingBranch   *api.BuildBranch
	pendingVersion  string

	// Context: which app we're uploading to
	appID   string
	appName string

	// API dependencies
	client *api.Client

	// State
	uploading bool
	spinner   spinner.Model
	err       error
	width     int
	height    int

	// Validated file info (set after validation in confirm step)
	resolvedPath string
	fileSize     int64

	// Result
	uploadedVersionID     string
	uploadedVersion       string
	uploadedBranchSummary string
	done                  bool

	// Post-upload action cursor (0=upload another, 1=back to builds)
	doneCursor int
	uploadMore bool
}

// newUploadBuildModel creates a new upload-build sub-model.
//
// Parameters:
//   - client: the API client (may be nil if auth failed)
//   - appID: UUID of the app to upload to
//   - appName: display name of the app
//   - width: terminal width
//   - height: terminal height
//
// Returns:
//   - uploadBuildModel: the initialized model
func newUploadBuildModel(client *api.Client, appID, appName string, width, height int) uploadBuildModel {
	fpInput := textinput.New()
	fpInput.Placeholder = "/path/to/build.apk"
	fpInput.CharLimit = 512
	fpInput.Width = 50
	fpInput.Focus()

	vInput := textinput.New()
	vInput.Placeholder = "(auto-generated if empty)"
	vInput.CharLimit = 128

	return uploadBuildModel{
		step:          uploadStepFilePath,
		filePathInput: fpInput,
		versionInput:  vInput,
		appID:         appID,
		appName:       appName,
		client:        client,
		spinner:       newSpinner(),
		width:         width,
		height:        height,
	}
}

// --- Tea commands ---

// uploadBuildCmd calls the API to upload a build (3-step presigned URL flow).
//
// Parameters:
//   - client: authenticated API client
//   - appID: UUID of the target app
//   - filePath: absolute path to the build file
//   - version: version string (may be auto-generated)
//
// Returns:
//   - tea.Cmd: async command that sends BuildUploadedMsg on completion
func uploadBuildCmd(client *api.Client, appID, filePath, version string, selections ...api.BuildSelection) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		selection := api.BuildSelection{}
		if len(selections) > 0 {
			selection = selections[0]
		}
		branch, err := client.ResolveUploadBranchWithContext(ctx, appID, selection.BranchID, selection.BranchName, selection.ContextBranchName)
		if err != nil {
			return BuildUploadedMsg{Err: fmt.Errorf("resolve upload branch: %w", err)}
		}
		resp, err := client.UploadBuild(ctx, &api.UploadBuildRequest{
			AppID:    appID,
			BranchID: branch.ID,
			Version:  version,
			FilePath: filePath,
		})
		if err != nil {
			return BuildUploadedMsg{Branch: branch, Err: fmt.Errorf("upload failed: %w", err)}
		}

		uploaded := buildselection.UploadedBuild{
			AppID: appID, BranchID: cmp.Or(resp.BranchID, branch.ID), BranchName: cmp.Or(resp.BranchName, branch.Name),
			BuildID: resp.VersionID, BuildVersion: resp.Version,
		}
		summary, summaryErr := buildselection.UploadedBuildBranchSummary(ctx, client, uploaded)
		if summaryErr != nil {
			summary = ""
		}
		return BuildUploadedMsg{
			Branch:        branch,
			VersionID:     resp.VersionID,
			Version:       resp.Version,
			BranchSummary: summary,
		}
	}
}

// --- Bubble Tea interface ---

// Init starts the text input blink cursor.
func (m uploadBuildModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages for the upload-build flow.
func (m uploadBuildModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.buildPicker != nil {
		picker, cmd := m.buildPicker.Update(msg)
		m.buildPicker = &picker
		if picker.done {
			m.buildPicker = nil
			if !picker.cancelled {
				m.branchSelection, m.branchLabel = picker.selection, picker.label
				m.pendingBranch, m.pendingVersion = nil, ""
			}
		}
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		if m.uploading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case BuildUploadedMsg:
		m.uploading = false
		if msg.Branch != nil {
			m.pendingBranch = msg.Branch
		}
		if msg.Err != nil {
			m.err = msg.Err
			m.step = uploadStepConfirm
			return m, nil
		}
		m.uploadedVersionID = msg.VersionID
		m.uploadedVersion = msg.Version
		m.uploadedBranchSummary = msg.BranchSummary
		if m.uploadedBranchSummary == "" && msg.Branch != nil {
			m.uploadedBranchSummary = msg.Branch.Name
		}
		m.step = uploadStepDone
		return m, nil
	}

	// Forward to active text input
	switch m.step {
	case uploadStepFilePath:
		var cmd tea.Cmd
		m.filePathInput, cmd = m.filePathInput.Update(msg)
		return m, cmd
	case uploadStepVersion:
		var cmd tea.Cmd
		m.versionInput, cmd = m.versionInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

// handleKey processes key events for the upload flow.
func (m uploadBuildModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "esc" && !m.uploading {
		return m, nil
	}

	switch m.step {
	case uploadStepFilePath:
		return m.handleFilePathKey(key, msg)
	case uploadStepVersion:
		return m.handleVersionKey(key, msg)
	case uploadStepConfirm:
		return m.handleConfirmKey(key)
	case uploadStepDone:
		return m.handleDoneKey(key)
	}

	return m, nil
}

// handleFilePathKey processes keys during the file path input step.
func (m uploadBuildModel) handleFilePathKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		raw := strings.TrimSpace(m.filePathInput.Value())
		if raw == "" {
			m.err = fmt.Errorf("file path cannot be empty")
			return m, nil
		}

		// Expand ~ to home directory
		resolved := raw
		if strings.HasPrefix(resolved, "~/") {
			if home, homeErr := os.UserHomeDir(); homeErr == nil {
				resolved = filepath.Join(home, resolved[2:])
			}
		}
		resolved, _ = filepath.Abs(resolved)

		// Validate file exists
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			m.err = fmt.Errorf("file not found: %s", resolved)
			return m, nil
		}
		if info.IsDir() {
			m.err = fmt.Errorf("path is a directory, not a file")
			return m, nil
		}

		// Validate extension
		ext := strings.ToLower(filepath.Ext(resolved))
		if !validBuildExts[ext] {
			m.err = fmt.Errorf("invalid file type '%s': must be .apk, .ipa, .zip, or .app", ext)
			return m, nil
		}

		m.resolvedPath = resolved
		m.fileSize = info.Size()
		m.err = nil
		m.step = uploadStepVersion
		m.filePathInput.Blur()
		m.versionInput.Focus()
		return m, textinput.Blink
	default:
		var cmd tea.Cmd
		m.filePathInput, cmd = m.filePathInput.Update(msg)
		m.err = nil
		return m, cmd
	}
}

// handleVersionKey processes keys during the version input step.
func (m uploadBuildModel) handleVersionKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		m.err = nil
		m.step = uploadStepConfirm
		m.versionInput.Blur()
		return m, nil
	case "backspace":
		if m.versionInput.Value() == "" {
			m.step = uploadStepFilePath
			m.versionInput.Blur()
			m.filePathInput.Focus()
			return m, textinput.Blink
		}
		var cmd tea.Cmd
		m.versionInput, cmd = m.versionInput.Update(msg)
		return m, cmd
	default:
		var cmd tea.Cmd
		m.versionInput, cmd = m.versionInput.Update(msg)
		m.err = nil
		return m, cmd
	}
}

// handleConfirmKey processes keys during the confirm step.
func (m uploadBuildModel) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "b":
		if m.client == nil {
			return m, nil
		}
		picker := newBuildPicker(m.client, m.appID, "Upload destination")
		picker.branchOnly, picker.allowCreate, picker.currentBranchID = true, true, m.branchSelection.BranchID
		m.buildPicker = &picker
		return m, picker.Init()
	case "enter", "y":
		if m.client == nil {
			m.err = fmt.Errorf("not authenticated")
			return m, nil
		}
		m.uploading = true
		m.err = nil
		m.step = uploadStepUploading

		version := strings.TrimSpace(m.versionInput.Value())
		if version == "" {
			version = m.pendingVersion
			if version == "" {
				version = fmt.Sprintf("tui-%d", time.Now().Unix())
			}
		}
		m.pendingVersion = version
		selection := m.branchSelection
		if m.pendingBranch != nil {
			selection.BranchID, selection.BranchName, selection.ContextBranchName = m.pendingBranch.ID, "", ""
		}

		return m, tea.Batch(m.spinner.Tick, uploadBuildCmd(m.client, m.appID, m.resolvedPath, version, selection))
	case "backspace", "n":
		m.pendingBranch, m.pendingVersion = nil, ""
		m.step = uploadStepVersion
		m.versionInput.Focus()
		m.err = nil
		return m, textinput.Blink
	}
	return m, nil
}

// handleDoneKey processes keys after upload is complete.
func (m uploadBuildModel) handleDoneKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.doneCursor > 0 {
			m.doneCursor--
		}
	case "down", "j":
		if m.doneCursor < 1 {
			m.doneCursor++
		}
	case "enter":
		m.done = true
		m.uploadMore = m.doneCursor == 0
	}
	return m, nil
}

// --- View rendering ---

// View renders the upload-build flow.
func (m uploadBuildModel) View() string {
	if m.buildPicker != nil {
		return m.buildPicker.View()
	}
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	sepW := min(w, 60)

	b.WriteString(titleStyle.Render(" REVYL") + "  " + dimStyle.Render("Upload build → "+m.appName) + "\n")
	b.WriteString(separator(sepW) + "\n\n")

	// Progress indicator
	steps := []string{"File", "Version", "Confirm", "Upload"}
	for i, s := range steps {
		style := dimStyle
		if i == int(m.step) || (m.step == uploadStepDone && i == 3) {
			style = selectedStyle
		} else if i < int(m.step) {
			style = lipgloss.NewStyle().Foreground(green)
		}
		if i > 0 {
			b.WriteString(dimStyle.Render(" → "))
		}
		b.WriteString(style.Render(s))
	}
	b.WriteString("\n\n")

	switch m.step {
	case uploadStepFilePath:
		b.WriteString("  " + normalStyle.Render("Build file path:") + "\n")
		b.WriteString("  " + m.filePathInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Accepted: .apk, .ipa, .zip, .app") + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, esc to cancel") + "\n")

	case uploadStepVersion:
		b.WriteString("  " + normalStyle.Render("Version string:") + " " + dimStyle.Render("(optional)") + "\n")
		b.WriteString("  " + m.versionInput.View() + "\n")
		b.WriteString("  " + dimStyle.Render("Leave empty for auto-generated version") + "\n")
		if m.err != nil {
			b.WriteString("  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to continue, backspace (empty) to go back") + "\n")

	case uploadStepConfirm:
		version := strings.TrimSpace(m.versionInput.Value())
		if version == "" {
			version = "(auto-generated)"
		}
		b.WriteString("  " + normalStyle.Render("Review:") + "\n\n")
		b.WriteString("  " + dimStyle.Render("App:      ") + normalStyle.Render(m.appName) + "\n")
		branchLabel := m.branchLabel
		if branchLabel == "" {
			branchLabel = defaultBranchLabel
		}
		if m.pendingBranch != nil {
			branchLabel = m.pendingBranch.Name + " (retry destination)"
		}
		b.WriteString("  Branch:   " + branchLabel + "\n")
		b.WriteString("  " + dimStyle.Render("File:     ") + normalStyle.Render(filepath.Base(m.resolvedPath)) + "\n")
		b.WriteString("  " + dimStyle.Render("Size:     ") + normalStyle.Render(formatFileSize(m.fileSize)) + "\n")
		b.WriteString("  " + dimStyle.Render("Version:  ") + normalStyle.Render(version) + "\n")
		b.WriteString("  " + dimStyle.Render(uploadPinNotice) + "\n")
		if m.err != nil {
			b.WriteString("\n  " + errorStyle.Render(m.err.Error()) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("b to choose branch, enter/y to upload, backspace/n to go back, esc to cancel") + "\n")

	case uploadStepUploading:
		b.WriteString("  " + m.spinner.View() + " Uploading build...\n")
		b.WriteString("  " + dimStyle.Render("This may take a while for large files") + "\n")

	case uploadStepDone:
		b.WriteString("  " + successStyle.Render("✓ Build uploaded successfully") + "\n")
		b.WriteString("  " + dimStyle.Render("Version: "+m.uploadedVersion) + "\n")
		b.WriteString("  " + dimStyle.Render("ID: "+m.uploadedVersionID) + "\n")
		if m.uploadedBranchSummary != "" {
			b.WriteString("  " + dimStyle.Render("Branch: "+m.uploadedBranchSummary) + "\n")
		}
		b.WriteString("\n")
		b.WriteString("  " + normalStyle.Render("What next?") + "\n\n")

		options := []string{"Upload another build", "Back to builds"}
		for i, opt := range options {
			cur := "  "
			style := normalStyle
			if i == m.doneCursor {
				cur = selectedStyle.Render("▸ ")
				style = selectedStyle
			}
			b.WriteString("  " + cur + style.Render(opt) + "\n")
		}
		b.WriteString("\n  " + helpStyle.Render("enter to select") + "\n")
	}

	return b.String()
}

// formatFileSize returns a human-readable file size string.
func formatFileSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
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
