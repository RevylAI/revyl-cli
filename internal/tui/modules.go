// Package tui provides the module browser screens for viewing test modules.
//
// Modules are read-only in the TUI -- creation requires file paths and stays CLI-only.
// The browser shows a list of modules with block counts, and detail view renders module
// blocks in a readable, scrollable outline. Deletion is supported with y/n confirmation.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
		m.moduleDetailScroll = 0
		return m, nil
	case "d":
		m.moduleConfirmDelete = true
	case "up":
		if m.moduleDetailScroll > 0 {
			m.moduleDetailScroll--
		}
	case "down":
		if m.moduleDetailScroll < moduleDetailMaxScroll(m) {
			m.moduleDetailScroll++
		}
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

// renderModuleDetail renders the module detail screen with readable block summaries.
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
	h := m.height
	if h == 0 {
		h = 24
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
	headerLine := bannerContent + strings.Repeat(" ", max(1, innerW-lipgloss.Width(bannerContent)-lipgloss.Width(blocksBadge)+4)) + blocksBadge
	banner := headerBannerStyle.Width(innerW).Render(headerLine)
	headerLines := []string{banner}
	if mod.Description != "" {
		headerLines = append(headerLines, renderModuleWrappedText("  ", mod.Description, innerW+2, normalStyle)...)
	}

	// Delete confirmation
	if m.moduleConfirmDelete {
		for _, line := range headerLines {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n  " + errorStyle.Render(fmt.Sprintf("Delete module \"%s\"? (y/n)", mod.Name)) + "\n")
		return b.String()
	}

	headerLines = append(headerLines, "", sectionStyle.Render("  BLOCKS"), "  "+separator(innerW))
	contentLines := renderModuleBlockList(mod.Blocks, innerW+2)
	footerLines := []string{
		"",
		"  " + dimStyle.Render("Create modules with: revyl module create <name> --from-file blocks.yaml"),
		"",
		"  " + separator(innerW),
		"  " + strings.Join([]string{
			helpKeyRender("↑/↓", "scroll"),
			helpKeyRender("d", "delete"),
			helpKeyRender("esc", "back"),
			helpKeyRender("q", "quit"),
		}, "  "),
	}

	for _, line := range headerLines {
		b.WriteString(line + "\n")
	}

	visibleContent := max(h-len(headerLines)-len(footerLines), 5)
	contentStart := 0
	contentEnd := len(contentLines)
	if len(contentLines) > visibleContent {
		contentStart = min(max(m.moduleDetailScroll, 0), len(contentLines)-1)
		showUp := contentStart > 0
		slots := visibleContent
		if showUp {
			slots--
		}
		slots = max(slots, 1)
		contentEnd = min(contentStart+slots, len(contentLines))
		showDown := contentEnd < len(contentLines)
		if showDown && contentEnd > contentStart {
			contentEnd--
		}

		if showUp {
			b.WriteString(dimStyle.Render("  ↑ more") + "\n")
		}
		for _, line := range contentLines[contentStart:contentEnd] {
			b.WriteString(line + "\n")
		}
		if contentEnd < len(contentLines) {
			b.WriteString(dimStyle.Render("  ↓ more") + "\n")
		}
	} else {
		for _, line := range contentLines {
			b.WriteString(line + "\n")
		}
	}

	for _, line := range footerLines {
		b.WriteString(line + "\n")
	}

	return b.String()
}

type moduleDisplayBlock struct {
	Type            string
	StepType        string
	StepDescription string
	VariableName    string
	ModuleID        string
	Condition       string
	Then            []moduleDisplayBlock
	Else            []moduleDisplayBlock
	Body            []moduleDisplayBlock
}

func moduleDetailMaxScroll(m hubModel) int {
	if m.selectedModule == nil {
		return 0
	}
	lines := renderModuleBlockList(m.selectedModule.Blocks, min(max(m.width, 80)-4, 58)+2)
	if len(lines) == 0 {
		return 0
	}
	return len(lines) - 1
}

func renderModuleBlockList(blocks []interface{}, width int) []string {
	if len(blocks) == 0 {
		return []string{"  " + dimStyle.Render("No blocks defined")}
	}

	var lines []string
	for i, raw := range blocks {
		block := normalizeModuleBlock(raw)
		prefix := fmt.Sprintf("  %d. ", i+1)
		lines = append(lines, renderModuleBlockLines(block, prefix, width)...)
	}
	return lines
}

func renderModuleBlockLines(block moduleDisplayBlock, prefix string, width int) []string {
	lines := renderModuleSummaryLines(prefix, block, width)

	switch block.Type {
	case "if":
		sectionIndent := strings.Repeat(" ", lipgloss.Width(prefix)+2)
		childPrefix := sectionIndent + "- "

		lines = append(lines, renderModuleBranchLine(sectionIndent, "THEN"))
		if len(block.Then) == 0 {
			lines = append(lines, dimStyle.Render(childPrefix+"No blocks"))
		} else {
			for _, child := range block.Then {
				lines = append(lines, renderModuleBlockLines(child, childPrefix, width)...)
			}
		}

		if len(block.Else) > 0 {
			lines = append(lines, renderModuleBranchLine(sectionIndent, "ELSE"))
			for _, child := range block.Else {
				lines = append(lines, renderModuleBlockLines(child, childPrefix, width)...)
			}
		}
	case "while":
		sectionIndent := strings.Repeat(" ", lipgloss.Width(prefix)+2)
		childPrefix := sectionIndent + "- "

		lines = append(lines, renderModuleBranchLine(sectionIndent, "BODY"))
		if len(block.Body) == 0 {
			lines = append(lines, dimStyle.Render(childPrefix+"No blocks"))
		} else {
			for _, child := range block.Body {
				lines = append(lines, renderModuleBlockLines(child, childPrefix, width)...)
			}
		}
	}

	return lines
}

func renderModuleSummaryLines(prefix string, block moduleDisplayBlock, width int) []string {
	summary := moduleBlockSummary(block)
	labelText, bodyText, hasBody := strings.Cut(summary, ": ")
	if !hasBody {
		return renderModuleWrappedText(prefix, summary, width, normalStyle)
	}

	badgeText := " " + labelText + " "
	prefixWidth := lipgloss.Width(prefix)
	badgeWidth := lipgloss.Width(badgeText)
	bodyWidth := max(width-prefixWidth-badgeWidth-1, 1)
	prefixRendered := moduleBlockPrefixStyle().Render(prefix)
	badgeRendered := moduleBlockBadgeStyle(block).Render(badgeText)

	if strings.TrimSpace(bodyText) == "" {
		return []string{prefixRendered + badgeRendered}
	}

	wrapped := wrapModuleText(bodyText, bodyWidth)
	lines := make([]string, 0, len(wrapped))
	bodyIndent := strings.Repeat(" ", prefixWidth+badgeWidth+1)
	bodyStyle := moduleBlockBodyStyle(block)
	for i, line := range wrapped {
		if i == 0 {
			lines = append(lines, prefixRendered+badgeRendered+" "+bodyStyle.Render(line))
			continue
		}
		lines = append(lines, bodyIndent+bodyStyle.Render(line))
	}
	return lines
}

func renderModuleWrappedText(prefix, text string, width int, style lipgloss.Style) []string {
	availableWidth := max(width-lipgloss.Width(prefix), 1)
	wrapped := wrapModuleText(text, availableWidth)
	hangingPrefix := strings.Repeat(" ", lipgloss.Width(prefix))
	lines := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		linePrefix := prefix
		if i > 0 {
			linePrefix = hangingPrefix
		}
		lines = append(lines, style.Render(linePrefix+line))
	}
	return lines
}

func renderModuleBranchLine(prefix, label string) string {
	return prefix + lipgloss.NewStyle().
		Foreground(dimGray).
		Background(subtleBg).
		Bold(true).
		Padding(0, 1).
		Render(label)
}

func moduleBlockPrefixStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(purple).
		Bold(true)
}

func moduleBlockBadgeStyle(block moduleDisplayBlock) lipgloss.Style {
	accent := purple
	switch block.Type {
	case "validation":
		accent = green
	case "extraction", "code_execution":
		accent = teal
	case "manual":
		accent = amber
	case "unknown", "":
		accent = dimGray
	}

	return lipgloss.NewStyle().
		Foreground(accent).
		Background(subtleBg).
		Bold(true)
}

func moduleBlockBodyStyle(block moduleDisplayBlock) lipgloss.Style {
	switch block.Type {
	case "validation":
		return lipgloss.NewStyle().Foreground(white)
	case "manual":
		return lipgloss.NewStyle().Foreground(white)
	default:
		return normalStyle
	}
}

func wrapModuleText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}

		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}

		current := ""
		for _, word := range words {
			for lipgloss.Width(word) > width {
				chunk, rest := splitModuleWord(word, width)
				if current != "" {
					lines = append(lines, current)
					current = ""
				}
				lines = append(lines, chunk)
				word = rest
			}

			if word == "" {
				continue
			}

			if current == "" {
				current = word
				continue
			}

			if lipgloss.Width(current)+1+lipgloss.Width(word) <= width {
				current += " " + word
				continue
			}

			lines = append(lines, current)
			current = word
		}

		if current != "" {
			lines = append(lines, current)
		}
	}

	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func splitModuleWord(word string, width int) (string, string) {
	if width <= 0 {
		return word, ""
	}

	var builder strings.Builder
	consumed := 0
	for idx, r := range word {
		next := builder.String() + string(r)
		if lipgloss.Width(next) > width {
			if builder.Len() == 0 {
				return string(r), word[idx+len(string(r)):]
			}
			return builder.String(), word[consumed:]
		}
		builder.WriteRune(r)
		consumed = idx + len(string(r))
	}
	return builder.String(), ""
}

func normalizeModuleBlock(raw interface{}) moduleDisplayBlock {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return moduleDisplayBlock{
			Type:            "unknown",
			StepDescription: "Unsupported block payload",
		}
	}

	return moduleDisplayBlock{
		Type:            moduleMapString(m, "type"),
		StepType:        moduleMapString(m, "step_type"),
		StepDescription: moduleMapString(m, "step_description", "instruction", "step"),
		VariableName:    moduleMapString(m, "variable_name"),
		ModuleID:        moduleMapString(m, "module_id"),
		Condition:       moduleMapString(m, "condition"),
		Then:            normalizeModuleChildren(m, "thenChildren", "then_children"),
		Else:            normalizeModuleChildren(m, "elseChildren", "else_children"),
		Body:            normalizeModuleChildren(m, "children", "body"),
	}
}

func normalizeModuleChildren(block map[string]interface{}, keys ...string) []moduleDisplayBlock {
	var children []interface{}
	for _, key := range keys {
		if raw, ok := block[key]; ok {
			switch typed := raw.(type) {
			case []interface{}:
				children = typed
			case []map[string]interface{}:
				for _, child := range typed {
					children = append(children, child)
				}
			}
			if len(children) > 0 {
				break
			}
		}
	}

	if len(children) == 0 {
		return nil
	}

	normalized := make([]moduleDisplayBlock, 0, len(children))
	for _, child := range children {
		normalized = append(normalized, normalizeModuleBlock(child))
	}
	return normalized
}

func moduleMapString(block map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if raw, ok := block[key]; ok {
			switch typed := raw.(type) {
			case string:
				return strings.TrimSpace(typed)
			case fmt.Stringer:
				return strings.TrimSpace(typed.String())
			case nil:
				continue
			default:
				return strings.TrimSpace(fmt.Sprintf("%v", typed))
			}
		}
	}
	return ""
}

func moduleBlockSummary(block moduleDisplayBlock) string {
	switch block.Type {
	case "instructions":
		return moduleSummaryWithFallback("Instructions", block.StepDescription, "Instruction block")
	case "validation":
		return moduleSummaryWithFallback("Validation", block.StepDescription, "Validation block")
	case "extraction":
		summary := moduleSummaryWithFallback("Extraction", block.StepDescription, "Extract value")
		if block.VariableName == "" {
			return summary
		}
		return summary + " -> {{" + block.VariableName + "}}"
	case "manual":
		return summarizeManualModuleBlock(block)
	case "if":
		return moduleSummaryWithFallback("If", moduleFirstNonEmpty(block.Condition, block.StepDescription), "condition not provided")
	case "while":
		return moduleSummaryWithFallback("While", moduleFirstNonEmpty(block.Condition, block.StepDescription), "condition not provided")
	case "code_execution":
		summary := moduleSummaryWithFallback("Code execution", block.StepDescription, "script not provided")
		if block.VariableName == "" {
			return summary
		}
		return summary + " -> {{" + block.VariableName + "}}"
	case "module_import":
		label := moduleFirstNonEmpty(block.StepDescription, block.ModuleID, "module not provided")
		return "Module import: " + label
	case "":
		return moduleFirstNonEmpty(block.StepDescription, "Unknown block")
	default:
		fallback := moduleFirstNonEmpty(block.StepDescription, block.Condition, block.ModuleID, "details unavailable")
		return moduleSummaryWithFallback(moduleTitle(block.Type), fallback, fallback)
	}
}

func summarizeManualModuleBlock(block moduleDisplayBlock) string {
	switch block.StepType {
	case "wait":
		if block.StepDescription == "" {
			return "Manual: Wait"
		}
		return "Manual: Wait " + block.StepDescription + "s"
	case "open_app":
		if block.StepDescription == "" {
			return "Manual: Open app"
		}
		return "Manual: Open app " + block.StepDescription
	case "kill_app":
		if block.StepDescription == "" {
			return "Manual: Kill app"
		}
		return "Manual: Kill app " + block.StepDescription
	case "go_home":
		return "Manual: Go home"
	case "navigate":
		if block.StepDescription == "" {
			return "Manual: Navigate"
		}
		return "Manual: Navigate to " + block.StepDescription
	case "set_location":
		if block.StepDescription == "" {
			return "Manual: Set location"
		}
		return "Manual: Set location " + block.StepDescription
	case "":
		return moduleSummaryWithFallback("Manual", block.StepDescription, "Manual block")
	default:
		if block.StepDescription == "" {
			return "Manual (" + moduleTitle(block.StepType) + ")"
		}
		return "Manual (" + moduleTitle(block.StepType) + "): " + block.StepDescription
	}
}

func moduleSummaryWithFallback(label, value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return label + ": " + fallback
	}
	return label + ": " + value
}

func moduleFirstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func moduleTitle(value string) string {
	parts := strings.Fields(strings.ReplaceAll(value, "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}
