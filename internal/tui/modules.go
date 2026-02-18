// Package tui provides the module browser screens for viewing test modules.
//
// Modules are read-only in the TUI -- creation requires file paths and stays CLI-only.
// The browser shows a list of modules with block counts, and detail view renders blocks
// as a numbered list. Deletion is supported with y/n confirmation.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revyl/cli/internal/api"
)

// --- Commands ---

// fetchModulesCmd fetches the full module list for the org.
//
// Parameters:
//   - client: the API client
//
// Returns:
//   - tea.Cmd: command producing ModuleListMsg
func fetchModulesCmd(client *api.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.ListModules(ctx)
		if err != nil {
			return ModuleListMsg{Err: err}
		}
		var modules []ModuleItem
		for _, m := range resp.Result {
			modules = append(modules, ModuleItem{
				ID:          m.ID,
				Name:        m.Name,
				Description: m.Description,
				BlockCount:  len(m.Blocks),
			})
		}
		return ModuleListMsg{Modules: modules}
	}
}

// fetchModuleDetailCmd fetches a single module's full detail.
//
// Parameters:
//   - client: the API client
//   - moduleID: the module to fetch
//
// Returns:
//   - tea.Cmd: command producing ModuleDetailMsg
func fetchModuleDetailCmd(client *api.Client, moduleID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.GetModule(ctx, moduleID)
		if err != nil {
			return ModuleDetailMsg{Err: err}
		}
		mod := &ModuleItem{
			ID:          resp.Result.ID,
			Name:        resp.Result.Name,
			Description: resp.Result.Description,
			BlockCount:  len(resp.Result.Blocks),
			Blocks:      resp.Result.Blocks,
		}
		return ModuleDetailMsg{Module: mod}
	}
}

// deleteModuleCmd deletes a module by ID.
//
// Parameters:
//   - client: the API client
//   - moduleID: the module to delete
//
// Returns:
//   - tea.Cmd: command producing ModuleDeletedMsg
func deleteModuleCmd(client *api.Client, moduleID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := client.DeleteModule(ctx, moduleID)
		return ModuleDeletedMsg{Err: err}
	}
}

// --- Key handling ---

// handleModuleListKey processes key events on the module list screen.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleModuleListKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewDashboard
		return m, nil
	case "up", "k":
		if m.moduleCursor > 0 {
			m.moduleCursor--
		}
	case "down", "j":
		if m.moduleCursor < len(m.moduleItems)-1 {
			m.moduleCursor++
		}
	case "enter":
		if m.moduleCursor < len(m.moduleItems) && m.client != nil {
			m.moduleLoading = true
			m.selectedModuleID = m.moduleItems[m.moduleCursor].ID
			return m, fetchModuleDetailCmd(m.client, m.selectedModuleID)
		}
	}
	return m, nil
}

// handleModuleDetailKey processes key events on the module detail screen.
//
// Parameters:
//   - m: the hub model
//   - msg: the key message
//
// Returns:
//   - tea.Model: updated model
//   - tea.Cmd: next command
func handleModuleDetailKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Delete confirmation
	if m.moduleConfirmDelete {
		switch msg.String() {
		case "y":
			m.moduleConfirmDelete = false
			if m.selectedModule != nil && m.client != nil {
				m.moduleLoading = true
				return m, deleteModuleCmd(m.client, m.selectedModule.ID)
			}
		case "n", "esc":
			m.moduleConfirmDelete = false
		}
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.currentView = viewModuleList
		m.selectedModule = nil
		return m, nil
	case "d":
		m.moduleConfirmDelete = true
	}
	return m, nil
}

// --- Rendering ---

// renderModuleList renders the module list screen.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderModuleList(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Modules")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Modules  %d", len(m.moduleItems))) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if m.moduleLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if len(m.moduleItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No modules found") + "\n")
		b.WriteString("  " + dimStyle.Render("Create modules with: revyl module create <name> --from-file blocks.yaml") + "\n")
	} else {
		start, end := scrollWindow(m.moduleCursor, len(m.moduleItems), 15)
		for i := start; i < end; i++ {
			mod := m.moduleItems[i]
			cursor := "  "
			if i == m.moduleCursor {
				cursor = selectedStyle.Render("▸ ")
			}
			name := normalStyle.Render(fmt.Sprintf("%-22s", mod.Name))
			blocks := dimStyle.Render(fmt.Sprintf("%d blocks", mod.BlockCount))
			desc := ""
			if mod.Description != "" {
				desc = "   " + dimStyle.Render(truncate(mod.Description, 24))
			}
			b.WriteString(fmt.Sprintf("  %s%s  %s%s\n", cursor, name, blocks, desc))
		}
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("enter", "view detail"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}

// renderModuleDetail renders the module detail screen with blocks as a numbered list.
//
// Parameters:
//   - m: the hub model
//
// Returns:
//   - string: rendered output
func renderModuleDetail(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	mod := m.selectedModule
	if mod == nil {
		b.WriteString("  " + m.spinner.View() + " Loading module...\n")
		return b.String()
	}

	// Header
	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render(mod.Name)
	blocksBadge := dimStyle.Render(fmt.Sprintf("%d blocks", mod.BlockCount))
	headerLine := bannerContent + strings.Repeat(" ", max(1, innerW-len(bannerContent)-len(blocksBadge)+6)) + blocksBadge
	banner := headerBannerStyle.Width(innerW).Render(headerLine)
	b.WriteString(banner + "\n")

	if mod.Description != "" {
		b.WriteString("  " + normalStyle.Render(mod.Description) + "\n")
	}

	// Delete confirmation
	if m.moduleConfirmDelete {
		b.WriteString("\n  " + errorStyle.Render(fmt.Sprintf("Delete module \"%s\"? (y/n)", mod.Name)) + "\n")
		return b.String()
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("  BLOCKS") + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	if len(mod.Blocks) == 0 {
		b.WriteString("  " + dimStyle.Render("No blocks defined") + "\n")
	} else {
		for i, block := range mod.Blocks {
			blockStr := fmt.Sprintf("%v", block)
			// Try to extract instruction from map-like structure
			if m, ok := block.(map[string]interface{}); ok {
				if instr, exists := m["instruction"]; exists {
					blockStr = fmt.Sprintf("%v", instr)
				}
			}
			num := dimStyle.Render(fmt.Sprintf("  %d. ", i+1))
			b.WriteString(num + normalStyle.Render(truncate(blockStr, innerW-6)) + "\n")
		}
	}

	b.WriteString("\n  " + dimStyle.Render("Create modules with: revyl module create <name> --from-file blocks.yaml") + "\n")

	b.WriteString("\n  " + separator(innerW) + "\n")
	keys := []string{
		helpKeyRender("d", "delete"),
		helpKeyRender("esc", "back"),
		helpKeyRender("q", "quit"),
	}
	b.WriteString("  " + strings.Join(keys, "  ") + "\n")

	return b.String()
}
