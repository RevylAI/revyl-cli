// Package tui provides the unified Library hub: a tabbed browser for Modules,
// Launch Vars, Variables, Scripts, and Files. The hub stays deliberately
// modest -- browse, delete, and light targeted edits only. Heavy operations
// (file upload, module YAML editing, script code editing) are handled by the
// regular `revyl` subcommands, which the footer hints at on each tab.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// libraryTab identifies which tab of the Library hub is active.
type libraryTab int

const (
	libTabModules libraryTab = iota
	libTabLaunchVars
	libTabVariables
	libTabScripts
	libTabFiles
)

// libraryMode identifies what the user is doing within the current tab.
type libraryMode int

const (
	libModeList libraryMode = iota
	libModeDetail
	libModeEditing
	libModeConfirmDelete
)

// libraryTabOrder mirrors frontend/src/pages/library.tsx tab order.
var libraryTabOrder = []libraryTab{libTabModules, libTabLaunchVars, libTabVariables, libTabScripts, libTabFiles}

// libraryTabLabel returns the display label for a tab.
func libraryTabLabel(t libraryTab) string {
	switch t {
	case libTabModules:
		return "Modules"
	case libTabLaunchVars:
		return "Launch Vars"
	case libTabVariables:
		return "Variables"
	case libTabScripts:
		return "Scripts"
	case libTabFiles:
		return "Files"
	}
	return ""
}

// libraryFetchCurrentTab loads data for whichever tab is active.
func libraryFetchCurrentTab(m hubModel) tea.Cmd {
	if m.client == nil {
		return nil
	}
	switch m.libraryTab {
	case libTabModules:
		return fetchModulesCmd(m.client)
	case libTabLaunchVars:
		return fetchLaunchVarsCmd(m.client)
	case libTabVariables:
		return fetchVariablesCmd(m.client)
	case libTabScripts:
		return fetchScriptsCmd(m.client)
	case libTabFiles:
		return fetchFilesCmd(m.client)
	}
	return nil
}

// --- Key handling ---

// handleLibraryKey is the top-level router for the Library view. It dispatches
// to the active tab's mode-specific handler.
func handleLibraryKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.libraryTab {
	case libTabModules:
		return handleLibraryModulesKey(m, msg)
	case libTabLaunchVars:
		return handleLibraryLaunchVarsKey(m, msg)
	case libTabVariables:
		return handleLibraryVariablesKey(m, msg)
	case libTabScripts:
		return handleLibraryScriptsKey(m, msg)
	case libTabFiles:
		return handleLibraryFilesKey(m, msg)
	}
	return m, nil
}

// handleLibraryTabNav handles global key events (tab/shift+tab/1-5/esc/q)
// that apply when the current tab is in list mode. Returns (model, cmd, handled).
// When handled is false the caller should process the key itself.
func handleLibraryTabNav(m hubModel, msg tea.KeyMsg) (hubModel, tea.Cmd, bool) {
	setLibraryTabLoading := func(m hubModel) hubModel {
		switch m.libraryTab {
		case libTabModules:
			m.moduleLoading = true
		case libTabLaunchVars:
			m.launchVarsLoading = true
		case libTabVariables:
			m.varsLoading = true
		case libTabScripts:
			m.scriptsLoading = true
		case libTabFiles:
			m.filesLoading = true
		}
		return m
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "esc":
		m.currentView = viewDashboard
		return m, nil, true
	case "tab", "right", "L":
		m.libraryTab = nextLibraryTab(m.libraryTab, +1)
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	case "shift+tab", "left", "H":
		m.libraryTab = nextLibraryTab(m.libraryTab, -1)
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	case "1":
		m.libraryTab = libTabModules
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	case "2":
		m.libraryTab = libTabLaunchVars
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	case "3":
		m.libraryTab = libTabVariables
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	case "4":
		m.libraryTab = libTabScripts
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	case "5":
		m.libraryTab = libTabFiles
		m.libraryMode = libModeList
		m = setLibraryTabLoading(m)
		return m, libraryFetchCurrentTab(m), true
	}
	return m, nil, false
}

// nextLibraryTab advances (or rewinds) the tab index with wraparound.
func nextLibraryTab(current libraryTab, delta int) libraryTab {
	idx := 0
	for i, t := range libraryTabOrder {
		if t == current {
			idx = i
			break
		}
	}
	n := len(libraryTabOrder)
	idx = (idx + delta + n) % n
	return libraryTabOrder[idx]
}

// handleLibraryModulesKey handles key events for the Modules tab. This tab
// reuses the existing modules.go fetch/render helpers but scopes navigation to
// the library hub (esc from detail returns to the modules list within the hub,
// not to the dashboard).
func handleLibraryModulesKey(m hubModel, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.libraryMode == libModeConfirmDelete {
		switch msg.String() {
		case "y":
			m.libraryMode = libModeDetail
			m.moduleConfirmDelete = false
			if m.selectedModule != nil && m.client != nil {
				m.moduleLoading = true
				return m, deleteModuleCmd(m.client, m.selectedModule.ID)
			}
		case "n", "esc":
			m.libraryMode = libModeDetail
			m.moduleConfirmDelete = false
		}
		return m, nil
	}

	if m.libraryMode == libModeDetail {
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.libraryMode = libModeList
			m.selectedModule = nil
			m.moduleDetailScroll = 0
			return m, nil
		case "d":
			m.libraryMode = libModeConfirmDelete
			m.moduleConfirmDelete = true
		case "up", "k":
			if m.moduleDetailScroll > 0 {
				m.moduleDetailScroll--
			}
		case "down", "j":
			if m.moduleDetailScroll < moduleDetailMaxScroll(m) {
				m.moduleDetailScroll++
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

// --- Rendering ---

// renderLibrary renders the full Library hub: header, tab strip, body for the
// active tab, and footer hint. The body is delegated to per-tab render funcs.
func renderLibrary(m hubModel) string {
	var b strings.Builder
	w := m.width
	if w == 0 {
		w = 80
	}
	innerW := min(w-4, 58)

	bannerContent := titleStyle.Render("REVYL") + "  " + dimStyle.Render("Library")
	banner := headerBannerStyle.Width(innerW).Render(bannerContent)
	b.WriteString(banner + "\n")

	b.WriteString(renderLibraryTabStrip(m.libraryTab) + "\n")
	b.WriteString("  " + separator(innerW) + "\n")

	switch m.libraryTab {
	case libTabModules:
		b.WriteString(renderLibraryModulesBody(m, innerW))
	case libTabLaunchVars:
		b.WriteString(renderLibraryLaunchVarsBody(m, innerW))
	case libTabVariables:
		b.WriteString(renderLibraryVariablesBody(m, innerW))
	case libTabScripts:
		b.WriteString(renderLibraryScriptsBody(m, innerW))
	case libTabFiles:
		b.WriteString(renderLibraryFilesBody(m, innerW))
	}

	b.WriteString("\n  " + separator(innerW) + "\n")
	b.WriteString("  " + renderLibraryFooter(m) + "\n")
	return b.String()
}

// renderLibraryTabStrip renders the horizontal tab strip with the active tab
// highlighted in purple.
func renderLibraryTabStrip(active libraryTab) string {
	var parts []string
	for i, t := range libraryTabOrder {
		label := fmt.Sprintf("%d %s", i+1, libraryTabLabel(t))
		if t == active {
			parts = append(parts, selectedStyle.Render("["+label+"]"))
		} else {
			parts = append(parts, dimStyle.Render(" "+label+" "))
		}
	}
	return "  " + strings.Join(parts, "  ")
}

// renderLibraryFooter returns the per-tab/per-mode help line.
func renderLibraryFooter(m hubModel) string {
	var keys []string
	switch m.libraryMode {
	case libModeDetail:
		keys = []string{
			helpKeyRender("↑/↓", "scroll"),
			helpKeyRender("d", "delete"),
			helpKeyRender("esc", "back"),
		}
	case libModeEditing:
		keys = []string{
			helpKeyRender("enter", "save"),
			helpKeyRender("esc", "cancel"),
		}
	case libModeConfirmDelete:
		keys = []string{
			helpKeyRender("y", "confirm"),
			helpKeyRender("n", "cancel"),
		}
	default: // list
		switch m.libraryTab {
		case libTabModules:
			keys = []string{
				helpKeyRender("tab", "next"),
				helpKeyRender("enter", "detail"),
				helpKeyRender("esc", "back"),
			}
		case libTabLaunchVars:
			keys = []string{
				helpKeyRender("tab", "next"),
				helpKeyRender("n", "new"),
				helpKeyRender("e", "edit"),
				helpKeyRender("d", "delete"),
				helpKeyRender("v", "values"),
				helpKeyRender("esc", "back"),
			}
		case libTabVariables:
			keys = []string{
				helpKeyRender("tab", "next"),
				helpKeyRender("n", "new"),
				helpKeyRender("e", "edit"),
				helpKeyRender("d", "delete"),
				helpKeyRender("esc", "back"),
			}
		case libTabScripts:
			keys = []string{
				helpKeyRender("tab", "next"),
				helpKeyRender("enter", "detail"),
				helpKeyRender("e", "edit"),
				helpKeyRender("d", "delete"),
				helpKeyRender("esc", "back"),
			}
		case libTabFiles:
			keys = []string{
				helpKeyRender("tab", "next"),
				helpKeyRender("enter", "detail"),
				helpKeyRender("e", "edit"),
				helpKeyRender("d", "delete"),
				helpKeyRender("esc", "back"),
			}
		}
	}
	return strings.Join(keys, "  ")
}

// renderLibraryCLIHint returns a second-line hint pointing users at the
// relevant `revyl` CLI command for heavy operations on the active tab.
func renderLibraryCLIHint(tab libraryTab) string {
	switch tab {
	case libTabModules:
		return dimStyle.Render("Create/edit modules: revyl module create <name> --from-file blocks.yaml")
	case libTabLaunchVars:
		return dimStyle.Render("Or via CLI: revyl global launch-var list")
	case libTabVariables:
		return dimStyle.Render("Or via CLI: revyl global var set NAME=VALUE")
	case libTabScripts:
		return dimStyle.Render("Create/edit script code: revyl script create <name> --runtime python --file script.py")
	case libTabFiles:
		return dimStyle.Render("Upload/download files: revyl file upload <path> | revyl file download <name>")
	}
	return ""
}

// renderLibraryModulesBody renders the Modules tab body. It reuses the module
// block renderer for the detail view, but trims the banner/help footer that
// renderModuleDetail would otherwise add (those are handled by renderLibrary).
func renderLibraryModulesBody(m hubModel, innerW int) string {
	var b strings.Builder

	if m.moduleLoading {
		b.WriteString("  " + m.spinner.View() + " Loading...\n")
		return b.String()
	}

	if m.libraryMode == libModeDetail || m.libraryMode == libModeConfirmDelete {
		mod := m.selectedModule
		if mod == nil {
			b.WriteString("  " + dimStyle.Render("Module not loaded") + "\n")
			return b.String()
		}
		b.WriteString("  " + normalStyle.Render(mod.Name) + "  " + dimStyle.Render(fmt.Sprintf("%d blocks", mod.BlockCount)) + "\n")
		if mod.Description != "" {
			b.WriteString("  " + dimStyle.Render(mod.Description) + "\n")
		}
		if m.libraryMode == libModeConfirmDelete {
			b.WriteString("\n  " + errorStyle.Render(fmt.Sprintf("Delete module \"%s\"? (y/n)", mod.Name)) + "\n")
			return b.String()
		}
		b.WriteString("\n  " + sectionStyle.Render("BLOCKS") + "\n")
		blockLines := renderModuleBlockList(mod.Blocks, innerW+2)
		visible := max(m.height-12, 5)
		start := min(m.moduleDetailScroll, max(len(blockLines)-1, 0))
		end := min(start+visible, len(blockLines))
		if start > 0 {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for _, ln := range blockLines[start:end] {
			b.WriteString(ln + "\n")
		}
		if end < len(blockLines) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
		b.WriteString("\n  " + renderLibraryCLIHint(libTabModules) + "\n")
		return b.String()
	}

	// list mode
	b.WriteString(sectionStyle.Render(fmt.Sprintf("  Modules  %d", len(m.moduleItems))) + "\n")
	if len(m.moduleItems) == 0 {
		b.WriteString("  " + dimStyle.Render("No modules found") + "\n")
		b.WriteString("  " + renderLibraryCLIHint(libTabModules) + "\n")
		return b.String()
	}
	start, end := scrollWindow(m.moduleCursor, len(m.moduleItems), 12)
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
	b.WriteString("\n  " + renderLibraryCLIHint(libTabModules) + "\n")
	return b.String()
}
